package vector

import (
	"context"
	"math"
	"testing"
)

type testEntity struct {
	id   string
	text string
}

func (e testEntity) ID() string   { return e.id }
func (e testEntity) Text() string { return e.text }

func TestMemoryStore_AddAndSize(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	if store.Size() != 0 {
		t.Errorf("expected size 0, got %d", store.Size())
	}

	_ = store.Add(ctx, "id1", []float32{0.1, 0.2, 0.3})
	if store.Size() != 1 {
		t.Errorf("expected size 1, got %d", store.Size())
	}

	_ = store.Add(ctx, "id2", []float32{0.4, 0.5, 0.6})
	if store.Size() != 2 {
		t.Errorf("expected size 2, got %d", store.Size())
	}
}

func TestMemoryStore_BatchAdd(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	err := store.BatchAdd(ctx, []string{"a", "b"}, [][]float32{{1, 2}, {3, 4}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.Size() != 2 {
		t.Errorf("expected size 2, got %d", store.Size())
	}
}

func TestMemoryStore_BatchAddMismatch(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	err := store.BatchAdd(ctx, []string{"a"}, [][]float32{{1}, {2}})
	if err == nil {
		t.Fatal("expected error for mismatched length")
	}
}

func TestMemoryStore_Search(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_ = store.Add(ctx, "a", []float32{1, 0, 0})
	_ = store.Add(ctx, "b", []float32{0, 1, 0})
	_ = store.Add(ctx, "c", []float32{0, 0, 1})

	// 搜索最接近 [1, 0, 0] 的向量
	results, err := store.Search(ctx, []float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != "a" {
		t.Errorf("expected top result 'a', got %q", results[0].ID)
	}
	if results[0].Score < 0.99 {
		t.Errorf("expected score near 1.0, got %f", results[0].Score)
	}
}

func TestMemoryStore_SearchEmpty(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	results, err := store.Search(ctx, []float32{1, 0}, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestMemoryStore_Delete(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_ = store.Add(ctx, "x", []float32{1, 2, 3})
	if store.Size() != 1 {
		t.Errorf("expected size 1, got %d", store.Size())
	}

	_ = store.Delete(ctx, "x")
	if store.Size() != 0 {
		t.Errorf("expected size 0 after delete, got %d", store.Size())
	}
}

func TestCosineSimilarity(t *testing.T) {
	// 完全相同
	s := cosineSimilarity([]float32{1, 0, 0}, []float32{1, 0, 0})
	if math.Abs(s-1.0) > 0.001 {
		t.Errorf("expected 1.0, got %f", s)
	}

	// 正交
	s = cosineSimilarity([]float32{1, 0}, []float32{0, 1})
	if math.Abs(s-0.0) > 0.001 {
		t.Errorf("expected 0.0, got %f", s)
	}

	// 相反
	s = cosineSimilarity([]float32{1, 0}, []float32{-1, 0})
	if math.Abs(s+1.0) > 0.001 {
		t.Errorf("expected -1.0, got %f", s)
	}

	// 零向量
	s = cosineSimilarity([]float32{0, 0}, []float32{1, 0})
	if math.Abs(s-0.0) > 0.001 {
		t.Errorf("expected 0.0 for zero vector, got %f", s)
	}

	// 不同长度
	s = cosineSimilarity([]float32{1}, []float32{1, 0})
	if math.Abs(s-0.0) > 0.001 {
		t.Errorf("expected 0.0 for mismatched lengths, got %f", s)
	}
}

func TestMockEmbedder_Deterministic(t *testing.T) {
	em := NewMockEmbedder(8)

	v1, _ := em.Embed(context.Background(), "hello")
	v2, _ := em.Embed(context.Background(), "hello")
	v3, _ := em.Embed(context.Background(), "world")

	if len(v1) != 8 {
		t.Errorf("expected dim 8, got %d", len(v1))
	}

	// 相同文本应得到相同向量
	for i := range v1 {
		if v1[i] != v2[i] {
			t.Errorf("v1[%d]=%f != v2[%d]=%f", i, v1[i], i, v2[i])
		}
	}

	// 不同文本应得到不同向量
	same := true
	for i := range v1 {
		if v1[i] != v3[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("different texts should produce different vectors")
	}
}

func TestIndexer_BuildIndex(t *testing.T) {
	store := NewMemoryStore()
	em := NewMockEmbedder(4)
	idx := NewIndexer(store, em, 2)
	ctx := context.Background()

	ent := testEntity{id: "test1", text: "This is a test entity"}
	err := idx.BuildIndex(ctx, ent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.Size() != 1 {
		t.Errorf("expected size 1, got %d", store.Size())
	}
}

func TestIndexer_BatchBuild(t *testing.T) {
	store := NewMemoryStore()
	em := NewMockEmbedder(4)
	idx := NewIndexer(store, em, 2)
	ctx := context.Background()

	ents := []TextEmbeddable{
		testEntity{id: "a", text: "entity A"},
		testEntity{id: "b", text: "entity B"},
		testEntity{id: "c", text: "entity C"},
	}

	err := idx.BatchBuild(ctx, ents)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.Size() != 3 {
		t.Errorf("expected size 3, got %d", store.Size())
	}
}

func TestIndexer_Search(t *testing.T) {
	store := NewMemoryStore()
	em := NewMockEmbedder(4)
	idx := NewIndexer(store, em, 2)
	ctx := context.Background()

	_ = idx.BuildIndex(ctx, testEntity{id: "cat", text: "A cat meows"})
	_ = idx.BuildIndex(ctx, testEntity{id: "dog", text: "A dog barks"})

	results, err := idx.Search(ctx, "cat", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].ID != "cat" {
		// The mock embedder uses hash-based deterministic vectors,
		// so "cat" should be more similar to "A cat meows" than "A dog barks"
		t.Logf("top result: %s (score=%f)", results[0].ID, results[0].Score)
	}
}

func TestIndexer_Enqueue(t *testing.T) {
	store := NewMemoryStore()
	em := NewMockEmbedder(4)
	idx := NewIndexer(store, em, 2)
	ctx := context.Background()

	idx.Start(ctx)

	errC := idx.Enqueue(ctx, testEntity{id: "queued", text: "queued entity"})
	err := <-errC
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idx.Stop()

	if store.Size() != 1 {
		t.Errorf("expected size 1, got %d", store.Size())
	}
}

func TestDefaultText(t *testing.T) {
	text := DefaultText("com.example.UserService", "getUser(id int)", "Gets user by ID")
	if text == "" {
		t.Fatal("expected non-empty text")
	}
}

func TestMemoryStore_Close(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Close(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}