package fsperm

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMkdirAllTightensExisting R3-7：已存在的 0755 目录必须被收敛到 0700，
// 覆盖旧版本数据迁移场景。
func TestMkdirAllTightensExisting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := MkdirAll(dir); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	assertPerm(t, dir, 0o700)
}

// TestWriteFileTightensExisting 已存在的 0644 文件重写后必须为 0600。
func TestWriteFileTightensExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	assertPerm(t, path, 0o600)
}

// TestOpenFileCreatesParents 自动建父目录 0700，文件 0600。
func TestOpenFileCreatesParents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "f.txt")
	f, err := OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := f.WriteString("x"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	assertPerm(t, filepath.Dir(path), 0o700)
	assertPerm(t, path, 0o600)
}

// TestMkdirAllTightensAllCreatedLevels 复核 P3-2：多级不存在的目录一次创建后，
// 每一级都必须是 0700，而不只是叶子目录。
func TestMkdirAllTightensAllCreatedLevels(t *testing.T) {
	base := t.TempDir()
	// 旧版本可能留下 0755 的父目录；新建的子链路仍必须全部 0700。
	if err := os.Chmod(base, 0o755); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(base, "a", "b", "c")
	if err := MkdirAll(target); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, dir := range []string{target, filepath.Dir(target), filepath.Dir(filepath.Dir(target))} {
		assertPerm(t, dir, 0o700)
	}
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s perm = %04o, want %04o", path, got, want)
	}
}
