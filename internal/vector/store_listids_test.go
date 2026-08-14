package vector

import (
	"context"
	"testing"
)

func TestMemoryStore_ListIDs(t *testing.T) {
	s := NewMemoryStore()
	ids := []string{"usr/UserService.java", "src/Order.go", "lib/util.py"}
	vecs := [][]float32{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	if err := s.BatchAdd(context.Background(), ids, vecs); err != nil {
		t.Fatalf("BatchAdd: %v", err)
	}

	got, err := s.ListIDs(context.Background())
	if err != nil {
		t.Fatalf("ListIDs: %v", err)
	}
	if len(got) != len(ids) {
		t.Fatalf("expected %d ids, got %d", len(ids), len(got))
	}
	set := make(map[string]bool, len(got))
	for _, id := range got {
		set[id] = true
	}
	for _, id := range ids {
		if !set[id] {
			t.Errorf("ListIDs missing %q", id)
		}
	}
}
