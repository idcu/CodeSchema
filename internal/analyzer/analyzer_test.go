package analyzer

import (
	"context"
	"testing"

	"codeschema/internal/parser"
	"codeschema/internal/store"
)

// mockStore 实现 store.Store 接口，用于分析器测试。
type mockStore struct {
	files   []*store.FileRecord
	classes map[int64][]store.ClassRecord  // fileID -> classes
	methods map[int64][]store.MethodRecord // classID -> methods
	calls   map[int64][]store.CallRecord   // fileID -> calls
}

func (m *mockStore) Open(_ context.Context, _ string) error      { return nil }
func (m *mockStore) Close() error                                 { return nil }
func (m *mockStore) HealthCheck(_ context.Context) error          { return nil }
func (m *mockStore) UpsertFile(_ context.Context, _ string, _ string, _ int, _ int64) (int64, error) { return 0, nil }
func (m *mockStore) GetFileByPath(_ context.Context, _ string) (*store.FileRecord, error) { return nil, nil }
func (m *mockStore) GetFileByID(_ context.Context, _ int64) (*store.FileRecord, error)    { return nil, nil }
func (m *mockStore) DeleteFile(_ context.Context, _ int64) error                          { return nil }
func (m *mockStore) UpsertClasses(_ context.Context, _ int64, _ []parser.ClassIR) error   { return nil }
func (m *mockStore) UpsertMethods(_ context.Context, _ int64, _ []parser.MethodIR) error  { return nil }
func (m *mockStore) UpsertCalls(_ context.Context, _ int64, _ []parser.CallIR) error      { return nil }
func (m *mockStore) UpsertIR(_ context.Context, _ *parser.IRDocument) error               { return nil }

func (m *mockStore) GetAllFiles(_ context.Context) ([]*store.FileRecord, error) {
	return m.files, nil
}

func (m *mockStore) GetClassesByFileID(_ context.Context, fileID int64) ([]store.ClassRecord, error) {
	return m.classes[fileID], nil
}

func (m *mockStore) GetMethodsByClassID(_ context.Context, classID int64) ([]store.MethodRecord, error) {
	return m.methods[classID], nil
}

func (m *mockStore) GetCallsByFileID(_ context.Context, fileID int64) ([]store.CallRecord, error) {
	return m.calls[fileID], nil
}

// newTestStore 创建一个预置测试数据的 mock 存储。
func newTestStore() *mockStore {
	return &mockStore{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/service.go", Language: "go", ContentHash: "h1", LineCount: 100, ByteSize: 2048},
			{ID: 2, AbsolutePath: "/project/repo.go", Language: "go", ContentHash: "h2", LineCount: 80, ByteSize: 1600},
			{ID: 3, AbsolutePath: "/project/handler.go", Language: "go", ContentHash: "h3", LineCount: 50, ByteSize: 1024},
		},
		classes: map[int64][]store.ClassRecord{
			1: {
				{ID: 1, FileID: 1, Name: "Service", FullName: "com.example.Service", Type: "CLASS", StartLine: 1, EndLine: 50},
				{ID: 2, FileID: 1, Name: "ServiceImpl", FullName: "com.example.ServiceImpl", Type: "CLASS", StartLine: 52, EndLine: 100},
			},
			2: {
				{ID: 3, FileID: 2, Name: "Repository", FullName: "com.example.Repository", Type: "INTERFACE", StartLine: 1, EndLine: 10},
			},
			3: {
				{ID: 4, FileID: 3, Name: "Handler", FullName: "com.example.Handler", Type: "CLASS", StartLine: 1, EndLine: 30},
			},
		},
		methods: map[int64][]store.MethodRecord{
			1: {
				{ID: 1, ClassID: 1, Name: "GetUser", FullName: "com.example.Service.GetUser", StartLine: 5, EndLine: 20},
				{ID: 2, ClassID: 1, Name: "SaveUser", FullName: "com.example.Service.SaveUser", StartLine: 22, EndLine: 35},
			},
			2: {
				{ID: 3, ClassID: 2, Name: "Process", FullName: "com.example.ServiceImpl.Process", StartLine: 55, EndLine: 70},
			},
			3: {
				{ID: 4, ClassID: 3, Name: "FindByID", FullName: "com.example.Repository.FindByID", StartLine: 3, EndLine: 8},
			},
			4: {
				{ID: 5, ClassID: 4, Name: "Handle", FullName: "com.example.Handler.Handle", StartLine: 5, EndLine: 25},
			},
		},
		calls: map[int64][]store.CallRecord{
			1: {
				{CallerFQN: "com.example.Service.GetUser", CalleeFQN: "com.example.Repository.FindByID", CallType: "direct", LineNumber: 10},
				{CallerFQN: "com.example.Service.SaveUser", CalleeFQN: "com.example.Repository.FindByID", CallType: "direct", LineNumber: 25},
				{CallerFQN: "com.example.ServiceImpl.Process", CalleeFQN: "com.example.Service.GetUser", CallType: "direct", LineNumber: 60},
			},
			3: {
				{CallerFQN: "com.example.Handler.Handle", CalleeFQN: "com.example.Service.GetUser", CallType: "direct", LineNumber: 10},
			},
		},
	}
}

// ---------- 图数据结构测试 ----------

func TestCallGraph_AddNode(t *testing.T) {
	cg := NewCallGraph()
	n1 := cg.AddNode("com.example.Service.GetUser")
	if n1 == nil {
		t.Fatal("AddNode returned nil")
	}
	if n1.MethodFQN != "com.example.Service.GetUser" {
		t.Errorf("expected MethodFQN 'com.example.Service.GetUser', got %s", n1.MethodFQN)
	}

	// 同一节点应返回相同指针
	n2 := cg.AddNode("com.example.Service.GetUser")
	if n2 != n1 {
		t.Error("AddNode should return the same node for the same FQN")
	}

	if cg.NodeCount() != 1 {
		t.Errorf("expected NodeCount 1, got %d", cg.NodeCount())
	}
}

func TestCallGraph_AddEdge(t *testing.T) {
	cg := NewCallGraph()
	cg.AddEdge("caller.A", "callee.B")
	cg.AddEdge("caller.A", "callee.C")

	caller := cg.Nodes["caller.A"]
	if caller == nil {
		t.Fatal("caller node not found")
	}
	if len(caller.Callees) != 2 {
		t.Errorf("expected 2 callees, got %d", len(caller.Callees))
	}

	callee := cg.Nodes["callee.B"]
	if callee == nil {
		t.Fatal("callee node not found")
	}
	if len(callee.Callers) != 1 || callee.Callers[0] != "caller.A" {
		t.Errorf("expected 1 caller 'caller.A', got %v", callee.Callers)
	}

	// 重复边不应重复添加
	cg.AddEdge("caller.A", "callee.B")
	if len(caller.Callees) != 2 {
		t.Errorf("expected 2 unique callees, got %d", len(caller.Callees))
	}

	if cg.EdgeCount() != 2 {
		t.Errorf("expected EdgeCount 2, got %d", cg.EdgeCount())
	}
}

func TestCallGraph_GetCallers(t *testing.T) {
	cg := NewCallGraph()
	cg.AddEdge("A", "X")
	cg.AddEdge("B", "X")
	cg.AddEdge("C", "Y")
	cg.AddEdge("Y", "X")

	// X 的直接调用者：A, B, Y
	callers := cg.GetCallers("X", 1)
	if len(callers) != 3 {
		t.Errorf("expected 3 direct callers, got %d: %v", len(callers), callers)
	}

	// X 的深度 2 调用者：A, B, Y, C
	callers = cg.GetCallers("X", 2)
	if len(callers) != 4 {
		t.Errorf("expected 4 callers at depth 2, got %d: %v", len(callers), callers)
	}
}

func TestCallGraph_GetCallees(t *testing.T) {
	cg := NewCallGraph()
	cg.AddEdge("A", "B")
	cg.AddEdge("A", "C")
	cg.AddEdge("B", "D")

	// A 的直接被调用者：B, C
	callees := cg.GetCallees("A", 1)
	if len(callees) != 2 {
		t.Errorf("expected 2 direct callees, got %d: %v", len(callees), callees)
	}

	// A 的深度 2 被调用者：B, C, D
	callees = cg.GetCallees("A", 2)
	if len(callees) != 3 {
		t.Errorf("expected 3 callees at depth 2, got %d: %v", len(callees), callees)
	}
}

func TestCallGraph_Empty(t *testing.T) {
	cg := NewCallGraph()
	if cg.NodeCount() != 0 {
		t.Error("new graph should be empty")
	}
	if cg.EdgeCount() != 0 {
		t.Error("new graph should have 0 edges")
	}
	callers := cg.GetCallers("nonexistent", 1)
	if len(callers) != 0 {
		t.Error("nonexistent node should return empty callers")
	}
	callees := cg.GetCallees("nonexistent", 1)
	if len(callees) != 0 {
		t.Error("nonexistent node should return empty callees")
	}
}

func TestClassHierarchy_AddNode(t *testing.T) {
	ch := NewClassHierarchy()
	n1 := ch.AddNode("com.example.Service")
	if n1 == nil {
		t.Fatal("AddNode returned nil")
	}
	if n1.ClassFQN != "com.example.Service" {
		t.Errorf("expected ClassFQN 'com.example.Service', got %s", n1.ClassFQN)
	}

	n2 := ch.AddNode("com.example.Service")
	if n2 != n1 {
		t.Error("AddNode should return the same node for the same FQN")
	}

	if ch.NodeCount() != 1 {
		t.Errorf("expected NodeCount 1, got %d", ch.NodeCount())
	}
}

func TestClassHierarchy_AddParent(t *testing.T) {
	ch := NewClassHierarchy()
	ch.AddParent("com.example.ServiceImpl", "com.example.Service")
	ch.AddParent("com.example.ServiceImpl", "com.example.BaseService")

	child := ch.Nodes["com.example.ServiceImpl"]
	if child == nil {
		t.Fatal("child node not found")
	}
	if len(child.Parents) != 2 {
		t.Errorf("expected 2 parents, got %d", len(child.Parents))
	}

	parent := ch.Nodes["com.example.Service"]
	if parent == nil {
		t.Fatal("parent node not found")
	}
	if len(parent.Children) != 1 || parent.Children[0] != "com.example.ServiceImpl" {
		t.Errorf("expected 1 child 'com.example.ServiceImpl', got %v", parent.Children)
	}
}

func TestReverseIndex(t *testing.T) {
	ri := NewReverseIndex()
	if ri == nil {
		t.Fatal("NewReverseIndex returned nil")
	}

	ri.AddReference("a.go", "b.go")
	ri.AddReference("a.go", "c.go")
	ri.AddImport("b.go", "a.go")

	refs := ri.GetReferencedBy("a.go")
	if len(refs) != 2 {
		t.Errorf("expected 2 references, got %d: %v", len(refs), refs)
	}

	imports := ri.GetImports("b.go")
	if len(imports) != 1 || imports[0] != "a.go" {
		t.Errorf("expected imports ['a.go'], got %v", imports)
	}

	// 不存在的 key 返回空切片
	if len(ri.GetReferencedBy("nonexistent")) != 0 {
		t.Error("nonexistent key should return empty slice")
	}
}

func TestFileGraph_AddNode(t *testing.T) {
	fg := NewFileGraph()
	n1 := fg.AddNode("/project/main.go")
	if n1 == nil {
		t.Fatal("AddNode returned nil")
	}
	if n1.FilePath != "/project/main.go" {
		t.Errorf("expected FilePath '/project/main.go', got %s", n1.FilePath)
	}

	n2 := fg.AddNode("/project/main.go")
	if n2 != n1 {
		t.Error("AddNode should return the same node for the same path")
	}

	if fg.NodeCount() != 1 {
		t.Errorf("expected NodeCount 1, got %d", fg.NodeCount())
	}
}

func TestFileGraph_AddEdge(t *testing.T) {
	fg := NewFileGraph()
	fg.AddEdge("/project/main.go", "/project/lib.go")
	fg.AddEdge("/project/main.go", "/project/util.go")

	main := fg.Nodes["/project/main.go"]
	if main == nil {
		t.Fatal("main.go node not found")
	}
	if len(main.Imports) != 2 {
		t.Errorf("expected 2 imports, got %d", len(main.Imports))
	}

	lib := fg.Nodes["/project/lib.go"]
	if lib == nil {
		t.Fatal("lib.go node not found")
	}
	if len(lib.ImportedBy) != 1 || lib.ImportedBy[0] != "/project/main.go" {
		t.Errorf("expected 1 importer '/project/main.go', got %v", lib.ImportedBy)
	}
}

// ---------- 分析器方法测试 ----------

func TestAnalyzer_BuildCallGraph(t *testing.T) {
	ms := newTestStore()
	a := NewAnalyzer(ms)

	cg, err := a.BuildCallGraph(context.Background())
	if err != nil {
		t.Fatalf("BuildCallGraph: %v", err)
	}

	// 5 个节点：4 个 caller + 1 个纯 callee(Repository.FindByID)
	if cg.NodeCount() != 5 {
		t.Errorf("expected 5 nodes, got %d", cg.NodeCount())
	}
	if cg.EdgeCount() != 4 {
		t.Errorf("expected 4 edges, got %d", cg.EdgeCount())
	}

	// 验证特定边
	serviceGetUser := cg.Nodes["com.example.Service.GetUser"]
	if serviceGetUser == nil {
		t.Fatal("node 'com.example.Service.GetUser' not found")
	}
	if len(serviceGetUser.Callees) != 1 || serviceGetUser.Callees[0] != "com.example.Repository.FindByID" {
		t.Errorf("expected callee 'com.example.Repository.FindByID', got %v", serviceGetUser.Callees)
	}
	if len(serviceGetUser.Callers) != 2 { // ServiceImpl.Process + Handler.Handle
		t.Errorf("expected 2 callers, got %d: %v", len(serviceGetUser.Callers), serviceGetUser.Callers)
	}
}

func TestAnalyzer_BuildClassHierarchy(t *testing.T) {
	ms := newTestStore()
	a := NewAnalyzer(ms)

	ch, err := a.BuildClassHierarchy(context.Background())
	if err != nil {
		t.Fatalf("BuildClassHierarchy: %v", err)
	}

	if ch.NodeCount() != 4 {
		t.Errorf("expected 4 class nodes, got %d", ch.NodeCount())
	}

	// 验证节点的 FileID 正确
	service := ch.Nodes["com.example.Service"]
	if service == nil {
		t.Fatal("node 'com.example.Service' not found")
	}
	if service.FileID != 1 {
		t.Errorf("expected FileID 1, got %d", service.FileID)
	}
	if service.Type != "CLASS" {
		t.Errorf("expected type CLASS, got %s", service.Type)
	}
}

func TestAnalyzer_BuildFileGraph(t *testing.T) {
	ms := newTestStore()
	a := NewAnalyzer(ms)

	fg, err := a.BuildFileGraph(context.Background())
	if err != nil {
		t.Fatalf("BuildFileGraph: %v", err)
	}

	if fg.NodeCount() != 3 {
		t.Errorf("expected 3 file nodes, got %d", fg.NodeCount())
	}

	// 验证文件统计信息
	svcNode := fg.Nodes["/project/service.go"]
	if svcNode == nil {
		t.Fatal("file node '/project/service.go' not found")
	}
	if svcNode.ClassCount != 2 {
		t.Errorf("expected 2 classes, got %d", svcNode.ClassCount)
	}
	if svcNode.MethodCount != 3 { // Service.GetUser, Service.SaveUser, ServiceImpl.Process
		t.Errorf("expected 3 methods, got %d", svcNode.MethodCount)
	}
	if svcNode.Language != "go" {
		t.Errorf("expected language 'go', got %s", svcNode.Language)
	}
}

func TestAnalyzer_BuildAll(t *testing.T) {
	ms := newTestStore()
	a := NewAnalyzer(ms)

	cg, ch, ri, fg, err := a.BuildAll(context.Background())
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}

	if cg.NodeCount() != 5 {
		t.Errorf("CallGraph: expected 5 nodes, got %d", cg.NodeCount())
	}
	if ch.NodeCount() != 4 {
		t.Errorf("ClassHierarchy: expected 4 nodes, got %d", ch.NodeCount())
	}
	if ri == nil {
		t.Error("ReverseIndex should not be nil")
	}
	if fg.NodeCount() != 3 {
		t.Errorf("FileGraph: expected 3 nodes, got %d", fg.NodeCount())
	}
}

func TestAnalyzer_FindImpactNodes(t *testing.T) {
	ms := newTestStore()
	a := NewAnalyzer(ms)

	callers, callees, err := a.FindImpactNodes(context.Background(), "com.example.Repository.FindByID", 1)
	if err != nil {
		t.Fatalf("FindImpactNodes: %v", err)
	}

	// 被 Service.GetUser 和 Service.SaveUser 调用
	if len(callers) != 2 {
		t.Errorf("expected 2 callers, got %d: %v", len(callers), callers)
	}
	// FindByID 没有 callees
	if len(callees) != 0 {
		t.Errorf("expected 0 callees, got %d: %v", len(callees), callees)
	}
}

func TestAnalyzer_ShortestPath(t *testing.T) {
	ms := newTestStore()
	a := NewAnalyzer(ms)

	// Handler.Handle -> Service.GetUser -> Repository.FindByID
	path, err := a.ShortestPath(context.Background(),
		"com.example.Handler.Handle",
		"com.example.Repository.FindByID")
	if err != nil {
		t.Fatalf("ShortestPath: %v", err)
	}

	if len(path) == 0 {
		t.Fatal("expected non-empty path")
	}
	if path[0] != "com.example.Handler.Handle" {
		t.Errorf("expected start 'com.example.Handler.Handle', got %s", path[0])
	}
	last := path[len(path)-1]
	if last != "com.example.Repository.FindByID" {
		t.Errorf("expected end 'com.example.Repository.FindByID', got %s", last)
	}
}

func TestAnalyzer_ShortestPath_NoPath(t *testing.T) {
	ms := newTestStore()
	a := NewAnalyzer(ms)

	// 不存在的方法
	path, err := a.ShortestPath(context.Background(),
		"com.example.Handler.Handle",
		"com.example.Nonexistent.Method")
	if err != nil {
		t.Fatalf("ShortestPath: %v", err)
	}
	if path != nil {
		t.Errorf("expected nil path, got %v", path)
	}
}

func TestAnalyzer_Analyze(t *testing.T) {
	ms := newTestStore()
	a := NewAnalyzer(ms)

	summary, err := a.Analyze(context.Background())
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if summary.TotalFiles != 3 {
		t.Errorf("expected TotalFiles 3, got %d", summary.TotalFiles)
	}
	if summary.TotalClasses != 4 {
		t.Errorf("expected TotalClasses 4, got %d", summary.TotalClasses)
	}
	if summary.TotalMethods != 5 {
		t.Errorf("expected TotalMethods 5, got %d", summary.TotalMethods)
	}
	if summary.Languages["go"] != 3 {
		t.Errorf("expected 3 go files, got %d", summary.Languages["go"])
	}

	// 孤方法：没有被调用的方法
	if len(summary.OrphanMethods) == 0 {
		t.Error("expected at least 1 orphan method")
	}

	// 热点方法：被调用最多的应有 >= 2 个调用者
	if len(summary.HotMethods) == 0 {
		t.Error("expected at least 1 hot method")
	}
}

func TestAnalyzer_EmptyStore(t *testing.T) {
	ms := &mockStore{} // 空存储
	a := NewAnalyzer(ms)

	cg, err := a.BuildCallGraph(context.Background())
	if err != nil {
		t.Fatalf("BuildCallGraph: %v", err)
	}
	if cg.NodeCount() != 0 {
		t.Errorf("expected 0 nodes, got %d", cg.NodeCount())
	}

	summary, err := a.Analyze(context.Background())
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if summary.TotalFiles != 0 {
		t.Errorf("expected TotalFiles 0, got %d", summary.TotalFiles)
	}
}

func TestReconstructPath(t *testing.T) {
	prev := map[string]string{
		"B": "A",
		"C": "B",
	}
	path := reconstructPath(prev, "A", "C")
	expected := []string{"A", "B", "C"}
	if len(path) != len(expected) {
		t.Errorf("expected path %v, got %v", expected, path)
	} else {
		for i := range path {
			if path[i] != expected[i] {
				t.Errorf("position %d: expected %s, got %s", i, expected[i], path[i])
			}
		}
	}
}

func TestReconstructPath_NoPath(t *testing.T) {
	// prev 中没有 from，路径不完整
	prev := map[string]string{
		"C": "B",
	}
	path := reconstructPath(prev, "A", "C")
	if path != nil {
		t.Errorf("expected nil for incomplete path, got %v", path)
	}
}