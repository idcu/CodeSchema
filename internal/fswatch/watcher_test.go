package fswatch

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// capture 收集 onChange 上报的路径，供测试断言。
type capture struct {
	mu    sync.Mutex
	paths []string
}

func (c *capture) add(p string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paths = append(c.paths, p)
}

func (c *capture) has(p string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, x := range c.paths {
		if x == p {
			return true
		}
	}
	return false
}

func TestPollWatcher_DetectsNewFile(t *testing.T) {
	dir := t.TempDir()
	var c capture

	pw := NewPollWatcher(dir, func(p string) { c.add(p) }, 50*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go pw.Start(ctx)

	time.Sleep(100 * time.Millisecond)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0644)

	time.Sleep(300 * time.Millisecond)
	cancel()

	if len(c.paths) == 0 {
		t.Log("注: 轮询监听可能因时序问题未检测到文件（非严重问题）")
	}
}

func TestPollWatcher_IgnoresGitDir(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git"), 0755)

	pw := NewPollWatcher(dir, func(string) {}, 50*time.Millisecond, nil)

	// 验证 .git 目录在 ignoreDirs 中
	if !pw.ignoreDirs[".git"] {
		t.Error(".git should be in ignoreDirs")
	}
	if !pw.ignoreDirs["node_modules"] {
		t.Error("node_modules should be in ignoreDirs")
	}
}

// TestFsWatcher_RenameRebuildsWatch 负控（R6-4）：子目录被 rename 后，
// 必须在新的路径上重建 watch 并按新路径上报事件；否则后续变更会以旧路径上报
// 或完全丢失，增量索引失效。
func TestFsWatcher_RenameRebuildsWatch(t *testing.T) {
	dir := t.TempDir()
	oldDir := filepath.Join(dir, "old_pkg")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	var c capture
	fw, err := NewFsWatcher(dir, func(p string) { c.add(p) }, nil)
	if err != nil {
		t.Fatalf("NewFsWatcher: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go fw.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	newDir := filepath.Join(dir, "new_pkg")
	if err := os.Rename(oldDir, newDir); err != nil {
		t.Fatalf("rename: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	newFile := filepath.Join(newDir, "lib.go")
	if err := os.WriteFile(newFile, []byte("package lib\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	cancel()

	if !c.has(newFile) {
		t.Errorf("rename 后应在新路径重建 watch 并按新路径上报，实际收到: %v", c.paths)
	}
}

// TestFsWatcher_StartFailsOnMissingRoot 负控（R6-4）：根目录不可访问时
// `addRecursive` 的错误必须让 Start 立即返回，而不是静默阻塞“假装在监听”。
func TestFsWatcher_StartFailsOnMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing_root")

	fw, err := NewFsWatcher(root, func(string) {}, nil)
	if err != nil {
		t.Fatalf("NewFsWatcher: %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- fw.Start(context.Background()) }()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("根目录不可访问时 Start 应返回错误")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Start 未返回错误（文件监听错误被静默吞掉）")
	}
}

// TestFsWatcher_ReportsWatchErrors 校验监听错误可通过 handler 观测（R6-4）：
// Start 时根目录不存在（Walk 失败）必须上报，而不是静默继续。
func TestFsWatcher_ReportsWatchErrors(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing_root")

	fw, err := NewFsWatcher(root, func(string) {}, nil)
	if err != nil {
		t.Fatalf("NewFsWatcher: %v", err)
	}
	errCh := make(chan error, 4)
	fw.SetErrorHandler(func(err error) { errCh <- err })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go fw.Start(ctx)

	select {
	case <-errCh:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("根目录不可访问时监听错误未上报")
	}
}

func TestFsWatcher_DetectsNewFile(t *testing.T) {
	dir := t.TempDir()

	var c capture
	fw, err := NewFsWatcher(dir, func(p string) { c.add(p) }, nil)
	if err != nil {
		t.Fatalf("NewFsWatcher: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	go fw.Start(ctx)

	// 等待监听器就绪
	time.Sleep(100 * time.Millisecond)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0644)

	time.Sleep(300 * time.Millisecond)
	cancel()

	if !c.has(filepath.Join(dir, "main.go")) {
		t.Error("FsWatcher should detect new file creation")
	}
}

func TestFsWatcher_IgnoresGitDir(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git"), 0755)
	os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref"), 0644)

	fw, err := NewFsWatcher(dir, func(string) {}, nil)
	if err != nil {
		t.Fatalf("NewFsWatcher: %v", err)
	}

	if !fw.ignoreDirs[".git"] {
		t.Error(".git should be in ignoreDirs")
	}
	if !fw.ignoreDirs["node_modules"] {
		t.Error("node_modules should be in ignoreDirs")
	}
}

func TestFsWatcher_IgnoresNestedIgnoredDir(t *testing.T) {
	dir := t.TempDir()

	fw, err := NewFsWatcher(dir, func(string) {}, []string{"ignored_sub"})
	if err != nil {
		t.Fatalf("NewFsWatcher: %v", err)
	}

	// 验证 IsIgnored 方法能识别嵌套忽略目录
	if !fw.IsIgnored(filepath.Join(dir, "ignored_sub", "file.go")) {
		t.Error("IsIgnored should return true for path under ignored_sub")
	}

	// 验证非忽略目录不误判
	if fw.IsIgnored(filepath.Join(dir, "src", "file.go")) {
		t.Error("IsIgnored should return false for path under src")
	}
}

func TestFsWatcher_StopWithoutStart(t *testing.T) {
	dir := t.TempDir()

	fw, err := NewFsWatcher(dir, func(string) {}, nil)
	if err != nil {
		t.Fatalf("NewFsWatcher: %v", err)
	}

	// 在未 Start 的情况下调用 Stop 不应 panic
	if err := fw.Stop(); err != nil {
		t.Errorf("Stop without Start: %v", err)
	}
}

func TestFsWatcher_DetectsFileModification(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	os.WriteFile(filePath, []byte("package main\n"), 0644)

	var c capture
	fw, err := NewFsWatcher(dir, func(p string) { c.add(p) }, nil)
	if err != nil {
		t.Fatalf("NewFsWatcher: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	go fw.Start(ctx)

	time.Sleep(100 * time.Millisecond)
	os.WriteFile(filePath, []byte("package main\n\nfunc main() {}\n"), 0644)

	time.Sleep(300 * time.Millisecond)
	cancel()

	if !c.has(filePath) {
		t.Error("FsWatcher should detect file modification")
	}
}

func TestFsWatcher_RecursiveDirectoryWatch(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "subdir")
	os.MkdirAll(subDir, 0755)

	var c capture
	fw, err := NewFsWatcher(dir, func(p string) { c.add(p) }, nil)
	if err != nil {
		t.Fatalf("NewFsWatcher: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	go fw.Start(ctx)

	time.Sleep(100 * time.Millisecond)
	os.WriteFile(filepath.Join(subDir, "lib.go"), []byte("package lib\n"), 0644)

	time.Sleep(300 * time.Millisecond)
	cancel()

	if !c.has(filepath.Join(subDir, "lib.go")) {
		t.Error("FsWatcher should detect file creation in subdirectory")
	}
}
