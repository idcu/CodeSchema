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
// 读路径全部委托内嵌的基础存储；写路径（UpsertIR/BulkUpsert）落库后 best-effort
// 把类/调用关系推入 Redis，供高频「按 FQN 查类」「调用反查」走内存。
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

// populate 把类与调用关系 best-effort 推入 Redis；任一错误仅记录，不影响主路径。
func (c *redisCacheStore) populate(ctx context.Context, ir *parser.IRDocument) {
	for i := range ir.Classes {
		if err := c.cache.PutClass(ctx, &ir.Classes[i]); err != nil {
			log.Printf("[warn] redis 写入类 %s 失败：%v", ir.Classes[i].FullName, err)
		}
	}
	for i := range ir.Calls {
		if err := c.cache.PutCall(ctx, &ir.Calls[i]); err != nil {
			log.Printf("[warn] redis 写入调用 %s→%s 失败：%v", ir.Calls[i].CallerFQN, ir.Calls[i].CalleeFQN, err)
		}
	}
}
