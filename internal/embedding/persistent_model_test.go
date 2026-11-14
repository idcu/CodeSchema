package embedding

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestPersistentStore_ModelAndDimMismatchRejected 负控（R6-3 / P2-16）：
// 向量文件记录写入时的模型标识与维度；切换模型/维度后加载必须失败，
// 而不是静默沿用旧向量（余弦恒 0、检索结果错配）。
func TestPersistentStore_ModelAndDimMismatchRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vector.json")
	ctx := context.Background()

	ps, err := NewPersistentStoreWithModel(path, "mock-a", 4)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := ps.Add(ctx, "id1", []float32{1, 0, 0, 0}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := ps.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 模型与维度都一致 → 允许加载
	if _, err := NewPersistentStoreWithModel(path, "mock-a", 4); err != nil {
		t.Fatalf("同模型同维度不应拒绝: %v", err)
	}

	// 维度不一致 → 拒绝
	if _, err := NewPersistentStoreWithModel(path, "mock-a", 8); err == nil {
		t.Fatal("维度不一致（file=4, expect=8）应拒绝加载，实际 nil")
	}

	// 模型标识不一致 → 拒绝
	if _, err := NewPersistentStoreWithModel(path, "mock-b", 4); err == nil {
		t.Fatal("模型不一致（file=mock-a, expect=mock-b）应拒绝加载，实际 nil")
	}
}

// TestPersistentStore_LegacyFileInfersDim 负控（R6-3 / P2-16）：
// 旧版文件没有 model_id/dim 字段，按实际向量长度推断维度；与期望维度不符时拒绝，
// 相符则加载并在下次保存时补写元数据（此后模型变更同样会被拒绝）。
func TestPersistentStore_LegacyFileInfersDim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vector.json")
	old := map[string]any{
		"vectors": map[string][]float32{"class:1": {0.1, 0.2, 0.3, 0.4}},
	}
	raw, _ := json.Marshal(old)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := NewPersistentStoreWithModel(path, "mock-a", 8); err == nil {
		t.Fatal("旧文件 4 维向量与期望 8 维不符应拒绝加载，实际 nil")
	}

	ps, err := NewPersistentStoreWithModel(path, "mock-a", 4)
	if err != nil {
		t.Fatalf("旧文件 4 维与期望 4 维应允许加载: %v", err)
	}
	if err := ps.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := NewPersistentStoreWithModel(path, "mock-b", 4); err == nil {
		t.Fatal("升级后的文件应记录 model_id 并拒绝不同模型")
	}
}

// TestPersistentStore_InconsistentVectorRejected 负控（R6-3 / P2-16）：
// 文件声明 dim=4 但含 3 维向量（历史脏数据）时拒绝加载，
// 否则该条向量与查询永远算不出余弦相似度。
func TestPersistentStore_InconsistentVectorRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vector.json")
	bad := map[string]any{
		"vectors": map[string][]float32{
			"a": {1, 2, 3, 4},
			"b": {1, 2, 3},
		},
		"dim":      4,
		"model_id": "mock-a",
	}
	raw, _ := json.Marshal(bad)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := NewPersistentStoreWithModel(path, "mock-a", 4); err == nil {
		t.Fatal("含维度不一致向量的文件应拒绝加载，实际 nil")
	}
}
