package search

import (
	"context"
	"math"
	"testing"
)

func TestMemoryFTS_IndexAndSize(t *testing.T) {
	fts := NewMemoryFTS()
	ctx := context.Background()

	if fts.Size() != 0 {
		t.Errorf("expected size 0, got %d", fts.Size())
	}

	_ = fts.Index(ctx, "a", "hello world")
	if fts.Size() != 1 {
		t.Errorf("expected size 1, got %d", fts.Size())
	}
}

func TestMemoryFTS_BatchIndex(t *testing.T) {
	fts := NewMemoryFTS()
	ctx := context.Background()

	err := fts.BatchIndex(ctx, []string{"a", "b"}, []string{"doc a", "doc b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fts.Size() != 2 {
		t.Errorf("expected size 2, got %d", fts.Size())
	}
}

func TestMemoryFTS_BatchIndexMismatch(t *testing.T) {
	fts := NewMemoryFTS()
	ctx := context.Background()

	err := fts.BatchIndex(ctx, []string{"a"}, []string{"doc a", "doc b"})
	if err == nil {
		t.Fatal("expected error for mismatched length")
	}
}

func TestMemoryFTS_ExactSearch(t *testing.T) {
	fts := NewMemoryFTS()
	ctx := context.Background()

	_ = fts.Index(ctx, "usr/UserService.java", "class UserService { public User getUser() {} }")
	_ = fts.Index(ctx, "usr/OrderService.java", "class OrderService { public Order createOrder() {} }")

	results, err := fts.Search(ctx, "UserService", FTSModeExact, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].Symbol != "usr/UserService.java" {
		t.Errorf("expected UserService, got %q", results[0].Symbol)
	}
}

func TestMemoryFTS_FuzzySearch(t *testing.T) {
	fts := NewMemoryFTS()
	ctx := context.Background()

	_ = fts.Index(ctx, "src/UserService.java", "class UserService creates users")
	_ = fts.Index(ctx, "src/OrderService.java", "class OrderService processes orders")

	results, err := fts.Search(ctx, "user", FTSModeFuzzy, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].Symbol != "src/UserService.java" {
		t.Errorf("expected UserService, got %q", results[0].Symbol)
	}
}

func TestMemoryFTS_PrefixSearch(t *testing.T) {
	fts := NewMemoryFTS()
	ctx := context.Background()

	_ = fts.Index(ctx, "x/OrderController.java", "OrderController handles orders")
	_ = fts.Index(ctx, "x/OrderService.java", "OrderService processes orders")
	_ = fts.Index(ctx, "x/UserService.java", "UserService handles users")

	results, err := fts.Search(ctx, "Order*", FTSModeFuzzy, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
}

func TestMemoryFTS_EmptyQuery(t *testing.T) {
	fts := NewMemoryFTS()
	ctx := context.Background()

	_ = fts.Index(ctx, "a", "content")
	results, err := fts.Search(ctx, "", FTSModeExact, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty query, got %d", len(results))
	}
}

func TestMemoryFTS_EmptyIndex(t *testing.T) {
	fts := NewMemoryFTS()
	ctx := context.Background()

	results, err := fts.Search(ctx, "anything", FTSModeExact, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestMemoryFTS_Remove(t *testing.T) {
	fts := NewMemoryFTS()
	ctx := context.Background()

	_ = fts.Index(ctx, "x", "content")
	if fts.Size() != 1 {
		t.Errorf("expected size 1, got %d", fts.Size())
	}

	_ = fts.Remove(ctx, "x")
	if fts.Size() != 0 {
		t.Errorf("expected size 0, got %d", fts.Size())
	}
}

func TestReranker_DefaultWeights(t *testing.T) {
	r := DefaultReranker()
	if r.FTSWeight != 0.3 {
		t.Errorf("expected FTS weight 0.3, got %f", r.FTSWeight)
	}
	if r.VectorWeight != 0.7 {
		t.Errorf("expected vector weight 0.7, got %f", r.VectorWeight)
	}
}

func TestReranker_FTSOnly(t *testing.T) {
	r := NewReranker(1.0, 0.0)

	ftsResults := []SearchResult{
		{Symbol: "a", Score: 0.9},
		{Symbol: "b", Score: 0.5},
	}

	results := r.Rerank(ftsResults, nil, 0)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Symbol != "a" {
		t.Errorf("expected top result 'a', got %q", results[0].Symbol)
	}
}

func TestReranker_VectorOnly(t *testing.T) {
	r := NewReranker(0.0, 1.0)

	vectorResults := []SearchResult{
		{Symbol: "x", Score: 0.8},
		{Symbol: "y", Score: 0.6},
	}

	results := r.Rerank(nil, vectorResults, 0)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Symbol != "x" {
		t.Errorf("expected top result 'x', got %q", results[0].Symbol)
	}
}

func TestReranker_Fused(t *testing.T) {
	r := NewReranker(0.5, 0.5)

	ftsResults := []SearchResult{
		{Symbol: "a", Score: 1.0},
		{Symbol: "b", Score: 0.5},
	}

	vectorResults := []SearchResult{
		{Symbol: "b", Score: 1.0}, // b 在向量中匹配更好
		{Symbol: "c", Score: 0.8},
	}

	results := r.Rerank(ftsResults, vectorResults, 0)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d: %+v", len(results), results)
	}

	// b: 0.5*(0.5/1.0) + 0.5*(1.0/1.0) = 0.25 + 0.5 = 0.75
	// c: 0.0 + 0.5*(0.8/1.0) = 0.4
	// a: 0.5*(1.0/1.0) + 0.0 = 0.5
	// 排序: b (0.75) > a (0.5) > c (0.4)
	if results[0].Symbol != "b" {
		t.Errorf("expected top result 'b', got %q", results[0].Symbol)
	}
}

func TestReranker_Limit(t *testing.T) {
	r := NewReranker(1.0, 0.0)

	ftsResults := []SearchResult{
		{Symbol: "a", Score: 0.9},
		{Symbol: "b", Score: 0.5},
		{Symbol: "c", Score: 0.3},
	}

	results := r.Rerank(ftsResults, nil, 2)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestReranker_EmptyInputs(t *testing.T) {
	r := DefaultReranker()
	results := r.Rerank(nil, nil, 10)
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearcher_ExactMode(t *testing.T) {
	fts := NewMemoryFTS()
	ctx := context.Background()

	_ = fts.Index(ctx, "test/Hello.java", "class Hello { void hello() {} }")
	_ = fts.Index(ctx, "test/World.java", "class World { void world() {} }")

	s := NewSearcher(fts, nil, nil)
	results, err := s.Search(ctx, "Hello", SearchModeExact, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].Symbol != "test/Hello.java" {
		t.Errorf("expected Hello, got %q", results[0].Symbol)
	}
}

func TestSearcher_EmptyQuery(t *testing.T) {
	fts := NewMemoryFTS()
	s := NewSearcher(fts, nil, nil)
	_, err := s.Search(context.Background(), "", SearchModeBoth, 10)
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestSearcher_DefaultLimit(t *testing.T) {
	fts := NewMemoryFTS()
	ctx := context.Background()

	for i := 0; i < 30; i++ {
		_ = fts.Index(ctx, rpad(i), "content")
	}

	s := NewSearcher(fts, nil, nil)
	results, err := s.Search(ctx, "content", SearchModeExact, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 20 {
		t.Errorf("expected default limit 20, got %d", len(results))
	}
}

func rpad(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('a' + i - 10))
}

func TestTokenize(t *testing.T) {
	tokens := tokenize("HelloWorld_Test")
	if len(tokens) == 0 {
		t.Fatal("expected non-empty tokens")
	}
}

func TestCosineSimilarityEdge(t *testing.T) {
	// 全零向量
	s := cosineSimilarity([]float32{0, 0}, []float32{0, 0})
	if math.Abs(s-0.0) > 0.001 {
		t.Errorf("expected 0.0, got %f", s)
	}
}

// cosineSimilarity 供测试使用的内部函数
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dotProduct, normA, normB float64
	for i := range a {
		fa := float64(a[i])
		fb := float64(b[i])
		dotProduct += fa * fb
		normA += fa * fa
		normB += fb * fb
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

func TestPersistentFTS_SaveLoad(t *testing.T) {
	dir := t.TempDir()
	filePath := dir + "/fts_test.json"

	pf, err := NewPersistentFTS(filePath)
	if err != nil {
		t.Fatalf("NewPersistentFTS: %v", err)
	}
	ctx := context.Background()
	_ = pf.Index(ctx, "test/a", "hello world")
	_ = pf.Index(ctx, "test/b", "foo bar")
	_ = pf.Save()

	// reload
	pf2, err := NewPersistentFTS(filePath)
	if err != nil {
		t.Fatalf("NewPersistentFTS reload: %v", err)
	}
	if pf2.Size() != 2 {
		t.Errorf("expected size 2 after reload, got %d", pf2.Size())
	}
}

func TestPersistentFTS_Search(t *testing.T) {
	dir := t.TempDir()
	pf, err := NewPersistentFTS(dir + "/search_test.json")
	if err != nil {
		t.Fatalf("NewPersistentFTS: %v", err)
	}
	ctx := context.Background()
	_ = pf.Index(ctx, "test/Hello.java", "class HelloService { void hello() {} }")
	_ = pf.Index(ctx, "test/World.java", "class WorldService { void world() {} }")

	results, err := pf.Search(ctx, "Hello", FTSModeExact, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Symbol != "test/Hello.java" {
		t.Errorf("expected 'test/Hello.java', got %q", results[0].Symbol)
	}
}

func TestPersistentFTS_Remove(t *testing.T) {
	dir := t.TempDir()
	pf, err := NewPersistentFTS(dir + "/remove_test.json")
	if err != nil {
		t.Fatalf("NewPersistentFTS: %v", err)
	}
	ctx := context.Background()
	_ = pf.Index(ctx, "id1", "content")
	_ = pf.Remove(ctx, "id1")

	if pf.Size() != 0 {
		t.Errorf("expected size 0 after remove, got %d", pf.Size())
	}
}

func TestPersistentFTS_EmptySearch(t *testing.T) {
	dir := t.TempDir()
	pf, err := NewPersistentFTS(dir + "/empty_test.json")
	if err != nil {
		t.Fatalf("NewPersistentFTS: %v", err)
	}
	ctx := context.Background()
	results, err := pf.Search(ctx, "query", FTSModeExact, 10)
	if err != nil {
		t.Fatalf("Search empty: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}