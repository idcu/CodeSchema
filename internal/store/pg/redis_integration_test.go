//go:build pg && redis

// Redis 真实实例集成测试（T3-2）。
//
// 前提：本地已有 Redis 实例（推荐用 docker-compose：
//   docker compose --profile redis up -d
// 或：
//   docker run -d --name codeschema-redis -p 6379:6379 redis:7-alpine
//
// 需 -tags 'pg redis' 构建（依赖 internal/store/redis 包）。
// 实例不可达时优雅跳过。
package pg

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store/redis"
)

// TestRedisCache_RealInstance 验证真实 Redis 实例上的缓存读写（热点类缓存层）。
func TestRedisCache_RealInstance(t *testing.T) {
	addr := os.Getenv("CODESCHEMA_REDIS_ADDR")
	if addr == "" {
		addr = "redis://localhost:6379/0"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cache, err := redis.NewRedisCacheFromURL(addr)
	if err != nil {
		t.Skipf("Redis 不可达（%v），跳过缓存集成测试", err)
	}
	defer cache.Close()
	if err := cache.HealthCheck(ctx); err != nil {
		t.Skipf("Redis 不可达（%v），跳过缓存集成测试", err)
	}

	// 写入与读取热点类缓存
	cls := &parser.ClassIR{Name: "Svc", FullName: fmt.Sprintf("it.Svc.%d", time.Now().UnixNano()), Type: "CLASS"}
	if err := cache.PutClass(ctx, cls); err != nil {
		t.Fatalf("PutClass: %v", err)
	}
	got, err := cache.GetClass(ctx, cls.FullName)
	if err != nil {
		t.Fatalf("GetClass: %v", err)
	}
	if got == nil || got.Name != "Svc" {
		t.Fatalf("GetClass mismatch: %+v", got)
	}

	// 调用反查索引（caller→callees, callee→callers）
	call := &parser.CallIR{CallerFQN: cls.FullName + ".Run", CalleeFQN: "pkg.Other.Stop"}
	if err := cache.PutCall(ctx, call); err != nil {
		t.Fatalf("PutCall: %v", err)
	}
	callees, err := cache.CalleesOf(ctx, call.CallerFQN)
	if err != nil || len(callees) != 1 || callees[0] != call.CalleeFQN {
		t.Fatalf("CalleesOf: %v (%v)", err, callees)
	}
	callers, err := cache.CallersOf(ctx, call.CalleeFQN)
	if err != nil || len(callers) != 1 || callers[0] != call.CallerFQN {
		t.Fatalf("CallersOf: %v (%v)", err, callers)
	}
	t.Logf("Redis cache OK: class hit + caller/callee reverse index verified")

	// 清理
	_ = cache.Flush(ctx)
}
