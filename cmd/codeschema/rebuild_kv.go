//go:build !redis

package main

import (
	"context"
	"fmt"

	"github.com/idcu/codeschema/internal/config"
)

// rebuildKVCmd 默认构建下不可用：Redis KV 缓存层需 -tags redis 构建。
// 用 -tags redis 构建后，本文件被 rebuild_kv_redis.go 覆盖。
func rebuildKVCmd(ctx context.Context, cfg *config.Config, args []string) error {
	return fmt.Errorf("rebuild-kv requires -tags redis build (Redis KV cache layer not compiled in)")
}
