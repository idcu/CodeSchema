// Package service 提供测试关联策略实现。
//
// 五种策略（按置信度降序）：
//   - explicit  (100): 显式注解匹配（@TestFor(...) 解析目标类）
//   - call_graph(95):  测试方法直接调用目标方法（与候选类命名无关）
//   - dependency(80):  导入依赖递归
//   - naming    (70):  命名约定（OrderServiceTest ↔ OrderService）
//   - same_tag  (60):  同 Tag 聚类
package service

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gitee.com/idcu-go/metrics"
	"github.com/idcu/codeschema/internal/store"
)

// init 注册测试关联模块指标（差异点可观测性）。
func init() {
	metrics.RegisterCounter("testlink_lookups_total", "Total test link lookups", "strategy")
	metrics.RegisterCounter("testlink_hits_total", "Total test links found", "strategy")
}

// TestLink 测试关联结果。
type TestLink struct {
	TestMethod string `json:"test_method"`
	MethodName string `json:"method_name"`
	Strategy   string `json:"strategy"`
	Confidence int    `json:"confidence"`
}

// FindTestLinks 查找指定方法的所有关联单测（按置信度降序）。
//
// 实现三种策略：
//   - naming: 类名约定匹配（如 OrderService ↔ OrderServiceTest）
//   - same_tag: 同 Tag 聚类
//   - dependency: 导入依赖递归
func (s *Service) FindTestLinks(ctx context.Context, methodFQN string, minConfidence int) ([]TestLink, error) {
	links, _, err := s.FindTestLinksWithStats(ctx, methodFQN, minConfidence)
	return links, err
}

// FindTestLinksWithStats 与 FindTestLinks 相同，但额外返回因低于 minConfidence
// 被丢弃的候选条数（R6-1：上下文响应需要暴露 filtered 计数）。
func (s *Service) FindTestLinksWithStats(ctx context.Context, methodFQN string, minConfidence int) ([]TestLink, int, error) {
	if methodFQN == "" {
		return nil, 0, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "methodFQN is required"}
	}
	if minConfidence <= 0 {
		minConfidence = 60
	}

	// 获取所有文件数据（一次），随后从同一份快照读取类/方法/调用/标签，
	// 避免五种策略各自做全量「文件→类→方法」扫描（PG 后端上是独立 SQL）。
	files, err := s.store.GetAllFiles(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("get all files: %w", err)
	}
	snap := s.cachedTestLinkSnapshot(ctx, files)
	links, filtered := s.findTestLinksFromSnapshot(ctx, snap, methodFQN, minConfidence)
	return links, filtered, nil
}

// findTestLinksFromSnapshot 在已构建的快照上执行五种策略、排序与置信度过滤。
// 返回 (过滤后的关联, 被过滤条数)。
func (s *Service) findTestLinksFromSnapshot(ctx context.Context, snap *testLinkSnapshot, methodFQN string, minConfidence int) ([]TestLink, int) {
	// 收集所有结果，去重
	type linkKey struct {
		testMethod string
		strategy   string
	}
	seen := make(map[linkKey]bool)
	var results []TestLink

	addResult := func(testMethod, methodName, strategy string, confidence int) {
		key := linkKey{testMethod: testMethod, strategy: strategy}
		if seen[key] {
			return
		}
		// 排除自身
		if testMethod == methodFQN {
			return
		}
		seen[key] = true
		results = append(results, TestLink{
			TestMethod: testMethod,
			MethodName: methodName,
			Strategy:   strategy,
			Confidence: confidence,
		})
	}

	// 查找目标方法所在的类
	targetClass := extractClassFQN(methodFQN)
	allTestClasses := snap.testClasses
	allClasses := snap.allClasses
	fileDir := snap.fileDir
	classesByDir := snap.classesByDir
	knownTypes := snap.knownTypes

	// 0. explicit 策略：@TestFor(...) 显式注解（置信度 100）
	s.linkByExplicit(ctx, snap, methodFQN, addResult)

	// 1. naming 策略：类名匹配（OrderService ↔ OrderServiceTest）
	//    同一源类可能有多个测试类（OrderServiceTest / TestOrderService），全部参与。
	targetDir := fileDir[allClasses[targetClass].FileID]
	for _, testClassFQN := range allTestClasses[targetClass] {
		s.linkByNaming(ctx, snap, classesByDir[targetDir], knownTypes, targetClass, testClassFQN, methodFQN, addResult)
	}

	// 2.5 call_graph 策略：测试方法直接调用目标方法。
	//      行为型测试名（TestCollection_Delete 之于 CreateCollection）与跨类测试
	//      都不在 naming 的候选集里，直接调用边与类名约定无关。
	s.linkByCallGraph(ctx, snap, methodFQN, addResult)

	// 3. same_tag 策略：同 Tag 聚类
	s.linkBySameTag(ctx, snap, methodFQN, addResult)

	// 4. dependency 策略：依赖递归
	s.linkByDependency(ctx, snap, methodFQN, addResult)

	// 按置信度降序排序
	sortByConfidenceDesc(results)

	// 差异点打点：按策略统计查询与命中（可观测「测试关联」价值）。
	metrics.IncCounter("testlink_lookups_total", "all")
	strategyHits := map[string]int{}
	for _, r := range results {
		strategyHits[r.Strategy]++
	}
	for strat, n := range strategyHits {
		metrics.IncCounter("testlink_lookups_total", strat)
		// 批量累加：/affected 会对上万个符号逐个查询，逐次 IncCounter 的全局锁
		// 在命中较多时成为热点；最终计数与逐次递增一致。
		metrics.AddCounter("testlink_hits_total", float64(n), strat)
	}

	// 过滤置信度
	filtered := make([]TestLink, 0, len(results))
	for _, r := range results {
		if r.Confidence >= minConfidence {
			filtered = append(filtered, r)
		}
	}

	return filtered, len(results) - len(filtered)
}

// isGoTestFile 判断是否为 Go 测试文件（*_test.go）。
func isGoTestFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(strings.ReplaceAll(path, "\\", "/")), "_test.go")
}

// isGoTestFuncName 判断是否为 Go 测试函数名（TestXxx/BenchmarkXxx/ExampleXxx）。
func isGoTestFuncName(name string) bool {
	for _, prefix := range []string{"Test", "Benchmark", "Example"} {
		if strings.HasPrefix(name, prefix) && len(name) > len(prefix) {
			return true
		}
	}
	return false
}

// matchTestClass 判断是否为测试类，并返回对应的源类全限定名。
//
// 规则（与 isTestClass 口径对齐）：
//   - OrderServiceTest / OrderServiceTests / OrderServiceSpec → OrderService（去后缀）
//   - TestOrderService → OrderService（去掉 Test 前缀）
func (s *Service) matchTestClass(cls store.ClassRecord) string {
	name := cls.Name

	// 后缀匹配：Test / Tests / Spec（Spring/JUnit 常用 *Tests，Jest 常用 *Spec）。
	for _, suffix := range []string{"Tests", "Spec", "Test"} {
		if !strings.HasSuffix(name, suffix) || len(name) <= len(suffix) {
			continue
		}
		sourceName := strings.TrimSuffix(name, suffix)
		return strings.Replace(cls.FullName, name, sourceName, 1)
	}

	// 前缀匹配：TestOrderService
	if strings.HasPrefix(name, "Test") && len(name) > 4 {
		sourceName := strings.TrimPrefix(name, "Test")
		return strings.Replace(cls.FullName, name, sourceName, 1)
	}

	return ""
}

// linkByNaming 通过命名约定建立方法级关联。
// dirClasses 是目标类同目录（Go 包）的类集合，用于判断包级同名方法歧义。
func (s *Service) linkByNaming(ctx context.Context, snap *testLinkSnapshot, dirClasses []store.ClassRecord, knownTypes map[string]bool, classFQN, testClassFQN string, methodFQN string, add func(string, string, string, int)) {
	// 找到目标类及其全部方法
	var targetMethod store.MethodRecord
	found := false
	// 目标类全部方法名：用于判断测试方法是否已显式调用「同类另一个方法」，
	// 从而让命名关联让位给调用证据（见 hasCompetingCall）。
	classMethodNames := make(map[string]bool)
	for _, o := range snap.ownersByFQN[classFQN] {
		for _, m := range o.methods {
			classMethodNames[m.Name] = true
			if m.FullName == methodFQN {
				targetMethod = m
				found = true
			}
		}
		if len(classMethodNames) > 0 {
			break
		}
	}

	if !found {
		return
	}

	// 在测试类中查找同名方法（或带 Test 前缀/后缀的方法）
	methodName := targetMethod.Name
	for _, o := range snap.ownersByFQN[testClassFQN] {
		f := o.file
		cls := o.cls
		// 生产文件的 PACKAGE 容器与测试容器同名（Go 包名），不能当测试类扫描。
		if cls.Type == "PACKAGE" && !isGoTestFile(f.AbsolutePath) {
			continue
		}
		calls := snap.callsOf(ctx, f.ID)
		methods := o.methods
		// Go 包级容器的同包 helper（keysOfSets/newMockLSPAdapterMode）不是测试，
		// 只让 Test/Benchmark/Example 前缀的函数参与命名匹配。
		goPkg := cls.Type == "PACKAGE" && isGoTestFile(f.AbsolutePath)
		// Go 包级测试：同名方法分布在多个类型时，测试名里的公共词无法确定目标类型
		// （Resolve 之于 JavaResolver/GradleResolver/heuristicResolver），
		// 命名关联属于过匹配；真实调用边由 call_graph 策略保留。
		if goPkg && s.methodNameAmbiguousInDir(snap, dirClasses, methodName) {
			continue
		}
		for _, m := range methods {
			if goPkg && !isGoTestFuncName(m.Name) {
				continue
			}
			if goPkg {
				ok, typeToken := goTestMethodMatches(m.Name, methodName, knownTypes)
				if !ok {
					continue
				}
				// 测试名锚定了接收者类型时，只允许该类型的方法。
				if typeToken != "" && typeToken != classFQN && typeToken != shortMethodName(classFQN) {
					continue
				}
			} else if !matchesTestMethod(methodName, m.Name) {
				continue
			}
			// 测试已显式调用同类另一个方法时，仅凭名字里的公共词关联属于过匹配。
			if hasCompetingCall(calls, m, classMethodNames, methodName) {
				continue
			}
			add(m.FullName, methodName, "naming", 70)
		}
	}
}

// methodNameAmbiguousInDir 判断方法名在同目录（Go 包）内是否由多个类型实现。
// 只看生产类（跳过测试 PACKAGE 容器）：测试容器里的同名 helper 不是候选实现。
func (s *Service) methodNameAmbiguousInDir(snap *testLinkSnapshot, dirClasses []store.ClassRecord, methodName string) bool {
	owners := make(map[string]struct{}, 2)
	for _, cls := range dirClasses {
		if cls.Type == "PACKAGE" {
			continue
		}
		for _, m := range snap.methodsOf(cls.ID) {
			if m.Name == methodName {
				owners[cls.FullName] = struct{}{}
				break
			}
		}
		if len(owners) > 1 {
			return true
		}
	}
	return false
}

// goTestMethodMatches 判断 Go 包级测试方法名是否指向目标方法，并返回测试名里的
// 类型标记（`Test<Type>_<Method>` / `Example<Type>_<Method>` 中的 Type）。
//
// Go 惯例是 Test<Method>_<Description>；若首段是仓库内已知类型名，则测试名锚定了
// 该类型（TestMemoryStore_SearchEmpty ↔ MemoryStore.Search），不能再链到同包其它
// 类型的同名方法（ChromemStore.Search）。没有类型标记时，目标方法必须落在开头并
// 构成完整词（后接 `_` 或大写字母）。
func goTestMethodMatches(testName, methodName string, knownTypes map[string]bool) (bool, string) {
	rest := testName
	stripped := false
	for _, prefix := range []string{"Test", "Benchmark", "Example"} {
		if strings.HasPrefix(rest, prefix) {
			rest = rest[len(prefix):]
			stripped = true
			break
		}
	}
	if !stripped {
		return matchesTestMethod(testName, methodName), ""
	}
	if i := strings.Index(rest, "_"); i > 0 && knownTypes[rest[:i]] {
		tail := rest[i+1:]
		if tail == methodName || (strings.HasPrefix(tail, methodName) && wordBoundary(tail[len(methodName):])) {
			return true, rest[:i]
		}
		return false, rest[:i]
	}
	if rest == methodName {
		return true, ""
	}
	if strings.HasPrefix(rest, methodName) && wordBoundary(rest[len(methodName):]) {
		return true, ""
	}
	return false, ""
}

// wordBoundary 判断方法名之后是否仍是独立词（空、下划线或大写开头）。
func wordBoundary(tail string) bool {
	if tail == "" {
		return true
	}
	return tail[0] == '_' || (tail[0] >= 'A' && tail[0] <= 'Z')
}

// hasCompetingCall 判断测试方法是否已显式调用目标类中的其它方法。
//
// 命名策略只能看到「测试名里的公共词」：TestSearchByTags_* /
// TestSearch_Disambiguate* 都会命中 Search，而测试体实际调用的是
// SearchByTags / disambiguateMethodResults。这类关联属于过匹配，应让位给调用证据
// （真实调用边仍由 call_graph 策略保留）。
func hasCompetingCall(calls []store.CallRecord, testMethod store.MethodRecord, classMethodNames map[string]bool, targetName string) bool {
	for _, c := range calls {
		if c.CallerFQN != testMethod.FullName && shortMethodName(c.CallerFQN) != testMethod.Name {
			continue
		}
		callee := shortMethodName(c.CalleeFQN)
		if callee != targetName && classMethodNames[callee] {
			return true
		}
	}
	return false
}

// linkByCallGraph 通过「测试方法 → 生产方法」的直接调用边建立关联（置信度 95）。
//
// 与 naming 的差别：naming 只在「与目标类同名的测试类」里按方法名匹配，
// 行为型测试名与跨类测试都不在候选集。调用边与类名约定无关，可覆盖这两类漏召。
//
// 反向调用索引在快照内预建（按快照顺序），每次查询只取命中该方法的测试调用方。
func (s *Service) linkByCallGraph(ctx context.Context, snap *testLinkSnapshot, methodFQN string, add func(string, string, string, int)) {
	for _, caller := range snap.callersOf(ctx, methodFQN) {
		add(caller, shortMethodName(methodFQN), "call_graph", 95)
	}
}

// normalizeCallerFQN 把调用边里的 caller（可能带包名前缀）对齐到文件内的方法 FQN。
func normalizeCallerFQN(caller string, methods []store.MethodRecord) string {
	if caller == "" {
		return ""
	}
	for _, m := range methods {
		if caller == m.FullName || strings.HasSuffix(caller, "."+m.FullName) {
			return m.FullName
		}
	}
	return ""
}

// shortMethodName 取 FQN 的最后一段（方法名）。
func shortMethodName(fqn string) string {
	if i := strings.LastIndex(fqn, "."); i >= 0 {
		return fqn[i+1:]
	}
	return fqn
}

// matchesTestMethod 判断测试方法名是否匹配目标方法名（跨框架：Go/JUnit/pytest/Jest）。
func matchesTestMethod(targetName, testName string) bool {
	if targetName == "" || testName == "" {
		return false
	}
	// 精确匹配
	if testName == targetName {
		return true
	}
	// Go/pytest 前缀 + 精确余量：TestGetOrder / testGetOrder / test_get_order ↔ GetOrder
	for _, prefix := range []string{"Test", "test", "test_"} {
		if strings.HasPrefix(testName, prefix) && strings.TrimPrefix(testName, prefix) == targetName {
			return true
		}
	}
	// 描述型测试名：目标名必须落在词边界上，避免 Analyze 命中 Analyzer（P2-4）。
	descriptive := false
	for _, prefix := range []string{
		"Test", "test", "test_", "Benchmark", "benchmark", "Example", "example",
		"Should", "should_", "when_",
	} {
		if strings.HasPrefix(testName, prefix) {
			descriptive = true
			break
		}
	}
	if descriptive && containsNameWords(testName, targetName) {
		return true
	}
	// JUnit 描述型方法名（无测试前缀）：目标名是测试名前缀且下一字符为
	// 大写或下划线，如 processCreationFormSuccess ↔ processCreationForm、
	// addPetAddsPersistedPet ↔ addPet。目标名过短（<4）不做前缀匹配，
	// 避免 get 命中 getById。
	if len(targetName) >= 4 && strings.HasPrefix(testName, targetName) {
		rest := testName[len(targetName):]
		if rest == "" {
			return true
		}
		if rest[0] == '_' || (rest[0] >= 'A' && rest[0] <= 'Z') {
			return true
		}
	}
	return false
}

// containsNameWords 判断 target 能否由 testName 按词边界切分后的连续词拼接而成。
// 比较时忽略大小写与下划线，覆盖 getOrder / Get_Order / testGetOrderShouldReturn。
func containsNameWords(testName, target string) bool {
	words := splitNameWords(testName)
	targetNorm := strings.ToLower(strings.ReplaceAll(target, "_", ""))
	if targetNorm == "" {
		return false
	}
	for i := range words {
		var b strings.Builder
		for j := i; j < len(words); j++ {
			b.WriteString(strings.ToLower(words[j]))
			cur := b.String()
			if cur == targetNorm {
				return true
			}
			if len(cur) >= len(targetNorm) {
				break
			}
		}
	}
	return false
}

// splitNameWords 按下划线/分隔符与 camelCase 边界切词。
func splitNameWords(s string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = cur[:0]
		}
	}
	for _, r := range s {
		switch {
		case r == '_' || r == '-' || r == '.' || r == ' ':
			flush()
		case r >= 'A' && r <= 'Z':
			if len(cur) > 0 {
				flush()
			}
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return words
}

// linkBySameTag 通过同 Tag 聚类建立关联（置信度 60）。
//
// 目标方法所在类与测试类共享标签是类级信号；为避免把同 tag 测试类的所有
// 方法都算作关联，方法名仍须符合命名约定（P2-4）。
func (s *Service) linkBySameTag(ctx context.Context, snap *testLinkSnapshot, methodFQN string, add func(string, string, string, int)) {
	// 目标方法的类与方法名（旧实现：最后一个匹配类生效）。
	targetClassID := snap.classIDByMethod[methodFQN]
	targetMethodName := snap.methodNameByFQN[methodFQN]
	if targetClassID == 0 || targetMethodName == "" {
		return
	}

	// 获取目标方法所在类的标签
	targetTags := snap.tagsOf(ctx, targetClassID)
	if len(targetTags) == 0 {
		return
	}
	targetSet := make(map[string]bool, len(targetTags))
	for _, t := range targetTags {
		targetSet[t] = true
	}

	// 查找同标签的测试类（testOwners 是 owners 的保序子集，判定结果不变）
	for _, o := range snap.testOwners {
		cls := o.cls
		// 跳过自身
		if cls.ID == targetClassID {
			continue
		}

		clsTags := snap.tagsOf(ctx, cls.ID)
		if hasCommonTagSet(targetSet, clsTags) {
			for _, m := range o.methods {
				if !matchesTestMethod(targetMethodName, m.Name) {
					continue
				}
				add(m.FullName, m.Name, "same_tag", 60)
			}
		}
	}
}

// isTestClass 判断是否为测试类（跨框架：Go/JUnit/pytest/Jest）。
//
// 识别两类信号：
//   - 类名约定：*Test / Test* / *Tests / *Spec（Go/JUnit/Jest）
//   - 文件路径约定：*_test.go / *_test.py / test_*.py / *.test.ts|js / *.spec.ts|js（pytest/Jest 以文件命名的测试）
//     （文件级识别需由调用方传入 path；空 path 时仅按类名判断）
func isTestClass(cls store.ClassRecord, path string) bool {
	if path != "" {
		if isTestFileName(path) {
			return true
		}
		// Go 的测试函数只能位于 *_test.go；生产文件中以 Test 开头的类型
		// （TestLink/TestResult/TestBatchItem）不是测试类。否则整个生产文件会被
		// 误判为测试文件，文件内所有生产调用都会被当成「测试方法」进入关联结果。
		if strings.HasSuffix(strings.ToLower(path), ".go") {
			return false
		}
	}
	name := cls.Name
	if strings.HasSuffix(name, "Test") || strings.HasPrefix(name, "Test") ||
		strings.HasSuffix(name, "Tests") || strings.HasSuffix(name, "Spec") {
		return true
	}
	if path != "" && isTestFileName(path) {
		return true
	}
	return false
}

// isTestFileName 判断是否为多框架测试文件（按文件命名约定识别）。
func isTestFileName(path string) bool {
	path = strings.ReplaceAll(path, "\\", "/")
	if idx := strings.LastIndexByte(path, '/'); idx >= 0 {
		path = path[idx+1:]
	}
	base := strings.ToLower(path)
	// pytest: test_ 前缀文件（test_*.py / test_*.js）（仅前缀，避免 latest_file.go 误判）
	if strings.HasPrefix(base, "test_") {
		return true
	}
	// *_test.* / *.test.* / *.spec.*（Go/JUnit/Jest 文件级约定）
	for _, marker := range []string{"_test.", ".test.", ".spec."} {
		if strings.Contains(base, marker) {
			return true
		}
	}
	return false
}

// hasCommonTagSet 用已建好的标签集合检查交集，避免每个候选类重复建 map。
func hasCommonTagSet(set map[string]bool, tags []string) bool {
	for _, t := range tags {
		if set[t] {
			return true
		}
	}
	return false
}

// explicitAnnotationRe 匹配 @TestFor 注解，捕获目标类标识（支持 FQN 或简单类名）。
//
// 支持写法：@TestFor(OrderService.class) / @TestFor(com.example.OrderService) /
// @TestFor OrderService / @testfor: order.OrderService / @TestFor=OrderService
var explicitAnnotationRe = regexp.MustCompile(`(?i)@TestFor\s*[:=(]?\s*([\w.]+)\s*\)?`)

// parseExplicitTargets 从一段文档注释中提取所有 @TestFor 标注的目标类标识。
func parseExplicitTargets(doc string) []string {
	if doc == "" {
		return nil
	}
	matches := explicitAnnotationRe.FindAllStringSubmatch(doc, -1)
	if len(matches) == 0 {
		return nil
	}
	targets := make([]string, 0, len(matches))
	for _, m := range matches {
		targets = append(targets, m[1])
	}
	return targets
}

// resolveExplicitClass 将 @TestFor 捕获的标识解析为已知类 FullName。
//
// 解析优先级：
//  1. 全限定名精确匹配（token 含 "." 且等于某类 FullName）
//  2. 全限定名后缀匹配（token 是某类 FullName 的尾部，如 com.example.OrderService 匹配 OrderService）
//  3. 简单类名匹配（取 FQN 最后一段与 token 相等）
func resolveExplicitClass(token string, allClasses map[string]store.ClassRecord) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}

	// 1. 精确匹配
	if _, ok := allClasses[token]; ok {
		return token
	}

	// 1. 全限定名后缀匹配
	var suffixMatches []string
	// 2. 简单类名匹配
	var simpleMatches []string
	for fqn := range allClasses {
		if strings.HasSuffix(fqn, "."+token) {
			suffixMatches = append(suffixMatches, fqn)
			continue
		}
		parts := strings.Split(fqn, ".")
		if parts[len(parts)-1] == token {
			simpleMatches = append(simpleMatches, fqn)
		}
	}
	// 多命中时按 FQN 字典序取首个，保证跨请求确定。
	if len(suffixMatches) > 0 {
		sort.Strings(suffixMatches)
		return suffixMatches[0]
	}
	if len(simpleMatches) > 0 {
		sort.Strings(simpleMatches)
		return simpleMatches[0]
	}
	return ""
}

// linkByExplicit 通过显式 @TestFor 注解建立关联（置信度 100）。
//
// 注解解析在快照内预建为「目标类 → 测试方法」反向索引；仅当注解指向被查询
// 方法所属的类时才建立关联，且只关联被查询的那个方法（P1-12）。
func (s *Service) linkByExplicit(ctx context.Context, snap *testLinkSnapshot, methodFQN string, add func(string, string, string, int)) {
	targetClassFQN := extractClassFQN(methodFQN)
	targetMethodName := methodFQN
	if idx := strings.LastIndex(methodFQN, "."); idx >= 0 {
		targetMethodName = methodFQN[idx+1:]
	}

	for _, m := range snap.explicitMethods(targetClassFQN) {
		add(m.FullName, targetMethodName, "explicit", 100)
	}
}

// linkByDependency 通过依赖递归建立关联。
//
// 策略：目标方法所在类 → 同一目录下的测试类 + 引用该类的测试类。
// 两个反向索引在快照内预建，按快照顺序取候选，语义与逐文件扫描一致。
func (s *Service) linkByDependency(ctx context.Context, snap *testLinkSnapshot, methodFQN string, add func(string, string, string, int)) {
	// 目标方法所在的文件（旧实现：第一个包含该方法的文件生效）。
	targetFile := snap.fileOfMethod[methodFQN]
	if targetFile == nil {
		return
	}

	// 策略1: 同一目录下的测试文件（仅 Go：同目录 *_test.go 属于同一包，构成覆盖证据）
	targetDir := dirFromPath(targetFile.AbsolutePath)
	for _, o := range snap.testOwnersInDir(targetDir) {
		if o.file.ID == targetFile.ID {
			continue
		}
		for _, m := range o.methods {
			add(m.FullName, m.Name, "dependency", 80)
		}
	}

	// 策略2: 引用目标文件的测试类（import 末段等于目标目录名）。
	for _, o := range snap.testOwnersImporting(targetDir) {
		if o.file.ID == targetFile.ID {
			continue
		}
		for _, m := range o.methods {
			add(m.FullName, m.Name, "dependency", 80)
		}
	}
}

// extractClassFQN 从方法 FQN 中提取类 FQN。
//
// 例如："com.example.OrderService.getOrder" → "com.example.OrderService"
func extractClassFQN(methodFQN string) string {
	lastDot := strings.LastIndex(methodFQN, ".")
	if lastDot < 0 {
		return methodFQN
	}
	return methodFQN[:lastDot]
}

// dirFromPath 从文件路径中提取目录部分。
//
// 例如："/project/internal/payment/service.go" → "/project/internal/payment"
func dirFromPath(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	lastSlash := strings.LastIndex(path, "/")
	if lastSlash < 0 {
		return ""
	}
	return path[:lastSlash]
}

// sortByConfidenceDesc 按置信度降序排序。
func sortByConfidenceDesc(links []TestLink) {
	for i := 0; i < len(links); i++ {
		maxIdx := i
		for j := i + 1; j < len(links); j++ {
			if links[j].Confidence > links[maxIdx].Confidence {
				maxIdx = j
			}
		}
		links[i], links[maxIdx] = links[maxIdx], links[i]
	}
}
