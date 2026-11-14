package service

import (
	"context"
	"testing"

	"github.com/idcu/codeschema/internal/analyzer"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
)

// TestService_ImpactSeesNewGraphAfterReindex 负控（结构性问题 1）：
// 改完代码重索引后，立刻查询 /impact 必须看到新调用图，而不是缓存里的旧图。
//
// 这是调用图缓存失效路径的负控：若只按代次缓存而漏接失效钩子，
// 第二次查询会命中旧图，断言"看到新边、看不到旧边"失败。
func TestService_ImpactSeesNewGraphAfterReindex(t *testing.T) {
	ctx := context.Background()
	st := store.NewStore("file")
	if err := st.Open(ctx, t.TempDir()); err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	fileID, err := st.UpsertFile(ctx, "/repo/a.go", "h1", 10, 100)
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	if err := st.UpsertCalls(ctx, fileID, []parser.CallIR{
		{CallerFQN: "pkg.A.Run", CalleeFQN: "pkg.B.Helper"},
	}); err != nil {
		t.Fatalf("UpsertCalls: %v", err)
	}

	svc := NewService(st).WithImpactAnalyzer(analyzer.NewAnalyzer(st))

	before, err := svc.GetImpact(ctx, "pkg.A.Run", 1)
	if err != nil {
		t.Fatalf("GetImpact before reindex: %v", err)
	}
	if !hasCallee(before, "pkg.B.Helper") {
		t.Fatalf("expected callee pkg.B.Helper before reindex, got %v", before.Callees)
	}

	// 模拟"改完代码重新索引"：文件调用关系整体替换为 A -> C。
	if err := st.UpsertCalls(ctx, fileID, []parser.CallIR{
		{CallerFQN: "pkg.A.Run", CalleeFQN: "pkg.C.New"},
	}); err != nil {
		t.Fatalf("UpsertCalls after edit: %v", err)
	}
	// 重索引/增量更新后必须显式失效派生缓存（生产路径由 watcher 回调与
	// Service.BuildIndex 调用）。
	svc.InvalidateDerivedCaches()

	after, err := svc.GetImpact(ctx, "pkg.A.Run", 1)
	if err != nil {
		t.Fatalf("GetImpact after reindex: %v", err)
	}
	if !hasCallee(after, "pkg.C.New") {
		t.Fatalf("expected new callee pkg.C.New after reindex, got %v", after.Callees)
	}
	if hasCallee(after, "pkg.B.Helper") {
		t.Fatalf("stale callee pkg.B.Helper still present after reindex: %v", after.Callees)
	}
}

func hasCallee(res *ImpactResult, method string) bool {
	for _, n := range res.Callees {
		if n.Method == method {
			return true
		}
	}
	return false
}
