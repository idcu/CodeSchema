package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
)

// TestParseCoverProfile 验证 go tool cover 输出格式解析。
func TestParseCoverProfile(t *testing.T) {
	content := `mode: set
github.com/idcu/codeschema/internal/store/store.go:10.1,12.2 1 1
github.com/idcu/codeschema/internal/store/store.go:15.1,20.2 2 0
github.com/idcu/codeschema/internal/store/filestore.go:30.1,40.2 3 1
`
	blocks, err := parseCoverProfile(strings.NewReader(content))
	if err != nil {
		t.Fatalf("parseCoverProfile: %v", err)
	}
	if len(blocks) != 3 {
		t.Fatalf("blocks = %d, want 3", len(blocks))
	}
	if blocks[0].StartLine != 10 || blocks[0].EndLine != 12 || blocks[0].Count != 1 {
		t.Fatalf("block[0] = %+v", blocks[0])
	}
	if blocks[1].Count != 0 {
		t.Fatalf("block[1] count = %d, want 0", blocks[1].Count)
	}
}

// TestParseCoverProfile_ModeOnly 验证空/仅 mode 行。
func TestParseCoverProfile_ModeOnly(t *testing.T) {
	blocks, err := parseCoverProfile(strings.NewReader("mode: count\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 0 {
		t.Fatalf("blocks = %d, want 0", len(blocks))
	}
}

// setupCoverStore 构造含生产类 + 测试类的 FileStore（模拟扫描入库结果）。
// 目录结构模拟真实仓库：<root>/internal/store/{order.go, order_test.go}，
// 使 coverprofile 的模块相对路径（internal/store/order.go）可经后缀匹配命中。
func setupCoverStore(t *testing.T) (context.Context, *store.FileStore, *Service) {
	t.Helper()
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "internal", "store")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fs := &store.FileStore{}
	if err := fs.Open(ctx, filepath.Join(dir, "..", "..", "data")); err != nil {
		t.Fatalf("open store: %v", err)
	}

	// 生产文件：order.go（OrderService.Create 方法在第 3-5 行）
	prodIR := &parser.IRDocument{
		Source: "t", Language: "go",
		FilePath: filepath.Join(dir, "order.go"), FileHash: "h-prod",
		LineCount: 8, ByteSize: 200,
	}
	prodIR.Classes = []parser.ClassIR{
		{Name: "OrderService", FullName: "store.OrderService", Type: "CLASS", StartLine: 2, EndLine: 6},
	}
	prodIR.Methods = []parser.MethodIR{
		{Name: "Create", ClassFQN: "store.OrderService", StartLine: 4, EndLine: 5},
	}
	if err := fs.UpsertIR(ctx, prodIR); err != nil {
		t.Fatalf("upsert prod: %v", err)
	}

	// 测试文件：order_test.go（OrderServiceTest.TestCreate，行 2-4）
	testIR := &parser.IRDocument{
		Source: "t", Language: "go",
		FilePath: filepath.Join(dir, "order_test.go"), FileHash: "h-test",
		LineCount: 6, ByteSize: 150,
	}
	testIR.Classes = []parser.ClassIR{
		{Name: "OrderServiceTest", FullName: "store.OrderServiceTest", Type: "CLASS", StartLine: 1, EndLine: 5},
	}
	testIR.Methods = []parser.MethodIR{
		{Name: "TestCreate", ClassFQN: "store.OrderServiceTest", StartLine: 3, EndLine: 4},
	}
	if err := fs.UpsertIR(ctx, testIR); err != nil {
		t.Fatalf("upsert test: %v", err)
	}

	return ctx, fs, NewService(fs)
}

// TestParseGoCoverProfile_EndToEnd 端到端：真实 coverprofile → coverage 策略命中。
func TestParseGoCoverProfile_EndToEnd(t *testing.T) {
	ctx, fs, svc := setupCoverStore(t)
	defer fs.Close()

	// coverprofile：order.go 的 Create 方法（4-5 行）被覆盖
	profile := "mode: set\n" +
		filepath.Join("internal", "store", "order.go") + ":4.1,5.2 1 1\n"
	if err := svc.ParseGoCoverProfile(ctx, strings.NewReader(profile)); err != nil {
		t.Fatalf("ParseGoCoverProfile: %v", err)
	}

	// coverage 策略：查询 Create 应命中 OrderServiceTest.TestCreate
	links, err := svc.FindTestLinks(ctx, "store.OrderService.Create", 60)
	if err != nil {
		t.Fatalf("FindTestLinks: %v", err)
	}
	foundCoverage := false
	for _, l := range links {
		if l.Strategy == "coverage" && l.TestMethod == "store.OrderServiceTest.TestCreate" {
			foundCoverage = true
			break
		}
	}
	if !foundCoverage {
		t.Fatalf("coverage link not found, got: %+v", links)
	}
}

// TestParseGoCoverProfile_FileNotFound 验证缺失文件报错。
func TestParseGoCoverProfile_FileNotFound(t *testing.T) {
	ctx := context.Background()
	fs := &store.FileStore{}
	if err := fs.Open(ctx, filepath.Join(t.TempDir(), "data")); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	svc := NewService(fs)

	if err := svc.LoadGoCoverProfile(ctx, "/nonexistent/cover.out"); err == nil {
		t.Fatal("expected error for missing coverprofile")
	}
}

// TestRelativeStorePath 验证相对路径启发式。
func TestRelativeStorePath(t *testing.T) {
	cases := map[string]string{
		"/repo/internal/store/order.go": "internal/store/order.go",
		"/repo/pkg/util/helper.go":      "pkg/util/helper.go",
		"/repo/cmd/main.go":             "",
	}
	for abs, want := range cases {
		if got := relativeStorePath(abs); got != want {
			t.Fatalf("relativeStorePath(%s) = %q, want %q", abs, got, want)
		}
	}
}
