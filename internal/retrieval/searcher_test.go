package retrieval

import (
	"context"
	"testing"
)

// TestSearcherExactMode P1-2 负控：exact 模式必须是 token 精确匹配，
// 不能落到 fuzzy 子串匹配（否则 Sv 会命中 Svc，java 会命中 javascript）。
func TestSearcherExactMode(t *testing.T) {
	ctx := context.Background()
	fts := NewMemoryFTS()
	for id, content := range map[string]string{
		"1": "Svc",
		"2": "Svc.Method",
		"3": "javascript",
	} {
		if err := fts.Index(ctx, id, content); err != nil {
			t.Fatalf("index %s: %v", id, err)
		}
	}
	s := NewSearcher(fts, nil, nil)

	got, err := s.Search(ctx, "Sv", SearchModeExact, 10)
	if err != nil {
		t.Fatalf("exact Sv: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("exact Sv 不应命中 Svc/Svc.Method, 实际 %d 条: %+v", len(got), got)
	}

	got, err = s.Search(ctx, "Svc", SearchModeExact, 10)
	if err != nil {
		t.Fatalf("exact Svc: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("exact Svc 应命中")
	}

	got, err = s.Search(ctx, "java", SearchModeExact, 10)
	if err != nil {
		t.Fatalf("exact java: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("exact java 不应命中 javascript, 实际 %d 条: %+v", len(got), got)
	}

	// fuzzy 行为不变：both 模式的 FTS 子串仍可命中（vector 为 nil 时走纯 FTS）。
	got, err = s.Search(ctx, "Sv", SearchModeBoth, 10)
	if err != nil {
		t.Fatalf("fuzzy Sv: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("fuzzy Sv 应命中 Svc")
	}
}
