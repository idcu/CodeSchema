//go:build redis

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"

	"github.com/idcu/codeschema/internal/config"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/store/redis"
)

// redisNamespace 由存储驱动 + DSN 派生键命名空间：多租户/多实例共用同一
// Redis 时键互不覆盖，Flush 也只影响自身（P1-4）。
func redisNamespace(cfg *config.Config) string {
	sum := sha256.Sum256([]byte(cfg.Storage.Driver + "|" + cfg.Storage.DSN))
	return "codeschema-" + hex.EncodeToString(sum[:6])
}

func newRedisCacheForConfig(cfg *config.Config) (*redis.RedisCache, error) {
	return redis.NewRedisCacheFromURLWithNamespace(cfg.Storage.KV, redisNamespace(cfg))
}

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
	cache, err := newRedisCacheForConfig(cfg)
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

// MarkParseSkipped 转发基础存储的跳过留痕，并失效该文件在 Redis 的旧缓存。
//
// 基础存储未实现 store.SkippedWriter 时回退 UpsertFile，与 Scanner 语义一致。
// 包装器若不显式实现该接口，Scanner 的类型断言会失败并回退 UpsertFile，
// 把跳过文件写成 parse_ok（-tags redis 形态下留痕退化，P2-1）。
func (c *redisCacheStore) MarkParseSkipped(ctx context.Context, path string, byteSize int64, lineCount int) (int64, error) {
	var (
		id  int64
		err error
	)
	if sw, ok := c.Store.(store.SkippedWriter); ok {
		id, err = sw.MarkParseSkipped(ctx, path, byteSize, lineCount)
	} else {
		id, err = c.Store.UpsertFile(ctx, path, "", lineCount, byteSize)
	}
	if err != nil {
		return 0, err
	}
	if c.cache != nil {
		if ferr := c.cache.InvalidateFile(ctx, path); ferr != nil {
			log.Printf("[warn] redis 失效文件缓存 %s 失败：%v", path, ferr)
		}
	}
	return id, nil
}

// DeleteFile 转发基础存储删除文件记录，并失效该文件在 Redis 的派生键
// （类、方法、路径索引、出边）。包装器不覆写时调用会经嵌入接口直达基础存储，
// Redis 中的旧键会继续被读路径命中，把已删除代码当成存在。
func (c *redisCacheStore) DeleteFile(ctx context.Context, fileID int64) error {
	file, err := c.Store.GetFileByID(ctx, fileID)
	if err != nil {
		return err
	}
	if err := c.Store.DeleteFile(ctx, fileID); err != nil {
		return err
	}
	if c.cache == nil || file == nil {
		return nil
	}
	if ferr := c.cache.InvalidateFile(ctx, file.AbsolutePath); ferr != nil {
		log.Printf("[warn] redis 失效文件缓存 %s 失败：%v", file.AbsolutePath, ferr)
	}
	return nil
}

// populate 把当前文件 IR best-effort 同步进 Redis：先失效旧版本键，再写当前
// 类/方法/调用与文件→类索引。任一错误仅记录，不影响主路径。
func (c *redisCacheStore) populate(ctx context.Context, ir *parser.IRDocument) {
	if err := c.cache.SyncIR(ctx, ir); err != nil {
		log.Printf("[warn] redis 同步 %s 缓存失败：%v", ir.FilePath, err)
	}
}

// —— 可选接口实现（读路径/跳过留痕接入） ——

var _ store.CacheReader = (*redisCacheStore)(nil)
var _ store.SkippedWriter = (*redisCacheStore)(nil)

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

// GetMethod 按全限定名读取缓存的方法；未命中返回 (nil, nil)。
func (c *redisCacheStore) GetMethod(ctx context.Context, fqn string) (*parser.MethodIR, error) {
	return c.cache.GetMethod(ctx, fqn)
}

// MethodFilePath 返回方法全限定名对应的源文件路径（Redis 反查索引）。
func (c *redisCacheStore) MethodFilePath(ctx context.Context, fqn string) (string, bool) {
	return c.cache.MethodPath(ctx, fqn)
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

// —— store.FieldConstantStore 透传（2026-09-03 新增）——
// field/constant 表读写仅底层关系型后端（PG）实现，Redis 层不缓存、仅转发；
// 底层未实现该接口时返回错误，由调用方回退。

var _ store.FieldConstantStore = (*redisCacheStore)(nil)

func (c *redisCacheStore) fcs() (store.FieldConstantStore, error) {
	fcs, ok := c.Store.(store.FieldConstantStore)
	if !ok {
		return nil, errors.New("base store does not implement store.FieldConstantStore")
	}
	return fcs, nil
}

func (c *redisCacheStore) UpsertClassFields(ctx context.Context, classID int64, fields []store.FieldRecord) error {
	fcs, err := c.fcs()
	if err != nil {
		return err
	}
	return fcs.UpsertClassFields(ctx, classID, fields)
}

func (c *redisCacheStore) UpsertMethodFields(ctx context.Context, methodID int64, fields []store.FieldRecord) error {
	fcs, err := c.fcs()
	if err != nil {
		return err
	}
	return fcs.UpsertMethodFields(ctx, methodID, fields)
}

func (c *redisCacheStore) GetClassFields(ctx context.Context, classID int64) ([]store.FieldRecord, error) {
	fcs, err := c.fcs()
	if err != nil {
		return nil, err
	}
	return fcs.GetClassFields(ctx, classID)
}

func (c *redisCacheStore) GetMethodFields(ctx context.Context, methodID int64) ([]store.FieldRecord, error) {
	fcs, err := c.fcs()
	if err != nil {
		return nil, err
	}
	return fcs.GetMethodFields(ctx, methodID)
}

func (c *redisCacheStore) UpsertFileConstants(ctx context.Context, fileID int64, constants []store.ConstantRecord) error {
	fcs, err := c.fcs()
	if err != nil {
		return err
	}
	return fcs.UpsertFileConstants(ctx, fileID, constants)
}

func (c *redisCacheStore) UpsertClassConstants(ctx context.Context, classID int64, constants []store.ConstantRecord) error {
	fcs, err := c.fcs()
	if err != nil {
		return err
	}
	return fcs.UpsertClassConstants(ctx, classID, constants)
}

func (c *redisCacheStore) GetFileConstants(ctx context.Context, fileID int64) ([]store.ConstantRecord, error) {
	fcs, err := c.fcs()
	if err != nil {
		return nil, err
	}
	return fcs.GetFileConstants(ctx, fileID)
}

func (c *redisCacheStore) GetClassConstants(ctx context.Context, classID int64) ([]store.ConstantRecord, error) {
	fcs, err := c.fcs()
	if err != nil {
		return nil, err
	}
	return fcs.GetClassConstants(ctx, classID)
}
