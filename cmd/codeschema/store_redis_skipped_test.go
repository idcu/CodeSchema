//go:build redis

package main

import (
	"context"
	"testing"

	"github.com/idcu/codeschema/internal/store"
)

// fakeSkippedStore 只实现 SkippedWriter，其余 store.Store 方法由 nil 嵌入兜底；
// 测试只调用 MarkParseSkipped，不会触及其他方法。
type fakeSkippedStore struct {
	store.Store
	calls int
	path  string
	size  int64
	lines int
}

func (f *fakeSkippedStore) MarkParseSkipped(_ context.Context, path string, byteSize int64, lineCount int) (int64, error) {
	f.calls++
	f.path = path
	f.size = byteSize
	f.lines = lineCount
	return 42, nil
}

// fakePlainStore 不实现 SkippedWriter，仅记录 UpsertFile 回退路径。
type fakePlainStore struct {
	store.Store
	upsertCalls int
}

func (f *fakePlainStore) UpsertFile(_ context.Context, _ string, _ string, _ int, _ int64) (int64, error) {
	f.upsertCalls++
	return 7, nil
}

// TestRedisCacheStore_ImplementsSkippedWriter 负控（P2-1）：
// 包装器必须显式实现 SkippedWriter，否则 Scanner 断言失败会回退 UpsertFile，
// 把跳过文件写成 parse_ok（Redis 形态下退化）。
func TestRedisCacheStore_ImplementsSkippedWriter(t *testing.T) {
	var s store.Store = &redisCacheStore{}
	if _, ok := s.(store.SkippedWriter); !ok {
		t.Fatalf("redisCacheStore 未实现 store.SkippedWriter：跳过留痕会退化为 UpsertFile(parse_ok)")
	}
}

// TestRedisCacheStore_MarkParseSkippedForwards 验证包装器把调用转发给基础存储，
// 并原样返回 ID；cache 为 nil 时不得 panic。
func TestRedisCacheStore_MarkParseSkippedForwards(t *testing.T) {
	base := &fakeSkippedStore{}
	var s store.Store = &redisCacheStore{Store: base}
	sw, ok := s.(store.SkippedWriter)
	if !ok {
		t.Fatal("redisCacheStore 未实现 store.SkippedWriter")
	}
	id, err := sw.MarkParseSkipped(context.Background(), "/repo/big.go", 2048, 300)
	if err != nil {
		t.Fatalf("MarkParseSkipped: %v", err)
	}
	if id != 42 {
		t.Fatalf("id = %d, want 42", id)
	}
	if base.calls != 1 || base.path != "/repo/big.go" || base.size != 2048 || base.lines != 300 {
		t.Fatalf("未转发到基础存储: %+v", base)
	}
}

// TestRedisCacheStore_MarkParseSkippedFallsBack 验证基础存储未实现 SkippedWriter 时，
// 包装器回退 UpsertFile（与 Scanner 原语义一致）。
func TestRedisCacheStore_MarkParseSkippedFallsBack(t *testing.T) {
	base := &fakePlainStore{}
	var s store.Store = &redisCacheStore{Store: base}
	sw, ok := s.(store.SkippedWriter)
	if !ok {
		t.Fatal("redisCacheStore 未实现 store.SkippedWriter")
	}
	if _, err := sw.MarkParseSkipped(context.Background(), "/repo/big.go", 1024, 100); err != nil {
		t.Fatalf("MarkParseSkipped: %v", err)
	}
	if base.upsertCalls != 1 {
		t.Fatalf("upsertCalls = %d, want 1", base.upsertCalls)
	}
}
