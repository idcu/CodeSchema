// Package contextsdk 提供 context-sdk 在仓库内的集成适配（B 级生态资产）。
//
// 定位：对外发布契约与权威编排实现位于 contrib/contextsdk（自包含、可独立发布）。
// 本包作为仓库内适配层，把 internal/service.Service 桥接为 contrib/contextsdk.SDKProvider，
// 使真实后端（多租户 Service 实例）能被权威 Client.Compose 编排。
//
// 用法：
//
//	client := contextsdk.NewClient(func(tenant string) (*service.Service, error) {
//	    return mgr.Service(ctx, tenant)
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

	"github.com/idcu/codeschema/contrib/contextsdk"
	"github.com/idcu/codeschema/internal/service"
)

// ServiceProvider 把 internal/service.Service 适配为 SDKProvider（权威契约）。
type ServiceProvider struct {
	svc *service.Service
}

// NewServiceProvider 创建适配器。
func NewServiceProvider(svc *service.Service) *ServiceProvider {
	return &ServiceProvider{svc: svc}
}

// GetContextMode 实现 SDKProvider：桥接 service.SymbolContext → contextsdk.SymbolContext。
func (p *ServiceProvider) GetContextMode(ctx context.Context, symbol string, opts contextsdk.ContextOptions) (*contextsdk.SymbolContext, error) {
	sc, err := p.svc.GetContextMode(ctx, symbol, service.ContextOptions{
		ContextLines: opts.ContextLines,
		Mode:         service.ContextMode(opts.Mode),
		IncludeTrace: opts.IncludeTrace,
	})
	if err != nil {
		return nil, err
	}
	return &contextsdk.SymbolContext{
		Symbol:       sc.Symbol,
		Source:       sc.Source,
		FilePath:     sc.FilePath,
		StartLine:    sc.StartLine,
		EndLine:      sc.EndLine,
		Doc:          sc.Doc,
		RelatedTests: sc.RelatedTests,
		Trace:        traceToSDK(sc.Trace),
	}, nil
}

// GetImpact 实现 SDKProvider：桥接 service.ImpactResult → contextsdk.ImpactResult。
func (p *ServiceProvider) GetImpact(ctx context.Context, method string, depth int) (*contextsdk.ImpactResult, error) {
	ir, err := p.svc.GetImpact(ctx, method, depth)
	if err != nil {
		return nil, err
	}
	out := &contextsdk.ImpactResult{
		Method:  ir.Method,
		Callers: make([]contextsdk.ImpactNode, 0, len(ir.Callers)),
		Callees: make([]contextsdk.ImpactNode, 0, len(ir.Callees)),
		Trace:   traceToSDK(ir.Trace),
	}
	for _, n := range ir.Callers {
		out.Callers = append(out.Callers, contextsdk.ImpactNode{
			Method:       n.Method,
			Depth:        n.Depth,
			RelatedTests: n.RelatedTests,
		})
	}
	for _, n := range ir.Callees {
		out.Callees = append(out.Callees, contextsdk.ImpactNode{
			Method:       n.Method,
			Depth:        n.Depth,
			RelatedTests: n.RelatedTests,
		})
	}
	return out, nil
}

// traceToSDK 桥接 service.TraceEntry → contextsdk.TraceEntry（nil 安全）。
func traceToSDK(t *service.TraceEntry) *contextsdk.TraceEntry {
	if t == nil {
		return nil
	}
	return &contextsdk.TraceEntry{
		Source:        t.Source,
		HitSymbols:    t.HitSymbols,
		HitLines:      t.HitLines,
		TrimReason:    t.TrimReason,
		TrimmedLines:  t.TrimmedLines,
		TokenEstimate: t.TokenEstimate,
		Timestamp:     t.Timestamp,
	}
}

// ResolveService 返回指定租户的 Service 实例；tenant 为空表示默认租户。
// 保留向后兼容的便捷签名，内部经 ServiceProvider 桥接为 SDKProvider。
type ResolveService func(tenant string) (*service.Service, error)

// Client 是 internal 层对外暴露的编排客户端（代理 contrib/contextsdk.Client）。
type Client = contextsdk.Client

// Request 一次上下文编排请求（对外别名，指向权威 DTO）。
type Request = contextsdk.Request

// Package 编排出的上下文包（对外别名，指向权威 DTO）。
type Package = contextsdk.Package

// NewClient 创建上下文编排客户端。resolve 必须非 nil，否则 Compose 返回错误。
// 签名兼容旧版（直接传 *service.Service 解析器），内部桥接为 SDKProvider。
func NewClient(resolve ResolveService) *Client {
	if resolve == nil {
		return contextsdk.NewClient(nil)
	}
	return contextsdk.NewClient(func(tenant string) (contextsdk.SDKProvider, error) {
		svc, err := resolve(tenant)
		if err != nil {
			return nil, err
		}
		return NewServiceProvider(svc), nil
	})
}
