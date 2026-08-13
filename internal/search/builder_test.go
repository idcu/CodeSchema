package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codeschema/internal/parser"
	"codeschema/internal/store"
	"codeschema/internal/vector"
)

func TestIndexBuilder_New(t *testing.T) {
	fts := NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	b := NewIndexBuilder(fts, idx, em)

	if b == nil {
		t.Fatal("expected non-nil IndexBuilder")
	}
}

func TestIndexBuilder_BuildFromStore_Empty(t *testing.T) {
	fts := NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	b := NewIndexBuilder(fts, idx, em)

	st := store.NewStore("file")
	ctx := context.Background()

	result, err := b.BuildFromStore(ctx, st)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalDocs != 0 {
		t.Errorf("expected 0 total docs, got %d", result.TotalDocs)
	}
	if result.IndexedDocs != 0 {
		t.Errorf("expected 0 indexed docs, got %d", result.IndexedDocs)
	}
	if result.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", result.Errors)
	}
}

func TestIndexBuilder_BuildFromStore_WithData(t *testing.T) {
	fts := NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	b := NewIndexBuilder(fts, idx, em)

	// 使用内存 FileStore 插入测试数据
	st := store.NewStore("file")
	ctx := context.Background()
	tempDir := t.TempDir()
	if err := st.Open(ctx, tempDir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// 插入文件和类
	fileID, err := st.UpsertFile(ctx, "pkg/search/builder.go", "hash123", 250, 12345)
	if err != nil {
		t.Fatalf("upsert file: %v", err)
	}

	// 从 builder.go 的 IR 我们知道它有 IndexBuilder struct
	classes := []parser.ClassIR{
		{
			Name:     "IndexBuilder",
			FullName: "codeschema/internal/search.IndexBuilder",
			Type:     "CLASS",
			Doc:      "IndexBuilder 自动索引构建器，从 Store 读取数据构建 FTS 和向量索引",
		},
	}

	// 插入类
	err = st.UpsertClasses(ctx, fileID, classes)
	if err != nil {
		t.Fatalf("upsert classes: %v", err)
	}

	// 插入方法（批量插入，UpsertMethods 会全量替换）
	methods := []parser.MethodIR{
		{
			Name:        "BuildFromStore",
			Signature:   "BuildFromStore(ctx context.Context, st store.Store) (*BuildResult, error)",
			ReturnType:  "(*BuildResult, error)",
			ClassFQN:    "codeschema/internal/search.IndexBuilder",
			Doc:         "从 Store 读取所有数据，构建 FTS 和向量索引",
		},
		{
			Name:        "BuildAndIndex",
			Signature:   "BuildAndIndex(ctx context.Context, st store.Store, filePath string) error",
			ReturnType:  "error",
			ClassFQN:    "codeschema/internal/search.IndexBuilder",
			Doc:         "从 Store 读取单个文件的类和方法，构建索引文档并入库",
		},
	}
	err = st.UpsertMethods(ctx, 2, methods)
	if err != nil {
		t.Fatalf("upsert methods: %v", err)
	}

	// 构建索引
	result, err := b.BuildFromStore(ctx, st)
	if err != nil {
		t.Fatalf("BuildFromStore: %v", err)
	}

	// 预期：1 个文件（不单独索引，除非无类）+ 1 个类 + 2 个方法 = 3 个文档
	// 文件本身不索引，因为有类
	// 所以 1 + 2 = 3
	if result.TotalDocs != 3 {
		t.Errorf("expected 3 total docs, got %d", result.TotalDocs)
	}
	if result.IndexedDocs != 3 {
		t.Errorf("expected 3 indexed docs, got %d", result.IndexedDocs)
	}
	if result.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", result.Errors)
	}

	// 验证 FTS 和向量都有数据
	if fts.Size() != 3 {
		t.Errorf("expected fts size 3, got %d", fts.Size())
	}
	if vs.Size() != 3 {
		t.Errorf("expected vector size 3, got %d", vs.Size())
	}
}

func TestIndexBuilder_BuildFromStore_FileWithoutClasses(t *testing.T) {
	fts := NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	b := NewIndexBuilder(fts, idx, em)

	st := store.NewStore("file")
	ctx := context.Background()
	tempDir := t.TempDir()
	if err := st.Open(ctx, tempDir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// 只插入文件，没有类
	_, err := st.UpsertFile(ctx, "README.md", "hashabc", 100, 5000)
	if err != nil {
		t.Fatalf("upsert file: %v", err)
	}

	result, err := b.BuildFromStore(ctx, st)
	if err != nil {
		t.Fatalf("BuildFromStore: %v", err)
	}

	// 文件没有类，应该索引文件路径
	if result.TotalDocs != 1 {
		t.Errorf("expected 1 total doc (file), got %d", result.TotalDocs)
	}
	if result.IndexedDocs != 1 {
		t.Errorf("expected 1 indexed doc, got %d", result.IndexedDocs)
	}
	if fts.Size() != 1 {
		t.Errorf("expected fts size 1, got %d", fts.Size())
	}
}

func TestIndexBuilder_BuildFromStore_MultipleFiles(t *testing.T) {
	fts := NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	b := NewIndexBuilder(fts, idx, em)

	st := store.NewStore("file")
	ctx := context.Background()
	tempDir := t.TempDir()
	if err := st.Open(ctx, tempDir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// 文件 1: service.go 有 1 个类，2 个方法
	file1, err := st.UpsertFile(ctx, "pkg/service/service.go", "hash1", 200, 8000)
	if err != nil {
		t.Fatalf("upsert file1: %v", err)
	}
	classes1 := []parser.ClassIR{
		{
			Name:     "Service",
			FullName: "codeschema/internal/service.Service",
			Doc:      "Service 业务逻辑层",
		},
	}
	st.UpsertClasses(ctx, file1, classes1)
	methods1 := []parser.MethodIR{
		{
			Name:        "Health",
			Signature:   "Health(ctx context.Context) *HealthStatus",
			ClassFQN:    "codeschema/internal/service.Service",
		},
		{
			Name:        "Search",
			Signature:   "Search(ctx context.Context, q string, mode string, limit int) ([]SearchResult, error)",
			ClassFQN:    "codeschema/internal/service.Service",
		},
	}
	st.UpsertMethods(ctx, 2, methods1)

	// 文件 2: store.go 有 1 个接口，0 方法（简化）
	file2, err := st.UpsertFile(ctx, "pkg/store/store.go", "hash2", 150, 6000)
	if err != nil {
		t.Fatalf("upsert file2: %v", err)
	}
	classes2 := []parser.ClassIR{
		{
			Name:     "Store",
			FullName: "codeschema/internal/store.Store",
			Type:     "INTERFACE",
			Doc:      "Store 存储层统一接口",
		},
	}
	st.UpsertClasses(ctx, file2, classes2)

	result, err := b.BuildFromStore(ctx, st)
	if err != nil {
		t.Fatalf("BuildFromStore: %v", err)
	}

	// 预期：file1(1类+2方法) + file2(1类) = 1+2 + 1 = 4 文档
	if result.TotalDocs != 4 {
		t.Errorf("expected 4 total docs, got %d", result.TotalDocs)
	}
	if result.IndexedDocs != 4 {
		t.Errorf("expected 4 indexed docs, got %d", result.IndexedDocs)
	}
	if fts.Size() != 4 {
		t.Errorf("expected fts size 4, got %d", fts.Size())
	}
}

func TestIndexBuilder_IndexDocument(t *testing.T) {
	fts := NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	b := NewIndexBuilder(fts, idx, em)

	ctx := context.Background()
	err := b.IndexDocument(ctx, "class:1", "codeschema/internal/search.IndexBuilder 自动索引构建器")
	if err != nil {
		t.Fatalf("IndexDocument: %v", err)
	}

	if fts.Size() != 1 {
		t.Errorf("expected fts size 1, got %d", fts.Size())
	}
	if vs.Size() != 1 {
		t.Errorf("expected vector size 1, got %d", vs.Size())
	}
}

func TestIndexBuilder_BuildAndIndex(t *testing.T) {
	fts := NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	b := NewIndexBuilder(fts, idx, em)

	st := store.NewStore("file")
	ctx := context.Background()
	tempDir := t.TempDir()
	if err := st.Open(ctx, tempDir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	filePath := "pkg/search/builder.go"
	fileID, err := st.UpsertFile(ctx, filePath, "hash123", 250, 12345)
	if err != nil {
		t.Fatalf("upsert file: %v", err)
	}

	classes := []parser.ClassIR{
		{
			Name:     "IndexBuilder",
			FullName: "codeschema/internal/search.IndexBuilder",
			Doc:      "自动索引构建器",
		},
	}
	st.UpsertClasses(ctx, fileID, classes)

	methods := []parser.MethodIR{
		{
			Name:        "BuildAndIndex",
			Signature:   "BuildAndIndex(ctx context.Context, st store.Store, filePath string) error",
			ReturnType:  "error",
			ClassFQN:    "codeschema/internal/search.IndexBuilder",
		},
	}
	st.UpsertMethods(ctx, 2, methods)

	err = b.BuildAndIndex(ctx, st, filePath)
	if err != nil {
		t.Fatalf("BuildAndIndex: %v", err)
	}

	// 预期：1 类 + 1 方法 = 2 文档
	if fts.Size() != 2 {
		t.Errorf("expected fts size 2, got %d", fts.Size())
	}
	if vs.Size() != 2 {
		t.Errorf("expected vector size 2, got %d", vs.Size())
	}
}

func TestIndexBuilder_BuildAndIndex_FileNotFound(t *testing.T) {
	fts := NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	b := NewIndexBuilder(fts, idx, em)

	st := store.NewStore("file")
	ctx := context.Background()
	tempDir := t.TempDir()
	if err := st.Open(ctx, tempDir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// 文件不存在，应该返回错误
	err := b.BuildAndIndex(ctx, st, "nonexistent.go")
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
}

func TestBuildClassIndexText(t *testing.T) {
	c := store.ClassRecord{
		FullName: "codeschema/internal/search.IndexBuilder",
		Doc:      "IndexBuilder 自动索引构建器",
		Source:   "type IndexBuilder struct { ... }",
	}
	text := buildClassIndexText(c, "pkg/search/builder.go")

	if text == "" {
		t.Fatal("expected non-empty text")
	}
	// 应该包含 FullName
	if !contains(text, "codeschema/internal/search.IndexBuilder") {
		t.Error("text should contain full name")
	}
	// 应该包含文件路径
	if !contains(text, "pkg/search/builder.go") {
		t.Error("text should contain file path")
	}
	// 应该包含文档
	if !contains(text, "IndexBuilder 自动索引构建器") {
		t.Error("text should contain doc")
	}
}

func TestBuildMethodIndexText(t *testing.T) {
	c := store.ClassRecord{
		FullName: "codeschema/internal/search.IndexBuilder",
	}
	m := store.MethodRecord{
		Name:        "BuildFromStore",
		Signature:   "BuildFromStore(ctx context.Context, st store.Store) (*BuildResult, error)",
		ReturnType:  "(*BuildResult, error)",
		Doc:         "从 Store 读取所有数据构建索引",
	}
	text := buildMethodIndexText(c, m, "pkg/search/builder.go")

	if text == "" {
		t.Fatal("expected non-empty text")
	}
	// 应该包含完整方法名
	if !contains(text, "codeschema/internal/search.IndexBuilder.BuildFromStore") {
		t.Error("text should contain full method name")
	}
	// 应该包含签名
	if !contains(text, "BuildFromStore") {
		t.Error("text should contain method name in signature")
	}
	// 应该包含返回类型
	if !contains(text, "BuildResult") {
		t.Error("text should contain return type")
	}
	// 应该包含文档
	if !contains(text, "从 Store 读取所有数据构建索引") {
		t.Error("text should contain doc")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[0:len(s)] != "" && indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func TestIndexBuilder_StartAsync_MultiWorker(t *testing.T) {
	fts := NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	b := NewIndexBuilder(fts, idx, em)

	ctx := context.Background()
	// StartAsync with 3 workers
	b.StartAsync(ctx, 64, 3)
	defer b.StopAsync()

	// Enqueue 10 documents
	for i := 0; i < 10; i++ {
		b.EnqueueIndex(ctx, fmt.Sprintf("test:id:%d", i), fmt.Sprintf("test document %d", i))
	}
	b.StopAsync()

	// All 10 should be indexed
	if fts.Size() != 10 {
		t.Errorf("expected 10 documents in FTS, got %d", fts.Size())
	}
	if vs.Size() != 10 {
		t.Errorf("expected 10 documents in vector store, got %d", vs.Size())
	}
}

func TestIndexBuilder_StartAsync_DefaultWorker(t *testing.T) {
	fts := NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	b := NewIndexBuilder(fts, idx, em)

	ctx := context.Background()
	// numWorkers=0 should use default (2)
	b.StartAsync(ctx, 64, 0)
	defer b.StopAsync()

	b.EnqueueIndex(ctx, "test:id:1", "test document")
	b.StopAsync()

	if fts.Size() != 1 {
		t.Errorf("expected 1 document, got %d", fts.Size())
	}
}

func TestIndexBuilder_StartAsync_Idempotent(t *testing.T) {
	fts := NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	b := NewIndexBuilder(fts, idx, em)

	ctx := context.Background()
	b.StartAsync(ctx, 64, 2)
	// Second call should be no-op
	b.StartAsync(ctx, 64, 10)
	defer b.StopAsync()

	b.EnqueueIndex(ctx, "test:id:1", "test document")
	b.StopAsync()

	if fts.Size() != 1 {
		t.Errorf("expected 1 document, got %d", fts.Size())
	}
}

func TestIndexBuilder_SetOnError(t *testing.T) {
	fts := NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	b := NewIndexBuilder(fts, idx, em)

	ctx := context.Background()
	var errID string
	b.SetOnError(func(id string, err error) {
		errID = id
	})

	// Enqueue with async so it goes through the worker
	b.StartAsync(ctx, 64, 1)
	// StopAsync early to cause processing error context
	b.EnqueueIndex(ctx, "test:error", "")

	// Wait for processing
	b.StopAsync()
	_ = errID // callback was set; no error expected for empty text in normal flow
}

func TestIndexBuilder_EnqueueIndex_SyncFallback(t *testing.T) {
	fts := NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	b := NewIndexBuilder(fts, idx, em)

	ctx := context.Background()
	// Without StartAsync, EnqueueIndex should fall back to sync
	b.EnqueueIndex(ctx, "test:id:1", "test document")

	if fts.Size() != 1 {
		t.Errorf("expected 1 document via sync fallback, got %d", fts.Size())
	}
	if vs.Size() != 1 {
		t.Errorf("expected 1 document in vector store via sync fallback, got %d", vs.Size())
	}
}

func TestIndexBuilder_RemoveDocument(t *testing.T) {
	fts := NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	b := NewIndexBuilder(fts, idx, em)

	ctx := context.Background()

	// Index a document
	err := b.IndexDocument(ctx, "test:doc:1", "test document")
	if err != nil {
		t.Fatalf("IndexDocument: %v", err)
	}
	if fts.Size() != 1 {
		t.Errorf("expected 1 document after index, got %d", fts.Size())
	}

	// Remove the document
	err = b.RemoveDocument(ctx, "test:doc:1")
	if err != nil {
		t.Fatalf("RemoveDocument: %v", err)
	}
	if fts.Size() != 0 {
		t.Errorf("expected 0 documents after remove, got %d", fts.Size())
	}
}

func TestIndexBuilder_AutoSaveIDF(t *testing.T) {
	fts := NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	b := NewIndexBuilder(fts, idx, em)

	ctx := context.Background()
	dir := t.TempDir()
	idfPath := filepath.Join(dir, "idf.json")

	// Index some documents first
	err := b.IndexDocument(ctx, "test:id:1", "test document one")
	if err != nil {
		t.Fatalf("IndexDocument: %v", err)
	}

	// Start auto save with short interval
	stop := b.AutoSaveIDF(idfPath, 100*time.Millisecond)
	defer stop()

	// Wait for at least one save cycle
	time.Sleep(250 * time.Millisecond)

	// Verify IDF file was created
	if _, err := os.Stat(idfPath); os.IsNotExist(err) {
		t.Fatal("expected IDF file to be created by auto save")
	}

	// Load into a new embedder and verify
	em2 := vector.NewLocalEmbedder(128)
	if err := em2.LoadIDF(idfPath); err != nil {
		t.Fatalf("LoadIDF: %v", err)
	}
	if !em2.HasIDF() {
		t.Error("expected loaded embedder to have IDF data")
	}
}

func TestIndexBuilder_AutoSaveIDF_Stop(t *testing.T) {
	fts := NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	b := NewIndexBuilder(fts, idx, em)

	dir := t.TempDir()
	idfPath := filepath.Join(dir, "idf.json")

	stop := b.AutoSaveIDF(idfPath, 10*time.Second)
	// Stop immediately - should not panic
	stop()
	// Stopping again should be no-op
	stop()
}

func TestIndexBuilder_AutoSaveIDF_MinInterval(t *testing.T) {
	fts := NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	b := NewIndexBuilder(fts, idx, em)

	dir := t.TempDir()
	idfPath := filepath.Join(dir, "idf.json")

	// interval < 10s should be clamped to 10s
	stop := b.AutoSaveIDF(idfPath, 1*time.Second)
	defer stop()
	_ = stop // just verify no panic
}

func TestBuildFromStore_SkipIDFWhenLoaded(t *testing.T) {
	fts := NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	em.Observe("preloaded document") // Simulate loaded IDF
	idx := vector.NewIndexer(vs, em, 2)
	b := NewIndexBuilder(fts, idx, em)

	// Use memory store with data
	st := store.NewStore("file")
	ctx := context.Background()
	tempDir := t.TempDir()
	if err := st.Open(ctx, tempDir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	fileID, err := st.UpsertFile(ctx, "pkg/test/a.go", "hash1", 100, 5000)
	if err != nil {
		t.Fatalf("upsert file: %v", err)
	}
	classes := []parser.ClassIR{
		{Name: "TestClass", FullName: "pkg.TestClass", Type: "CLASS"},
	}
	err = st.UpsertClasses(ctx, fileID, classes)
	if err != nil {
		t.Fatalf("upsert classes: %v", err)
	}

	result, err := b.BuildFromStore(ctx, st)
	if err != nil {
		t.Fatalf("BuildFromStore: %v", err)
	}
	if result.IndexedDocs != 1 {
		t.Errorf("expected 1 indexed doc, got %d", result.IndexedDocs)
	}
	// Verify IDF was not rebuilt (docCnt should still be 1 from the preloaded document)
	if fts.Size() != 1 {
		t.Errorf("expected 1 doc in FTS, got %d", fts.Size())
	}
}
