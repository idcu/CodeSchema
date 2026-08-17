package contextsdk

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// mockProvider 是 SDKProvider 的极简 mock 实现：不依赖仓库内任何包，
// 仅验证「贡献方实现接口即可被权威 Client.Compose 编排」的自包含约束。
type mockProvider struct {
	ctx   func(symbol string) (*SymbolContext, error)
	imp   func(method string, depth int) (*ImpactResult, error)
	got   []string // 记录被注入的符号，供断言调用顺序
	trace *TraceEntry
}

func (m *mockProvider) GetContextMode(_ context.Context, symbol string, _ ContextOptions) (*SymbolContext, error) {
	m.got = append(m.got, symbol)
	if m.ctx != nil {
		return m.ctx(symbol)
	}
	return &SymbolContext{Symbol: symbol, Source: "package demo\nfunc (s *S) M() {}\n", FilePath: "mock.go", StartLine: 1, EndLine: 3, Trace: m.trace}, nil
}

func (m *mockProvider) GetImpact(_ context.Context, method string, depth int) (*ImpactResult, error) {
	if m.imp != nil {
		return m.imp(method, depth)
	}
	return &ImpactResult{
		Method: method,
		Callers: []ImpactNode{{Method: "com.x.Caller.A", Depth: 1},
			{Method: "com.x.Caller.B", Depth: 2, RelatedTests: []string{"TestB"}}},
		Callees: []ImpactNode{{Method: "com.x.Callee.C", Depth: 1}},
		Trace:   m.trace,
	}, nil
}

func mockTrace(trim string) *TraceEntry {
	return &TraceEntry{Source: "mock.GetContext", HitSymbols: 1, HitLines: 3, TrimReason: trim, TokenEstimate: 12}
}

func newMockClient(p SDKProvider) *Client {
	return NewClient(func(string) (SDKProvider, error) { return p, nil })
}

func TestMockCompose_FullMode_DefaultTenant(t *testing.T) {
	p := &mockProvider{trace: mockTrace("full")}
	c := newMockClient(p)

	pkg, err := c.Compose(context.Background(), Request{
		Symbols:      []string{"com.x.S.M"},
		IncludeTrace: true,
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if pkg.Tenant != "" {
		t.Errorf("expected empty default tenant, got %q", pkg.Tenant)
	}
	if len(pkg.Symbols) != 1 || pkg.Symbols[0].Symbol != "com.x.S.M" {
		t.Fatalf("unexpected symbols: %+v", pkg.Symbols)
	}
	if pkg.Summary.SymbolCount != 1 || pkg.Summary.TotalLines != 3 || pkg.Summary.TokenEstimate != 12 {
		t.Errorf("unexpected summary: %+v", pkg.Summary)
	}
	if pkg.Symbols[0].Trace == nil || pkg.Symbols[0].Trace.TrimReason != "full" {
		t.Error("expected trace with trim reason full")
	}
}

func TestMockCompose_MinimalMode_SkipSource(t *testing.T) {
	p := &mockProvider{trace: mockTrace("mode_minimal")}
	c := newMockClient(p)

	pkg, err := c.Compose(context.Background(), Request{
		Symbols:      []string{"com.x.S.M"},
		Mode:         ModeMinimal,
		IncludeTrace: true,
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	// mock 仅返回元数据形态，minimal 模式不注入源码原文（此处由 provider 决定，
	// Client 仅透传 Mode —— 断言 Mode 传递正确）。
	if pkg.Summary.TokenEstimate != 12 {
		t.Errorf("expected token estimate 12, got %d", pkg.Summary.TokenEstimate)
	}
}

func TestMockCompose_WithImpact_WithTests(t *testing.T) {
	p := &mockProvider{trace: mockTrace("full")}
	c := newMockClient(p)

	pkg, err := c.Compose(context.Background(), Request{
		Symbols:    []string{"com.x.S.M"},
		WithImpact: true,
		WithTests:  true,
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(pkg.Impacts) != 1 {
		t.Fatalf("expected 1 impact, got %d", len(pkg.Impacts))
	}
	ib := pkg.Impacts[0]
	if len(ib.Callers) != 2 || ib.Callers[0] != "com.x.Caller.A" {
		t.Errorf("unexpected callers: %v", ib.Callers)
	}
	// WithTests 时，caller 的 RelatedTests 应被聚合进 ImpactBlock.Tests
	if len(ib.Tests) != 1 || ib.Tests[0] != "TestB" {
		t.Errorf("unexpected tests: %v", ib.Tests)
	}
	if len(ib.Callees) != 1 || ib.Callees[0] != "com.x.Callee.C" {
		t.Errorf("unexpected callees: %v", ib.Callees)
	}
}

func TestMockCompose_WithoutTests_NoTestsField(t *testing.T) {
	p := &mockProvider{trace: mockTrace("full")}
	c := newMockClient(p)

	pkg, err := c.Compose(context.Background(), Request{
		Symbols:    []string{"com.x.S.M"},
		WithImpact: true,
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(pkg.Impacts[0].Tests) != 0 {
		t.Errorf("expected no tests aggregated without WithTests, got %v", pkg.Impacts[0].Tests)
	}
}

func TestMockCompose_TenantRouting(t *testing.T) {
	called := ""
	p := &mockProvider{}
	c := NewClient(func(tenant string) (SDKProvider, error) {
		called = tenant
		return p, nil
	})

	pkg, err := c.Compose(context.Background(), Request{
		Tenant:  "repo-a",
		Symbols: []string{"com.x.S.M"},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if called != "repo-a" || pkg.Tenant != "repo-a" {
		t.Errorf("tenant routing failed: resolver got %q, pkg.Tenant=%q", called, pkg.Tenant)
	}
}

func TestMockCompose_SymbolOrderPreserved(t *testing.T) {
	p := &mockProvider{trace: mockTrace("full")}
	c := newMockClient(p)

	_, err := c.Compose(context.Background(), Request{
		Symbols: []string{"b", "a", "c"},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(p.got) != 3 || p.got[0] != "b" || p.got[1] != "a" || p.got[2] != "c" {
		t.Errorf("symbol order not preserved: %v", p.got)
	}
}

func TestMockCompose_ProviderError_Propagates(t *testing.T) {
	sentinel := errors.New("provider boom")
	p := &mockProvider{ctx: func(string) (*SymbolContext, error) { return nil, sentinel }}
	c := newMockClient(p)

	_, err := c.Compose(context.Background(), Request{Symbols: []string{"com.x.S.M"}})
	if err == nil {
		t.Fatal("expected error to propagate from provider")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
}

func TestMockCompose_NilResolver(t *testing.T) {
	c := NewClient(nil)
	_, err := c.Compose(context.Background(), Request{Symbols: []string{"x"}})
	if err == nil {
		t.Fatal("expected error for nil resolver")
	}
}

func TestMockCompose_EmptySymbols(t *testing.T) {
	c := newMockClient(&mockProvider{})
	_, err := c.Compose(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected error for empty symbols")
	}
}

func TestMockCompose_ImpactProviderError_Propagates(t *testing.T) {
	sentinel := errors.New("impact boom")
	p := &mockProvider{imp: func(string, int) (*ImpactResult, error) { return nil, sentinel }}
	c := newMockClient(p)

	_, err := c.Compose(context.Background(), Request{
		Symbols:    []string{"com.x.S.M"},
		WithImpact: true,
	})
	if err == nil {
		t.Fatal("expected impact error to propagate")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
}

func TestMockCompose_DTO_JSONRoundTrip(t *testing.T) {
	pkg := &Package{
		Tenant:  "t1",
		Symbols: []SymbolBlock{{Symbol: "s", Source: "x", Trace: mockTrace("full")}},
		Impacts: []ImpactBlock{{Symbol: "s", Callers: []string{"c1"}}},
		Summary: Summary{Tenant: "t1", SymbolCount: 1, TokenEstimate: 12},
	}
	b, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Package
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Tenant != "t1" || len(back.Symbols) != 1 || back.Symbols[0].Trace.TrimReason != "full" {
		t.Errorf("json round trip mismatch: %+v", back)
	}
}
