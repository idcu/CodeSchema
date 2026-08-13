package service

import (
	"context"
	"testing"

	"codeschema/internal/store"
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