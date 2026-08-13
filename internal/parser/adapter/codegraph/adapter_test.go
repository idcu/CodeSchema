package codegraph

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"codeschema/internal/errors"
)

func TestCodeGraphAdapter_Name(t *testing.T) {
	a := NewCodeGraphAdapter("")
	if a.Name() != "codegraph" {
		t.Errorf("expected codegraph, got %s", a.Name())
	}
}

func TestCodeGraphAdapter_Supports(t *testing.T) {
	a := NewCodeGraphAdapter("")
	supported := []string{"go", "java", "ts", "py", "rust", "cpp", "c"}
	unsupported := []string{"ruby", "php", "swift"}

	for _, lang := range supported {
		if !a.Supports(lang) {
			t.Errorf("expected %s to be supported", lang)
		}
	}
	for _, lang := range unsupported {
		if a.Supports(lang) {
			t.Errorf("expected %s to be unsupported", lang)
		}
	}
}

func TestCodeGraphAdapter_Parse_NoDatabase(t *testing.T) {
	a := NewCodeGraphAdapter("/nonexistent/path/db.sqlite")
	ctx := context.Background()

	_, err := a.Parse(ctx, "test.go")
	if err == nil {
		t.Fatal("expected error for missing database")
	}
	if err != errors.ErrSourceUnavailable {
		t.Logf("got error: %v (expected ErrSourceUnavailable)", err)
	}
}

func TestCodeGraphAdapter_Parse_EmptyPath(t *testing.T) {
	a := NewCodeGraphAdapter("")
	ctx := context.Background()

	_, err := a.Parse(ctx, "test.go")
	if err == nil {
		t.Fatal("expected error for empty db path")
	}
}

func TestCodeGraphAdapter_ParseAll_NoDatabase(t *testing.T) {
	a := NewCodeGraphAdapter("/nonexistent/db.sqlite")
	ctx := context.Background()

	_, err := a.ParseAll(ctx, []string{"test.go"})
	if err == nil {
		t.Fatal("expected error for missing database")
	}
}

func TestCodeGraphAdapter_ParseAll_EmptyPaths(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "codegraph.db")
	// 创建空文件模拟数据库
	os.WriteFile(dbPath, []byte{}, 0644)

	a := NewCodeGraphAdapter(dbPath)
	ctx := context.Background()

	ch, err := a.ParseAll(ctx, []string{})
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}

	count := 0
	for range ch {
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 docs for empty paths, got %d", count)
	}
}

func TestCodeGraphAdapter_ParseAll_GroupByExt(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "codegraph.db")
	os.WriteFile(dbPath, []byte("test"), 0644)

	a := NewCodeGraphAdapter(dbPath)
	ctx := context.Background()

	paths := []string{
		filepath.Join(dir, "main.go"),
		filepath.Join(dir, "util.go"),
		filepath.Join(dir, "service.java"),
		filepath.Join(dir, "README.md"),
	}

	ch, err := a.ParseAll(ctx, paths)
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}

	count := 0
	for doc := range ch {
		count++
		_ = doc
	}
	// README.md 应被跳过（unknown 扩展名）
	if count != 3 {
		t.Errorf("expected 3 docs (skipping README.md), got %d", count)
	}
}

func TestCodeGraphAdapter_InitClose(t *testing.T) {
	a := NewCodeGraphAdapter("")
	ctx := context.Background()
	if err := a.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestCodeGraphAdapter_InitWithConfig(t *testing.T) {
	a := NewCodeGraphAdapter("")
	ctx := context.Background()

	if err := a.Init(ctx, map[string]any{"db_path": "/custom/path/db.sqlite"}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// ParseAll 应检测到路径变化
	_, err := a.ParseAll(ctx, []string{"test.go"})
	if err == nil {
		t.Fatal("expected error since custom path still doesn't exist")
	}
}

func TestGroupByExt(t *testing.T) {
	paths := []string{
		"/path/main.go",
		"/path/util.go",
		"/path/service.java",
		"/path/helper.ts",
		"/path/Makefile",
	}

	groups := groupByExt(paths)
	if len(groups[".go"]) != 2 {
		t.Errorf("expected 2 .go files, got %d", len(groups[".go"]))
	}
	if len(groups[".java"]) != 1 {
		t.Errorf("expected 1 .java file, got %d", len(groups[".java"]))
	}
	if len(groups[".ts"]) != 1 {
		t.Errorf("expected 1 .ts file, got %d", len(groups[".ts"]))
	}
}