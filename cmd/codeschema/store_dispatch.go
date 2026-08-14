package main

import (
	"context"
	"fmt"

	"github.com/idcu/codeschema/internal/config"
	"github.com/idcu/codeschema/internal/store"
	sqlitestore "github.com/idcu/codeschema/internal/store/sqlite"
)

// storeProvider 描述一个"可选后端"的构造器，由 build-tagged 文件通过
// registerStoreProvider 注入（如 pg 需 -tags pg 构建才会注册）。
//
// 之所以把多后端分发放在 cmd 层而非 internal/store.NewStore：sqlite/pg 子包
// 反向 import internal/store（实现其接口），若在 store.NewStore 内再 import
// 这两个子包会形成循环依赖；cmd 包处于依赖图顶端，可安全同时引用 store 及其子包。
type storeProvider struct {
	match func(driver string) bool
	build func(ctx context.Context, cfg *config.Config, openTarget string) (store.Store, error)
}

// storeProviders 由各 build-tagged 文件经 registerStoreProvider 注入。
var storeProviders []storeProvider

func registerStoreProvider(p storeProvider) {
	storeProviders = append(storeProviders, p)
}

// redisCacheApplier 在基础存储之上叠加 Redis L2 缓存层。仅当 -tags redis 构建
// 且 cfg.Storage.KV 非空时生效；默认（无 redis tag 或 KV 为空）原样返回基础存储。
// 由 store_redis.go（//go:build redis）在 init 中覆盖为真实实现。
var redisCacheApplier = func(ctx context.Context, cfg *config.Config, base store.Store) (store.Store, error) {
	return base, nil
}

// newStore 按配置解析存储后端，并按需叠加 Redis L2 缓存层。
//
// openTarget 为存储打开目标：file/sqlite 为目录路径，pg 为连接串；
// cmd 各子命令的 --store 标志默认即 cfg.Storage.DSN，故可直接透传。
func newStore(ctx context.Context, cfg *config.Config, openTarget string) (store.Store, error) {
	base, err := newBaseStore(ctx, cfg, openTarget)
	if err != nil {
		return nil, err
	}
	return redisCacheApplier(ctx, cfg, base)
}

// newBaseStore 按 cfg.Storage.Driver 解析基础存储（file/sqlite/pg...）。
func newBaseStore(ctx context.Context, cfg *config.Config, openTarget string) (store.Store, error) {
	switch cfg.Storage.Driver {
	case "sqlite":
		s := sqlitestore.NewSQLiteStore()
		if err := s.Open(ctx, openTarget); err != nil {
			return nil, fmt.Errorf("open sqlite store: %w", err)
		}
		return s, nil
	case "file", "":
		fs := &store.FileStore{}
		if err := fs.Open(ctx, openTarget); err != nil {
			return nil, fmt.Errorf("open file store: %w", err)
		}
		return fs, nil
	}

	// 可选后端（pg 等，由 build-tagged 文件注入）
	for _, p := range storeProviders {
		if p.match(cfg.Storage.Driver) {
			return p.build(ctx, cfg, openTarget)
		}
	}

	return nil, fmt.Errorf("storage.driver %q 不受支持（可选后端需对应 -tags 构建，例如 pg 需 -tags pg；允许值：file/sqlite/pg/postgres）", cfg.Storage.Driver)
}
