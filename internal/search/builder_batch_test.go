package search

import (
	"context"
	"testing"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/vector"
)

// countingDocStore 包装内存向量存储，统计原文写入走逐篇还是批量通道。
type countingDocStore struct {
	*vector.MemoryStore
	setCalls   int
	batchCalls int
}

func (c *countingDocStore) SetContent(ctx context.Context, id, content string) error {
	c.setCalls++
	return c.MemoryStore.SetContent(ctx, id, content)
}

func (c *countingDocStore) BatchSetContents(ctx context.Context, ids, contents []string) error {
	c.batchCalls++
	for i := range ids {
		if err := c.MemoryStore.SetContent(ctx, ids[i], contents[i]); err != nil {
			return err
		}
	}
	return nil
}

// TestIndexBuilder_BuildFromStore_BatchesDocContent 守护构建期原文落盘走批量通道。
//
// 背景：持久化向量存储的 SetContent 每 10 次变更就把整个索引 Marshal 落盘；
// 真实仓库（GOROOT src 9.6 万文档）逐篇写入会退化为 O(n²) 全量序列化。
// 构建期必须一次批量提交，逐篇 SetContent 次数应为 0。
func TestIndexBuilder_BuildFromStore_BatchesDocContent(t *testing.T) {
	fts := NewMemoryFTS()
	vs := &countingDocStore{MemoryStore: vector.NewMemoryStore()}
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	b := NewIndexBuilder(fts, idx, em)

	st := store.NewStore("file")
	ctx := context.Background()
	if err := st.Open(ctx, t.TempDir()); err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	fileID, err := st.UpsertFile(ctx, "pkg/demo/demo.go", "hash-demo", 10, 128)
	if err != nil {
		t.Fatalf("upsert file: %v", err)
	}
	if err := st.UpsertClasses(ctx, fileID, []parser.ClassIR{{
		Name: "Demo", FullName: "pkg/demo.Demo", Type: "CLASS",
	}}); err != nil {
		t.Fatalf("upsert classes: %v", err)
	}
	if err := st.UpsertMethods(ctx, fileID, []parser.MethodIR{
		{Name: "A", Signature: "A()", ClassFQN: "pkg/demo.Demo"},
		{Name: "B", Signature: "B()", ClassFQN: "pkg/demo.Demo"},
		{Name: "C", Signature: "C()", ClassFQN: "pkg/demo.Demo"},
	}); err != nil {
		t.Fatalf("upsert methods: %v", err)
	}

	if _, err := b.BuildFromStore(ctx, st); err != nil {
		t.Fatalf("BuildFromStore: %v", err)
	}
	if vs.batchCalls != 1 {
		t.Fatalf("batch doc-content writes = %d, want 1", vs.batchCalls)
	}
	if vs.setCalls != 0 {
		t.Fatalf("per-doc SetContent calls = %d, want 0 (batch path required)", vs.setCalls)
	}
}
