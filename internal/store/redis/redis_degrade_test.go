//go:build redis

package redis

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/parser"
)

// failingBackend 在内存后端上注入读/写故障，用于验证降级与陈旧窗口。
type failingBackend struct {
	*memBackend
	failReads  bool
	failWrites bool
}

var errBackendDown = errors.New("redis: backend unavailable")

func (f *failingBackend) HGet(ctx context.Context, key, field string) (string, error) {
	if f.failReads {
		return "", errBackendDown
	}
	return f.memBackend.HGet(ctx, key, field)
}

func (f *failingBackend) Get(ctx context.Context, key string) (string, error) {
	if f.failReads {
		return "", errBackendDown
	}
	return f.memBackend.Get(ctx, key)
}

func (f *failingBackend) SMembers(ctx context.Context, key string) ([]string, error) {
	if f.failReads {
		return nil, errBackendDown
	}
	return f.memBackend.SMembers(ctx, key)
}

func (f *failingBackend) HSet(ctx context.Context, key string, values ...any) error {
	if f.failWrites {
		return errBackendDown
	}
	return f.memBackend.HSet(ctx, key, values...)
}

func (f *failingBackend) SAdd(ctx context.Context, key string, members ...any) error {
	if f.failWrites {
		return errBackendDown
	}
	return f.memBackend.SAdd(ctx, key, members...)
}

func (f *failingBackend) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if f.failWrites {
		return errBackendDown
	}
	return f.memBackend.Set(ctx, key, value, ttl)
}

func (f *failingBackend) Del(ctx context.Context, keys ...string) error {
	if f.failWrites {
		return errBackendDown
	}
	return f.memBackend.Del(ctx, keys...)
}

func (f *failingBackend) Expire(ctx context.Context, key string, ttl time.Duration) error {
	if f.failWrites {
		return errBackendDown
	}
	return f.memBackend.Expire(ctx, key, ttl)
}

// TestReadFailureSurfacesError 对抗性用例（场景 4）：
// Redis 读失败必须显式返回错误（供上层回退），不能伪装成"未命中"或旧值。
func TestReadFailureSurfacesError(t *testing.T) {
	ctx := context.Background()
	backend := &failingBackend{memBackend: newMemBackend()}
	c := newRedisCache(backend, "tenant-a")

	ir := &parser.IRDocument{
		FilePath: "/repo/service.go",
		Classes:  []parser.ClassIR{{Name: "Service", FullName: "pkg.Service", Type: "CLASS"}},
		Methods:  []parser.MethodIR{{Name: "Run", ClassFQN: "pkg.Service"}},
	}
	if err := c.SyncIR(ctx, ir); err != nil {
		t.Fatalf("SyncIR: %v", err)
	}

	backend.failReads = true
	if _, err := c.GetClass(ctx, "pkg.Service"); err == nil {
		t.Fatal("GetClass 在后端读失败时应返回错误，否则上层无法降级")
	}
	if _, ok := c.ClassPath(ctx, "pkg.Service"); ok {
		t.Fatal("ClassPath 在后端读失败时应返回未命中")
	}
	if _, err := c.GetMethod(ctx, "pkg.Service.Run"); err == nil {
		t.Fatal("GetMethod 在后端读失败时应返回错误")
	}

	// 后端恢复后数据仍完整（读失败不应破坏缓存内容）。
	backend.failReads = false
	cls, err := c.GetClass(ctx, "pkg.Service")
	if err != nil || cls == nil {
		t.Fatalf("后端恢复后 GetClass 应命中: cls=%v err=%v", cls, err)
	}
}

// TestFailedInvalidationKeepsStaleKeyUntilTTL 对抗性用例（场景 4）：
// 写失败时 SyncIR 必须把错误返回给调用方（不能静默），旧键会保留到 TTL 过期——
// 这是包注释声明的"陈旧窗口"，不是降级成功；此处把窗口形状固化下来，
// 避免后续误以为写失败后缓存自动一致。
func TestFailedInvalidationKeepsStaleKeyUntilTTL(t *testing.T) {
	ctx := context.Background()
	backend := &failingBackend{memBackend: newMemBackend()}
	c := newRedisCache(backend, "tenant-a")

	v1 := &parser.IRDocument{
		FilePath: "/repo/service.go",
		Classes:  []parser.ClassIR{{Name: "Gone", FullName: "pkg.Gone", Type: "CLASS"}},
	}
	if err := c.SyncIR(ctx, v1); err != nil {
		t.Fatalf("SyncIR(v1): %v", err)
	}
	oldKey := c.key("class:", "pkg.Gone")
	if ttl := backend.ttlOf(oldKey); ttl <= 0 {
		t.Fatalf("旧键未设置 TTL，陈旧窗口无上界: %s", oldKey)
	}

	v2 := &parser.IRDocument{
		FilePath: "/repo/service.go",
		Classes:  []parser.ClassIR{{Name: "New", FullName: "pkg.New", Type: "CLASS"}},
	}
	backend.failWrites = true
	if err := c.SyncIR(ctx, v2); err == nil {
		t.Fatal("写失败时 SyncIR 必须返回错误，否则调用方无法感知缓存不一致")
	}
	backend.failWrites = false

	// 失效失败 → 旧类仍可读，新类未写入：窗口内读路径会把过期数据当有效。
	if cls, err := c.GetClass(ctx, "pkg.Gone"); err != nil || cls == nil {
		t.Fatalf("预期窗口内旧键仍可读: cls=%v err=%v", cls, err)
	}
	if cls, err := c.GetClass(ctx, "pkg.New"); err != nil || cls != nil {
		t.Fatalf("写失败不应写入新键: cls=%v err=%v", cls, err)
	}

	// 恢复后重新 SyncIR 应立即收敛（不依赖 TTL）。
	if err := c.SyncIR(ctx, v2); err != nil {
		t.Fatalf("SyncIR(v2 retry): %v", err)
	}
	if cls, _ := c.GetClass(ctx, "pkg.Gone"); cls != nil {
		t.Fatal("重试成功后旧类应被清除")
	}
	if cls, _ := c.GetClass(ctx, "pkg.New"); cls == nil {
		t.Fatal("重试成功后新类应可读")
	}
}
