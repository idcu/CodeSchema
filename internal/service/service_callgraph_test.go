package service

import (
	"context"
	"testing"

	"github.com/idcu/codeschema/internal/analyzer"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
)

// TestService_GetCallGraph 验证 T2 修复：GetCallGraph 由硬编码空桩改为返回真实调用子图。
// 调用关系使用包限定 FQN（与 T1 适配器产出一致），并验证双向归一化定位命中节点。
func TestService_GetCallGraph(t *testing.T) {
	st := store.NewStore("file")
	dir := t.TempDir()
	if err := st.Open(context.Background(), dir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// 写入一个文件及其调用关系
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

	// analyzer 与 service 共用同一 store
	an := analyzer.NewAnalyzer(st)
	svc := NewService(st).WithImpactAnalyzer(an)

	// 以包限定 FQN 查询真实调用子图
	res, err := svc.GetCallGraph(context.Background(), "config.Watcher.ReloadNow", 1)
	if err != nil {
		t.Fatalf("GetCallGraph: %v", err)
	}

	nodes, ok := res["nodes"].([]string)
	if !ok {
		t.Fatalf("nodes not []string: %T", res["nodes"])
	}
	edges, ok := res["edges"].([]string)
	if !ok {
		t.Fatalf("edges not []string: %T", res["edges"])
	}
	if len(nodes) == 0 {
		t.Fatal("expected non-empty nodes")
	}
	if len(edges) == 0 {
		t.Fatal("expected non-empty edges")
	}

	nodeSet := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		nodeSet[n] = true
	}
	if !nodeSet["config.Watcher.ReloadNow"] {
		t.Errorf("expected target node present, got %v", nodes)
	}
	if !nodeSet["config.NewWatcher"] || !nodeSet["config.loadConfig"] {
		t.Errorf("expected caller/callee nodes present, got %v", nodes)
	}
	// 边应包含 NewWatcher -> Watcher.ReloadNow 与 Watcher.ReloadNow -> loadConfig
	edgeSet := make(map[string]bool, len(edges))
	for _, e := range edges {
		edgeSet[e] = true
	}
	if !edgeSet["config.NewWatcher -> config.Watcher.ReloadNow"] {
		t.Errorf("expected edge NewWatcher->Watcher.ReloadNow, got %v", edges)
	}
	if !edgeSet["config.Watcher.ReloadNow -> config.loadConfig"] {
		t.Errorf("expected edge Watcher.ReloadNow->loadConfig, got %v", edges)
	}
}

// TestService_GetCallGraph_BareQuery 验证去掉包前缀的裸查询仍能命中（双向归一化）。
func TestService_GetCallGraph_BareQuery(t *testing.T) {
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

	res, err := svc.GetCallGraph(context.Background(), "Watcher.ReloadNow", 1)
	if err != nil {
		t.Fatalf("GetCallGraph: %v", err)
	}
	if res["symbol"] != "config.Watcher.ReloadNow" {
		t.Errorf("expected resolved symbol 'config.Watcher.ReloadNow', got %v", res["symbol"])
	}
	nodes, _ := res["nodes"].([]string)
	if len(nodes) == 0 {
		t.Fatal("expected non-empty nodes for bare query")
	}
}

// TestService_GetCallGraph_NoAnalyzer 验证未注入 analyzer 时返回空图（向后兼容）。
func TestService_GetCallGraph_NoAnalyzer(t *testing.T) {
	svc := newTestService(t)
	res, err := svc.GetCallGraph(context.Background(), "config.Watcher.ReloadNow", 1)
	if err != nil {
		t.Fatalf("GetCallGraph: %v", err)
	}
	nodes, _ := res["nodes"].([]string)
	if len(nodes) != 0 {
		t.Errorf("expected empty nodes when analyzer nil, got %v", nodes)
	}
}
