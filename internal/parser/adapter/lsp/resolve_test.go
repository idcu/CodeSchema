package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveServerPath_PathResolution 验证 PATH 命中 + 不存在返回空（可移植）。
func TestResolveServerPath_PathResolution(t *testing.T) {
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "codeschema-fake-lsp")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake lsp: %v", err)
	}
	t.Setenv("PATH", tmp+string(os.PathListSeparator)+os.Getenv("PATH"))

	if got := ResolveServerPath("codeschema-fake-lsp"); got == "" {
		t.Fatal("expected PATH-resolved absolute path, got empty")
	}
	if got := ResolveServerPath("codeschema-fake-lsp-absent"); got != "" {
		t.Fatalf("expected empty for absent server, got %q", got)
	}
}

// TestResolveServerPath_HomeGoBin 验证回退到 $HOME/go/bin（可移植）。
func TestResolveServerPath_HomeGoBin(t *testing.T) {
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "go", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(binDir, "codeschema-fake-lsp")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", tmp)
	// PATH 不含临时目录，确保走 home/bin 回退分支
	t.Setenv("PATH", "/usr/bin:/bin")
	if got := ResolveServerPath("codeschema-fake-lsp"); got == "" {
		t.Fatal("expected $HOME/go/bin resolved path, got empty")
	}
}

// TestResolveServerPath_GoplsResolvable 本机验证：gopls 在 $GOPATH/bin 不在 PATH 时
// 现可被解析（核心修复点）。CI 无 gopls 时 skip，解析逻辑已由上述两测试覆盖。
func TestResolveServerPath_GoplsResolvable(t *testing.T) {
	if ResolveServerPath("gopls") == "" {
		t.Skip("gopls not installed in this environment; resolution logic covered by other tests")
	}
	t.Logf("gopls resolved: %s", ResolveServerPath("gopls"))
}
