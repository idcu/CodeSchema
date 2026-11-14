package service

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/idcu/codeschema/internal/store"
)

// testLinkClassOwner 是一个类及其方法，保持「GetAllFiles → 每文件类序 → 类内方法序」。
//
// 五种关联策略原本各自做 6~8 轮全量扫描；快照把存储读取合并为一次构建，
// 策略只在这份有序内存视图上遍历，输出顺序与逐条结果保持不变。
type testLinkClassOwner struct {
	file    *store.FileRecord
	cls     store.ClassRecord
	methods []store.MethodRecord
}

// testLinkSnapshot 一次 FindTestLinks 所需的不可变存储视图。
//
// classes/methods 在构建时一次性读取；calls/tags 按需惰性读取并缓存
// （测试类之外的文件很少被访问，避免为全量文件付出额外查询）。
type testLinkSnapshot struct {
	st    store.Store
	files []*store.FileRecord

	owners       []*testLinkClassOwner
	ownersByFQN  map[string][]*testLinkClassOwner
	ownersByFile map[int64][]*testLinkClassOwner

	allClasses     map[string]store.ClassRecord
	classesByDir   map[string][]store.ClassRecord
	fileDir        map[int64]string
	knownTypes     map[string]bool
	testClasses    map[string][]string
	testOwners     []*testLinkClassOwner
	fileIsTest     map[int64]bool
	methodsByFile  map[int64][]store.MethodRecord
	methodsByClass map[int64][]store.MethodRecord

	// same_tag：旧实现“最后一个匹配类”生效；dependency：旧实现“第一个匹配文件”生效。
	methodNameByFQN map[string]string
	classIDByMethod map[string]int64
	fileOfMethod    map[string]*store.FileRecord

	callsMu     sync.Mutex
	callsByFile map[int64][]store.CallRecord
	callsLoaded map[int64]bool

	tagsMu      sync.Mutex
	tagsByClass map[int64][]string
	tagsLoaded  map[int64]bool

	indexOnce  sync.Once
	exact      map[string]struct{}
	shortIndex map[string][]string

	// 反向索引：把「逐次全量扫描」降为按目标取有序子集。
	// 所有列表都按快照原始遍历顺序构建，保证关联结果的顺序不变。
	explicitOnce    sync.Once
	explicitByClass map[string][]store.MethodRecord

	callersOnce     sync.Once
	callersByMethod map[string][]string

	depOnce               sync.Once
	goTestOwnersByDir     map[string][]*testLinkClassOwner
	testOwnersByImportSeg map[string][]*testLinkClassOwner

	// same_tag 的匹配信号与「当前符号」无关：测试名切词、词连接集、
	// 测试类标签集合都在首次使用时预计算，符号循环只做查表。
	sameTagOnce sync.Once
	sameTagIdx  *sameTagIndex
}

// buildTestLinkSnapshot 单次遍历「文件→类→方法」构建快照。
func (s *Service) buildTestLinkSnapshot(ctx context.Context, files []*store.FileRecord) *testLinkSnapshot {
	snap := &testLinkSnapshot{
		st:              s.store,
		files:           files,
		ownersByFQN:     make(map[string][]*testLinkClassOwner),
		ownersByFile:    make(map[int64][]*testLinkClassOwner),
		allClasses:      make(map[string]store.ClassRecord),
		classesByDir:    make(map[string][]store.ClassRecord),
		fileDir:         make(map[int64]string, len(files)),
		knownTypes:      make(map[string]bool),
		fileIsTest:      make(map[int64]bool),
		methodsByFile:   make(map[int64][]store.MethodRecord),
		methodsByClass:  make(map[int64][]store.MethodRecord),
		methodNameByFQN: make(map[string]string),
		classIDByMethod: make(map[string]int64),
		fileOfMethod:    make(map[string]*store.FileRecord),
		callsByFile:     make(map[int64][]store.CallRecord),
		callsLoaded:     make(map[int64]bool),
		tagsByClass:     make(map[int64][]string),
		tagsLoaded:      make(map[int64]bool),
	}

	for _, f := range files {
		dir := dirFromPath(f.AbsolutePath)
		snap.fileDir[f.ID] = dir
		snap.fileIsTest[f.ID] = isTestFileName(f.AbsolutePath)

		classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
		for _, cls := range classes {
			methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
			owner := &testLinkClassOwner{file: f, cls: cls, methods: methods}
			snap.owners = append(snap.owners, owner)
			snap.ownersByFQN[cls.FullName] = append(snap.ownersByFQN[cls.FullName], owner)
			snap.ownersByFile[f.ID] = append(snap.ownersByFile[f.ID], owner)
			snap.allClasses[cls.FullName] = cls
			snap.classesByDir[dir] = append(snap.classesByDir[dir], cls)
			snap.methodsByClass[cls.ID] = methods
			if isTestClass(cls, f.AbsolutePath) {
				snap.fileIsTest[f.ID] = true
				snap.testOwners = append(snap.testOwners, owner)
			}
			for _, m := range methods {
				snap.methodsByFile[f.ID] = append(snap.methodsByFile[f.ID], m)
				snap.methodNameByFQN[m.FullName] = m.Name
				snap.classIDByMethod[m.FullName] = cls.ID
				if _, ok := snap.fileOfMethod[m.FullName]; !ok {
					snap.fileOfMethod[m.FullName] = f
				}
			}
		}
	}

	for fqn := range snap.allClasses {
		snap.knownTypes[fqn] = true
		snap.knownTypes[shortMethodName(fqn)] = true
	}
	snap.testClasses = s.discoverTestClasses(snap)
	return snap
}

// discoverTestClasses 发现所有测试类及其对应的源类映射（与旧实现逐条同序）。
func (s *Service) discoverTestClasses(snap *testLinkSnapshot) map[string][]string {
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

	// 类名约定。
	for _, e := range snap.owners {
		if testClass := s.matchTestClass(e.cls); testClass != "" {
			add(testClass, e.cls.FullName)
		}
	}

	// Go 包级测试：*_test.go 的 PACKAGE 容器 → 同目录全部生产类。
	prodByDir := make(map[string][]string)
	for _, e := range snap.owners {
		if isTestFileName(e.file.AbsolutePath) {
			continue
		}
		dir := dirFromPath(e.file.AbsolutePath)
		prodByDir[dir] = append(prodByDir[dir], e.cls.FullName)
	}
	for _, e := range snap.owners {
		if e.cls.Type != "PACKAGE" || !isGoTestFile(e.file.AbsolutePath) {
			continue
		}
		for _, prodClass := range prodByDir[dirFromPath(e.file.AbsolutePath)] {
			add(prodClass, e.cls.FullName)
		}
	}

	return result
}

// methodsOf 返回类的方法（构建时已读取）。
func (snap *testLinkSnapshot) methodsOf(classID int64) []store.MethodRecord {
	return snap.methodsByClass[classID]
}

// callsOf 按需读取文件调用边并缓存（旧实现每文件都读，语义相同）。
func (snap *testLinkSnapshot) callsOf(ctx context.Context, fileID int64) []store.CallRecord {
	snap.callsMu.Lock()
	defer snap.callsMu.Unlock()
	if snap.callsLoaded[fileID] {
		return snap.callsByFile[fileID]
	}
	calls, _ := snap.st.GetCallsByFileID(ctx, fileID)
	snap.callsByFile[fileID] = calls
	snap.callsLoaded[fileID] = true
	return calls
}

// tagsOf 按需读取类标签并缓存。
func (snap *testLinkSnapshot) tagsOf(ctx context.Context, classID int64) []string {
	snap.tagsMu.Lock()
	defer snap.tagsMu.Unlock()
	if snap.tagsLoaded[classID] {
		return snap.tagsByClass[classID]
	}
	tags, _ := snap.st.GetTagsByClassID(ctx, classID)
	snap.tagsByClass[classID] = tags
	snap.tagsLoaded[classID] = true
	return tags
}

// calleeIndex 预建 call_graph 的 FQN 全名/短名索引（跨调用复用）。
func (snap *testLinkSnapshot) calleeIndex() (map[string]struct{}, map[string][]string) {
	snap.indexOnce.Do(func() {
		exact := make(map[string]struct{})
		shortIndex := make(map[string][]string)
		shortSeen := make(map[string]map[string]struct{})
		for _, o := range snap.owners {
			for _, m := range o.methods {
				exact[m.FullName] = struct{}{}
				short := shortMethodName(m.FullName)
				if shortSeen[short] == nil {
					shortSeen[short] = make(map[string]struct{})
				}
				if _, ok := shortSeen[short][m.FullName]; ok {
					continue
				}
				shortSeen[short][m.FullName] = struct{}{}
				shortIndex[short] = append(shortIndex[short], m.FullName)
			}
		}
		snap.exact, snap.shortIndex = exact, shortIndex
	})
	return snap.exact, snap.shortIndex
}

// resolveCallee 把调用边里的 callee 归一为快照内的方法 FQN。
//
// 全名优先；带包/模块前缀的 callee 逐级去掉前缀做后缀精确匹配
// （Go selector 产出 pkg.Type.Method，而生产方法索引键为 Type.Method）；最后接受
// 全局唯一短名。后缀匹配要求索引中存在完全相同的 Type.Method，不会放宽到模糊名。
func (snap *testLinkSnapshot) resolveCallee(callee string) (string, bool) {
	exact, shortIndex := snap.calleeIndex()
	if callee == "" {
		return "", false
	}
	if _, ok := exact[callee]; ok {
		return callee, true
	}
	parts := strings.Split(callee, ".")
	for i := 1; i < len(parts); i++ {
		suffix := strings.Join(parts[i:], ".")
		if _, ok := exact[suffix]; ok {
			return suffix, true
		}
	}
	if cands := shortIndex[shortMethodName(callee)]; len(cands) == 1 {
		return cands[0], true
	}
	return "", false
}

// explicitMethods 返回 @TestFor 指向 classFQN 的测试方法（快照顺序）。
func (snap *testLinkSnapshot) explicitMethods(classFQN string) []store.MethodRecord {
	snap.explicitOnce.Do(snap.buildExplicitIndex)
	return snap.explicitByClass[classFQN]
}

func (snap *testLinkSnapshot) buildExplicitIndex() {
	idx := make(map[string][]store.MethodRecord)
	for _, o := range snap.owners {
		cls := o.cls
		if !isTestClass(cls, o.file.AbsolutePath) {
			continue
		}
		classTargets := parseExplicitTargets(cls.Doc)
		for _, m := range o.methods {
			targets := parseExplicitTargets(m.Doc)
			if len(targets) == 0 {
				targets = classTargets
			}
			for _, tok := range targets {
				if resolved := resolveExplicitClass(tok, snap.allClasses); resolved != "" {
					idx[resolved] = append(idx[resolved], m)
				}
			}
		}
	}
	snap.explicitByClass = idx
}

// callersOf 返回直接调用 methodFQN 的测试方法（快照顺序）。
func (snap *testLinkSnapshot) callersOf(ctx context.Context, methodFQN string) []string {
	snap.callersOnce.Do(func() { snap.buildCallerIndex(ctx) })
	return snap.callersByMethod[methodFQN]
}

func (snap *testLinkSnapshot) buildCallerIndex(ctx context.Context) {
	idx := make(map[string][]string)
	for _, f := range snap.files {
		if !snap.fileIsTest[f.ID] {
			continue
		}
		methods := snap.methodsByFile[f.ID]
		for _, c := range snap.callsOf(ctx, f.ID) {
			prod, ok := snap.resolveCallee(c.CalleeFQN)
			if !ok {
				continue
			}
			caller := normalizeCallerFQN(c.CallerFQN, methods)
			if caller == "" || caller == prod {
				continue
			}
			idx[prod] = append(idx[prod], caller)
		}
	}
	snap.callersByMethod = idx
}

// testOwnersInDir 返回 dir 下 Go 文件的测试类（快照顺序）。
func (snap *testLinkSnapshot) testOwnersInDir(dir string) []*testLinkClassOwner {
	snap.depOnce.Do(snap.buildDependencyIndexes)
	return snap.goTestOwnersByDir[dir]
}

// testOwnersImporting 返回 import 末段为 dir 的文件的测试类（快照顺序）。
func (snap *testLinkSnapshot) testOwnersImporting(dir string) []*testLinkClassOwner {
	snap.depOnce.Do(snap.buildDependencyIndexes)
	return snap.testOwnersByImportSeg[dir]
}

// buildDependencyIndexes 预建 dependency 策略的两个反向索引。
//
// 原实现每个目标都遍历全部文件两次；这里按「文件顺序 → owner 顺序」建表，
// 保证取出的候选顺序与原实现一致。
func (snap *testLinkSnapshot) buildDependencyIndexes() {
	byDir := make(map[string][]*testLinkClassOwner)
	byImport := make(map[string][]*testLinkClassOwner)
	for _, f := range snap.files {
		owners := snap.ownersByFile[f.ID]
		if strings.EqualFold(f.Language, "go") {
			dir := dirFromPath(f.AbsolutePath)
			for _, o := range owners {
				if isTestClass(o.cls, f.AbsolutePath) {
					byDir[dir] = append(byDir[dir], o)
				}
			}
		}
		if len(f.Imports) == 0 {
			continue
		}
		seen := make(map[string]bool, len(f.Imports))
		for _, imp := range f.Imports {
			imp = strings.TrimSpace(imp)
			if imp == "" {
				continue
			}
			imp = strings.ReplaceAll(imp, "\\", "/")
			seg := imp
			if i := strings.LastIndex(seg, "/"); i >= 0 {
				seg = seg[i+1:]
			}
			if seg == "" || seen[seg] {
				continue
			}
			seen[seg] = true
			for _, o := range owners {
				if isTestClass(o.cls, f.AbsolutePath) {
					byImport[seg] = append(byImport[seg], o)
				}
			}
		}
	}
	snap.goTestOwnersByDir = byDir
	snap.testOwnersByImportSeg = byImport
}

// testLinkEntry 一代索引 + 文件指纹对应的快照。
type testLinkEntry struct {
	gen  uint64
	fp   uint64
	snap *testLinkSnapshot
}

// cachedTestLinkSnapshot 返回当前代次/指纹的快照；未命中时串行构建并原子发布。
//
// 指纹让“未显式失效”的文件增删/内容变更也能被感知；InvalidateDerivedCaches
// 递增代次并清空指针，保证重索引后立即失效。
func (s *Service) cachedTestLinkSnapshot(ctx context.Context, files []*store.FileRecord) *testLinkSnapshot {
	fp := testLinkFingerprint(files)
	if e := s.testLinkCache.Load(); e != nil && e.gen == s.testLinkGen.Load() && e.fp == fp {
		return e.snap
	}

	s.testLinkBuildMu.Lock()
	defer s.testLinkBuildMu.Unlock()
	gen := s.testLinkGen.Load()
	if e := s.testLinkCache.Load(); e != nil && e.gen == gen && e.fp == fp {
		return e.snap
	}

	// 构建期间若发生失效，本次结果可能混合两代数据：重试一次；
	// 持续写入下不再无限重试，直接返回本次结果且不写缓存。
	for attempt := 0; ; attempt++ {
		g := s.testLinkGen.Load()
		snap := s.buildTestLinkSnapshot(ctx, files)
		if s.testLinkGen.Load() == g {
			s.testLinkCache.Store(&testLinkEntry{gen: g, fp: fp, snap: snap})
			return snap
		}
		if attempt >= 1 {
			return snap
		}
	}
}

// invalidateTestLinkSnapshot 递增代次并清空快照（重索引 / 增量更新后调用）。
func (s *Service) invalidateTestLinkSnapshot() {
	s.testLinkGen.Add(1)
	s.testLinkCache.Store(nil)
}

// testLinkFingerprint 计算与文件顺序无关的轻量指纹：
// 每个文件的 ID/路径/语言/内容哈希/imports 做 FNV-1a，再按文件 XOR 汇总。
//
// GetAllFiles 在 file 后端可能来自 map 遍历（顺序不稳定），因此不能用顺序相关哈希；
// XOR 对顺序不敏感，同时能感知文件增删与内容变更。内联 FNV 避免每次调用为
// 一万个文件各分配一个 hasher（/affected 会对每个受影响符号调用一次）。
func testLinkFingerprint(files []*store.FileRecord) uint64 {
	var fp uint64
	for _, f := range files {
		h := uint64(fnvOffset64)
		id := uint64(f.ID)
		for i := 0; i < 8; i++ {
			h = fnvByte(h, byte(id))
			id >>= 8
		}
		h = fnvBytes(h, f.AbsolutePath)
		h = fnvByte(h, 0)
		h = fnvBytes(h, f.Language)
		h = fnvByte(h, 0)
		h = fnvBytes(h, f.ContentHash)
		h = fnvByte(h, 0)
		for _, imp := range f.Imports {
			h = fnvBytes(h, imp)
			h = fnvByte(h, 0)
		}
		fp ^= h
	}
	return fp ^ (uint64(len(files)) * 0x9e3779b97f4a7c15)
}

const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

func fnvByte(h uint64, b byte) uint64 {
	return (h ^ uint64(b)) * fnvPrime64
}

func fnvBytes(h uint64, s string) uint64 {
	for i := 0; i < len(s); i++ {
		h = (h ^ uint64(s[i])) * fnvPrime64
	}
	return h
}

// sameTagMethod 是 same_tag 策略的测试方法条目，按快照原始遍历顺序编号。
type sameTagMethod struct {
	fullName string
	name     string
	classID  int64
}

// sameTagIndex 预计算 same_tag 的匹配信号：
//   - 测试名一次切词，得到「词连接集」，查询时不再逐候选重建 Builder；
//   - 测试类标签一次转成集合，查询只做成员判断；
//   - 四类命名规则各建一张反向表（精确 / 去前缀 / 词连接 / 前缀边界）。
//
// 每张表的 value 都按快照遍历顺序追加，因此单独取出即有序；多表并集再排序
// 去重，得到与「逐测试类、逐方法」遍历完全一致的输出顺序。
type sameTagIndex struct {
	methods   []sameTagMethod
	classTags map[int64]map[string]bool

	byExact    map[string][]int32
	byStripped map[string][]int32
	byWords    map[string][]int32
	byPrefix   map[string][]int32
}

// sameTagStrippedPrefixes 对应 matchesTestMethod 的「前缀 + 精确余量」规则。
var sameTagStrippedPrefixes = [...]string{"Test", "test", "test_"}

// descriptiveTestPrefixes 对应 matchesTestMethod 的「描述型测试名」判定。
var descriptiveTestPrefixes = [...]string{
	"Test", "test", "test_", "Benchmark", "benchmark", "Example", "example",
	"Should", "should_", "when_",
}

// sameTagIndex 惰性构建一次；与快照同代，失效时随快照一起丢弃。
func (snap *testLinkSnapshot) sameTagIndex(ctx context.Context) *sameTagIndex {
	snap.sameTagOnce.Do(func() {
		idx := &sameTagIndex{
			classTags:  make(map[int64]map[string]bool),
			byExact:    make(map[string][]int32),
			byStripped: make(map[string][]int32),
			byWords:    make(map[string][]int32),
			byPrefix:   make(map[string][]int32),
		}
		for _, o := range snap.testOwners {
			if tags := snap.tagsOf(ctx, o.cls.ID); len(tags) > 0 {
				set := make(map[string]bool, len(tags))
				for _, t := range tags {
					set[t] = true
				}
				idx.classTags[o.cls.ID] = set
			}
			for _, m := range o.methods {
				pos := int32(len(idx.methods))
				idx.methods = append(idx.methods, sameTagMethod{
					fullName: m.FullName,
					name:     m.Name,
					classID:  o.cls.ID,
				})
				idx.add(m.Name, pos)
			}
		}
		snap.sameTagIdx = idx
	})
	return snap.sameTagIdx
}

// add 把测试方法名登记进四类反向表。
func (idx *sameTagIndex) add(name string, pos int32) {
	idx.byExact[name] = append(idx.byExact[name], pos)
	for _, prefix := range sameTagStrippedPrefixes {
		if strings.HasPrefix(name, prefix) {
			rest := strings.TrimPrefix(name, prefix)
			idx.byStripped[rest] = append(idx.byStripped[rest], pos)
		}
	}
	if isDescriptiveTestName(name) {
		for _, joined := range nameWordJoins(name) {
			idx.byWords[joined] = append(idx.byWords[joined], pos)
		}
	}
	if len(name) >= 4 {
		idx.byPrefix[name] = append(idx.byPrefix[name], pos)
		for i := 4; i < len(name); i++ {
			if c := name[i]; c == '_' || (c >= 'A' && c <= 'Z') {
				idx.byPrefix[name[:i]] = append(idx.byPrefix[name[:i]], pos)
			}
		}
	}
}

// isDescriptiveTestName 与 matchesTestMethod 的前缀集合保持一致。
func isDescriptiveTestName(name string) bool {
	for _, prefix := range descriptiveTestPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// nameWordJoins 返回测试名切词后所有连续片段的小写拼接（即 containsNameWords
// 可能命中的全部目标归一形式）。
func nameWordJoins(name string) []string {
	words := splitNameWords(name)
	joins := make([]string, 0, len(words))
	for i := range words {
		var b strings.Builder
		for j := i; j < len(words); j++ {
			b.WriteString(strings.ToLower(words[j]))
			joins = append(joins, b.String())
		}
	}
	return joins
}

// candidates 取目标方法名在四张表里的位置并集，升序去重后复用调用方的缓冲区。
//
// 位置升序 = 快照里「测试类顺序 → 类内方法顺序」，与旧实现的 add 顺序一致。
func (idx *sameTagIndex) candidates(target string, dst []int32) []int32 {
	dst = append(dst, idx.byExact[target]...)
	dst = append(dst, idx.byStripped[target]...)
	if norm := strings.ToLower(strings.ReplaceAll(target, "_", "")); norm != "" {
		dst = append(dst, idx.byWords[norm]...)
	}
	if len(target) >= 4 {
		dst = append(dst, idx.byPrefix[target]...)
	}
	if len(dst) < 2 {
		return dst
	}
	slices.Sort(dst)
	out := dst[:1]
	for _, pos := range dst[1:] {
		if pos != out[len(out)-1] {
			out = append(out, pos)
		}
	}
	return out
}
