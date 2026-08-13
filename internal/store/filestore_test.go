package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"codeschema/internal/parser"
)

func TestFileStore_OpenClose(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStore{}

	if err := fs.Open(context.Background(), dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := fs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 验证数据文件已创建
	if _, err := os.Stat(filepath.Join(dir, "store.json")); err != nil {
		t.Fatalf("store.json not created: %v", err)
	}
}

func TestFileStore_UpsertAndGetFile(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStore{}
	ctx := context.Background()

	if err := fs.Open(ctx, dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer fs.Close()

	// 插入文件
	id, err := fs.UpsertFile(ctx, "/test/main.go", "abc123", 100, 2048)
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	if id != 1 {
		t.Errorf("expected id 1, got %d", id)
	}

	// 查询文件
	f, err := fs.GetFileByPath(ctx, "/test/main.go")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if f == nil {
		t.Fatal("file not found")
	}
	if f.ContentHash != "abc123" {
		t.Errorf("expected hash abc123, got %s", f.ContentHash)
	}

	// 再次插入（更新）
	id2, err := fs.UpsertFile(ctx, "/test/main.go", "def456", 120, 4096)
	if err != nil {
		t.Fatalf("UpsertFile (update): %v", err)
	}
	if id2 != id {
		t.Errorf("expected same id %d, got %d", id, id2)
	}

	// 验证已更新
	f, _ = fs.GetFileByPath(ctx, "/test/main.go")
	if f.ContentHash != "def456" {
		t.Errorf("expected hash def456, got %s", f.ContentHash)
	}
	if f.LineCount != 120 {
		t.Errorf("expected line count 120, got %d", f.LineCount)
	}
}

func TestFileStore_DeleteFile(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStore{}
	ctx := context.Background()

	if err := fs.Open(ctx, dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer fs.Close()

	id, _ := fs.UpsertFile(ctx, "/test/main.go", "hash1", 100, 1024)
	if err := fs.DeleteFile(ctx, id); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}

	f, _ := fs.GetFileByPath(ctx, "/test/main.go")
	if f != nil {
		t.Error("file should be deleted")
	}
}

func TestFileStore_UpsertIR(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStore{}
	ctx := context.Background()

	if err := fs.Open(ctx, dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer fs.Close()

	// 模拟一个 IR 文档
	ir := &parser.IRDocument{
		Source:   "treesitter",
		Language: "go",
		FilePath: "/test/service.go",
		FileHash: "sha256hash",
		LineCount: 200,
		ByteSize:  8192,
		Classes: []parser.ClassIR{
			{Name: "UserService", FullName: "com.example.UserService", Type: "CLASS", StartLine: 1, EndLine: 50},
			{Name: "UserRepository", FullName: "com.example.UserRepository", Type: "INTERFACE", StartLine: 52, EndLine: 60},
		},
		Calls: []parser.CallIR{
			{CallerFQN: "com.example.UserService.GetUser", CalleeFQN: "com.example.UserRepository.FindByID", CallType: "direct", LineNumber: 30},
		},
	}

	if err := fs.UpsertIR(ctx, ir); err != nil {
		t.Fatalf("UpsertIR: %v", err)
	}

	// 验证文件已创建
	f, _ := fs.GetFileByPath(ctx, "/test/service.go")
	if f == nil {
		t.Fatal("file not found after UpsertIR")
	}
	if f.LineCount != 200 {
		t.Errorf("expected line count 200, got %d", f.LineCount)
	}
}

func TestFileStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// 第一次写入
	fs1 := &FileStore{}
	if err := fs1.Open(ctx, dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	fs1.UpsertFile(ctx, "/test/main.go", "hash1", 100, 1024)
	fs1.UpsertFile(ctx, "/test/util.go", "hash2", 50, 512)
	fs1.Close()

	// 第二次读取
	fs2 := &FileStore{}
	if err := fs2.Open(ctx, dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer fs2.Close()

	f1, _ := fs2.GetFileByPath(ctx, "/test/main.go")
	if f1 == nil || f1.ContentHash != "hash1" {
		t.Error("persistence failed for main.go")
	}

	f2, _ := fs2.GetFileByPath(ctx, "/test/util.go")
	if f2 == nil || f2.ContentHash != "hash2" {
		t.Error("persistence failed for util.go")
	}
}

func TestFileStore_HealthCheck(t *testing.T) {
	fs := &FileStore{}
	ctx := context.Background()

	// 未初始化时应返回错误
	if err := fs.HealthCheck(ctx); err == nil {
		t.Error("expected error for uninitialized store")
	}

	dir := t.TempDir()
	fs.Open(ctx, dir)
	if err := fs.HealthCheck(ctx); err != nil {
		t.Errorf("HealthCheck: %v", err)
	}
	fs.Close()
}