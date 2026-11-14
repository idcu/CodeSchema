//go:build redis

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/store/redis"
)

// TestRedisCacheStore_ReindexInvalidatesRemovedEntities 活实例负控：
// 生产包装层 redisCacheStore 在同一文件重索引后，必须让旧版本的类/方法键失效，
// 不能继续把已删除的实体返回给 service 快路径。
func TestRedisCacheStore_ReindexInvalidatesRemovedEntities(t *testing.T) {
	addr := os.Getenv("CODESCHEMA_REDIS_ADDR")
	if addr == "" {
		t.Skip("set CODESCHEMA_REDIS_ADDR (e.g. 127.0.0.1:6379) to run the live Redis test")
	}
	ns := fmt.Sprintf("it-reindex-%d", time.Now().UnixNano())
	// 兼容 host:port 与 redis:// URL 两种写法（CI 用 URL）。
	var cache *redis.RedisCache
	var err error
	if strings.Contains(addr, "://") {
		cache, err = redis.NewRedisCacheFromURLWithNamespace(addr, ns)
	} else {
		cache, err = redis.NewRedisCacheWithNamespace(addr, ns)
	}
	if err != nil {
		t.Fatalf("connect redis %s: %v", addr, err)
	}
	defer cache.Close()
	ctx := context.Background()
	defer func() { _ = cache.Flush(ctx) }()

	base := store.NewStore("file")
	if err := base.Open(ctx, t.TempDir()); err != nil {
		t.Fatalf("open file store: %v", err)
	}
	defer base.Close()
	wrapped := &redisCacheStore{Store: base, cache: cache}
	path := "/repo/svc.go"

	first := &parser.IRDocument{
		Source: "redis-it", Language: "go", FilePath: path, FileHash: "h1",
		Classes: []parser.ClassIR{{Name: "Old", FullName: "pkg.Old", Type: "CLASS"}},
		Methods: []parser.MethodIR{{Name: "Gone", ClassFQN: "pkg.Old"}},
		Calls:   []parser.CallIR{{CallerFQN: "pkg.Old.Gone", CalleeFQN: "pkg.Target"}},
	}
	if err := wrapped.UpsertIR(ctx, first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if cls, _ := cache.GetClass(ctx, "pkg.Old"); cls == nil {
		t.Fatal("首次写入后缓存未命中 pkg.Old")
	}

	second := &parser.IRDocument{
		Source: "redis-it", Language: "go", FilePath: path, FileHash: "h2",
		Classes: []parser.ClassIR{{Name: "New", FullName: "pkg.New", Type: "CLASS"}},
		Methods: []parser.MethodIR{{Name: "Keep", ClassFQN: "pkg.New"}},
	}
	if err := wrapped.UpsertIR(ctx, second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	if cls, _ := cache.GetClass(ctx, "pkg.Old"); cls != nil {
		t.Errorf("重索引后旧类仍可读: %+v", cls)
	}
	if p, ok := cache.ClassPath(ctx, "pkg.Old"); ok {
		t.Errorf("重索引后旧类路径仍可读: %s", p)
	}
	if m, _ := cache.GetMethod(ctx, "pkg.Old.Gone"); m != nil {
		t.Errorf("重索引后旧方法仍可读: %+v", m)
	}
	fqns, err := cache.ClassesOfFile(ctx, path)
	if err != nil {
		t.Fatalf("ClassesOfFile: %v", err)
	}
	if len(fqns) != 1 || fqns[0] != "pkg.New" {
		t.Errorf("文件→类索引应只剩 pkg.New，实际 %v", fqns)
	}
	if cls, _ := cache.GetClass(ctx, "pkg.New"); cls == nil {
		t.Error("当前类未写入缓存")
	}
}
