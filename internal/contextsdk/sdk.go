// Package contextsdk 提供上下文编排的程序化 SDK（建议 5：借鉴 dsh Code mode SDK）。
//
// 一次调用组合「多租户 × 多符号 × 影响面 × 关联单测」的上下文包，供
// dsh Code mode / Claude Code / 其他 Agent 运行时程序化调用，减少多轮往返。
//
// 用法：
//
//	client := contextsdk.NewClient(func(tenant string) (*service.Service, error) {
//	    return mgr.GetService(tenant)
//	})
//	pkg, err := client.Compose(ctx, contextsdk.Request{
//	    Tenant: "repo-a",
//	    Symbols: []string{"com.x.OrderService.getUser"},
//	    WithImpact: true,
//	    WithTests:  true,
//	})
package contextsdk

import (
	"context"
	"fmt"

	"github.com/idcu/codeschema/internal/service"
)

// ResolveService 返回指定租户的 Service 实例；tenant 为空表示默认租户。
type ResolveService func(tenant string) (*service.Service, error)

// Request 一次上下文编排请求。
type Request struct {
	// Tenant 目标租户（空 = 默认租户）。
	Tenant string
	// Symbols 待注入的符号（方法/类的 FullName）。
	Symbols []string
	// ContextLines 上下文裁剪行数（mode=full 生效；0=符号完整内容）。
	ContextLines int
	// Mode 注入模式：full（默认，注入源码原文）或 minimal（极简元数据）。
	Mode string
	// WithImpact 是否附带影响面（callers/callees 与对应关联单测）。
	WithImpact bool
	// ImpactDepth 影响面深度（默认 1）。
	ImpactDepth int
	// WithTests 是否附带关联单测（min_confidence=60，五策略）。
	WithTests bool
	// IncludeTrace 是否附带 _trace 追溯（默认 false，显式开启；
	// 与 service.ContextOptions 的 opt-in 语义一致）。
	IncludeTrace bool
}

// SymbolBlock 单个符号的注入块。
type SymbolBlock struct {
	Symbol       string               `json:"symbol"`
	Source       string               `json:"source"`
	FilePath     string               `json:"file_path,omitempty"`
	StartLine    int                  `json:"start_line,omitempty"`
	EndLine      int                  `json:"end_line,omitempty"`
	RelatedTests []string             `json:"related_tests,omitempty"`
	Trace        *service.TraceEntry  `json:"_trace,omitempty"`
}

// ImpactBlock 单个符号的影响面摘要。
type ImpactBlock struct {
	Symbol   string   `json:"symbol"`
	Callers  []string `json:"callers"`
	Callees  []string `json:"callees"`
	Tests    []string `json:"tests,omitempty"`
	Trace    *service.TraceEntry `json:"_trace,omitempty"`
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
	Tenant  string         `json:"tenant"`
	Symbols []SymbolBlock  `json:"symbols"`
	Impacts []ImpactBlock  `json:"impacts,omitempty"`
	Summary Summary        `json:"summary"`
}

// Client 上下文编排客户端。
type Client struct {
	resolve ResolveService
}

// NewClient 创建上下文编排客户端。resolve 必须非 nil，否则 Compose 返回错误。
func NewClient(resolve ResolveService) *Client {
	return &Client{resolve: resolve}
}

// Compose 编排一次上下文包：按租户解析 Service，逐个符号注入上下文，
// 可选附带影响面与关联单测，并汇总 token 估算。
func (c *Client) Compose(ctx context.Context, req Request) (*Package, error) {
	if c == nil || c.resolve == nil {
		return nil, fmt.Errorf("contextsdk: resolver is nil")
	}
	if req.Mode == "" {
		req.Mode = string(service.ModeFull)
	}
	if req.ImpactDepth <= 0 {
		req.ImpactDepth = 1
	}
	includeTrace := req.IncludeTrace
	if len(req.Symbols) == 0 {
		return nil, fmt.Errorf("contextsdk: symbols is empty")
	}

	svc, err := c.resolve(req.Tenant)
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
		block, err := svc.GetContextMode(ctx, sym, service.ContextOptions{
			ContextLines: req.ContextLines,
			Mode:         service.ContextMode(req.Mode),
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
			imp, err := svc.GetImpact(ctx, sym, req.ImpactDepth)
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
