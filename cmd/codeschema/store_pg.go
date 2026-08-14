//go:build pg

package main

import (
	"context"
	"fmt"

	"github.com/idcu/codeschema/internal/config"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/store/pg"
)

// 将 PostgreSQL 后端注册进统一分发。仅当以 `-tags pg` 构建时本文件才参与编译，
// 从而既避免默认构建引入 CGO/网络依赖，又能让 pg 后端经 storage.driver=pg|postgres 启用。
func init() {
	registerStoreProvider(storeProvider{
		match: func(driver string) bool {
			return driver == "pg" || driver == "postgres"
		},
		build: func(ctx context.Context, cfg *config.Config, openTarget string) (store.Store, error) {
			s := pg.NewPGStore()
			if err := s.Open(ctx, openTarget); err != nil {
				return nil, fmt.Errorf("open pg store: %w", err)
			}
			return s, nil
		},
	})
}
