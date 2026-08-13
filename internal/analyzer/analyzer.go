package analyzer

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"codeschema/internal/store"
)

// Analyzer 是代码图分析器，负责构建跨文件的代码关系图。
//
// 从 Store 中读取已解析的 IR 数据，构建以下关系图：
//   - 调用图（CallGraph）：方法间的调用关系
//   - 类层次（ClassHierarchy）：继承/实现关系
//   - 反向引用索引（ReverseIndex）：文件被哪些文件引用
//   - 文件依赖图（FileGraph）：文件间的依赖关系
type Analyzer struct {
	store    store.Store
	resolver *CompositeResolver  // 多语言 import 解析器
	goResolver *GoResolver      // Go 模块路径解析器
	javaResolver *JavaResolver  // Java 包路径解析器
	gradleResolver *GradleResolver // Gradle 多模块路径解析器
}

// NewAnalyzer 创建分析器实例。
//
// 初始化多语言 import 解析器，默认包含：
//   - GoResolver（未设置模块路径时始终回退）
//   - JavaResolver（使用默认源根目录，标准库过滤）
//   - GradleResolver（多模块 : 路径解析）
//   - heuristicResolver（最终的启发式回退）
func NewAnalyzer(st store.Store) *Analyzer {
	goR := NewGoResolver("")
	javaR := NewJavaResolver(nil)
	gradleR := NewGradleResolver(nil, nil)
	composite := NewCompositeResolver(goR, javaR, gradleR, &heuristicResolver{})
	return &Analyzer{
		store:          st,
		resolver:       composite,
		goResolver:     goR,
		javaResolver:   javaR,
		gradleResolver: gradleR,
	}
}

// SetModulePath 设置 Go 模块路径，用于精确 import 解析。
// 例如：go.mod 中的 module 声明 "codeschema"。
func (a *Analyzer) SetModulePath(mp string) {
	a.goResolver.modulePath = mp
}

// SetJavaSourceRoots 设置 Java 源根目录，用于精确 import 解析。
// 默认值：["src/main/java", "src/main/kotlin", "src/test/java", "src/test/kotlin"]。
func (a *Analyzer) SetJavaSourceRoots(roots []string) {
	if len(roots) > 0 {
		a.javaResolver.sourceRoots = roots
	}
}

// SetJavaStdlibPrefixes 设置 Java 标准库/框架前缀列表。
//
// 覆盖默认的 23 个前缀。传入 nil 或空切片表示不过滤任何 import。
func (a *Analyzer) SetJavaStdlibPrefixes(prefixes []string) {
	a.javaResolver.SetStdlibPrefixes(prefixes)
}

// SetGradleModuleNames 设置 Gradle 模块名白名单。
//
// 当设置了白名单后，只有白名单中的模块会被解析。
// 例如：a.SetGradleModuleNames([]string{"app", "core", "lib"})
// 传入 nil 表示不限制模块名。
func (a *Analyzer) SetGradleModuleNames(names []string) {
	a.gradleResolver.moduleNames = names
}

// BuildAll 构建所有代码图（单次遍历）。
//
// 返回所有四种图结构。如果某个图构建失败，返回错误。
func (a *Analyzer) BuildAll(ctx context.Context) (*CallGraph, *ClassHierarchy, *ReverseIndex, *FileGraph, error) {
	callGraph := NewCallGraph()
	classHierarchy := NewClassHierarchy()
	reverseIndex := NewReverseIndex()
	fileGraph := NewFileGraph()

	// 1. 读取所有文件
	files, err := a.store.GetAllFiles(ctx)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("get all files: %w", err)
	}

	// 2. 预构建 import 索引（仅依赖文件路径，无需遍历）
	importIdx := buildImportIndex(files)

	// 3. 单次遍历：构建所有图结构
	for _, f := range files {
		// 构建文件图节点
		fgNode := fileGraph.AddNode(f.AbsolutePath)
		fgNode.FileID = f.ID
		fgNode.Language = f.Language

		// 读取类记录
		classes, err := a.store.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("get classes for file %d: %w", f.ID, err)
		}
		fgNode.ClassCount = len(classes)

		// 构建类层次
		for _, cls := range classes {
			a.buildClassHierarchyNode(classHierarchy, cls, f.ID)
		}

		// 读取调用记录
		calls, err := a.store.GetCallsByFileID(ctx, f.ID)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("get calls for file %d: %w", f.ID, err)
		}

		// 构建调用图
		for _, call := range calls {
			if call.CallerFQN != "" && call.CalleeFQN != "" {
				callGraph.AddEdge(call.CallerFQN, call.CalleeFQN)
			}
		}

		// 统计方法数
		fgNode.MethodCount = a.countMethodsByFileID(ctx, f.ID)

		// 基于 imports 构建反向引用索引和文件依赖边
		for _, imp := range f.Imports {
			imp = strings.TrimSpace(imp)
			if imp == "" {
				continue
			}
			reverseIndex.AddImport(f.AbsolutePath, imp)
			if targets := a.resolveImport(imp, importIdx); len(targets) > 0 {
				for _, target := range targets {
					reverseIndex.AddReference(target, f.AbsolutePath)
					fileGraph.AddEdge(f.AbsolutePath, target)
				}
			}
		}
	}

	return callGraph, classHierarchy, reverseIndex, fileGraph, nil
}

// BuildCallGraph 构建调用图。
//
// 从 Store 中读取所有调用记录，构建方法间的调用关系图。
// 每个调用记录生成一条边（caller -> callee）。
func (a *Analyzer) BuildCallGraph(ctx context.Context) (*CallGraph, error) {
	cg := NewCallGraph()

	files, err := a.store.GetAllFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all files: %w", err)
	}

	for _, f := range files {
		calls, err := a.store.GetCallsByFileID(ctx, f.ID)
		if err != nil {
			return nil, fmt.Errorf("get calls for file %d: %w", f.ID, err)
		}
		for _, call := range calls {
			if call.CallerFQN != "" && call.CalleeFQN != "" {
				cg.AddEdge(call.CallerFQN, call.CalleeFQN)
			}
		}
	}

	return cg, nil
}

// BuildClassHierarchy 构建类层次结构。
//
// 从 Store 中读取所有类记录，构建继承/实现关系树。
// 通过 ClassIR.ParentFQNs 字段建立父子关系。
func (a *Analyzer) BuildClassHierarchy(ctx context.Context) (*ClassHierarchy, error) {
	ch := NewClassHierarchy()

	files, err := a.store.GetAllFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all files: %w", err)
	}

	for _, f := range files {
		classes, err := a.store.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			return nil, fmt.Errorf("get classes for file %d: %w", f.ID, err)
		}
		for _, cls := range classes {
			a.buildClassHierarchyNode(ch, cls, f.ID)
		}
	}

	return ch, nil
}

// buildClassHierarchyNode 将一条类记录添加到类层次结构中。
func (a *Analyzer) buildClassHierarchyNode(ch *ClassHierarchy, cls store.ClassRecord, fileID int64) {
	node := ch.AddNode(cls.FullName)
	node.Type = cls.Type
	node.FileID = fileID

	// 通过 ParentFQNs 建立父子关系
	for _, parent := range cls.ParentFQNs {
		if parent != "" {
			ch.AddParent(cls.FullName, parent)
		}
	}
}

// BuildReverseIndex 构建反向引用索引。
//
// 通过分析每个文件存储的 Imports 元数据，建立文件间的引用关系。
// 对于每个文件的 import 路径，尝试匹配到 Store 中已知的文件路径。
func (a *Analyzer) BuildReverseIndex(ctx context.Context) (*ReverseIndex, error) {
	ri := NewReverseIndex()

	files, err := a.store.GetAllFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all files: %w", err)
	}

	// 1. 构建 import 路径到文件路径的查找映射
	importIndex := buildImportIndex(files)

	// 2. 遍历每个文件，解析其 imports
	for _, f := range files {
		if len(f.Imports) == 0 {
			continue
		}
		for _, imp := range f.Imports {
			imp = strings.TrimSpace(imp)
			if imp == "" {
				continue
			}
			ri.AddImport(f.AbsolutePath, imp)
			if targets := a.resolveImport(imp, importIndex); len(targets) > 0 {
				for _, target := range targets {
					ri.AddReference(target, f.AbsolutePath)
				}
			}
		}
	}

	return ri, nil
}

// buildImportIndex 构建 import 路径到文件路径的快速查找索引。
//
// 对于每个文件，提取其路径中的关键路径段作为索引键。
// 例如："/project/internal/store/store.go" 会生成
// "store"、"internal/store"、"codeschema/internal/store" 等索引键。
func buildImportIndex(files []*store.FileRecord) map[string][]string {
	idx := make(map[string][]string)
	for _, f := range files {
		path := f.AbsolutePath
		normalized := strings.ReplaceAll(path, "\\", "/")
		parts := strings.Split(normalized, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			suffix := strings.Join(parts[i:], "/")
			dirSuffix := suffix
			for _, ext := range []string{".go", ".java", ".ts", ".py", ".rs", ".cpp", ".h"} {
				dirSuffix = strings.TrimSuffix(dirSuffix, ext)
			}
			if dirSuffix != "" && !strings.HasSuffix(dirSuffix, "/") {
				idx[dirSuffix] = append(idx[dirSuffix], f.AbsolutePath)
			}
		}
		base := filepath.Base(path)
		ext := filepath.Ext(base)
		if ext != "" {
			pkgName := strings.TrimSuffix(base, ext)
			idx[pkgName] = append(idx[pkgName], f.AbsolutePath)
		}
	}
	return idx
}

// resolveImport 尝试将 import 路径解析为 Store 中已知的文件路径。
//
// 使用多语言解析器链（CompositeResolver）：
//   - GoResolver: 模块路径精确解析
//   - JavaResolver: Java FQCN/通配符/源根目录解析
//   - heuristicResolver: 启发式回退匹配
func (a *Analyzer) resolveImport(imp string, importIndex map[string][]string) []string {
	return a.resolver.Resolve(imp, importIndex)
}

// BuildFileGraph 构建文件依赖图。
//
// 从 Store 中读取所有文件记录，构建文件间的依赖关系图。
// 包含每个文件的类/方法数量信息。
func (a *Analyzer) BuildFileGraph(ctx context.Context) (*FileGraph, error) {
	fg := NewFileGraph()

	files, err := a.store.GetAllFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all files: %w", err)
	}

	for _, f := range files {
		node := fg.AddNode(f.AbsolutePath)
		node.FileID = f.ID
		node.Language = f.Language

		classes, err := a.store.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			return nil, fmt.Errorf("get classes for file %d: %w", f.ID, err)
		}
		node.ClassCount = len(classes)
		node.MethodCount = a.countMethodsByFileID(ctx, f.ID)
	}

	return fg, nil
}

// countMethodsByFileID 统计指定文件中的方法总数。
func (a *Analyzer) countMethodsByFileID(ctx context.Context, fileID int64) int {
	classes, err := a.store.GetClassesByFileID(ctx, fileID)
	if err != nil {
		return 0
	}
	count := 0
	for _, cls := range classes {
		methods, err := a.store.GetMethodsByClassID(ctx, cls.ID)
		if err != nil {
			continue
		}
		count += len(methods)
	}
	return count
}

// FindImpactNodes 查找指定方法的影响面。
//
// depth 控制递归深度，0 表示不限制。
// 返回所有受影响的调用者和被调用者。
func (a *Analyzer) FindImpactNodes(ctx context.Context, methodFQN string, depth int) (callers, callees []string, err error) {
	cg, err := a.BuildCallGraph(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("build call graph: %w", err)
	}

	if depth <= 0 {
		depth = 10 // 默认深度
	}

	callers = cg.GetCallers(methodFQN, depth)
	callees = cg.GetCallees(methodFQN, depth)
	return callers, callees, nil
}

// ShortestPath 查找两个方法之间的最短调用路径（BFS）。
//
// 返回路径上的方法 FQN 列表（含起点和终点），
// 如果不存在路径则返回 nil。
func (a *Analyzer) ShortestPath(ctx context.Context, from, to string) ([]string, error) {
	cg, err := a.BuildCallGraph(ctx)
	if err != nil {
		return nil, fmt.Errorf("build call graph: %w", err)
	}

	if _, ok := cg.Nodes[from]; !ok {
		return nil, nil
	}
	if _, ok := cg.Nodes[to]; !ok {
		return nil, nil
	}

	// BFS 查找最短路径
	visited := make(map[string]bool)
	prev := make(map[string]string)
	queue := []string{from}
	visited[from] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current == to {
			return reconstructPath(prev, from, to), nil
		}

		node, ok := cg.Nodes[current]
		if !ok {
			continue
		}

		for _, callee := range node.Callees {
			if !visited[callee] {
				visited[callee] = true
				prev[callee] = current
				queue = append(queue, callee)
			}
		}
	}

	return nil, nil // 无路径
}

// reconstructPath 从 prev 映射中重建路径。
func reconstructPath(prev map[string]string, from, to string) []string {
	path := make([]string, 0)
	for cur := to; cur != ""; cur = prev[cur] {
		path = append([]string{cur}, path...)
	}
	// 只有路径以 from 开头时才有效
	if len(path) > 0 && path[0] == from {
		return path
	}
	return nil
}

// hotItem 用于热点方法排序。
type hotItem struct {
	fqn      string
	nCallers int
}

// ModuleSummary 模块概要信息，用于文件图统计。
type ModuleSummary struct {
	TotalFiles    int            `json:"total_files"`
	TotalClasses  int            `json:"total_classes"`
	TotalMethods  int            `json:"total_methods"`
	TotalCalls    int            `json:"total_calls"`
	Languages     map[string]int `json:"languages"`      // language -> count
	FileGraph     *FileGraph     `json:"file_graph"`
	OrphanMethods []string       `json:"orphan_methods"` // 无调用者的方法
	HotMethods    []string       `json:"hot_methods"`    // 调用者最多的方法
}

// Analyze 对仓库执行完整分析，返回模块概要。
func (a *Analyzer) Analyze(ctx context.Context) (*ModuleSummary, error) {
	cg, ch, _, fg, err := a.BuildAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("build all graphs: %w", err)
	}

	summary := &ModuleSummary{
		TotalFiles:   fg.NodeCount(),
		TotalClasses: ch.NodeCount(),
		Languages:    make(map[string]int),
		FileGraph:    fg,
	}

	// 统计
	for _, node := range fg.Nodes {
		summary.TotalMethods += node.MethodCount
		summary.TotalCalls += len(node.Imports) // 近似

		if node.Language != "" {
			summary.Languages[node.Language]++
		}
	}

	// 找出热点方法（被调用最多的 Top 20）
	var hotList []hotItem
	for fqn, node := range cg.Nodes {
		if len(node.Callers) == 0 {
			summary.OrphanMethods = append(summary.OrphanMethods, fqn)
		}
		hotList = append(hotList, hotItem{fqn, len(node.Callers)})
	}

	// 排序简化：取前 20
	hotList = sortByCallers(hotList)
	limit := 20
	if len(hotList) < limit {
		limit = len(hotList)
	}
	for _, h := range hotList[:limit] {
		summary.HotMethods = append(summary.HotMethods, h.fqn)
	}

	return summary, nil
}

// sortByCallers 按调用者数量降序排序。
func sortByCallers(items []hotItem) []hotItem {
	// 简单选择排序
	for i := 0; i < len(items); i++ {
		maxIdx := i
		for j := i + 1; j < len(items); j++ {
			if items[j].nCallers > items[maxIdx].nCallers {
				maxIdx = j
			}
		}
		items[i], items[maxIdx] = items[maxIdx], items[i]
	}
	return items
}

