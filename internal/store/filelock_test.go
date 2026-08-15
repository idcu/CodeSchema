package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestFileStore_ProcessLock 验证进程锁：同目录二次 Open 应失败（互斥）。
func TestFileStore_ProcessLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	fs1 := &FileStore{}
	if err := fs1.Open(context.Background(), dir); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	defer fs1.Close()

	// 第二次 Open 同一目录（同进程不同实例）应获取锁失败
	fs2 := &FileStore{}
	if err := fs2.Open(context.Background(), dir); err == nil {
		fs2.Close()
		t.Fatal("second Open should fail (lock held)")
	} else {
		t.Logf("second Open correctly failed: %v", err)
	}

	// Close 后释放锁，再次 Open 应成功
	if err := fs1.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	fs3 := &FileStore{}
	if err := fs3.Open(context.Background(), dir); err != nil {
		t.Fatalf("Open after release should succeed: %v", err)
	}
	fs3.Close()
}
