package service

import (
	"context"
	"testing"

	"codeschema/internal/parser"
	"codeschema/internal/search"
	"codeschema/internal/store"
	"codeschema/internal/vector"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	st := store.NewStore("file")
	if err := st.Open(context.Background(), dir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return NewService(st)
}

func TestHealth(t *testing.T) {
	svc := newTestService(t)
	status := svc.Health(context.Background())
	if status.Status != "ok" {
		t.Errorf("expected ok, got %s", status.Status)
	}
	if !status.StoreOK {
		t.Error("expected store to be ok")
	}
	if status.StoreType != "file" {
		t.Errorf("expected store type file, got %s", status.StoreType)
	}
	if status.Uptime == "" {
		t.Error("expected non-empty uptime")
	}
}

func TestGetContext_EmptySymbol(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.GetContext(context.Background(), "", 5)
	if err == nil {
		t.Fatal("expected error for empty symbol")
	}
	svcErr, ok := err.(*ServiceError)
	if !ok {
		t.Fatalf("expected ServiceError, got %T", err)
	}
	if svcErr.Code != "ERR_INVALID_PARAMETER" {
		t.Errorf("expected ERR_INVALID_PARAMETER, got %s", svcErr.Code)
	}
}

func TestGetContext_Success(t *testing.T) {
	svc := newTestService(t)
	ctx, err := svc.GetContext(context.Background(), "com.example.MyClass.myMethod", 5)
	if err != nil {
		t.Fatalf("GetContext: %v", err)
	}
	if ctx.Symbol != "com.example.MyClass.myMethod" {
		t.Errorf("expected symbol, got %s", ctx.Symbol)
	}
}

func TestGetImpact_EmptyMethod(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.GetImpact(context.Background(), "", 1)
	if err == nil {
		t.Fatal("expected error for empty method")
	}
}

func TestGetImpact_Success(t *testing.T) {
	svc := newTestService(t)
	result, err := svc.GetImpact(context.Background(), "com.example.MyClass.myMethod", 2)
	if err != nil {
		t.Fatalf("GetImpact: %v", err)
	}
	if result.Method != "com.example.MyClass.myMethod" {
		t.Errorf("expected method, got %s", result.Method)
	}
	if result.Callers == nil {
		t.Error("expected non-nil callers")
	}
	if result.Callees == nil {
		t.Error("expected non-nil callees")
	}
}

func TestGetTests_EmptyMethod(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.GetTests(context.Background(), "", 60)
	if err == nil {
		t.Fatal("expected error for empty method")
	}
}

func TestGetTests_Success(t *testing.T) {
	svc := newTestService(t)
	tests, err := svc.GetTests(context.Background(), "com.example.MyClass.myMethod", 60)
	if err != nil {
		t.Fatalf("GetTests: %v", err)
	}
	if tests == nil {
		t.Error("expected non-nil tests")
	}
	if len(tests) != 0 {
		t.Errorf("expected empty tests, got %d", len(tests))
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Search(context.Background(), "", "both", 20)
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestSearch_Success(t *testing.T) {
	svc := newTestService(t)
	results, err := svc.Search(context.Background(), "MyClass", "both", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if results == nil {
		t.Error("expected non-nil results")
	}
}

func TestSearch_WithSearcher(t *testing.T) {
	svc := newTestService(t)

	// 创建搜索器并注入数据
	fts := search.NewMemoryFTS()
	ctx := context.Background()
	_ = fts.Index(ctx, "test/HelloService.java", "class HelloService { void hello() {} }")
	_ = fts.Index(ctx, "test/WorldService.java", "class WorldService { void world() {} }")

	memStore := vector.NewMemoryStore()
	model := vector.NewMockEmbedder(128)
	indexer := vector.NewIndexer(memStore, model, 2)
	adapter := search.NewVectorAdapter(indexer)
	searcher := search.NewSearcher(fts, adapter, nil)

	svc.WithSearcher(searcher)

	// 测试 FTS 精确搜索
	results, err := svc.Search(ctx, "HelloService", "exact", 10)
	if err != nil {
		t.Fatalf("Search exact: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result for exact search")
	}
	if results[0].Symbol != "test/HelloService.java" {
		t.Errorf("expected 'test/HelloService.java', got %q", results[0].Symbol)
	}

	// 测试语义搜索（回退到 FTS 但走语义路径）
	semResults, err := svc.Search(ctx, "hello", "semantic", 10)
	if err != nil {
		t.Fatalf("Search semantic: %v", err)
	}
	// 语义搜索使用 MockEmbedder 的确定性哈希，可能返回匹配结果
	if len(semResults) == 0 {
		t.Log("semantic search returned 0 results (expected with MockEmbedder)")
	}

	// 测试双路融合搜索
	bothResults, err := svc.Search(ctx, "HelloService", "both", 10)
	if err != nil {
		t.Fatalf("Search both: %v", err)
	}
	// 至少应包含 FTS 匹配结果
	_ = bothResults
}

func TestSearch_WithSearcher_ModeMapping(t *testing.T) {
	svc := newTestService(t)
	fts := search.NewMemoryFTS()
	_ = fts.Index(context.Background(), "test/Hello.java", "hello world")
	searcher := search.NewSearcher(fts, nil, nil)
	svc.WithSearcher(searcher)

	// 验证默认模式（空字符串 → both）
	results, err := svc.Search(context.Background(), "hello", "", 10)
	if err != nil {
		t.Fatalf("Search with empty mode: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results with default mode")
	}
}

func TestServiceError_HTTPStatus(t *testing.T) {
	tests := []struct {
		code string
		want int
	}{
		{"ERR_SYMBOL_NOT_FOUND", 404},
		{"ERR_INVALID_PARAMETER", 400},
		{"ERR_RATE_LIMITED", 429},
		{"ERR_UNAUTHORIZED", 401},
		{"ERR_INTERNAL", 500},
		{"UNKNOWN", 500},
	}
	for _, tt := range tests {
		err := &ServiceError{Code: tt.code}
		got := err.HTTPStatus()
		if got != tt.want {
			t.Errorf("HTTPStatus(%s) = %d, want %d", tt.code, got, tt.want)
		}
	}
}

func TestFindDependencies_EmptySymbol(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.FindDependencies(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty symbol")
	}
}

func TestGetCallGraph_EmptySymbol(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.GetCallGraph(context.Background(), "", 1)
	if err == nil {
		t.Fatal("expected error for empty symbol")
	}
}

func TestSearchConfig_EmptyPattern(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.SearchConfig(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty pattern")
	}
}

func TestGetAffected_EmptySymbol(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.GetAffected(context.Background(), "", false)
	if err == nil {
		t.Fatal("expected error for empty symbol")
	}
}

func TestResolveSymbol_File(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	kind, file := svc.resolveSymbol(ctx, "file:/path/to/my/file.go")
	if kind != "file" {
		t.Errorf("expected kind 'file', got %q", kind)
	}
	if file != "/path/to/my/file.go" {
		t.Errorf("expected file '/path/to/my/file.go', got %q", file)
	}
}

func TestResolveSymbol_Class(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Insert test data
	st := svc.store
	fileID, err := st.UpsertFile(ctx, "pkg/search/builder.go", "hash123", 250, 12345)
	if err != nil {
		t.Fatalf("upsert file: %v", err)
	}
	classes := []parser.ClassIR{
		{
			Name: "IndexBuilder",
			FullName: "codeschema/internal/search.IndexBuilder",
			Type: "CLASS",
		},
	}
	err = st.UpsertClasses(ctx, fileID, classes)
	if err != nil {
		t.Fatalf("upsert classes: %v", err)
	}

	// Resolve the class (IDs: file=1, class=2)
	kind, file := svc.resolveSymbol(ctx, "class:2")
	if kind != "class" {
		t.Errorf("expected kind 'class', got %q", kind)
	}
	if file != "pkg/search/builder.go" {
		t.Errorf("expected file 'pkg/search/builder.go', got %q", file)
	}
}

func TestResolveSymbol_ClassWithInterfaceType(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	st := svc.store
	fileID, _ := st.UpsertFile(ctx, "pkg/search/fts.go", "hashabc", 180, 8000)
	classes := []parser.ClassIR{
		{
			Name: "FTSEngine",
			FullName: "codeschema/internal/search.FTSEngine",
			Type: "INTERFACE",
		},
	}
	st.UpsertClasses(ctx, fileID, classes)

	// IDs: file=1, class=2
	kind, _ := svc.resolveSymbol(ctx, "class:2")
	if kind != "interface" {
		t.Errorf("expected kind 'interface', got %q (should lower-case type)", kind)
	}
}

func TestResolveSymbol_Method(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	st := svc.store
	fileID, err := st.UpsertFile(ctx, "pkg/search/builder.go", "hash123", 250, 12345)
	if err != nil {
		t.Fatalf("upsert file: %v", err)
	}
	classes := []parser.ClassIR{
		{
			Name: "IndexBuilder",
			FullName: "codeschema/internal/search.IndexBuilder",
			Type: "CLASS",
		},
	}
	err = st.UpsertClasses(ctx, fileID, classes)
	if err != nil {
		t.Fatalf("upsert classes: %v", err)
	}
	methods := []parser.MethodIR{
		{
			Name: "BuildFromStore",
			Signature: "BuildFromStore(ctx context.Context, st store.Store) (*BuildResult, error)",
		},
	}
	err = st.UpsertMethods(ctx, 2, methods)
	if err != nil {
		t.Fatalf("upsert methods: %v", err)
	}

	// IDs: file=1, class=2, method=3
	kind, file := svc.resolveSymbol(ctx, "method:3")
	if kind != "method" {
		t.Errorf("expected kind 'method', got %q", kind)
	}
	if file != "pkg/search/builder.go" {
		t.Errorf("expected file 'pkg/search/builder.go', got %q", file)
	}
}

func TestResolveSymbol_Invalid(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// No prefix
	kind, file := svc.resolveSymbol(ctx, "invalid-id")
	if kind != "" || file != "" {
		t.Errorf("expected empty for invalid id, got (%q, %q)", kind, file)
	}

	// Invalid number
	kind, file = svc.resolveSymbol(ctx, "class:abc")
	if kind != "" || file != "" {
		t.Errorf("expected empty for invalid number, got (%q, %q)", kind, file)
	}

	// Not found
	kind, file = svc.resolveSymbol(ctx, "class:999")
	if kind != "" || file != "" {
		t.Errorf("expected empty for not found, got (%q, %q)", kind, file)
	}
}

func TestParseInt64(t *testing.T) {
	tests := []struct {
		s    string
		want int64
	}{
		{"", 0},
		{"0", 0},
		{"1", 1},
		{"123", 123},
		{"123456", 123456},
		{"12a3", 0},
		{"abc", 0},
	}
	for _, tt := range tests {
		got := parseInt64(tt.s)
		if got != tt.want {
			t.Errorf("parseInt64(%q) = %d, want %d", tt.s, got, tt.want)
		}
	}
}

func TestEnrichResults_FillsKindAndFile(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	st := svc.store
	fileID, _ := st.UpsertFile(ctx, "pkg/search/builder.go", "hash123", 250, 12345)
	classes := []parser.ClassIR{
		{
			Name: "IndexBuilder",
			Type: "CLASS",
		},
	}
	st.UpsertClasses(ctx, fileID, classes)

	// Prepare search results with empty Kind/File
	results := []search.SearchResult{
		{Symbol: "class:2", Score: 0.8},
		{Symbol: "file:pkg/search/builder.go", Score: 0.5},
	}

	svc.enrichResults(ctx, results)

	if results[0].Kind != "class" {
		t.Errorf("expected kind 'class', got %q", results[0].Kind)
	}
	if results[0].File != "pkg/search/builder.go" {
		t.Errorf("expected file 'pkg/search/builder.go', got %q", results[0].File)
	}
	if results[1].Kind != "file" {
		t.Errorf("expected kind 'file', got %q", results[1].Kind)
	}
	if results[1].File != "pkg/search/builder.go" {
		t.Errorf("expected file 'pkg/search/builder.go', got %q", results[1].File)
	}
}

func TestEnrichResults_AlreadyFilled(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Already filled, should not change (no extra lookup)
	results := []search.SearchResult{
		{Symbol: "symbol", Kind: "method", File: "test.go", Score: 0.9},
	}

	svc.enrichResults(ctx, results)

	if results[0].Kind != "method" || results[0].File != "test.go" {
		t.Errorf("should keep existing values, got kind=%q file=%q", results[0].Kind, results[0].File)
	}
}