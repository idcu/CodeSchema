package fsperm

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// skipOnWindows Windows 不套用 POSIX 权限位，权限断言直接跳过。
func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("windows 不套用 POSIX 权限位")
	}
}

func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Mode().Perm()
}

func TestMkdirAll_Sets0700(t *testing.T) {
	skipOnWindows(t)
	dir := filepath.Join(t.TempDir(), "a", "b", "idx")
	if err := MkdirAll(dir); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if got := modeOf(t, dir); got != DirMode {
		t.Errorf("leaf dir mode = %v, want %v", got, DirMode)
	}
}

func TestMkdirAll_TightensExistingWideDir(t *testing.T) {
	skipOnWindows(t)
	dir := filepath.Join(t.TempDir(), "idx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("pre-create: %v", err)
	}
	if got := modeOf(t, dir); got != 0o755 {
		t.Fatalf("precondition: mode = %v, want 0755", got)
	}
	if err := MkdirAll(dir); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if got := modeOf(t, dir); got != DirMode {
		t.Errorf("leaf dir mode = %v, want %v (tighten 0755->0700)", got, DirMode)
	}
}

func TestWriteFile_Sets0600AndDir0700(t *testing.T) {
	skipOnWindows(t)
	dir := filepath.Join(t.TempDir(), "idx")
	p := filepath.Join(dir, "vector.json")
	if err := WriteFile(p, []byte(`{}`)); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got := modeOf(t, p); got != FileMode {
		t.Errorf("file mode = %v, want %v", got, FileMode)
	}
	if got := modeOf(t, dir); got != DirMode {
		t.Errorf("parent dir mode = %v, want %v", got, DirMode)
	}
	if data, err := os.ReadFile(p); err != nil || string(data) != `{}` {
		t.Errorf("file content mismatch: data=%q err=%v", data, err)
	}
}

// WriteFile 内部对既有/新建文件都显式 Chmod 0600；含父目录 0700，
// 故 umask 是否放宽都不影响最终权限（已由 TestWriteFile_Sets0600AndDir0700 覆盖）。