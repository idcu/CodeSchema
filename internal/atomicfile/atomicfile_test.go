package atomicfile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteReplacesAtomically 验证成功路径：写入新内容、清理临时文件。
func TestWriteReplacesAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "data.json")
	if err := Write(path, func(w io.Writer) error {
		_, err := io.WriteString(w, `{"v":1}`)
		return err
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != `{"v":1}` {
		t.Fatalf("target content = %q", got)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("临时文件应被删除，stat err = %v", err)
	}
}

// TestWriteFailureKeepsPreviousVersion 负控（中断不产生半份文件）：
// 回调写出一半后返回错误，目标文件必须仍是上一完整版本，且不留临时文件。
func TestWriteFailureKeepsPreviousVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	const old = `{"v":"old","complete":true}`
	if err := Write(path, func(w io.Writer) error {
		_, err := io.WriteString(w, old)
		return err
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	errBoom := errors.New("boom")
	err := Write(path, func(w io.Writer) error {
		_, _ = io.WriteString(w, `{"v":"new",`) // 半份 JSON
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Write error = %v, want boom", err)
	}
	got, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("read target: %v", rerr)
	}
	if string(got) != old {
		t.Fatalf("目标文件被半份写入污染：%q", got)
	}
	if _, serr := os.Stat(path + ".tmp"); !os.IsNotExist(serr) {
		t.Fatalf("失败后临时文件应被删除，stat err = %v", serr)
	}
}

// TestWriteOverwritesStaleTemp 崩溃后残留的旧临时文件不得影响下一次成功写入。
func TestWriteOverwritesStaleTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	if err := os.WriteFile(path, []byte(`{"v":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".tmp", []byte(`{"half":`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Write(path, func(w io.Writer) error {
		_, err := io.WriteString(w, `{"v":"new"}`)
		return err
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `"new"`) {
		t.Fatalf("target content = %q", got)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("临时文件应被删除，stat err = %v", err)
	}
}
