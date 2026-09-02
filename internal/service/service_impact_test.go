package service

import (
	"context"
	"testing"

	"github.com/idcu/codeschema/internal/analyzer"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
)

// TestService_GetImpact 验证 T1 修复在 service 层的端到端效果：
// 包限定查询经 analyzer 双向归一化命中调用图节点，返回真实的 callers/callees。
func TestService_GetImpact(t *testing.T) {
	st := store.NewStore("file")
	dir := t.TempDir()
	if err := st.Open(context.Background(), dir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	fileID, err := st.UpsertFile(context.Background(), "/project/config/watcher.go", "h1", 100, 2048)
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	calls := []parser.CallIR{
		{CallerFQN: "config.NewWatcher", CalleeFQN: "config.loadConfig"},
		{CallerFQN: "config.NewWatcher", CalleeFQN: "config.Watcher.ReloadNow"},
		{CallerFQN: "config.Watcher.ReloadNow", CalleeFQN: "config.loadConfig"},
	}
	if err := st.UpsertCalls(context.Background(), fileID, calls); err != nil {
		t.Fatalf("UpsertCalls: %v", err)
	}

	an := analyzer.NewAnalyzer(st)
	svc := NewService(st).WithImpactAnalyzer(an)

	res, err := svc.GetImpact(context.Background(), "config.Watcher.ReloadNow", 1)
	if err != nil {
		t.Fatalf("GetImpact: %v", err)
	}
	if res.Method != "config.Watcher.ReloadNow" {
		t.Errorf("expected Method 'config.Watcher.ReloadNow', got %q", res.Method)
	}
	if len(res.Callers) == 0 {
		t.Fatal("expected non-empty callers")
	}
	if len(res.Callees) == 0 {
		t.Fatal("expected non-empty callees")
	}

	callerSet := make(map[string]bool, len(res.Callers))
	for _, n := range res.Callers {
		callerSet[n.Method] = true
	}
	calleeSet := make(map[string]bool, len(res.Callees))
	for _, n := range res.Callees {
		calleeSet[n.Method] = true
	}
	if !callerSet["config.NewWatcher"] {
		t.Errorf("expected caller config.NewWatcher, got %v", res.Callers)
	}
	if !calleeSet["config.loadConfig"] {
		t.Errorf("expected callee config.loadConfig, got %v", res.Callees)
	}
}

// TestService_GetImpact_BareQuery 验证去掉包前缀的裸查询仍能命中（双向归一化）。
func TestService_GetImpact_BareQuery(t *testing.T) {
	st := store.NewStore("file")
	dir := t.TempDir()
	if err := st.Open(context.Background(), dir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	fileID, _ := st.UpsertFile(context.Background(), "/project/config/watcher.go", "h1", 100, 2048)
	calls := []parser.CallIR{
		{CallerFQN: "config.NewWatcher", CalleeFQN: "config.Watcher.ReloadNow"},
	}
	if err := st.UpsertCalls(context.Background(), fileID, calls); err != nil {
		t.Fatalf("UpsertCalls: %v", err)
	}

	an := analyzer.NewAnalyzer(st)
	svc := NewService(st).WithImpactAnalyzer(an)

	res, err := svc.GetImpact(context.Background(), "Watcher.ReloadNow", 1)
	if err != nil {
		t.Fatalf("GetImpact: %v", err)
	}
	if res.Method != "config.Watcher.ReloadNow" {
		t.Errorf("expected resolved Method 'config.Watcher.ReloadNow', got %q", res.Method)
	}
	if len(res.Callers) == 0 {
		t.Fatal("expected non-empty callers for bare query")
	}
}

// TestService_GetImpact_NoAnalyzer 验证未注入 analyzer 时返回空影响面（向后兼容）。
func TestService_GetImpact_NoAnalyzer(t *testing.T) {
	svc := newTestService(t)
	res, err := svc.GetImpact(context.Background(), "config.Watcher.ReloadNow", 1)
	if err != nil {
		t.Fatalf("GetImpact: %v", err)
	}
	if len(res.Callers) != 0 || len(res.Callees) != 0 {
		t.Errorf("expected empty impact when analyzer nil, got callers=%v callees=%v", res.Callers, res.Callees)
	}
	if res.Trace == nil || res.Trace.TrimReason != "analyzer_unavailable" {
		t.Errorf("expected analyzer_unavailable trace, got %+v", res.Trace)
	}
}
