package search

import (
	"context"
	"testing"

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
