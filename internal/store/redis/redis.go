//go:build redis

// Package redis 提供基于 Redis 的热点缓存与调用反查层，用于超大仓场景下的
// 低延迟访问与横向扩展。它不替代主存储（FileStore/SQLite/PG），而是作为：
//   - 热点类/方法缓存（HASH class:<fqn>）
//   - 调用关系反查索引（SET caller:<fqn> / callee:<fqn>）
//   - 文件→类反向索引（SET file:<path>）
//
// 所有键都带命名空间前缀（默认 codeschema:）并设置 TTL，支持多租户/多实例
// 共用同一 Redis；Flush 只清理本命名空间（P1-4）。
//
// 启用步骤（需先解除 go.mod 写锁并联网）：
//
//	go get github.com/redis/go-redis/v9
//	go build -tags redis ./internal/store/redis
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/redis/go-redis/v9"
)

const (
	// defaultRedisNamespace 未指定命名空间时的前缀（永不使用裸键）。
	defaultRedisNamespace = "codeschema"
	// defaultRedisTTL 缓存条目的默认存活时间：索引可重建，过期即可自愈，
	// 避免删除/重建后残留键永久占用内存或返回陈旧数据（P1-4/P1-9）。
	defaultRedisTTL = 24 * time.Hour
)

// errKeyNotFound 统一表示键不存在（屏蔽具体驱动的 Nil 错误）。
var errKeyNotFound = errors.New("redis: key not found")

// kvBackend 抽象缓存所需的 Redis 命令，便于用内存实现做确定性测试。
type kvBackend interface {
	HSet(ctx context.Context, key string, values ...any) error
	HGet(ctx context.Context, key, field string) (string, error)
	SAdd(ctx context.Context, key string, members ...any) error
	SMembers(ctx context.Context, key string) ([]string, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	Get(ctx context.Context, key string) (string, error)
	Del(ctx context.Context, keys ...string) error
	Scan(ctx context.Context, cursor uint64, match string, count int64) ([]string, uint64, error)
	Expire(ctx context.Context, key string, ttl time.Duration) error
	Ping(ctx context.Context) error
	Close() error
}

// goRedisBackend 用 go-redis 实现 kvBackend，并把 redis.Nil 归一为 errKeyNotFound。
type goRedisBackend struct {
	client *redis.Client
}

func (b *goRedisBackend) HSet(ctx context.Context, key string, values ...any) error {
	return b.client.HSet(ctx, key, values...).Err()
}

func (b *goRedisBackend) HGet(ctx context.Context, key, field string) (string, error) {
	v, err := b.client.HGet(ctx, key, field).Result()
	if err == redis.Nil {
		return "", errKeyNotFound
	}
	return v, err
}

func (b *goRedisBackend) SAdd(ctx context.Context, key string, members ...any) error {
	return b.client.SAdd(ctx, key, members...).Err()
}

func (b *goRedisBackend) SMembers(ctx context.Context, key string) ([]string, error) {
	return b.client.SMembers(ctx, key).Result()
}

func (b *goRedisBackend) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return b.client.Set(ctx, key, value, ttl).Err()
}

func (b *goRedisBackend) Get(ctx context.Context, key string) (string, error) {
	v, err := b.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", errKeyNotFound
	}
	return v, err
}

func (b *goRedisBackend) Del(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return b.client.Del(ctx, keys...).Err()
}

func (b *goRedisBackend) Scan(ctx context.Context, cursor uint64, match string, count int64) ([]string, uint64, error) {
	return b.client.Scan(ctx, cursor, match, count).Result()
}

func (b *goRedisBackend) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return b.client.Expire(ctx, key, ttl).Err()
}

func (b *goRedisBackend) Ping(ctx context.Context) error { return b.client.Ping(ctx).Err() }

func (b *goRedisBackend) Close() error { return b.client.Close() }

// RedisCache 基于 Redis 的缓存/反查层。
type RedisCache struct {
	backend kvBackend
	prefix  string
	ttl     time.Duration
}

// NewRedisCache 连接到 Redis（addr 形如 "localhost:6379"）。
func NewRedisCache(addr string) (*RedisCache, error) {
	return NewRedisCacheWithNamespace(addr, "")
}

// NewRedisCacheWithNamespace 连接到 Redis 并使用给定命名空间隔离键。
//
// 多租户/多实例共用同一 Redis 时，必须使用不同命名空间，否则同名 FQN 会
// 互相覆盖，Flush 也会清掉其他应用的数据（P1-4）。空命名空间回退默认值。
func NewRedisCacheWithNamespace(addr, namespace string) (*RedisCache, error) {
	client := redis.NewClient(&redis.Options{Addr: addr})
	return newRedisCache(&goRedisBackend{client: client}, namespace), nil
}

// NewRedisCacheFromURL 从 redis:// URL 创建缓存（使用 go-redis 标准 URL 解析，
// 兼容 storage.kv 配置的 "redis://host:port/db" 形式，如 redis://localhost:6379/0）。
func NewRedisCacheFromURL(rawURL string) (*RedisCache, error) {
	return NewRedisCacheFromURLWithNamespace(rawURL, "")
}

// NewRedisCacheFromURLWithNamespace 同 NewRedisCacheWithNamespace，但使用 URL。
func NewRedisCacheFromURLWithNamespace(rawURL, namespace string) (*RedisCache, error) {
	opt, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("redis parse url: %w", err)
	}
	client := redis.NewClient(opt)
	return newRedisCache(&goRedisBackend{client: client}, namespace), nil
}

// newRedisCache 组装缓存实例并规范化命名空间。
func newRedisCache(backend kvBackend, namespace string) *RedisCache {
	return &RedisCache{backend: backend, prefix: normalizeNamespace(namespace), ttl: defaultRedisTTL}
}

// normalizeNamespace 返回带尾部冒号的键前缀；空值回退默认命名空间。
func normalizeNamespace(namespace string) string {
	namespace = strings.Trim(namespace, ": ")
	if namespace == "" {
		namespace = defaultRedisNamespace
	}
	return namespace + ":"
}

// key 拼接带命名空间的键，保证多租户/多实例互不干扰。
func (c *RedisCache) key(parts ...string) string {
	return c.prefix + strings.Join(parts, "")
}

// touch 为键刷新 TTL；ttl<=0 表示不设过期（仅测试用）。
func (c *RedisCache) touch(ctx context.Context, key string) error {
	if c.ttl <= 0 {
		return nil
	}
	return c.backend.Expire(ctx, key, c.ttl)
}

func (c *RedisCache) Close() error { return c.backend.Close() }

func (c *RedisCache) HealthCheck(ctx context.Context) error { return c.backend.Ping(ctx) }

// PutClass 缓存一个类（热点），并建立 class:<fqn> → file path 反查索引。
func (c *RedisCache) PutClass(ctx context.Context, class *parser.ClassIR) error {
	b, err := json.Marshal(class)
	if err != nil {
		return err
	}
	key := c.key("class:", class.FullName)
	if err := c.backend.HSet(ctx, key, "ir", b); err != nil {
		return err
	}
	return c.touch(ctx, key)
}

// PutClassPath 记录类全限定名 → 源文件路径（供 CacheReader.ClassFilePath 反查）。
func (c *RedisCache) PutClassPath(ctx context.Context, fqn, path string) error {
	if fqn == "" || path == "" {
		return nil
	}
	return c.backend.Set(ctx, c.key("classpath:", fqn), path, c.ttl)
}

// ClassPath 返回类全限定名对应的源文件路径；未命中返回 ("", false)。
func (c *RedisCache) ClassPath(ctx context.Context, fqn string) (string, bool) {
	path, err := c.backend.Get(ctx, c.key("classpath:", fqn))
	if errors.Is(err, errKeyNotFound) {
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
	key := c.key("method:", methodFQN(method))
	if err := c.backend.HSet(ctx, key, "ir", b); err != nil {
		return err
	}
	return c.touch(ctx, key)
}

// PutMethodPath 记录方法 FQN → 源文件路径（供 CacheReader.MethodFilePath 反查）。
func (c *RedisCache) PutMethodPath(ctx context.Context, fqn, path string) error {
	if fqn == "" || path == "" {
		return nil
	}
	return c.backend.Set(ctx, c.key("methodpath:", fqn), path, c.ttl)
}

// GetMethod 读取缓存的方法；未命中返回 (nil, nil)。
func (c *RedisCache) GetMethod(ctx context.Context, fqn string) (*parser.MethodIR, error) {
	b, err := c.backend.HGet(ctx, c.key("method:", fqn), "ir")
	if errors.Is(err, errKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m parser.MethodIR
	if err := json.Unmarshal([]byte(b), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// MethodPath 返回方法 FQN 对应的源文件路径；未命中返回 ("", false)。
func (c *RedisCache) MethodPath(ctx context.Context, fqn string) (string, bool) {
	path, err := c.backend.Get(ctx, c.key("methodpath:", fqn))
	if errors.Is(err, errKeyNotFound) {
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
	b, err := c.backend.HGet(ctx, c.key("class:", fqn), "ir")
	if errors.Is(err, errKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cls parser.ClassIR
	if err := json.Unmarshal([]byte(b), &cls); err != nil {
		return nil, err
	}
	return &cls, nil
}

// PutCall 写入调用关系反查索引（caller→callees, callee→callers）。
func (c *RedisCache) PutCall(ctx context.Context, call *parser.CallIR) error {
	callerKey := c.key("caller:", call.CallerFQN)
	calleeKey := c.key("callee:", call.CalleeFQN)
	if err := c.backend.SAdd(ctx, callerKey, call.CalleeFQN); err != nil {
		return err
	}
	if err := c.backend.SAdd(ctx, calleeKey, call.CallerFQN); err != nil {
		return err
	}
	if err := c.touch(ctx, callerKey); err != nil {
		return err
	}
	return c.touch(ctx, calleeKey)
}

// CalleesOf 返回某方法直接调用的被调者集合。
func (c *RedisCache) CalleesOf(ctx context.Context, fqn string) ([]string, error) {
	return c.backend.SMembers(ctx, c.key("caller:", fqn))
}

// CallersOf 返回某方法的调用者集合（反向索引）。
func (c *RedisCache) CallersOf(ctx context.Context, fqn string) ([]string, error) {
	return c.backend.SMembers(ctx, c.key("callee:", fqn))
}

// PutFileClasses 建立文件→类的反向索引（供按文件快速取类）。
func (c *RedisCache) PutFileClasses(ctx context.Context, path string, fqns []string) error {
	if len(fqns) == 0 {
		return nil
	}
	members := make([]any, len(fqns))
	for i, f := range fqns {
		members[i] = f
	}
	key := c.key("file:", path)
	if err := c.backend.SAdd(ctx, key, members...); err != nil {
		return err
	}
	return c.touch(ctx, key)
}

// ClassesOfFile 返回文件包含的类 FQN 集合。
func (c *RedisCache) ClassesOfFile(ctx context.Context, path string) ([]string, error) {
	return c.backend.SMembers(ctx, c.key("file:", path))
}

// Flush 清空本命名空间下的所有键（仅用于测试/重置）。
//
// 只按 prefix+"*" 扫描删除，绝不使用 "*" 清库：同一 Redis 可能被其他应用 /
// 其他租户共用（P1-4）。
func (c *RedisCache) Flush(ctx context.Context) error {
	var cursor uint64
	for {
		keys, next, err := c.backend.Scan(ctx, cursor, c.prefix+"*", 100)
		if err != nil {
			return fmt.Errorf("redis flush scan: %w", err)
		}
		if err := c.backend.Del(ctx, keys...); err != nil {
			return fmt.Errorf("redis flush del: %w", err)
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}
