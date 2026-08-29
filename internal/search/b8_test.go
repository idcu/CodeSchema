package search

import (
	"context"
	"testing"
)

// stubVectorSearcher 实现 VectorSearcher，返回带原始余弦相似度的结果（用于 B8 测试）。
type stubVectorSearcher struct {
	results []SearchResult
}

func (f *stubVectorSearcher) Search(_ context.Context, _ string, _ int) ([]SearchResult, error) {
	return f.results, nil
}

// TestRerank_ConfidenceAbsoluteVector 验证融合结果置信度取向量原始余弦（绝对量纲，非相对归一化）。
func TestRerank_ConfidenceAbsoluteVector(t *testing.T) {
	r := NewReranker(0.3, 0.7)
	ftsResults := []SearchResult{
		{Symbol: "a", Score: 1.0},
		{Symbol: "b", Score: 0.5},
	}
	// 原始余弦相似度（绝对 [0,1]），与集合最大值无关
	vectorResults := []SearchResult{
		{Symbol: "a", Score: 0.42},
		{Symbol: "b", Score: 0.91},
	}
	out := r.Rerank(ftsResults, vectorResults, 0)

	bySym := map[string]SearchResult{}
	for _, x := range out {
		bySym[x.Symbol] = x
	}
	if c := bySym["a"].Confidence; c != 0.42 {
		t.Errorf("a.Confidence: expected 0.42 (raw cosine), got %v", c)
	}
	if c := bySym["b"].Confidence; c != 0.91 {
		t.Errorf("b.Confidence: expected 0.91 (raw cosine), got %v", c)
	}
}

// TestRerank_ConfidenceFTSFallback 验证仅 FTS 时置信度回退到归一化 FTS 得分（相对 [0,1]）。
func TestRerank_ConfidenceFTSFallback(t *testing.T) {
	r := NewReranker(1.0, 0.0)
	ftsResults := []SearchResult{
		{Symbol: "a", Score: 0.9},
		{Symbol: "b", Score: 0.3},
	}
	out := r.Rerank(ftsResults, nil, 0)

	bySym := map[string]SearchResult{}
	for _, x := range out {
		bySym[x.Symbol] = x
	}
	if c := bySym["a"].Confidence; c != 1.0 {
		t.Errorf("a.Confidence: expected 1.0 (normalized FTS), got %v", c)
	}
	if c := bySym["b"].Confidence; c < 0.33 || c > 0.34 {
		t.Errorf("b.Confidence: expected ~0.333, got %v", c)
	}
}

// TestSearcher_ExactConfidence 验证纯 FTS 模式置信度按集合最大值归一化为 [0,1]。
func TestSearcher_ExactConfidence(t *testing.T) {
	fts := NewMemoryFTS()
	ctx := context.Background()
	_ = fts.Index(ctx, "x/Hello.java", "class Hello {}")
	_ = fts.Index(ctx, "x/World.java", "class World {}")

	s := NewSearcher(fts, nil, nil)
	results, err := s.Search(ctx, "Hello", SearchModeExact, 10)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Confidence != 1.0 {
		t.Errorf("top Confidence expected 1.0, got %v", results[0].Confidence)
	}
}

// TestSearcher_SemanticConfidence 验证语义模式置信度即向量余弦相似度（绝对量纲）。
func TestSearcher_SemanticConfidence(t *testing.T) {
	fv := &stubVectorSearcher{results: []SearchResult{
		{Symbol: "a", Score: 0.88},
		{Symbol: "b", Score: 0.55},
	}}
	s := NewSearcher(nil, fv, nil)
	results, err := s.Search(context.Background(), "q", SearchModeSemantic, 10)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Confidence != 0.88 {
		t.Errorf("semantic Confidence expected raw cosine 0.88, got %v", results[0].Confidence)
	}
}

// TestSearcher_SearchWithOptionsMinScore_Semantic 验证 MinScore 过滤语义结果 + 计数。
func TestSearcher_SearchWithOptionsMinScore_Semantic(t *testing.T) {
	fv := &stubVectorSearcher{results: []SearchResult{
		{Symbol: "a", Score: 0.88},
		{Symbol: "b", Score: 0.55},
		{Symbol: "c", Score: 0.20},
	}}
	s := NewSearcher(nil, fv, nil)
	ctx := context.Background()

	// MinScore=0：不过滤
	all, filtered, err := s.SearchWithOptions(ctx, "q", SearchOptions{Mode: SearchModeSemantic, Limit: 10, MinScore: 0})
	if err != nil {
		t.Fatal(err)
	}
	if filtered != 0 || len(all) != 3 {
		t.Fatalf("MinScore=0: expected 3 results, 0 filtered; got %d results, %d filtered", len(all), filtered)
	}

	// MinScore=0.6：过滤 b(0.55) 与 c(0.20)
	kept, filtered, err := s.SearchWithOptions(ctx, "q", SearchOptions{Mode: SearchModeSemantic, Limit: 10, MinScore: 0.6})
	if err != nil {
		t.Fatal(err)
	}
	if filtered != 2 || len(kept) != 1 || kept[0].Symbol != "a" {
		t.Fatalf("MinScore=0.6: expected 1 kept(a), 2 filtered; got %d kept, %d filtered", len(kept), filtered)
	}
	for _, r := range kept {
		if r.Confidence < 0.6 {
			t.Errorf("kept result %s confidence %v < 0.6", r.Symbol, r.Confidence)
		}
	}
}

// TestSearcher_SearchWithOptionsMinScore_Both 验证融合模式按向量原始余弦过滤。
func TestSearcher_SearchWithOptionsMinScore_Both(t *testing.T) {
	fts := NewMemoryFTS()
	ctx := context.Background()
	_ = fts.Index(ctx, "x/Apple.java", "apple fruit")
	_ = fts.Index(ctx, "x/Banana.java", "banana fruit")

	fv := &stubVectorSearcher{results: []SearchResult{
		{Symbol: "x/Apple.java", Score: 0.80},
		{Symbol: "x/Banana.java", Score: 0.25},
	}}
	s := NewSearcher(fts, fv, nil)

	// MinScore=0.5：应过滤 Banana（置信度=0.25 原始余弦）
	kept, filtered, err := s.SearchWithOptions(ctx, "fruit", SearchOptions{Mode: SearchModeBoth, Limit: 10, MinScore: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if filtered != 1 {
		t.Errorf("both MinScore=0.5: expected 1 filtered, got %d", filtered)
	}
	if len(kept) != 1 || kept[0].Symbol != "x/Apple.java" {
		t.Errorf("expected Apple kept, got %+v", kept)
	}
}

// TestSearcher_SearchWithOptionsMinScore_Exact 验证纯 FTS 模式按归一化置信度过滤。
func TestSearcher_SearchWithOptionsMinScore_Exact(t *testing.T) {
	fts := NewMemoryFTS()
	ctx := context.Background()
	_ = fts.Index(ctx, "x/High.java", "alpha")                 // 单 token，归一化置信度=1.0
	_ = fts.Index(ctx, "x/Low.java", "alpha beta gamma delta") // alpha 出现 1 次且 tokens 多，归一化<0.99
	s := NewSearcher(fts, nil, nil)

	results, filtered, err := s.SearchWithOptions(ctx, "alpha", SearchOptions{Mode: SearchModeExact, Limit: 10, MinScore: 0.99})
	if err != nil {
		t.Fatal(err)
	}
	if filtered != 1 {
		t.Errorf("exact MinScore=0.99: expected 1 filtered, got %d (results=%d)", filtered, len(results))
	}
	if len(results) != 1 || results[0].Symbol != "x/High.java" {
		t.Errorf("expected only x/High.java kept, got %+v", results)
	}
}
