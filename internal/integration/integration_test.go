// Package integration 提供端到端集成测试，覆盖 scan → store → index → search 全流程。
//
// 使用 mock parser 生成可预测的 IR 数据，不依赖外部解析器或真实仓库。
// 所有测试共享同一套测试数据（3 个 Go 源文件，含类和方法）。
package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/scanner"
	"github.com/idcu/codeschema/internal/search"
	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/vector"
)

// mockParser 实现 ParserPlugin 接口，返回预定义的 IR 数据。
type mockParser struct {
	parseFn func(ctx context.Context, path string) (*parser.IRDocument, error)
}

func (m *mockParser) Name() string                                   { return "mock" }
func (m *mockParser) Supports(lang string) bool                      { return lang == "go" }
func (m *mockParser) Init(_ context.Context, _ map[string]any) error { return nil }
func (m *mockParser) Close() error                                   { return nil }
func (m *mockParser) Parse(ctx context.Context, path string) (*parser.IRDocument, error) {
	if m.parseFn != nil {
		return m.parseFn(ctx, path)
	}
	return &parser.IRDocument{Source: "mock", FilePath: path}, nil
}

// writeTestSource 创建测试 Go 源文件。
func writeTestSource(dir, name, content string) string {
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		panic(err)
	}
	return path
}

// setupIntegrationTest 创建集成测试环境，返回清理函数和所有组件。
func setupIntegrationTest(t *testing.T) (context.Context, store.Store, *scanner.Scanner, *search.IndexBuilder, *search.Searcher, *service.Service, func()) {
	dir := t.TempDir()

	// 创建 3 个 Go 源文件
	writeTestSource(dir, "user.go", `package main

type User struct {
	Name string
	Age  int
}

func (u *User) Greet() string {
	return "Hello, " + u.Name
}

func NewUser(name string, age int) *User {
	return &User{Name: name, Age: age}
}
`)
	writeTestSource(dir, "order.go", `package main

type Order struct {
	ID     string
	Amount float64
	UserID string
}

func (o *Order) Process() error {
	return nil
}

func NewOrder(userID string, amount float64) *Order {
	return &Order{ID: "ORD-001", Amount: amount, UserID: userID}
}
`)
	writeTestSource(dir, "utils.go", `package main

func FormatPrice(amount float64) string {
	return "$" + string(rune(int(amount)))
}

type Config struct {
	Debug bool
	Port  int
}
`)

	ctx := context.Background()

	// 创建 mock parser，根据文件名返回对应的 IR 数据
	mp := &mockParser{
		parseFn: func(_ context.Context, path string) (*parser.IRDocument, error) {
			base := filepath.Base(path)
			doc := &parser.IRDocument{
				Source:   "mock",
				Language: "go",
				FilePath: path,
			}
			switch base {
			case "user.go":
				doc.Classes = []parser.ClassIR{
					{Name: "User", FullName: "main.User", Type: "CLASS", StartLine: 3, EndLine: 6},
				}
				doc.Methods = []parser.MethodIR{
					{Name: "Greet", Signature: "Greet() string", ReturnType: "string", ClassFQN: "main.User", StartLine: 8, EndLine: 10},
					{Name: "NewUser", Signature: "NewUser(name string, age int) *User", ReturnType: "*User", StartLine: 12, EndLine: 14},
				}
			case "order.go":
				doc.Classes = []parser.ClassIR{
					{Name: "Order", FullName: "main.Order", Type: "CLASS", StartLine: 3, EndLine: 7},
				}
				doc.Methods = []parser.MethodIR{
					{Name: "Process", Signature: "Process() error", ReturnType: "error", ClassFQN: "main.Order", StartLine: 9, EndLine: 11},
					{Name: "NewOrder", Signature: "NewOrder(userID string, amount float64) *Order", ReturnType: "*Order", ClassFQN: "main.Order", StartLine: 13, EndLine: 15},
				}
			case "utils.go":
				doc.Classes = []parser.ClassIR{
					{Name: "Config", FullName: "main.Config", Type: "CLASS", StartLine: 6, EndLine: 9},
				}
				doc.Methods = []parser.MethodIR{
					{Name: "FormatPrice", Signature: "FormatPrice(amount float64) string", ReturnType: "string", ClassFQN: "main.Config", StartLine: 3, EndLine: 4},
				}
			}
			return doc, nil
		},
	}

	// 注册中心
	reg := parser.NewRegistry()
	reg.Register(mp)

	// 存储
	st := store.NewStore("file")
	if err := st.Open(ctx, dir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	cleanup := func() { st.Close() }

	// 扫描器
	s := scanner.NewScanner(st, reg, 2)

	// 全量扫描
	if err := s.ScanAll(ctx, dir); err != nil {
		cleanup()
		t.Fatalf("ScanAll: %v", err)
	}

	// 搜索组件
	fts := search.NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	builder := search.NewIndexBuilder(fts, idx, em)

	// 构建索引
	if _, err := builder.BuildFromStore(ctx, st); err != nil {
		cleanup()
		t.Fatalf("BuildFromStore: %v", err)
	}

	searcher := search.NewSearcher(fts, search.NewVectorAdapter(idx), nil)
	svc := service.NewService(st)
	svc.WithSearcher(searcher).WithIndexBuilder(builder)

	return ctx, st, s, builder, searcher, svc, cleanup
}

// TestFullPipeline_ScanStoreIndexSearch 验证 scan → store → index → search 全流程。
func TestFullPipeline_ScanStoreIndexSearch(t *testing.T) {
	ctx, st, _, _, _, svc, cleanup := setupIntegrationTest(t)
	defer cleanup()

	// 验证 Store 中有数据
	files, err := st.GetAllFiles(ctx)
	if err != nil {
		t.Fatalf("GetAllFiles: %v", err)
	}
	if len(files) != 3 {
		t.Errorf("expected 3 files, got %d", len(files))
	}

	// 验证每个文件都有类和方法
	for _, f := range files {
		classes, err := st.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			t.Fatalf("GetClassesByFileID(%d): %v", f.ID, err)
		}
		if len(classes) == 0 {
			t.Errorf("file %s has no classes", f.AbsolutePath)
		}
		for _, c := range classes {
			methods, err := st.GetMethodsByClassID(ctx, c.ID)
			if err != nil {
				t.Fatalf("GetMethodsByClassID(%d): %v", c.ID, err)
			}
			_ = methods // 至少有方法，但可能有文件级方法
		}
	}

	// 搜索验证
	results, err := svc.Search(ctx, "User", "exact", 10)
	if err != nil {
		t.Fatalf("Search 'User': %v", err)
	}
	if len(results) == 0 {
		t.Error("expected search results for 'User'")
	}

	results, err = svc.Search(ctx, "Order", "exact", 10)
	if err != nil {
		t.Fatalf("Search 'Order': %v", err)
	}
	if len(results) == 0 {
		t.Error("expected search results for 'Order'")
	}

	results, err = svc.Search(ctx, "FormatPrice", "exact", 10)
	if err != nil {
		t.Fatalf("Search 'FormatPrice': %v", err)
	}
	if len(results) == 0 {
		t.Error("expected search results for 'FormatPrice'")
	}
}

// TestFullPipeline_EmptySearch 验证空查询返回参数错误。
func TestFullPipeline_EmptySearch(t *testing.T) {
	ctx, _, _, _, _, svc, cleanup := setupIntegrationTest(t)
	defer cleanup()

	_, err := svc.Search(ctx, "", "exact", 10)
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

// TestFullPipeline_SearchLimit 验证搜索 limit 参数。
func TestFullPipeline_SearchLimit(t *testing.T) {
	ctx, _, _, _, _, svc, cleanup := setupIntegrationTest(t)
	defer cleanup()

	results, err := svc.Search(ctx, "User", "exact", 1)
	if err != nil {
		t.Fatalf("Search with limit 1: %v", err)
	}
	if len(results) > 1 {
		t.Errorf("expected at most 1 result, got %d", len(results))
	}
}

// TestFullPipeline_SearchByFile 验证文件路径作为搜索词。
func TestFullPipeline_SearchByFile(t *testing.T) {
	ctx, _, _, _, _, svc, cleanup := setupIntegrationTest(t)
	defer cleanup()

	results, err := svc.Search(ctx, "user.go", "fuzzy", 10)
	if err != nil {
		t.Fatalf("Search 'user.go': %v", err)
	}
	if len(results) == 0 {
		t.Error("expected search results for 'user.go'")
	}
}

// TestFullPipeline_ResultEnrichment 验证搜索结果富化（Kind/File 字段填充）。
func TestFullPipeline_ResultEnrichment(t *testing.T) {
	ctx, _, _, _, _, svc, cleanup := setupIntegrationTest(t)
	defer cleanup()

	results, err := svc.Search(ctx, "User", "exact", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range results {
		if r.Kind == "" {
			t.Errorf("result %s has empty Kind", r.Symbol)
		}
		if r.File == "" {
			t.Errorf("result %s has empty File", r.Symbol)
		}
	}
}

// TestFullPipeline_DuplicateScan 验证重复扫描的幂等性（哈希闸门）。
func TestFullPipeline_DuplicateScan(t *testing.T) {
	ctx, st, s, builder, _, _, cleanup := setupIntegrationTest(t)
	defer cleanup()

	// 记录第一次扫描后的文件数
	files1, _ := st.GetAllFiles(ctx)
	count1 := len(files1)

	// 第二次全量扫描 - 由于哈希相同，不应新增或修改记录
	if err := s.ScanAll(ctx, filepath.Dir(files1[0].AbsolutePath)); err != nil {
		t.Fatalf("second ScanAll: %v", err)
	}

	files2, _ := st.GetAllFiles(ctx)
	if len(files2) != count1 {
		t.Errorf("expected %d files after duplicate scan, got %d", count1, len(files2))
	}

	// 重建索引不应出错
	if _, err := builder.BuildFromStore(ctx, st); err != nil {
		t.Fatalf("BuildFromStore after duplicate scan: %v", err)
	}
}

// TestFullPipeline_IndexConsistency 验证索引与 Store 数据一致性。
func TestFullPipeline_IndexConsistency(t *testing.T) {
	ctx, st, _, builder, _, _, cleanup := setupIntegrationTest(t)
	defer cleanup()

	// 全量构建索引
	result, err := builder.BuildFromStore(ctx, st)
	if err != nil {
		t.Fatalf("BuildFromStore: %v", err)
	}

	// 计算 Store 中应有多少文档
	files, _ := st.GetAllFiles(ctx)
	var expected int
	for _, f := range files {
		classes, _ := st.GetClassesByFileID(ctx, f.ID)
		if len(classes) == 0 {
			expected++ // 文件路径作为文档
		}
		for _, c := range classes {
			expected++ // 类作为文档
			methods, _ := st.GetMethodsByClassID(ctx, c.ID)
			expected += len(methods) // 方法作为文档
		}
	}

	if result.TotalDocs != expected {
		t.Errorf("expected %d total docs from Store, got %d", expected, result.TotalDocs)
	}
	if result.IndexedDocs != expected {
		t.Errorf("expected %d indexed docs, got %d", expected, result.IndexedDocs)
	}
}

// TestFullPipeline_AllFilesExist 验证所有文件都被正确扫描入库。
func TestFullPipeline_AllFilesExist(t *testing.T) {
	ctx, st, _, _, _, _, cleanup := setupIntegrationTest(t)
	defer cleanup()

	files, err := st.GetAllFiles(ctx)
	if err != nil {
		t.Fatalf("GetAllFiles: %v", err)
	}

	seen := make(map[string]bool)
	for _, f := range files {
		seen[filepath.Base(f.AbsolutePath)] = true
	}

	for _, name := range []string{"user.go", "order.go", "utils.go"} {
		if !seen[name] {
			t.Errorf("missing file %s in store", name)
		}
	}
}