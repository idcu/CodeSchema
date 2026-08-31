package vector

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestPersistentStore_Content 验证原文保存/读取/持久化（DocContentStore 能力）。
func TestPersistentStore_Content(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vector.json")
	ps, err := NewPersistentStore(path)
	if err != nil {
		t.Fatalf("NewPersistentStore: %v", err)
	}

	// 保存原文
	if err := ps.SetContent(context.Background(), "class:1", "OrderService 订单服务"); err != nil {
		t.Fatalf("SetContent: %v", err)
	}
	if err := ps.SetContent(context.Background(), "method:2", "CreateOrder 创建订单"); err != nil {
		t.Fatalf("SetContent: %v", err)
	}

	// 读取
	if c, ok := ps.Content(context.Background(), "class:1"); !ok || c != "OrderService 订单服务" {
		t.Fatalf("Content(class:1) = %q, %v; want OrderService 订单服务, true", c, ok)
	}
	// 不存在的 ID
	if _, ok := ps.Content(context.Background(), "nope"); ok {
		t.Fatal("Content(nope) should be ok=false")
	}

	// 强制保存后重开，验证持久化
	if err := ps.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	ps2, err := NewPersistentStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if c, ok := ps2.Content(context.Background(), "method:2"); !ok || c != "CreateOrder 创建订单" {
		t.Fatalf("reloaded Content(method:2) = %q, %v", c, ok)
	}
}

// TestPersistentStore_Content_BackwardCompat 验证旧版文件（仅 vectors，无 contents）可加载。
func TestPersistentStore_Content_BackwardCompat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vector.json")
	// 写入旧版格式（无 contents 字段）
	old := map[string]any{
		"vectors": map[string][]float32{"class:1": {0.1, 0.2}},
	}
	raw, _ := json.Marshal(old)
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}

	ps, err := NewPersistentStore(path)
	if err != nil {
		t.Fatalf("load old-format file: %v", err)
	}
	// 旧文件 vectors 正常加载
	if ps.Size() != 1 {
		t.Fatalf("Size = %d, want 1", ps.Size())
	}
	// contents 初始为空，不 panic
	if _, ok := ps.Content(context.Background(), "class:1"); ok {
		t.Fatal("old file should have no content")
	}
	// 新写入 content 后保存，格式升级
	if err := ps.SetContent(context.Background(), "class:1", "OrderService"); err != nil {
		t.Fatal(err)
	}
	if err := ps.Save(); err != nil {
		t.Fatal(err)
	}
	ps2, err := NewPersistentStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := ps2.Content(context.Background(), "class:1"); !ok || c != "OrderService" {
		t.Fatalf("after upgrade Content = %q, %v", c, ok)
	}
}

// TestMemoryStore_Content 验证内存实现的原文能力。
func TestMemoryStore_Content(t *testing.T) {
	ms := NewMemoryStore()
	if err := ms.SetContent(context.Background(), "a", "text-a"); err != nil {
		t.Fatal(err)
	}
	if c, ok := ms.Content(context.Background(), "a"); !ok || c != "text-a" {
		t.Fatalf("Content(a) = %q, %v", c, ok)
	}
	if _, ok := ms.Content(context.Background(), "b"); ok {
		t.Fatal("Content(b) should be ok=false")
	}
}

// TestIndexer_SetDocContent 验证 Indexer 转发（Persistent/Memory 保存；非实现后端跳过）。
func TestIndexer_SetDocContent(t *testing.T) {
	// Memory 后端 → 保存
	ms := NewMemoryStore()
	idx := NewIndexer(ms, NewLocalEmbedder(8), 1)
	if err := idx.SetDocContent(context.Background(), "id1", "hello world"); err != nil {
		t.Fatalf("SetDocContent (memory): %v", err)
	}
	if c, _ := ms.Content(context.Background(), "id1"); c != "hello world" {
		t.Fatalf("content = %q, want hello world", c)
	}
}
