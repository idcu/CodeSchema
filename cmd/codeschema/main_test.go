package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idcu/codeschema/internal/config"
)

// TestNewBaseStoreAppliesPoolLimits 负控（R3-11）：storage.max_open_conns /
// max_idle_conns 必须在打开存储后真正作用到 database/sql 连接池，
// 否则高并发下连接数无界（PG 尤甚）。
func TestNewBaseStoreAppliesPoolLimits(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Storage.Driver = "sqlite"
	cfg.Storage.DSN = filepath.Join(t.TempDir(), "pool.db")
	cfg.Storage.MaxOpenConns = 4
	cfg.Storage.MaxIdleConns = 2

	st, err := newBaseStore(context.Background(), cfg, cfg.Storage.DSN)
	if err != nil {
		t.Fatalf("newBaseStore: %v", err)
	}
	defer st.Close()

	pooled, ok := st.(interface{ PoolStats() sql.DBStats })
	if !ok {
		t.Fatalf("存储 %T 未暴露 PoolStats，无法校验连接池上限", st)
	}
	if got := pooled.PoolStats().MaxOpenConnections; got != 4 {
		t.Errorf("MaxOpenConnections = %d, want 4（storage.max_open_conns 未生效）", got)
	}
}

// TestValidateConfigReportsAllErrors P1-18 负控：启动校验必须聚合报告全部问题，
// 且错误信息包含可定位的配置项名（而不是只报第一条或静默放行）。
func TestValidateConfigReportsAllErrors(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Scanner.Workers = -1
	cfg.Storage.Driver = "bogus"

	err := validateConfig(cfg)
	if err == nil {
		t.Fatal("非法配置应返回错误（当前静默放行）")
	}
	for _, want := range []string{"scanner.workers", "storage.driver"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误未包含 %q: %v", want, err)
		}
	}
}

// TestValidateConfigAcceptsDefaults 默认配置必须通过启动校验。
func TestValidateConfigAcceptsDefaults(t *testing.T) {
	if err := validateConfig(config.DefaultConfig()); err != nil {
		t.Fatalf("默认配置不应报错: %v", err)
	}
}
