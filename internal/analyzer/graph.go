// Package analyzer 提供代码图分析器，负责构建跨文件的代码关系图。
//
// 分析器从 Store 中读取已解析的 IR 数据，构建以下关系图：
//   - 调用图（CallGraph）：方法间的调用关系
//   - 类层次（ClassHierarchy）：继承/实现关系
//   - 反向引用索引（ReverseIndex）：文件被哪些文件引用
//   - 文件依赖图（FileGraph）：文件间的 import/include 依赖
package analyzer

import "context"

// qItem BFS 队列项：fqn + 剩余可展开深度。
type qItem struct {
	fqn   string
	depth int
}

// CallGraphNode 调用图节点，表示一个方法及其调用关系。
type CallGraphNode struct {
	MethodFQN string   // 方法全限定名
	FileID    int64    // 所属文件 ID
	ClassFQN  string   // 所属类全限定名
	Callees   []string // 被调用方法 FQN 列表
	Callers   []string // 调用本方法的方法 FQN 列表
	LineCount int      // 方法行数
}

// CallGraph 是完整的调用图，包含所有方法的调用关系。
type CallGraph struct {
	Nodes map[string]*CallGraphNode // MethodFQN -> Node
}

// NewCallGraph 创建空的调用图。
func NewCallGraph() *CallGraph {
	return &CallGraph{
		Nodes: make(map[string]*CallGraphNode),
	}
}

// AddNode 添加或获取指定方法的节点。
func (cg *CallGraph) AddNode(methodFQN string) *CallGraphNode {
	if n, ok := cg.Nodes[methodFQN]; ok {
		return n
	}
	n := &CallGraphNode{
		MethodFQN: methodFQN,
		Callees:   make([]string, 0),
		Callers:   make([]string, 0),
	}
	cg.Nodes[methodFQN] = n
	return n
}

// AddEdge 添加一条调用边（caller -> callee）。
func (cg *CallGraph) AddEdge(callerFQN, calleeFQN string) {
	caller := cg.AddNode(callerFQN)
	callee := cg.AddNode(calleeFQN)

	// 避免重复边
	if !contains(caller.Callees, calleeFQN) {
		caller.Callees = append(caller.Callees, calleeFQN)
	}
	if !contains(callee.Callers, callerFQN) {
		callee.Callers = append(callee.Callers, callerFQN)
	}
}

// GetCallers 返回指定方法的所有直接调用者（按深度）。
func (cg *CallGraph) GetCallers(methodFQN string, depth int) []string {
	if depth <= 0 {
		return nil
	}
	visited := make(map[string]bool)
	result := make([]string, 0)
	cg.collectCallers(methodFQN, depth, visited, &result)
	return result
}

// GetCallees 返回指定方法的所有直接被调用者（按深度）。
func (cg *CallGraph) GetCallees(methodFQN string, depth int) []string {
	if depth <= 0 {
		return nil
	}
	visited := make(map[string]bool)
	result := make([]string, 0)
	cg.collectCallees(methodFQN, depth, visited, &result)
	return result
}

// GetCallersWithDepth 返回指定方法的所有调用者（带距目标的层级距离）。
//
// 返回节点按深度升序排列（深度 1 = 直接调用者），每层内保持收集顺序。
// 与 GetCallers 的区别：节点携带 Depth 字段，供影响面 API 直接渲染。
func (cg *CallGraph) GetCallersWithDepth(methodFQN string, depth int) []ImpactNode {
	if depth <= 0 {
		return nil
	}
	result, _ := getCallersWithDepth(context.Background(), cg, methodFQN, depth)
	return result
}

// GetCallersWithDepthContext 与 GetCallersWithDepth 相同，但在 BFS 每轮检查 ctx 取消。
func (cg *CallGraph) GetCallersWithDepthContext(ctx context.Context, methodFQN string, depth int) ([]ImpactNode, error) {
	return getCallersWithDepth(ctx, cg, methodFQN, depth)
}

func getCallersWithDepth(ctx context.Context, cg *CallGraph, methodFQN string, depth int) ([]ImpactNode, error) {
	if depth <= 0 {
		return nil, nil
	}
	// 发现即标记：同一祖先可能被多个同层节点发现，出队才标记会产生重复项。
	visited := map[string]bool{methodFQN: true}
	result := make([]ImpactNode, 0)
	queue := []qItem{{fqn: methodFQN, depth: depth}}

	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item := queue[0]
		queue = queue[1:]
		node, ok := cg.Nodes[item.fqn]
		if !ok || item.depth <= 0 {
			continue
		}
		curDepth := depth - item.depth + 1 // 距目标的层级（1 = 直接调用者）
		for _, caller := range node.Callers {
			if visited[caller] {
				continue
			}
			visited[caller] = true
			result = append(result, ImpactNode{Method: caller, Depth: curDepth})
			queue = append(queue, qItem{fqn: caller, depth: item.depth - 1})
		}
	}
	return result, nil
}

// GetCalleesWithDepth 返回指定方法的所有被调用者（带距目标的层级距离）。
//
// 返回节点按深度升序排列（深度 1 = 直接被调用者）。
func (cg *CallGraph) GetCalleesWithDepth(methodFQN string, depth int) []ImpactNode {
	if depth <= 0 {
		return nil
	}
	result, _ := getCalleesWithDepth(context.Background(), cg, methodFQN, depth)
	return result
}

// GetCalleesWithDepthContext 与 GetCalleesWithDepth 相同，但在 BFS 每轮检查 ctx 取消。
func (cg *CallGraph) GetCalleesWithDepthContext(ctx context.Context, methodFQN string, depth int) ([]ImpactNode, error) {
	return getCalleesWithDepth(ctx, cg, methodFQN, depth)
}

func getCalleesWithDepth(ctx context.Context, cg *CallGraph, methodFQN string, depth int) ([]ImpactNode, error) {
	if depth <= 0 {
		return nil, nil
	}
	// 发现即标记，避免多父节点重复入队产生的重复项。
	visited := map[string]bool{methodFQN: true}
	result := make([]ImpactNode, 0)
	queue := []qItem{{fqn: methodFQN, depth: depth}}

	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item := queue[0]
		queue = queue[1:]
		node, ok := cg.Nodes[item.fqn]
		if !ok || item.depth <= 0 {
			continue
		}
		curDepth := depth - item.depth + 1
		for _, callee := range node.Callees {
			if visited[callee] {
				continue
			}
			visited[callee] = true
			result = append(result, ImpactNode{Method: callee, Depth: curDepth})
			queue = append(queue, qItem{fqn: callee, depth: item.depth - 1})
		}
	}
	return result, nil
}

func (cg *CallGraph) collectCallers(fqn string, depth int, visited map[string]bool, result *[]string) {
	if depth <= 0 || visited[fqn] {
		return
	}
	visited[fqn] = true
	node, ok := cg.Nodes[fqn]
	if !ok {
		return
	}
	for _, caller := range node.Callers {
		if visited[caller] {
			continue
		}
		*result = append(*result, caller)
		cg.collectCallers(caller, depth-1, visited, result)
	}
}

func (cg *CallGraph) collectCallees(fqn string, depth int, visited map[string]bool, result *[]string) {
	if depth <= 0 || visited[fqn] {
		return
	}
	visited[fqn] = true
	node, ok := cg.Nodes[fqn]
	if !ok {
		return
	}
	for _, callee := range node.Callees {
		if visited[callee] {
			continue
		}
		*result = append(*result, callee)
		cg.collectCallees(callee, depth-1, visited, result)
	}
}

// NodeCount 返回图中节点数。
func (cg *CallGraph) NodeCount() int { return len(cg.Nodes) }

// EdgeCount 返回图中边数。
func (cg *CallGraph) EdgeCount() int {
	count := 0
	for _, n := range cg.Nodes {
		count += len(n.Callees)
	}
	return count
}

// contains 检查字符串切片中是否包含指定元素。
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// ClassHierarchyNode 类层次节点，表示一个类及其继承关系。
type ClassHierarchyNode struct {
	ClassFQN string   // 类全限定名
	Type     string   // CLASS / INTERFACE / ABSTRACT / ENUM
	Parents  []string // 父类/接口 FQN 列表
	Children []string // 子类 FQN 列表
	FileID   int64    // 所属文件 ID
}

// ClassHierarchy 是完整的类层次结构。
type ClassHierarchy struct {
	Nodes map[string]*ClassHierarchyNode // ClassFQN -> Node
}

// NewClassHierarchy 创建空的类层次结构。
func NewClassHierarchy() *ClassHierarchy {
	return &ClassHierarchy{
		Nodes: make(map[string]*ClassHierarchyNode),
	}
}

// AddNode 添加或获取指定类的节点。
func (ch *ClassHierarchy) AddNode(classFQN string) *ClassHierarchyNode {
	if n, ok := ch.Nodes[classFQN]; ok {
		return n
	}
	n := &ClassHierarchyNode{
		ClassFQN: classFQN,
		Parents:  make([]string, 0),
		Children: make([]string, 0),
	}
	ch.Nodes[classFQN] = n
	return n
}

// AddParent 添加父类关系（child -> parent）。
func (ch *ClassHierarchy) AddParent(childFQN, parentFQN string) {
	child := ch.AddNode(childFQN)
	parent := ch.AddNode(parentFQN)

	if !contains(child.Parents, parentFQN) {
		child.Parents = append(child.Parents, parentFQN)
	}
	if !contains(parent.Children, childFQN) {
		parent.Children = append(parent.Children, childFQN)
	}
}

// NodeCount 返回图中节点数。
func (ch *ClassHierarchy) NodeCount() int { return len(ch.Nodes) }

// ReverseIndex 是文件级别的反向引用索引。
// 记录每个文件被哪些文件引用，以及每个文件的导入列表。
type ReverseIndex struct {
	// References: filePath -> 引用该文件的文件路径列表
	References map[string][]string `json:"references"`
	// Imports: filePath -> 该文件导入的文件路径列表
	Imports map[string][]string `json:"imports"`
}

// NewReverseIndex 创建空的反向引用索引。
func NewReverseIndex() *ReverseIndex {
	return &ReverseIndex{
		References: make(map[string][]string),
		Imports:    make(map[string][]string),
	}
}

// AddReference 添加一条引用关系（referencedBy -> target）。
func (ri *ReverseIndex) AddReference(target, referencedBy string) {
	ri.References[target] = append(ri.References[target], referencedBy)
}

// AddImport 添加一条导入关系（importer -> imported）。
func (ri *ReverseIndex) AddImport(importer, imported string) {
	ri.Imports[importer] = append(ri.Imports[importer], imported)
}

// GetReferencedBy 返回引用指定文件的所有文件路径。
func (ri *ReverseIndex) GetReferencedBy(target string) []string {
	return ri.References[target]
}

// GetImports 返回指定文件导入的所有文件路径。
func (ri *ReverseIndex) GetImports(importer string) []string {
	return ri.Imports[importer]
}

// FileGraphNode 文件依赖图节点。
type FileGraphNode struct {
	FilePath    string   // 文件路径
	FileID      int64    // 文件 ID
	Language    string   // 语言
	Imports     []string // 导入的文件路径
	ImportedBy  []string // 被哪些文件导入
	ClassCount  int      // 类数量
	MethodCount int      // 方法数量
}

// FileGraph 是文件级别的依赖图。
type FileGraph struct {
	Nodes map[string]*FileGraphNode // FilePath -> Node
}

// NewFileGraph 创建空的文件依赖图。
func NewFileGraph() *FileGraph {
	return &FileGraph{
		Nodes: make(map[string]*FileGraphNode),
	}
}

// AddNode 添加或获取指定文件的节点。
func (fg *FileGraph) AddNode(filePath string) *FileGraphNode {
	if n, ok := fg.Nodes[filePath]; ok {
		return n
	}
	n := &FileGraphNode{
		FilePath:   filePath,
		Imports:    make([]string, 0),
		ImportedBy: make([]string, 0),
	}
	fg.Nodes[filePath] = n
	return n
}

// AddEdge 添加一条依赖边（importer -> imported）。
func (fg *FileGraph) AddEdge(importer, imported string) {
	src := fg.AddNode(importer)
	dst := fg.AddNode(imported)

	if !contains(src.Imports, imported) {
		src.Imports = append(src.Imports, imported)
	}
	if !contains(dst.ImportedBy, importer) {
		dst.ImportedBy = append(dst.ImportedBy, importer)
	}
}

// NodeCount 返回图中节点数。
func (fg *FileGraph) NodeCount() int { return len(fg.Nodes) }
