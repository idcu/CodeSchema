package service

import (
	"context"
	"testing"

	"github.com/idcu/codeschema/internal/search"
)

// stubVectorSearcher 实现 search.VectorSearcher，返回带原始余弦相似度的结果（用于 B8 测试）。
type stubVectorSearcher struct {
	results []search.SearchResult
}

func (f *stubVectorSearcher) Search(_ context.Context, _ string, _ int) ([]search.SearchResult, error) {
	return f.results, nil
}

// TestSearchWithOptions_BelowThreshold 验证 Service 层 B8：低置信度过滤 + trim_reason + Confidence 透传。
func TestSearchWithOptions_BelowThreshold(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	fv := &stubVectorSearcher{results: []search.SearchResult{
		{Symbol: "symA", Score: 0.90},
		{Symbol: "symB", Score: 0.55},
		{Symbol: "symC", Score: 0.20},
	}}
	searcher := search.NewSearcher(nil, fv, nil)
	svc.WithSearcher(searcher)

	// MinScore=0：不过滤
	out, err := svc.SearchWithOptions(ctx, "q", "semantic", 10, 0)
	if err != nil {
		t.Fatalf("SearchWithOptions: %v", err)
	}
	if out.TrimReason != "" || out.Filtered != 0 || len(out.Results) != 3 {
		t.Fatalf("MinScore=0: expected 3 results, 0 filtered; got %d results, filtered=%d, reason=%q",
			len(out.Results), out.Filtered, out.TrimReason)
	}
	// Confidence 透传 = 余弦相似度（绝对量纲）
	if out.Results[0].Confidence != 0.90 {
		t.Errorf("expected Confidence 0.90, got %v", out.Results[0].Confidence)
	}

	// MinScore=0.6：过滤 symB(0.55)、symC(0.20)，置 trim_reason=below_threshold
	out2, err := svc.SearchWithOptions(ctx, "q", "semantic", 10, 0.6)
	if err != nil {
		t.Fatalf("SearchWithOptions: %v", err)
	}
	if out2.Filtered != 2 || len(out2.Results) != 1 {
		t.Fatalf("MinScore=0.6: expected 1 result, 2 filtered; got %d results, filtered=%d",
			len(out2.Results), out2.Filtered)
	}
	if out2.TrimReason != "below_threshold" {
		t.Errorf("expected trim_reason=below_threshold, got %q", out2.TrimReason)
	}
	if out2.Results[0].Symbol != "symA" {
		t.Errorf("expected symA, got %q", out2.Results[0].Symbol)
	}
}

// TestSearch_BackwardCompat 验证 Search（旧签名）仍返回 []SearchResult 且不过滤（MinScore=0）。
func TestSearch_BackwardCompat(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	fv := &stubVectorSearcher{results: []search.SearchResult{
		{Symbol: "symA", Score: 0.90},
		{Symbol: "symB", Score: 0.10},
	}}
	searcher := search.NewSearcher(nil, fv, nil)
	svc.WithSearcher(searcher)

	results, err := svc.Search(ctx, "q", "semantic", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Search should return all 2 results (no filtering), got %d", len(results))
	}
}
