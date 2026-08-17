//go:build redis

// Package redis 提供基于 Redis 的热点缓存与调用反查层，用于超大仓场景下的
// 低延迟访问与横向扩展。它不替代主存储（FileStore/SQLite/PG），而是作为：
//   - 热点类/方法缓存（HASH class:<fqn>）
//   - 调用关系反查索引（SET caller:<fqn> / callee:<fqn>）
//   - 文件→类反向索引（SET file:<path>）
//
// 启用步骤（需先解除 go.mod 写锁并联网）：
//
//	go get github.com/redis/go-redis/v9
//	go build -tags redis ./internal/store/redis
package redis

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/redis/go-redis/v9"
)

// RedisCache 基于 Redis 的缓存/反查层。
type RedisCache struct {
	client *redis.Client
}

// NewRedisCache 连接到 Redis（addr 形如 "localhost:6379"）。
func NewRedisCache(addr string) (*RedisCache, error) {
	c := redis.NewClient(&redis.Options{Addr: addr})
	return &RedisCache{client: c}, nil
}

// NewRedisCacheFromURL 从 redis:// URL 创建缓存（使用 go-redis 标准 URL 解析，
// 兼容 storage.kv 配置的 "redis://host:port/db" 形式，如 redis://localhost:6379/0）。
func NewRedisCacheFromURL(rawURL string) (*RedisCache, error) {
	opt, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("redis parse url: %w", err)
	}
	c := redis.NewClient(opt)
	return &RedisCache{client: c}, nil
}

func (c *RedisCache) Close() error { return c.client.Close() }

func (c *RedisCache) HealthCheck(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

// PutClass 缓存一个类（热点），并建立 class:<fqn> → file path 反查索引。
func (c *RedisCache) PutClass(ctx context.Context, class *parser.ClassIR) error {
	b, err := json.Marshal(class)
	if err != nil {
		return err
	}
	if err := c.client.HSet(ctx, "class:"+class.FullName, "ir", b).Err(); err != nil {
		return err
	}
	return nil
}

// PutClassPath 记录类全限定名 → 源文件路径（供 CacheReader.ClassFilePath 反查）。
func (c *RedisCache) PutClassPath(ctx context.Context, fqn, path string) error {
	if fqn == "" || path == "" {
		return nil
	}
	return c.client.Set(ctx, "classpath:"+fqn, path, 0).Err()
}

// ClassPath 返回类全限定名对应的源文件路径；未命中返回 ("", false)。
func (c *RedisCache) ClassPath(ctx context.Context, fqn string) (string, bool) {
	path, err := c.client.Get(ctx, "classpath:"+fqn).Result()
	if err == redis.Nil {
		return "", false
	}
	if err != nil {
		return "", false
	}
	return path, true
}

// PutMethod 缓存一个方法（热点），key 为方法 FQN（ClassFQN + "." + Name）。
func (c *RedisCache) PutMethod(ctx context.Context, method *parser.MethodIR) error {
	b, err := json.Marshal(method)
	if err != nil {
		return err
	}
	return c.client.HSet(ctx, "method:"+methodFQN(method), "ir", b).Err()
}

// PutMethodPath 记录方法 FQN → 源文件路径（供 CacheReader.MethodFilePath 反查）。
func (c *RedisCache) PutMethodPath(ctx context.Context, fqn, path string) error {
	if fqn == "" || path == "" {
		return nil
	}
	return c.client.Set(ctx, "methodpath:"+fqn, path, 0).Err()
}

// GetMethod 读取缓存的方法；未命中返回 (nil, nil)。
func (c *RedisCache) GetMethod(ctx context.Context, fqn string) (*parser.MethodIR, error) {
	b, err := c.client.HGet(ctx, "method:"+fqn, "ir").Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m parser.MethodIR
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// MethodPath 返回方法 FQN 对应的源文件路径；未命中返回 ("", false)。
func (c *RedisCache) MethodPath(ctx context.Context, fqn string) (string, bool) {
	path, err := c.client.Get(ctx, "methodpath:"+fqn).Result()
	if err == redis.Nil {
		return "", false
	}
	if err != nil {
		return "", false
	}
	return path, true
}

// methodFQN 按三后端一致规则合成方法全限定名（ClassFQN + "." + Name）。
func methodFQN(m *parser.MethodIR) string {
	if m.ClassFQN == "" {
		return m.Name
	}
	return m.ClassFQN + "." + m.Name
}

// GetClass 读取缓存的类；未命中返回 (nil, nil)。
func (c *RedisCache) GetClass(ctx context.Context, fqn string) (*parser.ClassIR, error) {
	b, err := c.client.HGet(ctx, "class:"+fqn, "ir").Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cls parser.ClassIR
	if err := json.Unmarshal(b, &cls); err != nil {
		return nil, err
	}
	return &cls, nil
}

// PutCall 写入调用关系反查索引（caller→callees, callee→callers）。
func (c *RedisCache) PutCall(ctx context.Context, call *parser.CallIR) error {
	if err := c.client.SAdd(ctx, "caller:"+call.CallerFQN, call.CalleeFQN).Err(); err != nil {
		return err
	}
	return c.client.SAdd(ctx, "callee:"+call.CalleeFQN, call.CallerFQN).Err()
}

// CalleesOf 返回某方法直接调用的被调者集合。
func (c *RedisCache) CalleesOf(ctx context.Context, fqn string) ([]string, error) {
	return c.client.SMembers(ctx, "caller:"+fqn).Result()
}

// CallersOf 返回某方法的调用者集合（反向索引）。
func (c *RedisCache) CallersOf(ctx context.Context, fqn string) ([]string, error) {
	return c.client.SMembers(ctx, "callee:"+fqn).Result()
}

// PutFileClasses 建立文件→类的反向索引（供按文件快速取类）。
func (c *RedisCache) PutFileClasses(ctx context.Context, path string, fqns []string) error {
	if len(fqns) == 0 {
		return nil
	}
	return c.client.SAdd(ctx, "file:"+path, fqns).Err()
}

// ClassesOfFile 返回文件包含的类 FQN 集合。
func (c *RedisCache) ClassesOfFile(ctx context.Context, path string) ([]string, error) {
	return c.client.SMembers(ctx, "file:"+path).Result()
}

// Flush 清空所有 codeschema 相关键（仅用于测试/重置）。
func (c *RedisCache) Flush(ctx context.Context) error {
	iter := c.client.Scan(ctx, 0, "*", 0).Iterator()
	for iter.Next(ctx) {
		if err := c.client.Del(ctx, iter.Val()).Err(); err != nil {
			return fmt.Errorf("redis flush: %w", err)
		}
	}
	return iter.Err()
}
