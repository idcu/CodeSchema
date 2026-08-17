// Package contextsdk 是 context-sdk 的对外发布契约与权威编排实现（B 级生态资产）。
//
// 定位：独立发布 `github.com/idcu/codeschema-contextsdk`。本包**自包含**（仅依赖
// 标准库），对外暴露 SDKProvider 最小契约 + 公开 DTO + 编排（Compose）实现。任何
// 服务端（codeschema 后端 / 第三方）实现 SDKProvider 即可被编排，无需依赖本仓库
// 其他包，从而满足独立编译、独立测试、独立发布。
//
// 仓库内集成：internal/contextsdk 通过 ServiceProvider 适配器把 internal/service.Service
// 桥接为 SDKProvider，供真实后端使用（见 internal/contextsdk/sdk.go）。
//
// 发布评估与路线图见 README.md。
package contextsdk

import (
	"context"
	"fmt"
)

// ContextMode 上下文注入模式：控制"喂给 Agent 的内容形态"。
type ContextMode string

const (
	// ModeFull 注入真实源码原文（默认），可配 ContextLines 裁剪。
	ModeFull ContextMode = "full"
	// ModeMinimal 仅注入符号元数据（签名/文档/行列区间），不喂源码原文。
	ModeMinimal ContextMode = "minimal"
)

// ContextOptions 上下文注入选项（SDK 公开 DTO，与 internal/service.ContextOptions 对齐）。
type ContextOptions struct {
	// ContextLines 上下文裁剪行数（mode=full 生效）：
	//   0：注入符号完整内容（方法/类全量，不裁剪）；
	//   N>0：注入符号体并前后各附带 N 行上下文（夹在文件边界内）。
	ContextLines int
	// Mode 注入模式：ModeFull（默认）或 ModeMinimal。
	Mode ContextMode
	// IncludeTrace 是否在响应中附加 _trace 追溯字段（默认 true）。
	IncludeTrace bool
}

// TraceEntry 单次注入的追溯日志（仅供调试/审计，不公开；SDK 公开 DTO）。
type TraceEntry struct {
	Source        string `json:"source"`         // 来源：如 "store.GetContext" / "store.GetImpact"
	HitSymbols    int    `json:"hit_symbols"`    // 命中符号数
	HitLines      int    `json:"hit_lines"`      // 命中行数（实际注入行数）
	TrimReason    string `json:"trim_reason"`    // 裁剪原因：如 "context_lines" / "mode_minimal" / "full" / "file_unreadable"
	TrimmedLines  int    `json:"trimmed_lines"`  // 裁剪行数（文件总行 - 注入行）
	TokenEstimate int    `json:"token_estimate"` // 估算 token 数（≈ 注入行数 × 4）
	Timestamp     string `json:"timestamp"`      // ISO 8601 时间戳
}

// SymbolContext 单个符号的上下文注入结果（SDK 公开 DTO，与 internal/service.SymbolContext 对齐）。
type SymbolContext struct {
	Symbol       string      `json:"symbol"` // 方法/类全限定名
	Source       string      `json:"source"` // 注入的源码原文（minimal 模式为行区间摘要）
	FilePath     string      `json:"file_path"`
	StartLine    int         `json:"start_line"` // 命中起始行（1-based）
	EndLine      int         `json:"end_line"`   // 命中结束行
	Doc          string      `json:"doc,omitempty"`
	RelatedTests []string    `json:"related_tests,omitempty"` // 关联单测（五策略，min_confidence=60）
	Trace        *TraceEntry `json:"_trace,omitempty"`        // 追溯日志（仅供调试/审计，不公开）
}

// ImpactNode 影响面中的一个节点。
type ImpactNode struct {
	Method       string   `json:"method"` // 方法全限定名
	Depth        int      `json:"depth"`  // 距被分析方法的层数
	RelatedTests []string `json:"related_tests,omitempty"`
}

// ImpactResult 影响面分析响应（SDK 公开 DTO）。
type ImpactResult struct {
	Method  string       `json:"method"`
	Callers []ImpactNode `json:"callers"`
	Callees []ImpactNode `json:"callees"`
	Trace   *TraceEntry  `json:"_trace,omitempty"` // 追溯日志
}

// SDKProvider 是 context-sdk 对外依赖的最小契约：服务端需提供上下文注入与
// 影响面查询两个能力。任何实现（codeschema 后端 / 第三方）都可被编排。
type SDKProvider interface {
	// GetContextMode 注入单个符号的上下文（mode=full 注入源码原文 / minimal 仅元数据）。
	GetContextMode(ctx context.Context, symbol string, opts ContextOptions) (*SymbolContext, error)

	// GetImpact 查询影响面；depth<=0 使用默认深度 1。
	GetImpact(ctx context.Context, method string, depth int) (*ImpactResult, error)
}

// Resolver 按租户 ID 解析出 SDKProvider（空租户 = 默认租户）。
type Resolver func(tenant string) (SDKProvider, error)

// Request 一次上下文编排请求（SDK 公开 DTO，与 internal/contextsdk.Request 对齐）。
type Request struct {
	Tenant       string   // 目标租户（空 = 默认租户）
	Symbols      []string // 待注入符号全限定名
	ContextLines int      // 上下文裁剪行数（0 = 符号完整内容）
	Mode         ContextMode
	WithImpact   bool // 是否附带影响面
	ImpactDepth  int  // 影响面深度（默认 1）
	WithTests    bool // 是否附带关联单测
	IncludeTrace bool // 是否附带 _trace 追溯（默认 false，显式开启）
}

// SymbolBlock 单个符号的注入块。
type SymbolBlock struct {
	Symbol       string      `json:"symbol"`
	Source       string      `json:"source"`
	FilePath     string      `json:"file_path,omitempty"`
	StartLine    int         `json:"start_line,omitempty"`
	EndLine      int         `json:"end_line,omitempty"`
	RelatedTests []string    `json:"related_tests,omitempty"`
	Trace        *TraceEntry `json:"_trace,omitempty"`
}

// ImpactBlock 单个符号的影响面摘要。
type ImpactBlock struct {
	Symbol  string      `json:"symbol"`
	Callers []string    `json:"callers"`
	Callees []string    `json:"callees"`
	Tests   []string    `json:"tests,omitempty"`
	Trace   *TraceEntry `json:"_trace,omitempty"`
}

// Summary 编排汇总（token 估算等评测口径）。
type Summary struct {
	Tenant        string   `json:"tenant"`
	SymbolCount   int      `json:"symbol_count"`
	TotalLines    int      `json:"total_lines"`
	TokenEstimate int      `json:"token_estimate"`
	TrimReasons   []string `json:"trim_reasons,omitempty"`
}

// Package 编排出的上下文包。
type Package struct {
	Tenant  string        `json:"tenant"`
	Symbols []SymbolBlock `json:"symbols"`
	Impacts []ImpactBlock `json:"impacts,omitempty"`
	Summary Summary       `json:"summary"`
}

// Client 上下文编排客户端（权威编排实现，仅依赖 SDKProvider 接口）。
type Client struct {
	resolve Resolver
}

// NewClient 创建上下文编排客户端。resolve 必须非 nil，否则 Compose 返回错误。
func NewClient(resolve Resolver) *Client {
	return &Client{resolve: resolve}
}

// Compose 编排一次上下文包：按租户解析 SDKProvider，逐个符号注入上下文，
// 可选附带影响面与关联单测，并汇总 token 估算。
func (c *Client) Compose(ctx context.Context, req Request) (*Package, error) {
	if c == nil || c.resolve == nil {
		return nil, fmt.Errorf("contextsdk: resolver is nil")
	}
	if req.Mode == "" {
		req.Mode = ModeFull
	}
	if req.ImpactDepth <= 0 {
		req.ImpactDepth = 1
	}
	includeTrace := req.IncludeTrace
	if len(req.Symbols) == 0 {
		return nil, fmt.Errorf("contextsdk: symbols is empty")
	}

	prov, err := c.resolve(req.Tenant)
	if err != nil {
		return nil, fmt.Errorf("contextsdk: resolve tenant %q: %w", req.Tenant, err)
	}

	pkg := &Package{
		Tenant:  req.Tenant,
		Symbols: make([]SymbolBlock, 0, len(req.Symbols)),
		Summary: Summary{Tenant: req.Tenant},
	}

	for _, sym := range req.Symbols {
		// 1) 上下文注入
		block, err := prov.GetContextMode(ctx, sym, ContextOptions{
			ContextLines: req.ContextLines,
			Mode:         req.Mode,
			IncludeTrace: includeTrace,
		})
		if err != nil {
			return nil, fmt.Errorf("contextsdk: context %q: %w", sym, err)
		}
		sb := SymbolBlock{
			Symbol:    block.Symbol,
			Source:    block.Source,
			FilePath:  block.FilePath,
			StartLine: block.StartLine,
			EndLine:   block.EndLine,
		}
		if block.Trace != nil {
			sb.Trace = block.Trace
			pkg.Summary.TokenEstimate += block.Trace.TokenEstimate
			pkg.Summary.TotalLines += block.Trace.HitLines
			pkg.Summary.TrimReasons = append(pkg.Summary.TrimReasons, block.Trace.TrimReason)
		}
		if req.WithTests && block.RelatedTests != nil {
			sb.RelatedTests = block.RelatedTests
		}
		pkg.Symbols = append(pkg.Symbols, sb)
		pkg.Summary.SymbolCount++

		// 2) 影响面（可选）
		if req.WithImpact {
			imp, err := prov.GetImpact(ctx, sym, req.ImpactDepth)
			if err != nil {
				return nil, fmt.Errorf("contextsdk: impact %q: %w", sym, err)
			}
			ib := ImpactBlock{Symbol: sym, Callers: []string{}, Callees: []string{}}
			for _, n := range imp.Callers {
				ib.Callers = append(ib.Callers, n.Method)
				if req.WithTests {
					ib.Tests = append(ib.Tests, n.RelatedTests...)
				}
			}
			for _, n := range imp.Callees {
				ib.Callees = append(ib.Callees, n.Method)
			}
			ib.Trace = imp.Trace
			pkg.Impacts = append(pkg.Impacts, ib)
		}
	}

	return pkg, nil
}
