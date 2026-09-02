package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/idcu/codeschema/internal/ai"
	"github.com/idcu/codeschema/internal/analyzer"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/search"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/vector"
)

func newTestService(t testing.TB) *Service {
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
	seedContextFile(t, svc)

	ctx, err := svc.GetContext(context.Background(), "com.example.OrderService.GetUser", 5)
	if err != nil {
		t.Fatalf("GetContext: %v", err)
	}
	if ctx.Symbol != "com.example.OrderService.GetUser" {
		t.Errorf("expected symbol, got %s", ctx.Symbol)
	}
	// 真实解析：注入源码原文（非 P0 占位），并附带追溯字段
	if !strings.Contains(ctx.Source, "GetUser") {
		t.Errorf("expected real source body, got %q", ctx.Source)
	}
	if ctx.Trace == nil || ctx.Trace.Source != "store.GetContext" {
		t.Error("expected context injection trace on success path")
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

// TestSearchConfig_MatchSymbolDoc 验证 SearchConfig 能真实命中符号名与文档注释
// （对类/方法记录的 FullName/Name/Doc/Signature 做大小写不敏感子串匹配）。
func TestSearchConfig_MatchSymbolDoc(t *testing.T) {
	st := store.NewStore("file")
	dir := t.TempDir()
	if err := st.Open(context.Background(), dir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	fileID, err := st.UpsertFile(context.Background(), "/project/config/config.go", "h1", 20, 1024)
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	if err := st.UpsertClasses(context.Background(), fileID, []parser.ClassIR{
		{Name: "ConfigManager", FullName: "cfg.ConfigManager", Doc: "@Configuration 配置管理"},
	}); err != nil {
		t.Fatalf("UpsertClasses: %v", err)
	}
	classes, err := st.GetClassesByFileID(context.Background(), fileID)
	if err != nil || len(classes) == 0 {
		t.Fatalf("GetClassesByFileID: classes=%v err=%v", classes, err)
	}
	if err := st.UpsertMethods(context.Background(), classes[0].ID, []parser.MethodIR{
		{Name: "LoadConfig", Signature: "LoadConfig()", Doc: "加载配置"},
	}); err != nil {
		t.Fatalf("UpsertMethods: %v", err)
	}

	svc := NewService(st)
	// 命中类名与文档注释（不同大小写也命中）。
	hits, err := svc.SearchConfig(context.Background(), "config")
	if err != nil {
		t.Fatalf("SearchConfig: %v", err)
	}
	hitSet := make(map[string]bool, len(hits))
	for _, h := range hits {
		hitSet[h] = true
	}
	if !hitSet["cfg.ConfigManager"] {
		t.Errorf("expected class ConfigManager hit, got %v", hits)
	}
	// 方法名 LoadConfig 含 config 子串，理应命中；其存储 FQN 由后端生成，做子串检查。
	foundMethod := false
	for h := range hitSet {
		if strings.Contains(h, "LoadConfig") {
			foundMethod = true
			break
		}
	}
	if !foundMethod {
		t.Errorf("expected method LoadConfig hit, got %v", hits)
	}
}

// TestSearchConfig_NoHit 验证无命中时返回空（非 nil 错误）。
func TestSearchConfig_NoHit(t *testing.T) {
	svc := newTestService(t)
	hits, err := svc.SearchConfig(context.Background(), "__definitely_absent__")
	if err != nil {
		t.Fatalf("SearchConfig: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected no hits, got %v", hits)
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

	kind, file, _ := svc.resolveSymbol(ctx, "file:/path/to/my/file.go")
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
			Name:     "IndexBuilder",
			FullName: "github.com/idcu/codeschema/internal/search.IndexBuilder",
			Type:     "CLASS",
		},
	}
	err = st.UpsertClasses(ctx, fileID, classes)
	if err != nil {
		t.Fatalf("upsert classes: %v", err)
	}

	// Resolve the class (IDs: file=1, class=2)
	kind, file, fqn := svc.resolveSymbol(ctx, "class:2")
	if kind != "class" {
		t.Errorf("expected kind 'class', got %q", kind)
	}
	if file != "pkg/search/builder.go" {
		t.Errorf("expected file 'pkg/search/builder.go', got %q", file)
	}
	if fqn != "github.com/idcu/codeschema/internal/search.IndexBuilder" {
		t.Errorf("expected class fqn, got %q", fqn)
	}
}

func TestResolveSymbol_ClassWithInterfaceType(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	st := svc.store
	fileID, _ := st.UpsertFile(ctx, "pkg/search/fts.go", "hashabc", 180, 8000)
	classes := []parser.ClassIR{
		{
			Name:     "FTSEngine",
			FullName: "github.com/idcu/codeschema/internal/search.FTSEngine",
			Type:     "INTERFACE",
		},
	}
	st.UpsertClasses(ctx, fileID, classes)

	// IDs: file=1, class=2
	kind, _, _ := svc.resolveSymbol(ctx, "class:2")
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
			Name:     "IndexBuilder",
			FullName: "github.com/idcu/codeschema/internal/search.IndexBuilder",
			Type:     "CLASS",
		},
	}
	err = st.UpsertClasses(ctx, fileID, classes)
	if err != nil {
		t.Fatalf("upsert classes: %v", err)
	}
	methods := []parser.MethodIR{
		{
			Name:      "BuildFromStore",
			Signature: "BuildFromStore(ctx context.Context, st store.Store) (*BuildResult, error)",
		},
	}
	err = st.UpsertMethods(ctx, 2, methods)
	if err != nil {
		t.Fatalf("upsert methods: %v", err)
	}

	// IDs: file=1, class=2, method=3
	kind, file, fqn := svc.resolveSymbol(ctx, "method:3")
	if kind != "method" {
		t.Errorf("expected kind 'method', got %q", kind)
	}
	if file != "pkg/search/builder.go" {
		t.Errorf("expected file 'pkg/search/builder.go', got %q", file)
	}
	if fqn != "github.com/idcu/codeschema/internal/search.IndexBuilder.BuildFromStore" {
		t.Errorf("expected method fqn, got %q", fqn)
	}
}

func TestResolveSymbol_Invalid(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// No prefix
	kind, file, _ := svc.resolveSymbol(ctx, "invalid-id")
	if kind != "" || file != "" {
		t.Errorf("expected empty for invalid id, got (%q, %q)", kind, file)
	}

	// Invalid number
	kind, file, _ = svc.resolveSymbol(ctx, "class:abc")
	if kind != "" || file != "" {
		t.Errorf("expected empty for invalid number, got (%q, %q)", kind, file)
	}

	// Not found
	kind, file, _ = svc.resolveSymbol(ctx, "class:999")
	if kind != "" || file != "" {
		t.Errorf("expected empty for not found, got (%q, %q)", kind, file)
	}
}

// TestResolveSymbol_FQN 验证 resolveSymbol 同时返回与 context/impact/tests 解析口径一致的全限定名：
// 类 = ClassFQN，方法 = ClassFQN + "." + Name。这是 search→context 一致化 Fix 的回归锚点。
func TestResolveSymbol_FQN(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	st := svc.store
	fileID, err := st.UpsertFile(ctx, "pkg/search/builder.go", "hash123", 250, 12345)
	if err != nil {
		t.Fatalf("upsert file: %v", err)
	}
	classes := []parser.ClassIR{
		{
			Name:     "IndexBuilder",
			FullName: "github.com/idcu/codeschema/internal/search.IndexBuilder",
			Type:     "CLASS",
		},
	}
	if err := st.UpsertClasses(ctx, fileID, classes); err != nil {
		t.Fatalf("upsert classes: %v", err)
	}
	methods := []parser.MethodIR{
		{
			Name:      "BuildFromStore",
			Signature: "BuildFromStore(ctx context.Context, st store.Store) (*BuildResult, error)",
		},
	}
	if err := st.UpsertMethods(ctx, 2, methods); err != nil {
		t.Fatalf("upsert methods: %v", err)
	}

	// IDs: file=1, class=2, method=3
	_, _, classFQN := svc.resolveSymbol(ctx, "class:2")
	if classFQN != "github.com/idcu/codeschema/internal/search.IndexBuilder" {
		t.Errorf("class fqn mismatch: got %q", classFQN)
	}

	_, _, methodFQN := svc.resolveSymbol(ctx, "method:3")
	want := "github.com/idcu/codeschema/internal/search.IndexBuilder.BuildFromStore"
	if methodFQN != want {
		t.Errorf("method fqn mismatch: got %q, want %q", methodFQN, want)
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
func TestGetImpact_WithAnalyzer_IncludesRelatedTests(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore("file")
	if err := st.Open(context.Background(), dir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	svc := NewService(st)
	// 注入 analyzer，启用真实影响面
	an := analyzer.NewAnalyzer(st)
	svc.WithImpactAnalyzer(an)

	// 写入调用关系数据：handler.Handle → service.GetUser
	ir := &parser.IRDocument{
		Source:    "test",
		Language:  "go",
		FilePath:  "/proj/handler.go",
		FileHash:  "h1",
		LineCount: 30,
		ByteSize:  1024,
		Classes:   []parser.ClassIR{{Name: "Handler", FullName: "com.example.Handler", Type: "CLASS"}},
		Methods: []parser.MethodIR{
			{Name: "Handle", ClassFQN: "com.example.Handler"},
		},
	}
	if err := st.UpsertIR(context.Background(), ir); err != nil {
		t.Fatalf("upsert handler: %v", err)
	}

	ir2 := &parser.IRDocument{
		Source:    "test",
		Language:  "go",
		FilePath:  "/proj/service.go",
		FileHash:  "h2",
		LineCount: 50,
		ByteSize:  2048,
		Classes:   []parser.ClassIR{{Name: "Service", FullName: "com.example.Service", Type: "CLASS"}},
		Methods: []parser.MethodIR{
			{Name: "GetUser", ClassFQN: "com.example.Service"},
		},
		Calls: []parser.CallIR{
			{CallerFQN: "com.example.Handler.Handle", CalleeFQN: "com.example.Service.GetUser", CallType: "direct", LineNumber: 10},
		},
	}
	if err := st.UpsertIR(context.Background(), ir2); err != nil {
		t.Fatalf("upsert service: %v", err)
	}

	// 注入 coverage：HandlerTest.testHandle 覆盖 Handle → 应出现在 caller 的 related_tests 中
	svc.SetCoverage(map[string][]string{
		"com.example.HandlerTest.testHandle": {"com.example.Handler.Handle"},
	})

	result, err := svc.GetImpact(context.Background(), "com.example.Service.GetUser", 1)
	if err != nil {
		t.Fatalf("GetImpact: %v", err)
	}
	if result.Method != "com.example.Service.GetUser" {
		t.Errorf("expected method, got %s", result.Method)
	}
	if len(result.Callers) != 1 {
		t.Fatalf("expected 1 caller, got %d: %v", len(result.Callers), result.Callers)
	}
	if result.Callers[0].Method != "com.example.Handler.Handle" {
		t.Errorf("expected caller Handler.Handle, got %s", result.Callers[0].Method)
	}
	// 关联单测应通过 coverage 策略出现在 caller 节点上（改动一处能列出受影响单测）
	found := false
	for _, tm := range result.Callers[0].RelatedTests {
		if tm == "com.example.HandlerTest.testHandle" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected related test in caller node, got %v", result.Callers[0].RelatedTests)
	}
}

func TestGetImpact_WithoutAnalyzer_EmptyBackwardCompat(t *testing.T) {
	svc := newTestService(t)
	result, err := svc.GetImpact(context.Background(), "com.example.MyClass.myMethod", 2)
	if err != nil {
		t.Fatalf("GetImpact: %v", err)
	}
	if result.Callers == nil || result.Callees == nil {
		t.Error("expected non-nil (empty) callers/callees without analyzer")
	}
	if len(result.Callers) != 0 || len(result.Callees) != 0 {
		t.Errorf("expected empty impact without analyzer, got callers=%v callees=%v", result.Callers, result.Callees)
	}
}

// chooseLLM 模拟 LLMClient：Choose 返回固定索引，Complete 不可用。
type chooseLLM struct {
	idx int
}

func (m chooseLLM) Complete(_ context.Context, _ string) ([]string, error) { return nil, nil }
func (m chooseLLM) Choose(_ context.Context, _ string) (int, error)        { return m.idx, nil }

// TestSearch_Disambiguate 验证查询期同名方法消歧：同名方法多候选时保留 AI 选中项。
func TestSearch_Disambiguate(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore("file")
	if err := st.Open(context.Background(), dir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// 两个类各有同名方法 getUser（不同 FQN/Doc，模拟同名方法候选）
	ir1 := &parser.IRDocument{
		Source: "test", Language: "go", FilePath: "/proj/order.go", FileHash: "h1", LineCount: 10, ByteSize: 100,
		Classes: []parser.ClassIR{{Name: "OrderService", FullName: "com.x.OrderService", Type: "CLASS"}},
		Methods: []parser.MethodIR{{Name: "getUser", ClassFQN: "com.x.OrderService", Doc: "按订单维度查询用户"}},
	}
	ir2 := &parser.IRDocument{
		Source: "test", Language: "go", FilePath: "/proj/auth.go", FileHash: "h2", LineCount: 10, ByteSize: 100,
		Classes: []parser.ClassIR{{Name: "AuthService", FullName: "com.x.AuthService", Type: "CLASS"}},
		Methods: []parser.MethodIR{{Name: "getUser", ClassFQN: "com.x.AuthService", Doc: "认证上下文中的当前用户"}},
	}
	if err := st.UpsertIR(context.Background(), ir1); err != nil {
		t.Fatalf("upsert ir1: %v", err)
	}
	if err := st.UpsertIR(context.Background(), ir2); err != nil {
		t.Fatalf("upsert ir2: %v", err)
	}

	// 找到两个方法的 ID
	var id1, id2 int64
	files, _ := st.GetAllFiles(context.Background())
	for _, f := range files {
		classes, _ := st.GetClassesByFileID(context.Background(), f.ID)
		for _, cls := range classes {
			methods, _ := st.GetMethodsByClassID(context.Background(), cls.ID)
			for _, m := range methods {
				if m.Name == "getUser" {
					if id1 == 0 {
						id1 = m.ID
					} else {
						id2 = m.ID
					}
				}
			}
		}
	}
	if id1 == 0 || id2 == 0 {
		t.Fatalf("expected 2 getUser methods, got id1=%d id2=%d", id1, id2)
	}

	svc := NewService(st)
	// 注入 AI 增强层：Choose 恒选索引 0（第一个候选）
	svc.WithAIEnhancer(ai.NewEnhancer(chooseLLM{idx: 0}, ai.NewBudget(10, 10)))

	// 构造搜索结果：两个同名方法候选
	results := []search.SearchResult{
		{Symbol: fmt.Sprintf("method:%d", id1), Score: 0.9, Source: "fts"},
		{Symbol: fmt.Sprintf("method:%d", id2), Score: 0.8, Source: "fts"},
	}

	got := svc.disambiguateMethodResults(context.Background(), "查询当前登录用户", results)
	if len(got) != 1 {
		t.Fatalf("expected 1 result after disambiguation, got %d: %v", len(got), got)
	}
	if got[0].Symbol != fmt.Sprintf("method:%d", id1) {
		t.Errorf("expected chosen method:%d, got %s", id1, got[0].Symbol)
	}
}

// TestSearch_Disambiguate_NoEnhancer 验证未注入 enhancer 时结果原样返回（向后兼容）。
func TestSearch_Disambiguate_NoEnhancer(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore("file")
	if err := st.Open(context.Background(), dir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	svc := NewService(st)
	results := []search.SearchResult{
		{Symbol: "method:1", Score: 0.9},
		{Symbol: "method:2", Score: 0.8},
	}
	got := svc.disambiguateMethodResults(context.Background(), "查询", results)
	if len(got) != 2 {
		t.Fatalf("expected 2 results without enhancer, got %d", len(got))
	}
}

// TestSearch_Disambiguate_BudgetExhausted 验证预算耗尽时回退到原始结果。
func TestSearch_Disambiguate_BudgetExhausted(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore("file")
	if err := st.Open(context.Background(), dir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	svc := NewService(st)
	// 查询预算 0：Disambiguate 触发 ErrBudgetExceeded → 不消歧
	svc.WithAIEnhancer(ai.NewEnhancer(chooseLLM{idx: 0}, ai.NewBudget(10, 0)))
	results := []search.SearchResult{
		{Symbol: "method:1", Score: 0.9},
		{Symbol: "method:2", Score: 0.8},
	}
	got := svc.disambiguateMethodResults(context.Background(), "查询", results)
	if len(got) != 2 {
		t.Fatalf("expected 2 results with exhausted budget, got %d", len(got))
	}
}

// TestSearchByTags_MultiTagAND 验证 Service 层多标签 AND 检索：
// 只返回同时拥有全部标签的类/方法，并正确解析全限定名。
func TestSearchByTags_MultiTagAND(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	st := svc.store

	// 两个类：A=controller+service，B=controller
	fidA, _ := st.UpsertFile(ctx, "/a.go", "h1", 10, 100)
	fidB, _ := st.UpsertFile(ctx, "/b.go", "h2", 10, 100)
	if err := st.UpsertClasses(ctx, fidA, []parser.ClassIR{{Name: "A", FullName: "com.example.A", Type: "CLASS"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertClasses(ctx, fidB, []parser.ClassIR{{Name: "B", FullName: "com.example.B", Type: "CLASS"}}); err != nil {
		t.Fatal(err)
	}
	classesA, _ := st.GetClassesByFileID(ctx, fidA)
	classesB, _ := st.GetClassesByFileID(ctx, fidB)
	if err := st.UpsertTags(ctx, classesA[0].ID, []string{"controller", "service"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertTags(ctx, classesB[0].ID, []string{"controller"}); err != nil {
		t.Fatal(err)
	}

	// controller+service（AND）→ 仅 A
	res, err := svc.SearchByTags(ctx, []string{"controller", "service"})
	if err != nil {
		t.Fatalf("SearchByTags: %v", err)
	}
	if res.Tag != "controller,service" {
		t.Errorf("expected Tag controller,service, got %s", res.Tag)
	}
	if len(res.Classes) != 1 || res.Classes[0] != "com.example.A" {
		t.Fatalf("expected only com.example.A, got %v", res.Classes)
	}
	if len(res.MethodIDs) != 0 || len(res.Methods) != 0 {
		t.Fatalf("expected no methods, got %v %v", res.MethodIDs, res.Methods)
	}

	// 单标签 controller → A、B（兼容入口 SearchByTag）
	res, err = svc.SearchByTag(ctx, "controller")
	if err != nil {
		t.Fatalf("SearchByTag: %v", err)
	}
	if len(res.Classes) != 2 {
		t.Fatalf("expected 2 classes, got %v", res.Classes)
	}
}

// TestSearchByTags_Validation 验证多标签参数校验。
func TestSearchByTags_Validation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// 空列表
	_, err := svc.SearchByTags(ctx, nil)
	if err == nil {
		t.Fatal("expected error for nil tags")
	}
	svcErr, ok := err.(*ServiceError)
	if !ok || svcErr.Code != "ERR_INVALID_PARAMETER" {
		t.Fatalf("expected ERR_INVALID_PARAMETER, got %v", err)
	}

	// 含空标签
	_, err = svc.SearchByTags(ctx, []string{"service", ""})
	if err == nil {
		t.Fatal("expected error for empty tag")
	}
	svcErr, ok = err.(*ServiceError)
	if !ok || svcErr.Code != "ERR_INVALID_PARAMETER" {
		t.Fatalf("expected ERR_INVALID_PARAMETER, got %v", err)
	}
}

// TestSearchByTags_MethodResult 验证方法多标签检索结果解析。
func TestSearchByTags_MethodResult(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	st := svc.store

	fid, _ := st.UpsertFile(ctx, "/a.go", "h", 10, 100)
	if err := st.UpsertClasses(ctx, fid, []parser.ClassIR{{Name: "A", FullName: "com.example.A", Type: "CLASS"}}); err != nil {
		t.Fatal(err)
	}
	classes, _ := st.GetClassesByFileID(ctx, fid)
	cid := classes[0].ID
	if err := st.UpsertMethods(ctx, cid, []parser.MethodIR{
		{Name: "Get", ClassFQN: "com.example.A"},
		{Name: "Put", ClassFQN: "com.example.A"},
	}); err != nil {
		t.Fatal(err)
	}
	methods, _ := st.GetMethodsByClassID(ctx, cid)
	for _, m := range methods {
		var tags []string
		if m.Name == "Get" {
			tags = []string{"cache", "read"}
		} else {
			tags = []string{"cache"}
		}
		if err := st.UpsertMethodTags(ctx, m.ID, tags); err != nil {
			t.Fatal(err)
		}
	}

	res, err := svc.SearchByTags(ctx, []string{"cache", "read"})
	if err != nil {
		t.Fatalf("SearchByTags: %v", err)
	}
	if len(res.Classes) != 0 {
		t.Fatalf("expected no class, got %v", res.Classes)
	}
	if len(res.Methods) != 1 || res.Methods[0] != "com.example.A.Get" {
		t.Fatalf("expected only com.example.A.Get, got %v", res.Methods)
	}
}

// TestGetAffected_RequiresSymbol 验证 GetAffected 空符号入参报错（替换原空壳）。
func TestGetAffected_RequiresSymbol(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.GetAffected(context.Background(), "", false); err == nil {
		t.Fatal("expected error for empty symbol")
	}
}

// TestGetAffected_ReturnsLinkedTests 验证 GetAffected 收敛为受影响单测列表。
//
// 不依赖 analyzer（mock 无 call 边）：仅以 symbol 自身经 FindTestLinks 五策略
// 关联到的命名单测。验证空壳已被真实逻辑替换，且结果去重正确。
func TestGetAffected_ReturnsLinkedTests(t *testing.T) {
	ctx := context.Background()
	st := &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/internal/order/service.go", Language: "go"},
			{ID: 2, AbsolutePath: "/project/internal/order/service_test.go", Language: "go"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "OrderService", FullName: "github.com/idcu/codeschema/internal/order.OrderService"}},
			2: {{ID: 20, FileID: 2, Name: "OrderServiceTest", FullName: "github.com/idcu/codeschema/internal/order.OrderServiceTest"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {{ID: 100, ClassID: 10, Name: "getOrder", FullName: "github.com/idcu/codeschema/internal/order.OrderService.getOrder"}},
			20: {{ID: 200, ClassID: 20, Name: "TestGetOrder", FullName: "github.com/idcu/codeschema/internal/order.OrderServiceTest.TestGetOrder"}},
		},
	}
	svc := NewService(st)

	got, err := svc.GetAffected(ctx, "github.com/idcu/codeschema/internal/order.OrderService.getOrder", false)
	if err != nil {
		t.Fatalf("GetAffected: %v", err)
	}
	found := false
	for _, m := range got {
		if m == "github.com/idcu/codeschema/internal/order.OrderServiceTest.TestGetOrder" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected affected test TestGetOrder, got %v", got)
	}
}

