package main

import (
	"strings"
	"testing"

	"github.com/idcu/codeschema/internal/config"
)

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
