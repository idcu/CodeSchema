// Package service 提供测试关联策略实现。
//
// 六种策略（按置信度降序）：
//   - explicit  (100): 显式注解匹配（@TestFor(...) 解析目标类）
//   - call_graph(95):  测试方法直接调用目标方法（与候选类命名无关）
//   - coverage  (90):  覆盖率数据反查（注入 coverage 报告）
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
	if methodFQN == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "methodFQN is required"}
	}
	if minConfidence <= 0 {
		minConfidence = 60
	}

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

	// 获取所有文件数据
	files, err := s.store.GetAllFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all files: %w", err)
	}

	// 查找目标方法所在的类
	targetClass := extractClassFQN(methodFQN)
	allTestClasses := s.discoverTestClasses(ctx, files)

	// 预构建全量类映射（classFQN -> ClassRecord），供 explicit 策略解析目标类
	allClasses := make(map[string]store.ClassRecord)
	for _, f := range files {
		classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
		for _, cls := range classes {
			allClasses[cls.FullName] = cls
		}
	}

	// 0. explicit 策略：@TestFor(...) 显式注解（置信度 100）
	s.linkByExplicit(ctx, files, allClasses, methodFQN, addResult)

	// 1. coverage 策略：覆盖率数据反查（置信度 90）
	s.linkByCoverage(methodFQN, addResult)

	// 2. naming 策略：类名匹配（OrderService ↔ OrderServiceTest）
	//    同一源类可能有多个测试类（OrderServiceTest / TestOrderService），全部参与。
	for _, testClassFQN := range allTestClasses[targetClass] {
		s.linkByNaming(ctx, files, targetClass, testClassFQN, methodFQN, addResult)
	}

	// 2.5 call_graph 策略：测试方法直接调用目标方法。
	//      行为型测试名（TestCollection_Delete 之于 CreateCollection）与跨类测试
	//      都不在 naming 的候选集里，直接调用边与类名约定无关。
	s.linkByCallGraph(ctx, files, methodFQN, addResult)

	// 3. same_tag 策略：同 Tag 聚类
	s.linkBySameTag(ctx, files, methodFQN, addResult)

	// 4. dependency 策略：依赖递归
	s.linkByDependency(ctx, files, methodFQN, addResult)

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
		for i := 0; i < n; i++ {
			metrics.IncCounter("testlink_hits_total", strat)
		}
	}

	// 过滤置信度
	filtered := make([]TestLink, 0, len(results))
	for _, r := range results {
		if r.Confidence >= minConfidence {
			filtered = append(filtered, r)
		}
	}

	return filtered, nil
}

// discoverTestClasses 发现所有测试类及其对应的源类映射。
//
// 命名约定：OrderServiceTest ↔ OrderService（去掉 Test 后缀/前缀）
//
// 额外覆盖 Go 包级测试：Go 测试函数（TestXxx/BenchmarkXxx/ExampleXxx）
// 挂在合成 PACKAGE 容器下，容器名是包名而非「*Test/Test*」类名形态，
// 因此按「同目录同包」把测试容器归属到该目录的全部生产类。
func (s *Service) discoverTestClasses(ctx context.Context, files []*store.FileRecord) map[string][]string {
	result := make(map[string][]string) // classFQN -> []testClassFQN
	seen := make(map[string]map[string]bool)
	add := func(source, test string) {
		if source == "" || test == "" {
			return
		}
		if seen[source] == nil {
			seen[source] = make(map[string]bool)
		}
		if seen[source][test] {
			return
		}
		seen[source][test] = true
		result[source] = append(result[source], test)
	}

	type classEntry struct {
		file *store.FileRecord
		cls  store.ClassRecord
	}
	var entries []classEntry
	for _, f := range files {
		classes, err := s.store.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			continue
		}
		for _, cls := range classes {
			entries = append(entries, classEntry{file: f, cls: cls})
		}
	}

	// 类名约定。
	for _, e := range entries {
		if testClass := s.matchTestClass(e.cls); testClass != "" {
			add(testClass, e.cls.FullName)
		}
	}

	// Go 包级测试：*_test.go 的 PACKAGE 容器 → 同目录全部生产类。
	prodByDir := make(map[string][]string)
	for _, e := range entries {
		if isTestFileName(e.file.AbsolutePath) {
			continue
		}
		dir := dirFromPath(e.file.AbsolutePath)
		prodByDir[dir] = append(prodByDir[dir], e.cls.FullName)
	}
	for _, e := range entries {
		if e.cls.Type != "PACKAGE" || !isGoTestFile(e.file.AbsolutePath) {
			continue
		}
		for _, prodClass := range prodByDir[dirFromPath(e.file.AbsolutePath)] {
			add(prodClass, e.cls.FullName)
		}
	}

	return result
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
func (s *Service) linkByNaming(ctx context.Context, files []*store.FileRecord, classFQN, testClassFQN string, methodFQN string, add func(string, string, string, int)) {
	// 找到目标类中的方法
	var targetMethod store.MethodRecord
	found := false
	for _, f := range files {
		classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
		for _, cls := range classes {
			if cls.FullName != classFQN {
				continue
			}
			methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
			for _, m := range methods {
				if m.FullName == methodFQN {
					targetMethod = m
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		return
	}

	// 在测试类中查找同名方法（或带 Test 前缀/后缀的方法）
	methodName := targetMethod.Name
	for _, f := range files {
		classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
		for _, cls := range classes {
			if cls.FullName != testClassFQN {
				continue
			}
			// 生产文件的 PACKAGE 容器与测试容器同名（Go 包名），不能当测试类扫描。
			if cls.Type == "PACKAGE" && !isGoTestFile(f.AbsolutePath) {
				continue
			}
			methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
			// Go 包级容器的同包 helper（keysOfSets/newMockLSPAdapterMode）不是测试，
			// 只让 Test/Benchmark/Example 前缀的函数参与命名匹配。
			goPkg := cls.Type == "PACKAGE" && isGoTestFile(f.AbsolutePath)
			for _, m := range methods {
				if goPkg && !isGoTestFuncName(m.Name) {
					continue
				}
				if matchesTestMethod(methodName, m.Name) {
					add(m.FullName, methodName, "naming", 70)
				}
			}
		}
	}
}

// linkByCallGraph 通过「测试方法 → 生产方法」的直接调用边建立关联（置信度 95）。
//
// 与 naming 的差别：naming 只在「与目标类同名的测试类」里按方法名匹配，
// 行为型测试名与跨类测试都不在候选集。调用边与类名约定无关，可覆盖这两类漏召。
//
// FQN 归一（基准暴露）：
//   - callee 可能是接收者变量形态（entity.setName）；短名在全部方法中唯一时解析到唯一 FQN。
//   - caller 可能带包名前缀（org...ClinicServiceTests.shouldFindVets）；
//     按文件内测试方法 FQN 的后缀归一，保证返回口径与其它策略一致。
func (s *Service) linkByCallGraph(ctx context.Context, files []*store.FileRecord, methodFQN string, add func(string, string, string, int)) {
	type fileInfo struct {
		test    bool
		methods []store.MethodRecord
	}
	infos := make(map[int64]*fileInfo, len(files))
	exact := make(map[string]struct{})
	shortIndex := make(map[string][]string)
	for _, f := range files {
		info := &fileInfo{test: isTestFileName(f.AbsolutePath)}
		classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
		for _, cls := range classes {
			if isTestClass(cls, f.AbsolutePath) {
				info.test = true
			}
			methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
			for _, m := range methods {
				info.methods = append(info.methods, m)
				exact[m.FullName] = struct{}{}
				shortIndex[shortMethodName(m.FullName)] = append(shortIndex[shortMethodName(m.FullName)], m.FullName)
			}
		}
		infos[f.ID] = info
	}

	// resolveCallee 与基准真值同口径：全名优先，短名唯一时接受变量形态。
	resolveCallee := func(callee string) (string, bool) {
		if callee == "" {
			return "", false
		}
		if _, ok := exact[callee]; ok {
			return callee, true
		}
		if cands := shortIndex[shortMethodName(callee)]; len(cands) == 1 {
			return cands[0], true
		}
		return "", false
	}

	for _, f := range files {
		info := infos[f.ID]
		if info == nil || !info.test {
			continue
		}
		calls, _ := s.store.GetCallsByFileID(ctx, f.ID)
		for _, c := range calls {
			if prod, ok := resolveCallee(c.CalleeFQN); !ok || prod != methodFQN {
				continue
			}
			caller := normalizeCallerFQN(c.CallerFQN, info.methods)
			if caller == "" || caller == methodFQN {
				continue
			}
			add(caller, shortMethodName(methodFQN), "call_graph", 95)
		}
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
func (s *Service) linkBySameTag(ctx context.Context, files []*store.FileRecord, methodFQN string, add func(string, string, string, int)) {
	// 获取目标方法的标签
	var targetClassID int64
	var targetMethodName string

	for _, f := range files {
		classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
		for _, cls := range classes {
			methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
			for _, m := range methods {
				if m.FullName == methodFQN {
					targetMethodName = m.Name
					targetClassID = cls.ID
					break
				}
			}
		}
	}

	if targetClassID == 0 || targetMethodName == "" {
		return
	}

	// 获取目标方法所在类的标签
	targetTags, _ := s.store.GetTagsByClassID(ctx, targetClassID)
	if len(targetTags) == 0 {
		return
	}

	// 查找同标签的测试类
	for _, f := range files {
		classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
		for _, cls := range classes {
			// 跳过自身
			if cls.ID == targetClassID {
				continue
			}
			// 只考虑测试类（含文件级识别）
			if !isTestClass(cls, f.AbsolutePath) {
				continue
			}

			clsTags, _ := s.store.GetTagsByClassID(ctx, cls.ID)
			if hasCommonTag(targetTags, clsTags) {
				methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
				for _, m := range methods {
					if !matchesTestMethod(targetMethodName, m.Name) {
						continue
					}
					add(m.FullName, m.Name, "same_tag", 60)
				}
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

// hasCommonTag 检查两个标签列表是否有共同标签。
func hasCommonTag(a, b []string) bool {
	set := make(map[string]bool)
	for _, t := range a {
		set[t] = true
	}
	for _, t := range b {
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
// 遍历所有测试类/测试方法，解析其 Doc 中的 @TestFor(<目标类>) 注解；
// 仅当注解指向被查询方法所属的类时才建立关联，且只关联被查询的那个方法
// （P1-12：此前会把任意 @TestFor 的测试塞给所有生产方法）。
func (s *Service) linkByExplicit(ctx context.Context, files []*store.FileRecord, allClasses map[string]store.ClassRecord, methodFQN string, add func(string, string, string, int)) {
	targetClassFQN := extractClassFQN(methodFQN)
	targetMethodName := methodFQN
	if idx := strings.LastIndex(methodFQN, "."); idx >= 0 {
		targetMethodName = methodFQN[idx+1:]
	}

	for _, f := range files {
		classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
		for _, cls := range classes {
			if !isTestClass(cls, f.AbsolutePath) {
				continue
			}

			// 类级注解（如 @TestFor(OrderService) 标在测试类上）
			classTargets := parseExplicitTargets(cls.Doc)

			methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
			for _, m := range methods {
				// 方法级注解优先，其次类级注解
				targets := parseExplicitTargets(m.Doc)
				if len(targets) == 0 {
					targets = classTargets
				}
				for _, tok := range targets {
					if resolveExplicitClass(tok, allClasses) != targetClassFQN {
						continue
					}
					add(m.FullName, targetMethodName, "explicit", 100)
				}
			}
		}
	}
}

// linkByCoverage 通过覆盖率数据反查建立关联（置信度 90）。
//
// 使用注入的 coverage 报告（testMethodFQN -> 被覆盖的生产方法 FQN 列表），
// 找到覆盖目标方法的测试方法并关联。
func (s *Service) linkByCoverage(methodFQN string, add func(string, string, string, int)) {
	if len(s.coverage) == 0 {
		return
	}
	for testMethod, covered := range s.coverage {
		for _, prodFQN := range covered {
			if prodFQN == methodFQN {
				// MethodName 取 FQN 最后一段
				name := prodFQN
				if idx := strings.LastIndex(prodFQN, "."); idx >= 0 {
					name = prodFQN[idx+1:]
				}
				add(testMethod, name, "coverage", 90)
			}
		}
	}
}

// linkByDependency 通过依赖递归建立关联。
//
// 策略：目标方法所在类 → 同一目录下的测试类 + 引用该类的测试类。
func (s *Service) linkByDependency(ctx context.Context, files []*store.FileRecord, methodFQN string, add func(string, string, string, int)) {
	// 找到目标方法所在的文件
	var targetFile *store.FileRecord
	for _, f := range files {
		classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
		for _, cls := range classes {
			methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
			for _, m := range methods {
				if m.FullName == methodFQN {
					targetFile = f
					break
				}
			}
			if targetFile != nil {
				break
			}
		}
		if targetFile != nil {
			break
		}
	}

	if targetFile == nil {
		return
	}

	// 构建文件名到类名的映射
	fileToClasses := make(map[int64][]store.ClassRecord)
	for _, f := range files {
		classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
		fileToClasses[f.ID] = classes
	}

	// 策略1: 同一目录下的测试文件（仅 Go：同目录 *_test.go 属于同一包，构成覆盖证据）
	targetDir := dirFromPath(targetFile.AbsolutePath)
	for _, f := range files {
		if f.ID == targetFile.ID {
			continue
		}
		if !strings.EqualFold(f.Language, "go") {
			continue
		}
		if dirFromPath(f.AbsolutePath) != targetDir {
			continue
		}
		classes := fileToClasses[f.ID]
		for _, cls := range classes {
			if isTestClass(cls, f.AbsolutePath) {
				methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
				for _, m := range methods {
					add(m.FullName, m.Name, "dependency", 80)
				}
			}
		}
	}

	// 策略2: 引用目标文件的测试类
	for _, f := range files {
		if f.ID == targetFile.ID {
			continue
		}
		if len(f.Imports) == 0 {
			continue
		}

		referencesTarget := false
		targetPath := strings.ReplaceAll(targetFile.AbsolutePath, "\\", "/")
		targetParts := strings.Split(targetPath, "/")
		// 目标文件所在目录名 = 目标包路径的最后一段，用于 import 精确匹配。
		dirName := ""
		if len(targetParts) > 1 {
			dirName = targetParts[len(targetParts)-2]
		}

		for _, imp := range f.Imports {
			imp = strings.TrimSpace(imp)
			if imp == "" {
				continue
			}
			imp = strings.ReplaceAll(imp, "\\", "/")
			// 仅认「包路径等于目标目录名」或「以 /目标目录名 结尾」的 import，
			// 避免 strings.Contains 把 payment 误配到 mypaymentutil 之类（P2-4）。
			if dirName != "" && (imp == dirName || strings.HasSuffix(imp, "/"+dirName)) {
				referencesTarget = true
				break
			}
		}

		if referencesTarget {
			classes := fileToClasses[f.ID]
			for _, cls := range classes {
				if isTestClass(cls, f.AbsolutePath) {
					methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
					for _, m := range methods {
						add(m.FullName, m.Name, "dependency", 80)
					}
				}
			}
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
