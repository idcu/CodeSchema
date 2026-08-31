package lock

import "testing"

func TestAcquireRelease(t *testing.T) {
	dir := t.TempDir()
	l, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	// 释放后可再次获取
	l2, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	if err := l2.Release(); err != nil {
		t.Fatalf("Release 2: %v", err)
	}
}

func TestAcquireEmptyDir(t *testing.T) {
	if _, err := Acquire(""); err == nil {
		t.Fatal("expected error for empty dir")
	}
}
