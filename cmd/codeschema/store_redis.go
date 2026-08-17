//go:build redis

package main

import (
	"context"
	"log"

	"github.com/idcu/codeschema/internal/config"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/store/redis"
)

// 将 Redis L2 缓存层接入统一分发：当以 -tags redis 构建时，本文件覆盖
// redisCacheApplier，使 newStore 在基础存储之上叠加 Redis 缓存/反查层。
func init() {
	redisCacheApplier = applyRedisCache
}

// redisCacheStore 在基础 store.Store 之上叠加 Redis L2 缓存/反查层。
// 写路径（UpsertIR/BulkUpsert）落库后 best-effort 把类/调用关系/文件→类索引
// 推入 Redis；读路径经 store.CacheReader 可选接口暴露（按 FQN 查类、调用反查），
// 由上层（service 等）在探测到该接口时优先走缓存，未命中回退主存储。
type redisCacheStore struct {
	store.Store
	cache *redis.RedisCache
}

func applyRedisCache(ctx context.Context, cfg *config.Config, base store.Store) (store.Store, error) {
	if cfg.Storage.KV == "" {
		return base, nil
	}
	cache, err := redis.NewRedisCacheFromURL(cfg.Storage.KV)
	if err != nil {
		log.Printf("[warn] redis 缓存未启用（连接 %s 失败：%v），回退基础存储", cfg.Storage.KV, err)
		return base, nil
	}
	if err := cache.HealthCheck(ctx); err != nil {
		log.Printf("[warn] redis 缓存未启用（健康检测失败：%v），回退基础存储", err)
		_ = cache.Close()
		return base, nil
	}
	return &redisCacheStore{Store: base, cache: cache}, nil
}

func (c *redisCacheStore) UpsertIR(ctx context.Context, ir *parser.IRDocument) error {
	if err := c.Store.UpsertIR(ctx, ir); err != nil {
		return err
	}
	c.populate(ctx, ir)
	return nil
}

func (c *redisCacheStore) BulkUpsert(ctx context.Context, irs []*parser.IRDocument) error {
	if err := c.Store.BulkUpsert(ctx, irs); err != nil {
		return err
	}
	for _, ir := range irs {
		c.populate(ctx, ir)
	}
	return nil
}

// populate 把类、调用关系与文件→类索引 best-effort 推入 Redis；
// 任一错误仅记录，不影响主路径。
func (c *redisCacheStore) populate(ctx context.Context, ir *parser.IRDocument) {
	fqns := make([]string, 0, len(ir.Classes))
	for i := range ir.Classes {
		if err := c.cache.PutClass(ctx, &ir.Classes[i]); err != nil {
			log.Printf("[warn] redis 写入类 %s 失败：%v", ir.Classes[i].FullName, err)
		}
		// 类 FQN → 源文件路径反查索引（service 缓存快速路径依赖）。
		if err := c.cache.PutClassPath(ctx, ir.Classes[i].FullName, ir.FilePath); err != nil {
			log.Printf("[warn] redis 写入类路径索引 %s 失败：%v", ir.Classes[i].FullName, err)
		}
		fqns = append(fqns, ir.Classes[i].FullName)
	}
	if err := c.cache.PutFileClasses(ctx, ir.FilePath, fqns); err != nil {
		log.Printf("[warn] redis 写入文件→类索引 %s 失败：%v", ir.FilePath, err)
	}
	for i := range ir.Calls {
		if err := c.cache.PutCall(ctx, &ir.Calls[i]); err != nil {
			log.Printf("[warn] redis 写入调用 %s→%s 失败：%v", ir.Calls[i].CallerFQN, ir.Calls[i].CalleeFQN, err)
		}
	}
}

// —— store.CacheReader 可选接口实现（读路径接入） ——

var _ store.CacheReader = (*redisCacheStore)(nil)

// DriverName 报告驱动名（store.DriverNamer 可选接口）：
// Redis 缓存层叠加在基础驱动之上，报告基础驱动的名称。
func (c *redisCacheStore) DriverName() string {
	if dn, ok := c.Store.(store.DriverNamer); ok {
		return dn.DriverName()
	}
	return "generic"
}

// GetClass 按全限定名读取缓存的类；未命中返回 (nil, nil)。
func (c *redisCacheStore) GetClass(ctx context.Context, fqn string) (*parser.ClassIR, error) {
	return c.cache.GetClass(ctx, fqn)
}

// ClassFilePath 返回类全限定名对应的源文件路径（Redis 反查索引）。
func (c *redisCacheStore) ClassFilePath(ctx context.Context, fqn string) (string, bool) {
	return c.cache.ClassPath(ctx, fqn)
}

// CallersOf 返回某方法的调用者集合（反向索引）。
func (c *redisCacheStore) CallersOf(ctx context.Context, fqn string) ([]string, error) {
	return c.cache.CallersOf(ctx, fqn)
}

// CalleesOf 返回某方法直接调用的被调者集合。
func (c *redisCacheStore) CalleesOf(ctx context.Context, fqn string) ([]string, error) {
	return c.cache.CalleesOf(ctx, fqn)
}

// ClassesOfFile 返回文件包含的类 FQN 集合。
func (c *redisCacheStore) ClassesOfFile(ctx context.Context, path string) ([]string, error) {
	return c.cache.ClassesOfFile(ctx, path)
}
