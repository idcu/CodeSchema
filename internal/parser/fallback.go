package parser

import (
	"context"
	"fmt"

	"github.com/idcu/codeschema/internal/log"
	"github.com/idcu/codeschema/internal/metrics"
)

// FallbackParser 包装「高精度主适配器 + 兜底适配器」：
// 主适配器（如 LSP）解析失败时自动回退到兜底适配器（如 tree-sitter 正则），
// 保证解析链路永不因高精度路径失败而中断（编排层降级回退链路的落地实现）。
//
// 用法：注册到 Registry 时使用 fallback.Name()（与主适配器同名），
// 这样既有优先级映射（SetPriority）无需改动即可命中。
type FallbackParser struct {
	primary  ParserPlugin
	fallback ParserPlugin
	logger   *log.Logger
}

// NewFallbackParser 创建降级回退包装器。
// primary 为优先尝试的高精度适配器；fallback 为失败时兜底的适配器。
func NewFallbackParser(primary, fallback ParserPlugin) *FallbackParser {
	return &FallbackParser{
		primary:  primary,
		fallback: fallback,
		logger:   log.WithModule("parser.fallback:" + primary.Name()),
	}
}

func init() {
	metrics.RegisterCounter("parser_fallback_total",
		"解析器降级回退次数（高精度适配器失败回退到兜底适配器）", "primary")
}

// Name 返回主适配器名称（保持与优先级映射一致）。
func (f *FallbackParser) Name() string { return f.primary.Name() }

// Supports 委托给主适配器。
func (f *FallbackParser) Supports(lang string) bool { return f.primary.Supports(lang) }

// Init 初始化主适配器；失败时降级到兜底适配器（不视为错误，
// 后续 Parse 全部走兜底路径），并记录可观测指标。
func (f *FallbackParser) Init(ctx context.Context, config map[string]any) error {
	if err := f.primary.Init(ctx, config); err != nil {
		metrics.IncCounter("parser_fallback_total", f.primary.Name())
		f.logger.Warn("primary adapter init failed, degrading to fallback",
			"primary", f.primary.Name(), "fallback", f.fallback.Name(), "error", err.Error())
		// 主适配器初始化失败：尝试用兜底适配器完成初始化
		_ = f.primary.Close()
		f.primary = f.fallback
		if err := f.primary.Init(ctx, config); err != nil {
			return fmt.Errorf("fallback adapter %s init failed: %w", f.fallback.Name(), err)
		}
		return nil
	}
	return nil
}

// Close 关闭当前生效的适配器。
func (f *FallbackParser) Close() error { return f.primary.Close() }

// Parse 优先主适配器解析；失败（含 ErrSourceUnavailable）时自动回退兜底适配器，
// 并在日志中暴露降级原因（不静默丢失信息）。
func (f *FallbackParser) Parse(ctx context.Context, path string) (*IRDocument, error) {
	ir, err := f.primary.Parse(ctx, path)
	if err == nil {
		return ir, nil
	}
	metrics.IncCounter("parser_fallback_total", f.primary.Name())
	f.logger.Warn("primary parse failed, falling back",
		"path", path, "primary", f.primary.Name(),
		"fallback", f.fallback.Name(), "error", err.Error())
	return f.fallback.Parse(ctx, path)
}
