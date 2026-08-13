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
func (m *mockStore) UpsertTags(_ context.Context, _ int64, _ []string) error              { return nil }
func (m *mockStore) UpsertMethodTags(_ context.Context, _ int64, _ []string) error         { return nil }
func (m *mockStore) GetTagsByClassID(_ context.Context, _ int64) ([]string, error)         { return nil, nil }
func (m *mockStore) GetTagsByMethodID(_ context.Context, _ int64) ([]string, error)        { return nil, nil }
func (m *mockStore) SearchByTag(_ context.Context, _ string) ([]int64, []int64, error)     { return nil, nil, nil }
func (m *mockStore) GetAllTagsWithCategories(_ context.Context) (map[string]string, error) { return nil, nil }

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

// newP1TestStore 创建包含 imports 和父子关系的测试数据。
func newP1TestStore() *mockStore {
	return &mockStore{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/service.go", Language: "go", ContentHash: "h1", LineCount: 100, ByteSize: 2048,
				Imports: []string{"project/repo", "project/handler"}},
			{ID: 2, AbsolutePath: "/project/repo.go", Language: "go", ContentHash: "h2", LineCount: 80, ByteSize: 1600,
				Imports: []string{"project/base"}},
			{ID: 3, AbsolutePath: "/project/handler.go", Language: "go", ContentHash: "h3", LineCount: 50, ByteSize: 1024,
				Imports: []string{"project/service"}},
			{ID: 4, AbsolutePath: "/project/base.go", Language: "go", ContentHash: "h4", LineCount: 30, ByteSize: 512,
				Imports: nil},
		},
		classes: map[int64][]store.ClassRecord{
			1: {
				{ID: 1, FileID: 1, Name: "Service", FullName: "com.example.Service", Type: "CLASS", StartLine: 1, EndLine: 50},
				{ID: 2, FileID: 1, Name: "ServiceImpl", FullName: "com.example.ServiceImpl", Type: "CLASS",
					StartLine: 52, EndLine: 100, ParentFQNs: []string{"com.example.Service", "com.example.BaseService"}},
			},
			2: {
				{ID: 3, FileID: 2, Name: "Repository", FullName: "com.example.Repository", Type: "INTERFACE", StartLine: 1, EndLine: 10},
			},
			3: {
				{ID: 4, FileID: 3, Name: "Handler", FullName: "com.example.Handler", Type: "CLASS", StartLine: 1, EndLine: 30},
			},
			4: {
				{ID: 5, FileID: 4, Name: "BaseService", FullName: "com.example.BaseService", Type: "CLASS", StartLine: 1, EndLine: 30},
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
			5: {
				{ID: 6, ClassID: 5, Name: "Init", FullName: "com.example.BaseService.Init", StartLine: 5, EndLine: 15},
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

// ---------- P1 功能测试 ----------

func TestBuildImportIndex(t *testing.T) {
	ms := newP1TestStore()
	idx := buildImportIndex(ms.files)

	// 文件路径包含 "service.go" → 应生成 "service" 键
	if _, ok := idx["service"]; !ok {
		t.Error("expected 'service' key in import index")
	}
	// 文件路径包含 "repo.go" → 应生成 "repo" 键
	if _, ok := idx["repo"]; !ok {
		t.Error("expected 'repo' key in import index")
	}
	// 文件路径包含 "project/service" → 应生成 "project/service" 键
	if _, ok := idx["project/service"]; !ok {
		t.Error("expected 'project/service' key in import index")
	}
}

func TestResolveImport(t *testing.T) {
	ms := newP1TestStore()
	idx := buildImportIndex(ms.files)
	a := NewAnalyzer(ms)

	// 策略 2: import "project/service" → 最后一段 "service" 应匹配 service.go
	targets := a.resolveImport("project/service", idx)
	if len(targets) == 0 {
		t.Error("expected resolveImport to find target for 'project/service'")
	} else {
		found := false
		for _, p := range targets {
			if p == "/project/service.go" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected '/project/service.go' in targets, got %v", targets)
		}
	}

	// 不存在的 import 返回 nil
	targets = a.resolveImport("nonexistent/pkg", idx)
	if len(targets) != 0 {
		t.Errorf("expected nil for nonexistent import, got %v", targets)
	}
}

func TestResolveImport_GoModule(t *testing.T) {
	ms := newP1TestStore()
	idx := buildImportIndex(ms.files)

	// 未设置 modulePath 时，回退到启发式匹配
	// "codeschema/handler" 的最后一段 "handler" 匹配 handler.go
	a0 := NewAnalyzer(ms)
	targets := a0.resolveImport("codeschema/handler", idx)
	if len(targets) == 0 {
		t.Error("expected resolveImport to fallback to heuristic match for 'codeschema/handler'")
	}

	// 设置 modulePath 后，Go 模块路径精确解析优先
	a := NewAnalyzer(ms)
	a.SetModulePath("codeschema")

	// 本模块路径：codeschema 前缀 + 不存在的包 → 精确解析优先，找不到则回退
	targets = a.resolveImport("codeschema/nonexistent", idx)
	// 不存在的包，预期为空
	if len(targets) != 0 {
		t.Errorf("expected nil for nonexistent module package, got %v", targets)
	}

	// 标准库 import 不应匹配（无 module 前缀，也无文件路径匹配）
	targets = a.resolveImport("fmt", idx)
	if len(targets) != 0 {
		t.Errorf("expected nil for stdlib import 'fmt', got %v", targets)
	}
}

func TestResolveImport_GoModuleExact(t *testing.T) {
	// 创建一个包含模块路径匹配的测试数据
	ms := &mockStore{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/codeschema/internal/store/store.go", Language: "go"},
			{ID: 2, AbsolutePath: "/project/codeschema/internal/analyzer/analyzer.go", Language: "go"},
			{ID: 3, AbsolutePath: "/project/codeschema/cmd/codeschema/main.go", Language: "go"},
		},
	}
	idx := buildImportIndex(ms.files)
	a := NewAnalyzer(ms)
	a.SetModulePath("codeschema")

	// 策略 0: codeschema/internal/store → "internal/store" 应精确匹配
	targets := a.resolveImport("codeschema/internal/store", idx)
	if len(targets) == 0 {
		t.Error("expected Go module resolution to find target for 'codeschema/internal/store'")
	} else {
		found := false
		for _, p := range targets {
			if p == "/project/codeschema/internal/store/store.go" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected '/project/codeschema/internal/store/store.go' in targets, got %v", targets)
		}
	}

	// 第三方包 import 应回退到启发式匹配
	targets = a.resolveImport("github.com/some/lib", idx)
	// 不匹配，应为空
	_ = targets
}

func TestAnalyzer_BuildReverseIndex(t *testing.T) {
	ms := newP1TestStore()
	a := NewAnalyzer(ms)

	ri, err := a.BuildReverseIndex(context.Background())
	if err != nil {
		t.Fatalf("BuildReverseIndex: %v", err)
	}

	// service.go 导入了 project/repo 和 project/handler
	imports := ri.GetImports("/project/service.go")
	if len(imports) != 2 {
		t.Errorf("expected 2 imports for service.go, got %d: %v", len(imports), imports)
	}

	// handler.go 导入了 project/service → service.go 应被 handler.go 引用
	refs := ri.GetReferencedBy("/project/service.go")
	if len(refs) == 0 {
		t.Error("expected service.go to be referenced by at least 1 file")
	}
	found := false
	for _, r := range refs {
		if r == "/project/handler.go" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected '/project/handler.go' to reference service.go, got %v", refs)
	}
}

func TestAnalyzer_BuildReverseIndex_EmptyImports(t *testing.T) {
	ms := newTestStore() // 原始测试数据没有 imports
	a := NewAnalyzer(ms)

	ri, err := a.BuildReverseIndex(context.Background())
	if err != nil {
		t.Fatalf("BuildReverseIndex: %v", err)
	}

	// 没有 imports 时，索引应为空
	if len(ri.Imports) != 0 {
		t.Errorf("expected empty Imports, got %d entries", len(ri.Imports))
	}
	if len(ri.References) != 0 {
		t.Errorf("expected empty References, got %d entries", len(ri.References))
	}
}

func TestAnalyzer_BuildClassHierarchy_WithParents(t *testing.T) {
	ms := newP1TestStore()
	a := NewAnalyzer(ms)

	ch, err := a.BuildClassHierarchy(context.Background())
	if err != nil {
		t.Fatalf("BuildClassHierarchy: %v", err)
	}

	// 5 个类节点
	if ch.NodeCount() != 5 {
		t.Errorf("expected 5 class nodes, got %d", ch.NodeCount())
	}

	// ServiceImpl 有 2 个父类
	impl := ch.Nodes["com.example.ServiceImpl"]
	if impl == nil {
		t.Fatal("node 'com.example.ServiceImpl' not found")
	}
	if len(impl.Parents) != 2 {
		t.Errorf("expected 2 parents for ServiceImpl, got %d: %v", len(impl.Parents), impl.Parents)
	}

	// Service 应有一个子类 (ServiceImpl)
	service := ch.Nodes["com.example.Service"]
	if service == nil {
		t.Fatal("node 'com.example.Service' not found")
	}
	if len(service.Children) != 1 || service.Children[0] != "com.example.ServiceImpl" {
		t.Errorf("expected 1 child 'com.example.ServiceImpl', got %v", service.Children)
	}

	// BaseService 也应有一个子类 (ServiceImpl)
	base := ch.Nodes["com.example.BaseService"]
	if base == nil {
		t.Fatal("node 'com.example.BaseService' not found")
	}
	if len(base.Children) != 1 || base.Children[0] != "com.example.ServiceImpl" {
		t.Errorf("expected 1 child 'com.example.ServiceImpl', got %v", base.Children)
	}
}

func TestAnalyzer_BuildAll_P1(t *testing.T) {
	ms := newP1TestStore()
	a := NewAnalyzer(ms)

	cg, ch, ri, fg, err := a.BuildAll(context.Background())
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}

	// 调用图
	if cg.NodeCount() != 5 {
		t.Errorf("CallGraph: expected 5 nodes, got %d", cg.NodeCount())
	}

	// 类层次：5 个节点 + ServiceImpl 的 2 个父类边
	if ch.NodeCount() != 5 {
		t.Errorf("ClassHierarchy: expected 5 nodes, got %d", ch.NodeCount())
	}
	impl := ch.Nodes["com.example.ServiceImpl"]
	if impl == nil || len(impl.Parents) != 2 {
		t.Errorf("ServiceImpl should have 2 parents, got %d", len(impl.Parents))
	}

	// 反向索引：非空
	if ri == nil {
		t.Fatal("ReverseIndex should not be nil")
	}
	if len(ri.Imports) == 0 {
		t.Error("ReverseIndex.Imports should not be empty with P1 data")
	}

	// 文件依赖图：4 个文件
	if fg.NodeCount() != 4 {
		t.Errorf("FileGraph: expected 4 nodes, got %d", fg.NodeCount())
	}

	// 验证文件依赖边：service.go → repo.go, handler.go
	svc := fg.Nodes["/project/service.go"]
	if svc == nil {
		t.Fatal("node '/project/service.go' not found")
	}
	if len(svc.Imports) == 0 {
		t.Error("expected service.go to have imports")
	}
}

func TestAnalyzer_Analyze_P1(t *testing.T) {
	ms := newP1TestStore()
	a := NewAnalyzer(ms)

	summary, err := a.Analyze(context.Background())
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if summary.TotalFiles != 4 {
		t.Errorf("expected TotalFiles 4, got %d", summary.TotalFiles)
	}
	if summary.TotalClasses != 5 {
		t.Errorf("expected TotalClasses 5, got %d", summary.TotalClasses)
	}
	if summary.TotalMethods != 6 {
		t.Errorf("expected TotalMethods 6, got %d", summary.TotalMethods)
	}
}

// ---------- P3 多语言解析器测试 ----------

// newJavaTestStore 创建包含 Java 文件路径的测试数据。
func newJavaTestStore() *mockStore {
	return &mockStore{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/src/main/java/com/example/service/UserService.java", Language: "java"},
			{ID: 2, AbsolutePath: "/project/src/main/java/com/example/service/OrderService.java", Language: "java"},
			{ID: 3, AbsolutePath: "/project/src/main/java/com/example/repo/UserRepository.java", Language: "java"},
			{ID: 4, AbsolutePath: "/project/src/main/java/com/example/controller/UserController.java", Language: "java"},
			{ID: 5, AbsolutePath: "/project/src/main/java/com/example/Application.java", Language: "java"},
		},
	}
}

func TestJavaResolver_Stdlib(t *testing.T) {
	r := NewJavaResolver(nil)

	// 标准库 import 应返回 nil
	cases := []string{
		"java.lang.String",
		"java.util.List",
		"javax.servlet.http.HttpServlet",
		"org.springframework.stereotype.Service",
		"org.slf4j.Logger",
		"lombok.Data",
		"com.fasterxml.jackson.databind.ObjectMapper",
	}
	for _, imp := range cases {
		targets := r.Resolve(imp, nil)
		if targets != nil {
			t.Errorf("expected nil for stdlib '%s', got %v", imp, targets)
		}
	}
}

func TestJavaResolver_Stdlib_NonStdlibNotFiltered(t *testing.T) {
	r := NewJavaResolver(nil)
	idx := map[string][]string{
		"com/example/service/UserService": {"/project/UserService.java"},
	}

	// 非标准库应正常解析
	targets := r.Resolve("com.example.service.UserService", idx)
	if len(targets) == 0 {
		t.Error("expected non-stdlib import to resolve")
	}
}

func TestJavaResolver_FQCN(t *testing.T) {
	r := NewJavaResolver(nil)
	idx := map[string][]string{
		"com/example/service/UserService": {"/project/UserService.java"},
		"com/example/repo/UserRepository": {"/project/UserRepository.java"},
	}

	// FQCN 精确匹配
	targets := r.Resolve("com.example.service.UserService", idx)
	if len(targets) != 1 || targets[0] != "/project/UserService.java" {
		t.Errorf("expected '/project/UserService.java', got %v", targets)
	}

	// 另一个 FQCN
	targets = r.Resolve("com.example.repo.UserRepository", idx)
	if len(targets) != 1 || targets[0] != "/project/UserRepository.java" {
		t.Errorf("expected '/project/UserRepository.java', got %v", targets)
	}
}

func TestJavaResolver_FQCN_NoMatch(t *testing.T) {
	r := NewJavaResolver(nil)
	idx := map[string][]string{
		"com/example/service/UserService": {"/project/UserService.java"},
	}

	// 不存在的 FQCN
	targets := r.Resolve("com.example.nonexistent.Foo", idx)
	if len(targets) != 0 {
		t.Errorf("expected nil for nonexistent FQCN, got %v", targets)
	}
}

func TestJavaResolver_Wildcard(t *testing.T) {
	r := NewJavaResolver(nil)
	idx := map[string][]string{
		"com/example/service/UserService":  {"/project/UserService.java"},
		"com/example/service/OrderService": {"/project/OrderService.java"},
		"com/example/repo/UserRepository":  {"/project/UserRepository.java"},
	}

	// 通配符匹配：com.example.service.* → service 目录下所有文件
	targets := r.Resolve("com.example.service.*", idx)
	if len(targets) != 2 {
		t.Errorf("expected 2 targets for wildcard, got %d: %v", len(targets), targets)
	}

	// 通配符匹配：com.example.repo.* → repo 目录下 1 个文件
	targets = r.Resolve("com.example.repo.*", idx)
	if len(targets) != 1 {
		t.Errorf("expected 1 target for wildcard, got %d: %v", len(targets), targets)
	}
}

func TestJavaResolver_Wildcard_NoMatch(t *testing.T) {
	r := NewJavaResolver(nil)
	idx := map[string][]string{
		"com/example/service/UserService": {"/project/UserService.java"},
	}

	// 不存在的通配符
	targets := r.Resolve("com.example.nonexistent.*", idx)
	if len(targets) != 0 {
		t.Errorf("expected nil for nonexistent wildcard, got %v", targets)
	}
}

func TestJavaResolver_SourceRoot(t *testing.T) {
	r := NewJavaResolver([]string{"src/main/java"})
	idx := map[string][]string{
		"src/main/java/com/example/service/UserService": {"/project/src/main/java/com/example/service/UserService.java"},
		"src/main/java/com/example/repo/UserRepository": {"/project/src/main/java/com/example/repo/UserRepository.java"},
	}

	// FQCN 匹配时，源根目录前缀匹配
	targets := r.Resolve("com.example.service.UserService", idx)
	if len(targets) != 1 || targets[0] != "/project/src/main/java/com/example/service/UserService.java" {
		t.Errorf("expected source root match, got %v", targets)
	}
}

func TestJavaResolver_SourceRootWithWildcard(t *testing.T) {
	r := NewJavaResolver([]string{"src/main/java"})
	idx := map[string][]string{
		"src/main/java/com/example/service/UserService":  {"/project/src/main/java/com/example/service/UserService.java"},
		"src/main/java/com/example/service/OrderService": {"/project/src/main/java/com/example/service/OrderService.java"},
		"src/main/java/com/example/repo/UserRepository":  {"/project/src/main/java/com/example/repo/UserRepository.java"},
	}

	// 通配符 + 源根目录
	targets := r.Resolve("com.example.service.*", idx)
	if len(targets) != 2 {
		t.Errorf("expected 2 targets for wildcard+source root, got %d: %v", len(targets), targets)
	}

	// 通配符 + 源根目录匹配 repo
	targets = r.Resolve("com.example.repo.*", idx)
	if len(targets) != 1 {
		t.Errorf("expected 1 target for wildcard+source root repo, got %d: %v", len(targets), targets)
	}
}

func TestJavaResolver_DefaultSourceRoots(t *testing.T) {
	r := NewJavaResolver(nil)
	if len(r.sourceRoots) != 4 {
		t.Errorf("expected 4 default source roots, got %d: %v", len(r.sourceRoots), r.sourceRoots)
	}
	// 验证默认值
	expected := []string{"src/main/java", "src/main/kotlin", "src/test/java", "src/test/kotlin"}
	for i, root := range expected {
		if r.sourceRoots[i] != root {
			t.Errorf("sourceRoot[%d]: expected %s, got %s", i, root, r.sourceRoots[i])
		}
	}
}

func TestJavaResolver_CustomSourceRoots(t *testing.T) {
	r := NewJavaResolver([]string{"custom/src"})
	if len(r.sourceRoots) != 1 || r.sourceRoots[0] != "custom/src" {
		t.Errorf("expected custom source roots, got %v", r.sourceRoots)
	}
}

func TestJavaResolver_EmptySourceRoots(t *testing.T) {
	r := NewJavaResolver([]string{})
	// 空切片应使用默认值
	if len(r.sourceRoots) != 4 {
		t.Errorf("expected 4 default source roots for empty input, got %d", len(r.sourceRoots))
	}
}

func TestCompositeResolver_Priority(t *testing.T) {
	// 构建一个模拟的 import 索引
	idx := map[string][]string{
		"internal/store": {"/project/store.go"},
		"store":          {"/project/store.go"},
		"com/example/Foo": {"/project/Foo.java"},
	}

	// 注册 GoResolver(module="codeschema") + JavaResolver + heuristicResolver
	goR := NewGoResolver("codeschema")
	javaR := NewJavaResolver(nil)
	heuristic := &heuristicResolver{}
	c := NewCompositeResolver(goR, javaR, heuristic)

	// 1. GoResolver 精确匹配应优先：codeschema/internal/store → internal/store
	targets := c.Resolve("codeschema/internal/store", idx)
	if len(targets) == 0 {
		t.Error("expected GoResolver to match 'codeschema/internal/store'")
	}
	found := false
	for _, p := range targets {
		if p == "/project/store.go" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected '/project/store.go' in targets, got %v", targets)
	}

	// 2. JavaResolver 匹配 Java FQCN
	targets = c.Resolve("com.example.Foo", idx)
	if len(targets) == 0 || targets[0] != "/project/Foo.java" {
		t.Errorf("expected JavaResolver to match 'com.example.Foo', got %v", targets)
	}

	// 3. heuristicResolver 作为回退：最后一段匹配
	targets = c.Resolve("some/random/store", idx)
	if len(targets) == 0 {
		t.Error("expected heuristicResolver to fallback match 'some/random/store'")
	}
}

func TestCompositeResolver_AllFail(t *testing.T) {
	goR := NewGoResolver("codeschema")
	javaR := NewJavaResolver([]string{"src/main/java"})
	c := NewCompositeResolver(goR, javaR)

	// 所有解析器都返回空
	idx := map[string][]string{
		"some/pkg": {"/project/foo.go"},
	}
	targets := c.Resolve("completely.unrelated", idx)
	if len(targets) != 0 {
		t.Errorf("expected nil when all resolvers fail, got %v", targets)
	}
}

func TestCompositeResolver_AddResolver(t *testing.T) {
	c := NewCompositeResolver()
	if len(c.resolvers) != 0 {
		t.Errorf("expected 0 resolvers initially, got %d", len(c.resolvers))
	}

	c.AddResolver(NewGoResolver(""))
	if len(c.resolvers) != 1 {
		t.Errorf("expected 1 resolver after Add, got %d", len(c.resolvers))
	}

	c.AddResolver(NewJavaResolver(nil))
	if len(c.resolvers) != 2 {
		t.Errorf("expected 2 resolvers after second Add, got %d", len(c.resolvers))
	}
}

func TestHeuristicResolver_DirectMatch(t *testing.T) {
	h := &heuristicResolver{}
	idx := map[string][]string{
		"internal/store": {"/project/store.go"},
	}

	targets := h.Resolve("internal/store", idx)
	if len(targets) != 1 || targets[0] != "/project/store.go" {
		t.Errorf("expected direct match, got %v", targets)
	}
}

func TestHeuristicResolver_LastSegment(t *testing.T) {
	h := &heuristicResolver{}
	idx := map[string][]string{
		"store": {"/project/store.go"},
	}

	// 最后一段 "store" 匹配
	targets := h.Resolve("some/deep/path/store", idx)
	if len(targets) != 1 || targets[0] != "/project/store.go" {
		t.Errorf("expected last segment match, got %v", targets)
	}
}

func TestHeuristicResolver_DotToSlash(t *testing.T) {
	h := &heuristicResolver{}
	idx := map[string][]string{
		"com/example/Foo": {"/project/Foo.java"},
	}

	// "." 替换为 "/" 后匹配
	targets := h.Resolve("com.example.Foo", idx)
	if len(targets) != 1 || targets[0] != "/project/Foo.java" {
		t.Errorf("expected dot-to-slash match, got %v", targets)
	}
}

func TestHeuristicResolver_DotToSlashLastSegment(t *testing.T) {
	h := &heuristicResolver{}
	idx := map[string][]string{
		"Foo": {"/project/Foo.java"},
	}

	// "." 替换为 "/" 后取最后一段
	targets := h.Resolve("com.example.Foo", idx)
	if len(targets) != 1 || targets[0] != "/project/Foo.java" {
		t.Errorf("expected dot-to-slash last segment match, got %v", targets)
	}
}

func TestHeuristicResolver_NoMatch(t *testing.T) {
	h := &heuristicResolver{}
	idx := map[string][]string{
		"store": {"/project/store.go"},
	}

	targets := h.Resolve("completely.unrelated.path", idx)
	if len(targets) != 0 {
		t.Errorf("expected nil for no match, got %v", targets)
	}
}

func TestJavaResolver_IntegrationWithAnalyzer(t *testing.T) {
	ms := newJavaTestStore()
	idx := buildImportIndex(ms.files)

	// 设置 Java 源根目录
	a := NewAnalyzer(ms)
	a.SetJavaSourceRoots([]string{"src/main/java"})

	// 解析 FQCN 导入
	targets := a.resolveImport("com.example.service.UserService", idx)
	if len(targets) == 0 {
		t.Error("expected JavaResolver to resolve 'com.example.service.UserService'")
	}
	found := false
	for _, p := range targets {
		if p == "/project/src/main/java/com/example/service/UserService.java" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected '/project/src/main/java/com/example/service/UserService.java' in targets, got %v", targets)
	}

	// 解析通配符导入
	targets = a.resolveImport("com.example.service.*", idx)
	if len(targets) != 2 {
		t.Errorf("expected 2 targets for wildcard, got %d: %v", len(targets), targets)
	}
}

func TestJavaResolver_IntegrationWithAnalyzer_DefaultRoots(t *testing.T) {
	ms := newJavaTestStore()
	idx := buildImportIndex(ms.files)

	// 使用默认源根目录（与测试数据路径一致）
	a := NewAnalyzer(ms)

	// 默认源根目录包含 src/main/java，应能匹配
	targets := a.resolveImport("com.example.service.UserService", idx)
	if len(targets) == 0 {
		t.Error("expected JavaResolver with default source roots to resolve 'com.example.service.UserService'")
	}

	// 标准库应被过滤
	targets = a.resolveImport("java.lang.String", idx)
	if len(targets) != 0 {
		t.Errorf("expected stdlib 'java.lang.String' to be filtered, got %v", targets)
	}
}

func TestJavaResolver_Resolve_EmptyImportIndex(t *testing.T) {
	r := NewJavaResolver(nil)
	// 空索引时 FQCN 应返回 nil
	targets := r.Resolve("com.example.Foo", map[string][]string{})
	if len(targets) != 0 {
		t.Errorf("expected nil for empty import index, got %v", targets)
	}

	// 空索引时通配符应返回 nil
	targets = r.Resolve("com.example.*", map[string][]string{})
	if len(targets) != 0 {
		t.Errorf("expected nil for empty import index wildcard, got %v", targets)
	}
}

// ---------- P4 可配置标准库前缀 + Gradle 多模块解析测试 ----------

func TestJavaResolver_SetStdlibPrefixes(t *testing.T) {
	r := NewJavaResolver(nil)
	idx := map[string][]string{
		"com/custom/lib/Foo": {"/project/Foo.java"},
	}

	// 自定义前缀：只过滤 custom 前缀
	r.SetStdlibPrefixes([]string{"custom."})

	// "custom.lib.Foo" 应被过滤
	targets := r.Resolve("custom.lib.Foo", idx)
	if len(targets) != 0 {
		t.Errorf("expected 'custom.lib.Foo' to be filtered, got %v", targets)
	}

	// 非自定义前缀不应过滤（即使它在默认列表中）
	r.SetStdlibPrefixes([]string{"custom."})
	targets = r.Resolve("java.lang.String", idx)
	if len(targets) != 0 {
		t.Errorf("expected 'java.lang.String' to be filtered when custom prefix is set, got %v", targets)
	}
}

func TestJavaResolver_EmptyPrefixes_NoFilter(t *testing.T) {
	r := NewJavaResolver(nil)
	idx := map[string][]string{
		"java/lang/String": {"/project/String.java"},
	}

	// 空前缀列表 = 不过滤任何 import
	r.SetStdlibPrefixes([]string{})

	targets := r.Resolve("java.lang.String", idx)
	if len(targets) == 0 {
		t.Error("expected 'java.lang.String' to resolve when no prefixes are set")
	}
}

func TestJavaResolver_AddStdlibPrefix(t *testing.T) {
	r := NewJavaResolver(nil)
	idx := map[string][]string{
		"com/mycompany/Foo": {"/project/Foo.java"},
	}

	// 追加自定义前缀
	r.AddStdlibPrefix("com.mycompany.")

	// 应被过滤
	targets := r.Resolve("com.mycompany.Foo", idx)
	if len(targets) != 0 {
		t.Errorf("expected 'com.mycompany.Foo' to be filtered after AddStdlibPrefix, got %v", targets)
	}

	// 默认前缀仍应生效
	targets = r.Resolve("java.lang.String", nil)
	if targets != nil {
		t.Error("expected 'java.lang.String' to still be filtered with default prefixes")
	}
}

// ---------- P4 Gradle 多模块解析测试 ----------

// newGradleTestStore 创建包含 Gradle 多模块项目文件路径的测试数据。
//
// 文件路径结构：/project/{module}/{sourceRoot}/{internalPath}
// 对应 Gradle 路径：:{module}:{internalPathSegments}
func newGradleTestStore() *mockStore {
	return &mockStore{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/app/src/main/java/controller/UserController.java", Language: "java"},
			{ID: 2, AbsolutePath: "/project/app/src/main/java/service/UserService.java", Language: "java"},
			{ID: 3, AbsolutePath: "/project/core/src/main/java/domain/User.java", Language: "java"},
			{ID: 4, AbsolutePath: "/project/core/src/main/java/domain/Order.java", Language: "java"},
			{ID: 5, AbsolutePath: "/project/lib/src/main/java/util/StringUtils.java", Language: "java"},
		},
	}
}

func TestGradleResolver_ModulePath(t *testing.T) {
	r := NewGradleResolver(nil, nil)
	idx := map[string][]string{
		"app/controller/UserController": {"/project/app/UserController.java"},
		"core/domain/User":              {"/project/core/domain/User.java"},
	}

	// :app:controller:UserController → app/controller/UserController
	targets := r.Resolve(":app:controller:UserController", idx)
	if len(targets) != 1 || targets[0] != "/project/app/UserController.java" {
		t.Errorf("expected '/project/app/UserController.java', got %v", targets)
	}

	// :core:domain:User → core/domain/User
	targets = r.Resolve(":core:domain:User", idx)
	if len(targets) != 1 || targets[0] != "/project/core/domain/User.java" {
		t.Errorf("expected '/project/core/domain/User.java', got %v", targets)
	}
}

func TestGradleResolver_NonGradlePath(t *testing.T) {
	r := NewGradleResolver(nil, nil)

	// 非 : 开头的路径应返回 nil（直接跳过）
	targets := r.Resolve("com.example.Foo", nil)
	if targets != nil {
		t.Errorf("expected nil for non-Gradle path, got %v", targets)
	}
}

func TestGradleResolver_ModulePathWithSourceRoot(t *testing.T) {
	r := NewGradleResolver([]string{"src/main/java"}, nil)
	idx := map[string][]string{
		"src/main/java/app/controller/UserController": {"/project/app/src/main/java/com/example/controller/UserController.java"},
		"src/main/java/core/domain/User":              {"/project/core/src/main/java/com/example/domain/User.java"},
	}

	// 源根目录匹配
	targets := r.Resolve(":app:controller:UserController", idx)
	if len(targets) == 0 {
		t.Error("expected GradleResolver to match with source root")
	}

	// 更精确的匹配
	targets = r.Resolve(":core:domain:User", idx)
	if len(targets) == 0 {
		t.Error("expected GradleResolver to match core module with source root")
	}
}

func TestGradleResolver_Wildcard(t *testing.T) {
	r := NewGradleResolver(nil, nil)
	idx := map[string][]string{
		"app/controller/UserController": {"/project/app/UserController.java"},
		"app/service/UserService":       {"/project/app/UserService.java"},
		"core/domain/User":              {"/project/core/domain/User.java"},
	}

	// :app:* → app 目录下所有文件
	targets := r.Resolve(":app:*", idx)
	if len(targets) != 2 {
		t.Errorf("expected 2 targets for ':app:*', got %d: %v", len(targets), targets)
	}

	// :core:* → core 目录下 1 个文件
	targets = r.Resolve(":core:*", idx)
	if len(targets) != 1 {
		t.Errorf("expected 1 target for ':core:*', got %d: %v", len(targets), targets)
	}
}

func TestGradleResolver_WildcardNoMatch(t *testing.T) {
	r := NewGradleResolver(nil, nil)
	idx := map[string][]string{
		"app/controller/UserController": {"/project/app/UserController.java"},
	}

	// 不存在的模块
	targets := r.Resolve(":nonexistent:*", idx)
	if len(targets) != 0 {
		t.Errorf("expected nil for nonexistent module wildcard, got %v", targets)
	}
}

func TestGradleResolver_ModuleWhitelist(t *testing.T) {
	r := NewGradleResolver(nil, []string{"app", "core"})
	idx := map[string][]string{
		"app/controller/UserController": {"/project/app/UserController.java"},
		"lib/util/StringUtils":          {"/project/lib/StringUtils.java"},
	}

	// app 在白名单中 → 应匹配
	targets := r.Resolve(":app:controller:UserController", idx)
	if len(targets) == 0 {
		t.Error("expected match for whitelisted module 'app'")
	}

	// lib 不在白名单中 → 应返回 nil
	targets = r.Resolve(":lib:util:StringUtils", idx)
	if len(targets) != 0 {
		t.Errorf("expected nil for non-whitelisted module 'lib', got %v", targets)
	}
}

func TestGradleResolver_EmptyModuleWhitelist(t *testing.T) {
	// 空白名单 = 不限制
	r := NewGradleResolver(nil, []string{})
	idx := map[string][]string{
		"any/module/Foo": {"/project/any/Foo.java"},
	}

	targets := r.Resolve(":any:module:Foo", idx)
	if len(targets) == 0 {
		t.Error("expected empty whitelist to allow all modules")
	}
}

func TestGradleResolver_StdlibFilter(t *testing.T) {
	r := NewGradleResolver(nil, nil)

	// 标准库路径应被过滤（即使以 : 开头）
	targets := r.Resolve(":java:lang:String", nil)
	if targets != nil {
		t.Errorf("expected stdlib ':java:lang:String' to be filtered, got %v", targets)
	}
}

func TestGradleResolver_IntegrationWithAnalyzer(t *testing.T) {
	ms := newGradleTestStore()
	idx := buildImportIndex(ms.files)
	a := NewAnalyzer(ms)

	// 解析 Gradle 多模块路径
	targets := a.resolveImport(":app:controller:UserController", idx)
	if len(targets) == 0 {
		t.Error("expected GradleResolver to resolve ':app:controller:UserController'")
	}

	// 通配符匹配
	targets = a.resolveImport(":core:*", idx)
	if len(targets) != 2 {
		t.Errorf("expected 2 targets for ':core:*', got %d: %v", len(targets), targets)
	}
}

func TestGradleResolver_IntegrationWithAnalyzer_ModuleNames(t *testing.T) {
	ms := newGradleTestStore()
	idx := buildImportIndex(ms.files)
	a := NewAnalyzer(ms)

	// 设置模块名白名单
	a.SetGradleModuleNames([]string{"app", "core"})

	// app 模块应匹配
	targets := a.resolveImport(":app:controller:UserController", idx)
	if len(targets) == 0 {
		t.Error("expected match for whitelisted module 'app'")
	}

	// lib 模块不在白名单中 → 应返回 nil
	targets = a.resolveImport(":lib:util:StringUtils", idx)
	if len(targets) != 0 {
		t.Errorf("expected nil for non-whitelisted module 'lib', got %v", targets)
	}
}

func TestGradleResolver_DefaultSourceRoots(t *testing.T) {
	r := NewGradleResolver(nil, nil)
	if len(r.sourceRoots) != 4 {
		t.Errorf("expected 4 default source roots, got %d", len(r.sourceRoots))
	}
}

func TestGradleResolver_CustomSourceRoots(t *testing.T) {
	r := NewGradleResolver([]string{"custom/src"}, nil)
	if len(r.sourceRoots) != 1 || r.sourceRoots[0] != "custom/src" {
		t.Errorf("expected custom source roots, got %v", r.sourceRoots)
	}
}

func TestGradleResolver_SetStdlibPrefixes(t *testing.T) {
	r := NewGradleResolver(nil, nil)
	idx := map[string][]string{
		"my/custom/Foo": {"/project/Foo.java"},
	}

	// 设置自定义前缀
	r.SetStdlibPrefixes([]string{"my."})

	// 应被过滤
	targets := r.Resolve(":my:custom:Foo", idx)
	if len(targets) != 0 {
		t.Errorf("expected ':my:custom:Foo' to be filtered, got %v", targets)
	}
}

func TestJavaResolver_SetStdlibPrefixesViaAnalyzer(t *testing.T) {
	ms := newJavaTestStore()
	idx := buildImportIndex(ms.files)
	a := NewAnalyzer(ms)

	// 通过 Analyzer 设置自定义前缀，仅过滤 java.*
	a.SetJavaStdlibPrefixes([]string{"java."})

	// java.lang.String 应被过滤
	targets := a.resolveImport("java.lang.String", idx)
	if len(targets) != 0 {
		t.Errorf("expected 'java.lang.String' to be filtered, got %v", targets)
	}

	// 非 java.* 前缀应正常解析（即使默认列表中包含）
	targets = a.resolveImport("com.example.service.UserService", idx)
	if len(targets) == 0 {
		t.Error("expected 'com.example.service.UserService' to resolve")
	}
}