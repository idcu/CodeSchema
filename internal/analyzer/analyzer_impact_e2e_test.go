package analyzer

import (
	"context"
	"testing"

	"github.com/idcu/codeschema/internal/store"
)

// TestAnalyzer_Impact_GoQualifiedFQN 验证 T1 修复：Go 默认正则路径产出的包限定 FQN
// （config.Watcher.ReloadNow）能被影响面查询精确命中，callers/callees 双向均非空。
//
// 这是 impact / tests / get_call_graph 真实可用的核心前置：此前调用图节点是裸名
// （Watcher.ReloadNow），而查询用包限定全名（config.Watcher.ReloadNow），命名空间错位
// 导致永远查不到。adapter.go 已改为产出包限定 FQN，此处验证 analyzer 侧查询能命中。
func TestAnalyzer_Impact_GoQualifiedFQN(t *testing.T) {
	ms := &mockStore{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/config/watcher.go", Language: "go"},
		},
		calls: map[int64][]store.CallRecord{
			1: {
				{CallerFQN: "config.NewWatcher", CalleeFQN: "config.loadConfig"},
				{CallerFQN: "config.NewWatcher", CalleeFQN: "config.Watcher.ReloadNow"},
				{CallerFQN: "config.Watcher.ReloadNow", CalleeFQN: "config.loadConfig"},
				{CallerFQN: "main.main", CalleeFQN: "config.NewWatcher"},
			},
		},
	}
	a := NewAnalyzer(ms)

	// 全量构建调用图（模拟真实扫描后入库 BuildAll 中的 BuildCallGraph）
	if _, err := a.BuildCallGraph(context.Background()); err != nil {
		t.Fatalf("BuildCallGraph: %v", err)
	}

	// 以包限定 FQN 查询（与 Go 适配器产出一致）。
	// depth=1 等价于 service.GetImpact 默认深度，仅取直接调用者/被调用者。
	callers, callees, err := a.FindImpactNodes(context.Background(), "config.Watcher.ReloadNow", 1)
	if err != nil {
		t.Fatalf("FindImpactNodes: %v", err)
	}
	if len(callers) != 1 || callers[0] != "config.NewWatcher" {
		t.Errorf("expected caller [config.NewWatcher], got %v", callers)
	}
	if len(callees) != 1 || callees[0] != "config.loadConfig" {
		t.Errorf("expected callee [config.loadConfig], got %v", callees)
	}

	// FindImpactNodesWithDepth 同样应命中，且带深度层级
	dc, dce, err := a.FindImpactNodesWithDepth(context.Background(), "config.Watcher.ReloadNow", 1)
	if err != nil {
		t.Fatalf("FindImpactNodesWithDepth: %v", err)
	}
	if len(dc) != 1 || dc[0].Method != "config.NewWatcher" || dc[0].Depth != 1 {
		t.Errorf("expected depth caller config.NewWatcher@1, got %v", dc)
	}
	if len(dce) != 1 || dce[0].Method != "config.loadConfig" || dce[0].Depth != 1 {
		t.Errorf("expected depth callee config.loadConfig@1, got %v", dce)
	}
}

// TestAnalyzer_Impact_NormalizationFallback 验证归一化兜底：查询使用「去掉包前缀」的
// 部分限定名（Watcher.ReloadNow）时，仍能匹配到包限定节点。覆盖多语言/旧数据命名空间不一致。
func TestAnalyzer_Impact_NormalizationFallback(t *testing.T) {
	ms := &mockStore{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/config/watcher.go", Language: "go"},
		},
		calls: map[int64][]store.CallRecord{
			1: {
				{CallerFQN: "config.NewWatcher", CalleeFQN: "config.Watcher.ReloadNow"},
			},
		},
	}
	a := NewAnalyzer(ms)

	cases := []string{"config.Watcher.ReloadNow", "Watcher.ReloadNow"}
	for _, q := range cases {
		callers, _, err := a.FindImpactNodes(context.Background(), q, 0)
		if err != nil {
			t.Fatalf("FindImpactNodes(%q): %v", q, err)
		}
		if len(callers) != 1 || callers[0] != "config.NewWatcher" {
			t.Errorf("query %q: expected caller [config.NewWatcher], got %v", q, callers)
		}
	}
}

// TestAnalyzer_ShortestPath_Normalization 验证 ShortestPath 同样支持部分限定名归一化。
func TestAnalyzer_ShortestPath_Normalization(t *testing.T) {
	ms := &mockStore{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/config/watcher.go", Language: "go"},
		},
		calls: map[int64][]store.CallRecord{
			1: {
				{CallerFQN: "main.main", CalleeFQN: "config.NewWatcher"},
				{CallerFQN: "config.NewWatcher", CalleeFQN: "config.Watcher.ReloadNow"},
			},
		},
	}
	a := NewAnalyzer(ms)

	// 终点用部分限定名（去掉包前缀）查询
	path, err := a.ShortestPath(context.Background(), "main.main", "Watcher.ReloadNow")
	if err != nil {
		t.Fatalf("ShortestPath: %v", err)
	}
	if len(path) == 0 {
		t.Fatal("expected non-empty path")
	}
	if path[0] != "main.main" {
		t.Errorf("expected start 'main.main', got %s", path[0])
	}
	if path[len(path)-1] != "config.Watcher.ReloadNow" {
		t.Errorf("expected end 'config.Watcher.ReloadNow', got %s", path[len(path)-1])
	}
}
