package analyzer

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/store"
)

// genCountingStore 在真实 Store 之上统计 GetCallsByFileID 次数（BuildCallGraph
// 每文件调用一次，是"建图次数"的可靠代理指标），并可注入逐次延迟放大并发窗口。
type genCountingStore struct {
	store.Store
	delay time.Duration

	mu    sync.Mutex
	calls int
}

func (g *genCountingStore) GetCallsByFileID(ctx context.Context, fileID int64) ([]store.CallRecord, error) {
	g.mu.Lock()
	g.calls++
	g.mu.Unlock()
	if g.delay > 0 {
		time.Sleep(g.delay)
	}
	return g.Store.GetCallsByFileID(ctx, fileID)
}

func (g *genCountingStore) Count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

// TestBuildCallGraph_CachesByGeneration 负控（结构性问题 1）：
// 调用图必须按索引代次缓存——同一代次内重复查询不重建；失效后代次递增，下一次查询重建。
//
// 修前（红）：每次 BuildCallGraph 都全量读盘，3 个文件 → 6 次 GetCallsByFileID；
// 修后（绿）：首次 3 次，第二次 0 次，失效后 3 次。
func TestBuildCallGraph_CachesByGeneration(t *testing.T) {
	cs := &genCountingStore{Store: newTestStore()}
	a := NewAnalyzer(cs)
	ctx := context.Background()

	if _, err := a.BuildCallGraph(ctx); err != nil {
		t.Fatalf("first BuildCallGraph: %v", err)
	}
	first := cs.Count()
	if first != 3 {
		t.Fatalf("expected 3 file reads on first build, got %d", first)
	}

	if _, err := a.BuildCallGraph(ctx); err != nil {
		t.Fatalf("second BuildCallGraph: %v", err)
	}
	if got := cs.Count(); got != first {
		t.Fatalf("expected cache hit (still %d file reads), got %d", first, got)
	}

	a.InvalidateCallGraph()
	if _, err := a.BuildCallGraph(ctx); err != nil {
		t.Fatalf("BuildCallGraph after invalidate: %v", err)
	}
	if got := cs.Count(); got != first*2 {
		t.Fatalf("expected rebuild after invalidate (%d file reads), got %d", first*2, got)
	}
}

// TestBuildCallGraph_ConcurrentRequestsBuildOnce 负控（结构性问题 1）：
// 并发请求共享同一次建图，且每个调用方拿到的都是完整图（4 条边 / 5 个节点），
// 不会看到构建中的半成品。
//
// 修前（红）：8 个并发请求各自建图 → 24 次文件读取；
// 修后（绿）：串行化后只建一次 → 3 次。
func TestBuildCallGraph_ConcurrentRequestsBuildOnce(t *testing.T) {
	cs := &genCountingStore{Store: newTestStore(), delay: 2 * time.Millisecond}
	a := NewAnalyzer(cs)

	const n = 8
	graphs := make([]*CallGraph, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			graphs[i], errs[i] = a.BuildCallGraph(context.Background())
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	if got := cs.Count(); got != 3 {
		t.Fatalf("expected a single build (3 file reads) across %d concurrent requests, got %d", n, got)
	}
	for i, cg := range graphs {
		if cg == nil {
			t.Fatalf("goroutine %d got nil graph", i)
		}
		if cg.NodeCount() != 5 || cg.EdgeCount() != 4 {
			t.Fatalf("goroutine %d got incomplete graph: nodes=%d edges=%d", i, cg.NodeCount(), cg.EdgeCount())
		}
	}
}
