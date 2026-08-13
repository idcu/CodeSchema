package vector

import (
	"context"
	"testing"

	"github.com/philippgille/chromem-go"
)

func TestNewChromemStore(t *testing.T) {
	s := NewChromemStore("test", 4, nil)
	if s == nil {
		t.Fatal("expected non-nil store")
	}
	if s.dim != 4 {
		t.Errorf("expected dim=4, got %d", s.dim)
	}
}

func TestChromemStore_AddAndSearch(t *testing.T) {
	s := NewChromemStore("test-search", 4, nil)
	if s == nil {
		t.Fatal("expected non-nil store")
	}

	ctx := context.Background()

	// 添加向量
	err := s.Add(ctx, "doc1", []float32{1, 0, 0, 0})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	err = s.Add(ctx, "doc2", []float32{0, 1, 0, 0})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// 搜索（k 必须 <= 文档数）
	results, err := s.Search(ctx, []float32{1, 0, 0, 0}, 2)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].ID != "doc1" {
		t.Errorf("expected top result=doc1, got %s", results[0].ID)
	}
}

func TestChromemStore_BatchAdd(t *testing.T) {
	s := NewChromemStore("test-batch", 4, nil)
	if s == nil {
		t.Fatal("expected non-nil store")
	}

	ctx := context.Background()
	ids := []string{"a", "b", "c"}
	vecs := [][]float32{
		{1, 0, 0, 0},
		{0, 1, 0, 0},
		{0, 0, 1, 0},
	}

	err := s.BatchAdd(ctx, ids, vecs)
	if err != nil {
		t.Fatalf("BatchAdd failed: %v", err)
	}

	results, err := s.Search(ctx, []float32{0, 0, 1, 0}, 1)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ID != "c" {
		t.Errorf("expected top result=c, got %s", results[0].ID)
	}
}

func TestChromemStore_BatchAdd_Mismatch(t *testing.T) {
	s := NewChromemStore("test-mismatch", 4, nil)
	if s == nil {
		t.Fatal("expected non-nil store")
	}

	ctx := context.Background()
	err := s.BatchAdd(ctx, []string{"a"}, [][]float32{{1, 0}, {0, 1}})
	if err == nil {
		t.Fatal("expected error for mismatched lengths")
	}
}

func TestChromemStore_Search_ZeroK(t *testing.T) {
	s := NewChromemStore("test-zero-k", 4, nil)
	if s == nil {
		t.Fatal("expected non-nil store")
	}

	ctx := context.Background()
	s.Add(ctx, "doc1", []float32{1, 0, 0, 0})
	s.Add(ctx, "doc2", []float32{0, 1, 0, 0})

	results, err := s.Search(ctx, []float32{1, 0, 0, 0}, 2)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result with k=0")
	}
}

func TestChromemStore_Delete_Unsupported(t *testing.T) {
	s := NewChromemStore("test-delete", 4, nil)
	if s == nil {
		t.Fatal("expected non-nil store")
	}

	err := s.Delete(context.Background(), "doc1")
	if err == nil {
		t.Fatal("expected error for delete (unsupported)")
	}
}

func TestChromemStore_Close(t *testing.T) {
	s := NewChromemStore("test-close", 4, nil)
	if s == nil {
		t.Fatal("expected non-nil store")
	}

	err := s.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestNewPersistentChromemStore(t *testing.T) {
	dir := t.TempDir()
	s, err := NewPersistentChromemStore("test-persist", dir, 4, nil)
	if err != nil {
		t.Fatalf("NewPersistentChromemStore failed: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil store")
	}
	if s.dim != 4 {
		t.Errorf("expected dim=4, got %d", s.dim)
	}
}

func TestPersistentChromemStore_AddAndSearch(t *testing.T) {
	dir := t.TempDir()
	s, err := NewPersistentChromemStore("test-persist-search", dir, 4, nil)
	if err != nil {
		t.Fatalf("NewPersistentChromemStore failed: %v", err)
	}

	ctx := context.Background()
	s.Add(ctx, "x", []float32{1, 0, 0, 0})

	results, err := s.Search(ctx, []float32{1, 0, 0, 0}, 1)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].ID != "x" {
		t.Errorf("expected top result=x, got %s", results[0].ID)
	}
}

func TestChromemStore_WithCustomEmbedFn(t *testing.T) {
	embedFn := chromem.EmbeddingFunc(func(ctx context.Context, text string) ([]float32, error) {
		return []float32{0.5, 0.5, 0.5, 0.5}, nil
	})

	s := NewChromemStore("test-custom-embed", 4, embedFn)
	if s == nil {
		t.Fatal("expected non-nil store")
	}

	ctx := context.Background()
	err := s.Add(ctx, "doc1", []float32{0.5, 0.5, 0.5, 0.5})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
}

func TestChromemStore_Size(t *testing.T) {
	s := NewChromemStore("test-size", 4, nil)
	if s == nil {
		t.Fatal("expected non-nil store")
	}

	// chromem-go 不支持 Size 查询
	if s.Size() != -1 {
		t.Errorf("expected Size()=-1 for chromem store, got %d", s.Size())
	}
}