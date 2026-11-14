package service

import (
	"context"
	"sync"
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

// countingStore 统计 GetCallsByFileID 调用次数（每次 BuildCallGraph 对每个文件调用一次），
// 用于验证单次请求只建一次调用图。GetAllFiles 也会被 FindTestLinks 等路径使用，
// 不适合作为建图次数指标。
type countingStore struct {
	store.Store
	mu    sync.Mutex
	calls int
}

func (c *countingStore) GetCallsByFileID(ctx context.Context, fileID int64) ([]store.CallRecord, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.Store.GetCallsByFileID(ctx, fileID)
}

func (c *countingStore) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func newCountingStore(t *testing.T) *countingStore {
	t.Helper()
	st := store.NewStore("file")
	if err := st.Open(context.Background(), t.TempDir()); err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	fileID, err := st.UpsertFile(context.Background(), "/repo/a.go", "h1", 10, 100)
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	if err := st.UpsertCalls(context.Background(), fileID, []parser.CallIR{
		{CallerFQN: "pkg.A.Run", CalleeFQN: "pkg.B.Helper"},
	}); err != nil {
		t.Fatalf("UpsertCalls: %v", err)
	}
	return &countingStore{Store: st}
}

// TestService_GetImpactBuildsGraphOnce 负控（P1-13）：一次 /impact 只应建一次调用图。
func TestService_GetImpactBuildsGraphOnce(t *testing.T) {
	cs := newCountingStore(t)
	svc := NewService(cs).WithImpactAnalyzer(analyzer.NewAnalyzer(cs))
	if _, err := svc.GetImpact(context.Background(), "pkg.A.Run", 1); err != nil {
		t.Fatalf("GetImpact: %v", err)
	}
	if got := cs.Count(); got != 1 {
		t.Fatalf("expected 1 call-graph build, got %d", got)
	}
}

// TestService_GetCallGraphBuildsGraphOnce 负控（P1-13）：一次 /call-graph 只应建一次调用图。
func TestService_GetCallGraphBuildsGraphOnce(t *testing.T) {
	cs := newCountingStore(t)
	svc := NewService(cs).WithImpactAnalyzer(analyzer.NewAnalyzer(cs))
	if _, err := svc.GetCallGraph(context.Background(), "pkg.A.Run", 1); err != nil {
		t.Fatalf("GetCallGraph: %v", err)
	}
	if got := cs.Count(); got != 1 {
		t.Fatalf("expected 1 call-graph build, got %d", got)
	}
}

// TestService_GetCallGraphDepthCapped（P1-13）：depth 超过上限时收敛到 MaxImpactDepth。
func TestService_GetCallGraphDepthCapped(t *testing.T) {
	cs := newCountingStore(t)
	svc := NewService(cs).WithImpactAnalyzer(analyzer.NewAnalyzer(cs))
	res, err := svc.GetCallGraph(context.Background(), "pkg.A.Run", 100)
	if err != nil {
		t.Fatalf("GetCallGraph: %v", err)
	}
	if got, _ := res["depth"].(int); got != analyzer.MaxImpactDepth {
		t.Fatalf("expected depth %d, got %v", analyzer.MaxImpactDepth, res["depth"])
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
