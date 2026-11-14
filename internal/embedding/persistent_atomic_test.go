package embedding

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestPersistentStore_SaveFailureKeepsPreviousVersion 负控（整索引流式落盘）：
// 序列化中途失败（NaN 无法编码为 JSON）时，磁盘上必须仍是上一完整版本，
// 不得出现半份索引或残留临时文件。
func TestPersistentStore_SaveFailureKeepsPreviousVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vector.json")
	ctx := context.Background()
	ps, err := NewPersistentStoreWithModel(path, "mock-a", 2)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := ps.Add(ctx, "aa-good", []float32{1, 0}); err != nil {
		t.Fatalf("Add good: %v", err)
	}
	if err := ps.Save(); err != nil {
		t.Fatalf("Save good: %v", err)
	}

	// 排序后 "zz-bad" 在合法条目之后：流式写出会先落盘部分内容再失败，
	// 正好覆盖「写出中途失败」而非「写前失败」。
	if err := ps.Add(ctx, "zz-bad", []float32{float32(math.NaN()), 1}); err != nil {
		t.Fatalf("Add bad: %v", err)
	}
	if err := ps.Save(); err == nil {
		t.Fatal("含 NaN 的向量 Save 应返回错误，实际 nil")
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("失败后临时文件应被删除，stat err = %v", err)
	}

	reloaded, err := NewPersistentStoreWithModel(path, "mock-a", 2)
	if err != nil {
		t.Fatalf("失败后旧文件应仍可加载: %v", err)
	}
	if reloaded.Size() != 1 {
		t.Fatalf("旧版本应只含 1 条向量，实际 %d", reloaded.Size())
	}
	if _, ok := reloaded.vecs["aa-good"]; !ok {
		t.Fatal("旧版本向量 aa-good 丢失")
	}
	if _, ok := reloaded.vecs["zz-bad"]; ok {
		t.Fatal("失败写入的向量不应落盘")
	}
}

// TestPersistentStore_SaveDeterministicAndCompatible 验证流式输出：
// 1) 与旧 persistentData 结构语义一致（可被旧结构 Unmarshal 后 DeepEqual）；
// 2) 两次保存字节一致（排序键保证确定性）；
// 3) 旧 MarshalIndent 文件可被新代码加载。
func TestPersistentStore_SaveDeterministicAndCompatible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vector.json")
	ctx := context.Background()
	ps, err := NewPersistentStoreWithModel(path, "mock-a", 3)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := ps.BatchAdd(ctx, []string{"b", "a"}, [][]float32{{0, 1, 0}, {1, 0, 0}}); err != nil {
		t.Fatalf("BatchAdd: %v", err)
	}
	if err := ps.BatchSetContents(ctx, []string{"b", "a"}, []string{"doc-b", "doc-a"}); err != nil {
		t.Fatalf("BatchSetContents: %v", err)
	}
	if err := ps.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Save(); err != nil {
		t.Fatalf("Save again: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("相同数据两次保存应字节一致")
	}

	var old persistentData
	if err := json.Unmarshal(first, &old); err != nil {
		t.Fatalf("新文件应可被旧结构解析: %v", err)
	}
	want := persistentData{
		Vectors:  map[string][]float32{"a": {1, 0, 0}, "b": {0, 1, 0}},
		Contents: map[string]string{"a": "doc-a", "b": "doc-b"},
		ModelID:  "mock-a",
		Dim:      3,
	}
	if !reflect.DeepEqual(old, want) {
		t.Fatalf("旧结构解析结果不一致:\n got=%+v\nwant=%+v", old, want)
	}

	// 旧版文件（整索引 MarshalIndent）必须能被新代码加载。
	legacyPath := filepath.Join(t.TempDir(), "legacy.json")
	legacyRaw, _ := json.MarshalIndent(persistentData{
		Vectors: map[string][]float32{"x": {0, 0, 1}},
		ModelID: "mock-a",
		Dim:     3,
	}, "", "  ")
	if err := os.WriteFile(legacyPath, legacyRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	legacy, err := NewPersistentStoreWithModel(legacyPath, "mock-a", 3)
	if err != nil {
		t.Fatalf("旧文件应可加载: %v", err)
	}
	if _, ok := legacy.vecs["x"]; !ok {
		t.Fatal("旧文件向量 x 丢失")
	}
}
