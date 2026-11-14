package retrieval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestPersistentFTS_StaleTempIgnoredOnLoad 负控（中断不产生半份索引）：
// 进程在 rename 前被强杀时只会留下 fts.json.tmp；重新加载必须只读主文件，
// 下次保存再清理残留临时文件。
func TestPersistentFTS_StaleTempIgnoredOnLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fts.json")
	ctx := context.Background()
	pf, err := NewPersistentFTS(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := pf.Index(ctx, "doc-1", "alpha beta"); err != nil {
		t.Fatalf("Index: %v", err)
	}
	if err := pf.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// 模拟写到一半被中断：临时文件里是半份 JSON。
	if err := os.WriteFile(path+".tmp", []byte(`{"docs":{"doc-2":{"id":"doc-2",`), 0o600); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewPersistentFTS(path)
	if err != nil {
		t.Fatalf("重开应忽略半份临时文件: %v", err)
	}
	if reopened.Size() != 1 {
		t.Fatalf("应只加载 1 篇文档，实际 %d", reopened.Size())
	}
	if _, err := reopened.Search(ctx, "alpha", FTSModeFuzzy, 10); err != nil {
		t.Fatalf("Search: %v", err)
	}

	if err := reopened.Save(); err != nil {
		t.Fatalf("Save after stale tmp: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("保存成功应清理残留临时文件，stat err = %v", err)
	}
}

// TestPersistentFTS_OnlyTempNoMain 负控：只有半份 .tmp、没有主文件时，
// 加载必须得到空索引，而不是把半份临时文件当成有效索引。
func TestPersistentFTS_OnlyTempNoMain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fts.json")
	if err := os.WriteFile(path+".tmp", []byte(`{"docs":{"doc-2":{"id":"doc-2",`), 0o600); err != nil {
		t.Fatal(err)
	}
	pf, err := NewPersistentFTS(path)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if pf.Size() != 0 {
		t.Fatalf("半份临时文件不得被当作索引，Size = %d", pf.Size())
	}
}

// TestPersistentFTS_SaveDeterministicAndCompatible 验证流式输出：
// 与旧 persistentFTSData 语义一致、两次保存字节一致、旧 MarshalIndent 文件可加载。
func TestPersistentFTS_SaveDeterministicAndCompatible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fts.json")
	ctx := context.Background()
	pf, err := NewPersistentFTS(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := pf.BatchIndex(ctx, []string{"b", "a"}, []string{"beta gamma", "alpha gamma"}); err != nil {
		t.Fatalf("BatchIndex: %v", err)
	}
	if err := pf.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := pf.Save(); err != nil {
		t.Fatalf("Save again: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("相同数据两次保存应字节一致")
	}

	var old persistentFTSData
	if err := json.Unmarshal(first, &old); err != nil {
		t.Fatalf("新文件应可被旧结构解析: %v", err)
	}
	if len(old.Docs) != 2 || old.Docs["a"] == nil || old.Docs["a"].Content != "alpha gamma" {
		t.Fatalf("旧结构解析结果不一致: %+v", old.Docs)
	}
	if !reflect.DeepEqual(old.Docs["b"].Tokens, Tokenize("beta gamma")) {
		t.Fatalf("tokens 不一致: %v", old.Docs["b"].Tokens)
	}

	// 旧版文件（整索引 MarshalIndent）必须能被新代码加载。
	legacyPath := filepath.Join(t.TempDir(), "legacy.json")
	legacyRaw, _ := json.MarshalIndent(persistentFTSData{
		Docs: map[string]*docEntryJSON{
			"x": {ID: "x", Content: "legacy doc", Tokens: Tokenize("legacy doc")},
		},
	}, "", "  ")
	if err := os.WriteFile(legacyPath, legacyRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	legacy, err := NewPersistentFTS(legacyPath)
	if err != nil {
		t.Fatalf("旧文件应可加载: %v", err)
	}
	if legacy.Size() != 1 {
		t.Fatalf("旧文件应加载 1 篇文档，实际 %d", legacy.Size())
	}
}
