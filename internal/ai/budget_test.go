package ai

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestBudget_ScanLimit(t *testing.T) {
	b := NewBudget(2, -1) // 扫描期限 2，查询期不限
	if !b.tryConsumeScan() {
		t.Fatal("first scan consume should succeed")
	}
	if !b.tryConsumeScan() {
		t.Fatal("second scan consume should succeed")
	}
	if b.tryConsumeScan() {
		t.Fatal("third scan consume should exceed limit")
	}
	if b.ScanRemaining() != 0 {
		t.Errorf("ScanRemaining want 0, got %d", b.ScanRemaining())
	}
	if !b.ScanExhausted() {
		t.Error("ScanExhausted should be true")
	}
	b.ResetScan()
	if !b.tryConsumeScan() {
		t.Fatal("scan should be reusable after reset")
	}
	if b.ScanRemaining() != 1 {
		t.Errorf("ScanRemaining after reset want 1, got %d", b.ScanRemaining())
	}
}

func TestBudget_QueryUnlimited(t *testing.T) {
	b := NewBudget(0, -1)
	// 扫描期限 0 → 立即耗尽；查询期不限
	if b.tryConsumeScan() {
		t.Error("scan with limit 0 should be exhausted immediately")
	}
	q := b.NewQueryBudget()
	for i := 0; i < 100; i++ {
		if !q.tryConsume() {
			t.Fatalf("query should be unlimited, failed at %d", i)
		}
	}
	if q.Remaining() != -1 {
		t.Errorf("QueryRemaining unlimited want -1, got %d", q.Remaining())
	}
}

func TestBudget_IndependentScopes(t *testing.T) {
	b := NewBudget(1, 1)
	if !b.tryConsumeScan() {
		t.Fatal("scan consume failed")
	}
	q := b.NewQueryBudget()
	if !q.tryConsume() {
		t.Fatal("query consume failed (must be independent of scan)")
	}
	if b.tryConsumeScan() {
		t.Error("scan should be exhausted after 1")
	}
	if q.tryConsume() {
		t.Error("query should be exhausted after 1")
	}
	// 新请求（新 QueryBudget）不受上一请求影响
	q2 := b.NewQueryBudget()
	if !q2.tryConsume() {
		t.Error("每个查询应拥有独立预算")
	}
}

// TestBudget_ConcurrentConsume_NoOvershoot 验证并发消费不会超发预算。
// P21 生产化后 Budget 原子化：多个 worker 并发 tryConsume* 时总消费次数
// 不得超过 limit（并发调用 LLM 不超发硬限）。
func TestBudget_ConcurrentConsume_NoOvershoot(t *testing.T) {
	const limit = 50
	const goroutines = 8
	const attempts = 200 // 每个 goroutine 尝试次数远超 limit，放大并发竞争
	b := NewBudget(limit, limit)

	var success atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < attempts; n++ {
				if b.tryConsumeScan() {
					success.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if got := success.Load(); got != limit {
		t.Errorf("concurrent scan consume overshot: want %d, got %d", limit, got)
	}
	if b.ScanRemaining() != 0 {
		t.Errorf("ScanRemaining want 0 after concurrent consume, got %d", b.ScanRemaining())
	}
	if !b.ScanExhausted() {
		t.Error("ScanExhausted should be true after concurrent consume")
	}
}

// TestBudget_ConcurrentQuery_Independent 验证同一查询作用域内的并发消费不超发，
// 且扫描期完全不受影响。
func TestBudget_ConcurrentQuery_Independent(t *testing.T) {
	const qlimit = 20
	b := NewBudget(-1, qlimit) // 扫描不限，查询限 20
	q := b.NewQueryBudget()
	var success atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				if q.tryConsume() {
					success.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if got := success.Load(); got != qlimit {
		t.Errorf("concurrent query consume overshot: want %d, got %d", qlimit, got)
	}
	// 扫描期应完全不受影响
	if b.ScanRemaining() != -1 {
		t.Errorf("scan unlimited remaining want -1, got %d", b.ScanRemaining())
	}
}
