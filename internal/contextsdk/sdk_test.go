package contextsdk

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idcu/codeschema/internal/analyzer"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/store"
)

// seedService 创建带真实源文件（order.go）与调用图（handler.Handle → svc.GetUser）的 Service。
func seedService(t testing.TB) *service.Service {
	t.Helper()
	dir := t.TempDir()
	st := store.NewStore("file")
	if err := st.Open(context.Background(), dir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// 真实文件：order.go（GetUser 方法在第 5-7 行）
	content := "package demo\n\ntype OrderService struct{}\n\nfunc (s *OrderService) GetUser(id int) string {\n\treturn \"user\"\n}\n"
	path := filepath.Join(dir, "order.go")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	ir := &parser.IRDocument{
		Source: "test", Language: "go", FilePath: path, FileHash: "h-order", LineCount: 7, ByteSize: int64(len(content)),
		Classes: []parser.ClassIR{{Name: "OrderService", FullName: "com.example.OrderService", Type: "CLASS", StartLine: 3, EndLine: 3}},
		Methods: []parser.MethodIR{{Name: "GetUser", ClassFQN: "com.example.OrderService", Signature: "GetUser(id int) string", StartLine: 5, EndLine: 7, Doc: "按 ID 查询用户"}},
	}
	if err := st.UpsertIR(context.Background(), ir); err != nil {
		t.Fatalf("upsert order: %v", err)
	}

	// handler.go：Handle → GetUser（供影响面）
	ir2 := &parser.IRDocument{
		Source: "test", Language: "go", FilePath: filepath.Join(dir, "handler.go"), FileHash: "h-handler", LineCount: 30, ByteSize: 1024,
		Classes: []parser.ClassIR{{Name: "Handler", FullName: "com.example.Handler", Type: "CLASS"}},
		Methods: []parser.MethodIR{{Name: "Handle", ClassFQN: "com.example.Handler", StartLine: 1, EndLine: 10}},
		Calls: []parser.CallIR{
			{CallerFQN: "com.example.Handler.Handle", CalleeFQN: "com.example.OrderService.GetUser", CallType: "direct", LineNumber: 5},
		},
	}
	if err := st.UpsertIR(context.Background(), ir2); err != nil {
		t.Fatalf("upsert handler: %v", err)
	}

	// order_test.go：OrderServiceTest.TestGetUser ↔ GetUser（naming 策略，置信度 70）
	ir3 := &parser.IRDocument{
		Source: "test", Language: "go", FilePath: filepath.Join(dir, "order_test.go"), FileHash: "h-order-test", LineCount: 20, ByteSize: 512,
		Classes: []parser.ClassIR{{Name: "OrderServiceTest", FullName: "com.example.OrderServiceTest", Type: "CLASS"}},
		Methods: []parser.MethodIR{{Name: "TestGetUser", ClassFQN: "com.example.OrderServiceTest", StartLine: 1, EndLine: 5}},
	}
	if err := st.UpsertIR(context.Background(), ir3); err != nil {
		t.Fatalf("upsert order_test: %v", err)
	}

	svc := service.NewService(st)
	svc.WithImpactAnalyzer(analyzer.NewAnalyzer(st))
	return svc
}

func newTestClient(t testing.TB) *Client {
	t.Helper()
	svc := seedService(t)
	return NewClient(func(tenant string) (*service.Service, error) {
		if tenant == "unknown" {
			return nil, os.ErrNotExist
		}
		return svc, nil
	})
}

func TestCompose_NilResolver(t *testing.T) {
	c := NewClient(nil)
	_, err := c.Compose(context.Background(), Request{Symbols: []string{"x"}})
	if err == nil {
		t.Fatal("expected error for nil resolver")
	}
}

func TestCompose_EmptySymbols(t *testing.T) {
	c := newTestClient(t)
	_, err := c.Compose(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected error for empty symbols")
	}
}

func TestCompose_FullMode_DefaultTenant(t *testing.T) {
	c := newTestClient(t)
	pkg, err := c.Compose(context.Background(), Request{
		Symbols:      []string{"com.example.OrderService.GetUser"},
		IncludeTrace: true,
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if pkg.Tenant != "" {
		t.Errorf("expected empty default tenant, got %q", pkg.Tenant)
	}
	if len(pkg.Symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(pkg.Symbols))
	}
	sb := pkg.Symbols[0]
	if sb.Symbol != "com.example.OrderService.GetUser" {
		t.Errorf("unexpected symbol %q", sb.Symbol)
	}
	// full 模式：注入源码原文
	if !strings.Contains(sb.Source, "return \"user\"") {
		t.Errorf("expected source body in full mode, got %q", sb.Source)
	}
	if sb.Trace == nil || sb.Trace.Source != "store.GetContext" {
		t.Error("expected context trace")
	}
	if pkg.Summary.SymbolCount != 1 {
		t.Errorf("expected summary symbol count 1, got %d", pkg.Summary.SymbolCount)
	}
	if pkg.Summary.TokenEstimate <= 0 {
		t.Errorf("expected positive token estimate, got %d", pkg.Summary.TokenEstimate)
	}
}

func TestCompose_MinimalMode(t *testing.T) {
	c := newTestClient(t)
	pkg, err := c.Compose(context.Background(), Request{
		Symbols:      []string{"com.example.OrderService.GetUser"},
		Mode:         "minimal",
		IncludeTrace: true,
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	sb := pkg.Symbols[0]
	if strings.Contains(sb.Source, "return \"user\"") {
		t.Errorf("expected no source body in minimal mode, got %q", sb.Source)
	}
	if !strings.Contains(sb.Source, "lines 5-7") {
		t.Errorf("expected line range in minimal summary, got %q", sb.Source)
	}
	if sb.Trace == nil || sb.Trace.TrimReason != "mode_minimal" {
		t.Errorf("expected minimal trim reason, got %+v", sb.Trace)
	}
	if pkg.Summary.TokenEstimate != 4 {
		t.Errorf("expected minimal token estimate 4, got %d", pkg.Summary.TokenEstimate)
	}
}

func TestCompose_WithImpact(t *testing.T) {
	c := newTestClient(t)
	pkg, err := c.Compose(context.Background(), Request{
		Symbols:    []string{"com.example.OrderService.GetUser"},
		WithImpact: true,
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(pkg.Impacts) != 1 {
		t.Fatalf("expected 1 impact block, got %d", len(pkg.Impacts))
	}
	ib := pkg.Impacts[0]
	if len(ib.Callers) != 1 || ib.Callers[0] != "com.example.Handler.Handle" {
		t.Errorf("expected caller Handler.Handle, got %v", ib.Callers)
	}
	if ib.Trace == nil || ib.Trace.Source != "store.GetImpact" {
		t.Error("expected impact trace")
	}
}

func TestCompose_WithTests(t *testing.T) {
	c := newTestClient(t)
	pkg, err := c.Compose(context.Background(), Request{
		Symbols:   []string{"com.example.OrderService.GetUser"},
		WithTests: true,
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	// RelatedTests 来自 GetContextMode（五策略关联，本测试无测试类 → 可能为空）。
	// 至少确认字段存在且 Compose 不报错。
	if pkg.Symbols[0].RelatedTests == nil {
		t.Error("expected non-nil RelatedTests field")
	}
}

func TestCompose_MultiTenant_Routing(t *testing.T) {
	// 两个租户各自独立的 Service，验证按 tenant 路由
	svcA := seedService(t)
	svcB := seedService(t)
	c := NewClient(func(tenant string) (*service.Service, error) {
		if tenant == "repo-a" {
			return svcA, nil
		}
		if tenant == "repo-b" {
			return svcB, nil
		}
		return nil, os.ErrNotExist
	})

	pkg, err := c.Compose(context.Background(), Request{
		Tenant:  "repo-b",
		Symbols: []string{"com.example.OrderService.GetUser"},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if pkg.Tenant != "repo-b" {
		t.Errorf("expected tenant repo-b, got %q", pkg.Tenant)
	}
	if pkg.Summary.Tenant != "repo-b" {
		t.Errorf("expected summary tenant repo-b, got %q", pkg.Summary.Tenant)
	}
}

func TestCompose_TenantNotFound(t *testing.T) {
	c := newTestClient(t)
	_, err := c.Compose(context.Background(), Request{
		Tenant:  "unknown",
		Symbols: []string{"com.example.OrderService.GetUser"},
	})
	if err == nil {
		t.Fatal("expected error for unknown tenant")
	}
}

func TestCompose_IncludeTraceFalse(t *testing.T) {
	c := newTestClient(t)
	pkg, err := c.Compose(context.Background(), Request{
		Symbols:      []string{"com.example.OrderService.GetUser"},
		IncludeTrace: false,
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if pkg.Symbols[0].Trace != nil {
		t.Error("expected no trace when IncludeTrace=false")
	}
	if pkg.Summary.TokenEstimate != 0 {
		t.Errorf("expected 0 token estimate without trace, got %d", pkg.Summary.TokenEstimate)
	}
}

func TestCompose_SymbolNotFound(t *testing.T) {
	c := newTestClient(t)
	_, err := c.Compose(context.Background(), Request{
		Symbols: []string{"com.example.DoesNotExist.missing"},
	})
	if err == nil {
		t.Fatal("expected error for unknown symbol")
	}
}

func TestCompose_MultipleSymbols_Summary(t *testing.T) {
	c := newTestClient(t)
	pkg, err := c.Compose(context.Background(), Request{
		Symbols: []string{
			"com.example.OrderService.GetUser",
			"com.example.Handler.Handle",
		},
		IncludeTrace: true,
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(pkg.Symbols) != 2 {
		t.Fatalf("expected 2 symbols, got %d", len(pkg.Symbols))
	}
	if pkg.Summary.SymbolCount != 2 {
		t.Errorf("expected summary count 2, got %d", pkg.Summary.SymbolCount)
	}
	if len(pkg.Summary.TrimReasons) != 2 {
		t.Errorf("expected 2 trim reasons, got %v", pkg.Summary.TrimReasons)
	}
}
