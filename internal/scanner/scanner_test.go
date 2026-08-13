package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
)

// mockAdapter 实现 ParserPlugin 接口的测试适配器。
type mockParser struct {
	name     string
	supports map[string]bool
	parseFn  func(ctx context.Context, path string) (*parser.IRDocument, error)
}

func (m *mockParser) Name() string                               { return m.name }
func (m *mockParser) Supports(lang string) bool                   { return m.supports[lang] }
func (m *mockParser) Init(ctx context.Context, config map[string]any) error { return nil }
func (m *mockParser) Close() error                               { return nil }
func (m *mockParser) Parse(ctx context.Context, path string) (*parser.IRDocument, error) {
	if m.parseFn != nil {
		return m.parseFn(ctx, path)
	}
	return &parser.IRDocument{Source: m.name, FilePath: path}, nil
}

func TestProcessFile_HashHit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	os.WriteFile(path, []byte("package main\n"), 0644)

	reg := parser.NewRegistry()
	st := store.NewStore("file")
	st.Open(context.Background(), dir)
	defer st.Close()

	s := NewScanner(st, reg, 1)

	// 第一次处理：无适配器，应标记为跳过
	ctx := context.Background()
	if err := s.ProcessFile(ctx, path); err != nil {
		t.Fatalf("ProcessFile: %v", err)
	}

	// 第二次处理：哈希相同，应跳过（即使有适配器也不触发解析）
	parseCalled := false
	reg.Register(&mockParser{
		name:     "test",
		supports: map[string]bool{"go": true},
		parseFn: func(ctx context.Context, path string) (*parser.IRDocument, error) {
			parseCalled = true
			return &parser.IRDocument{FilePath: path}, nil
		},
	})
	if err := s.ProcessFile(ctx, path); err != nil {
		t.Fatalf("ProcessFile (hash hit): %v", err)
	}
	if parseCalled {
		t.Error("parse should not be called when hash matches")
	}
}

func TestProcessFile_HashMiss(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	os.WriteFile(path, []byte("package main\n"), 0644)

	reg := parser.NewRegistry()
	st := store.NewStore("file")
	st.Open(context.Background(), dir)
	defer st.Close()

	parseCalled := false
	reg.Register(&mockParser{
		name:     "test",
		supports: map[string]bool{"go": true},
		parseFn: func(ctx context.Context, path string) (*parser.IRDocument, error) {
			parseCalled = true
			return &parser.IRDocument{FilePath: path, Source: "test"}, nil
		},
	})

	s := NewScanner(st, reg, 1)
	ctx := context.Background()

	// 第一次处理：应触发解析
	if err := s.ProcessFile(ctx, path); err != nil {
		t.Fatalf("ProcessFile: %v", err)
	}
	if !parseCalled {
		t.Error("parse should be called for new file")
	}

	// 修改文件内容
	os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0644)

	parseCalled = false
	if err := s.ProcessFile(ctx, path); err != nil {
		t.Fatalf("ProcessFile (hash miss): %v", err)
	}
	if !parseCalled {
		t.Error("parse should be called for modified file")
	}
}

func TestScanAll(t *testing.T) {
	dir := t.TempDir()

	// 创建测试文件
	files := map[string]string{
		"main.go":     "package main\nfunc main() {}\n",
		"util.go":     "package main\nfunc util() {}\n",
		"helper.go":   "package main\nfunc helper() {}\n",
		"README.md":   "documentation",
	}
	for name, content := range files {
		os.WriteFile(filepath.Join(dir, name), []byte(content), 0644)
	}

	// 创建 .git 目录（应被忽略）
	gitDir := filepath.Join(dir, ".git", "objects")
	os.MkdirAll(gitDir, 0755)
	os.WriteFile(filepath.Join(gitDir, "pack"), []byte("git data"), 0644)

	reg := parser.NewRegistry()
	reg.Register(&mockParser{
		name:     "test",
		supports: map[string]bool{"go": true, "java": true},
	})

	st := store.NewStore("file")
	st.Open(context.Background(), filepath.Join(dir, "data"))
	defer st.Close()

	s := NewScanner(st, reg, 2)
	ctx := context.Background()

	if err := s.ScanAll(ctx, dir); err != nil {
		t.Fatalf("ScanAll: %v", err)
	}

	// 验证 go 文件被扫描（README.md 无适配器，应跳过）
	for name := range files {
		f, _ := st.GetFileByPath(ctx, filepath.Join(dir, name))
		if name == "README.md" {
			if f == nil {
				t.Log("README.md skipped (no adapter)")
			}
		} else {
			if f == nil {
				t.Errorf("%s should be indexed", name)
			}
		}
	}
}

func TestListFiles_IgnoreDirs(t *testing.T) {
	dir := t.TempDir()

	os.MkdirAll(filepath.Join(dir, ".git"), 0755)
	os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0755)
	os.MkdirAll(filepath.Join(dir, "src"), 0755)

	os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref"), 0644)
	os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "index.js"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte(""), 0644)

	files, err := listFiles(dir)
	if err != nil {
		t.Fatalf("listFiles: %v", err)
	}

	for _, f := range files {
		rel, _ := filepath.Rel(dir, f)
		if rel == ".git\\HEAD" || rel == "node_modules\\pkg\\index.js" {
			t.Errorf("ignored dir file should not be listed: %s", rel)
		}
	}

	if len(files) != 1 {
		t.Errorf("expected 1 file, got %d: %v", len(files), files)
	}
}