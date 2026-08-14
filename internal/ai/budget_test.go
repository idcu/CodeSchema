package ai

import "testing"

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
	for i := 0; i < 100; i++ {
		if !b.tryConsumeQuery() {
			t.Fatalf("query should be unlimited, failed at %d", i)
		}
	}
	if b.QueryRemaining() != -1 {
		t.Errorf("QueryRemaining unlimited want -1, got %d", b.QueryRemaining())
	}
}

func TestBudget_IndependentScopes(t *testing.T) {
	b := NewBudget(1, 1)
	if !b.tryConsumeScan() {
		t.Fatal("scan consume failed")
	}
	if !b.tryConsumeQuery() {
		t.Fatal("query consume failed (must be independent of scan)")
	}
	if b.tryConsumeScan() {
		t.Error("scan should be exhausted after 1")
	}
	if b.tryConsumeQuery() {
		t.Error("query should be exhausted after 1")
	}
	b.ResetQuery()
	if !b.tryConsumeQuery() {
		t.Error("query should reset independently of scan")
	}
}
