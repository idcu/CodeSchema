// Package contextsdk 是 context-sdk 的对外发布契约骨架（B 级生态资产，独立发布评估）。
//
// 定位：当前仓库内的完整编排实现位于 internal/contextsdk（依赖 internal/service）。
// 独立发布 `github.com/idcu/codeschema-contextsdk` 的前置是把对 internal/service 的
// 硬依赖抽象为轻量接口——本包即该抽象：SDKProvider 是任何服务端（codeschema 后端
// 或第三方实现）接入 context-sdk 编排的最小契约，配合公开 DTO 即可独立编译、测试。
//
// 编排逻辑（Compose：多租户 × 多符号 × 影响面 × 关联单测）当前保留在
// internal/contextsdk，独立发布时随本契约一同迁移为独立 module。
// 发布评估与路线图见 README.md。
package contextsdk

import "context"

// SymbolContext 单个符号的上下文注入结果（SDK 公开 DTO，语义与
// internal/service.SymbolContext 对齐；独立发布时迁入公共类型包）。
type SymbolContext struct {
	Symbol       string   // 方法/类全限定名
	Source       string   // 注入的源码原文（minimal 模式为行区间摘要）
	FilePath     string   // 源文件绝对路径
	StartLine    int      // 命中起始行（1-based）
	EndLine      int      // 命中结束行
	RelatedTests []string // 关联单测（五策略，min_confidence=60）
}

// ImpactResult 影响面摘要（SDK 公开 DTO）。
type ImpactResult struct {
	Symbol  string   // 被分析方法全限定名
	Callers []string // 调用方方法全限定名
	Callees []string // 被调用方法全限定名
	Tests   []string // 影响面内方法关联的单测
}

// SDKProvider 是 context-sdk 对外依赖的最小契约：服务端需提供上下文注入与
// 影响面查询两个能力。任何实现（codeschema 后端 / 第三方）都可被编排。
type SDKProvider interface {
	// GetContext 注入单个符号的上下文；lines=0 表示符号完整内容。
	GetContext(ctx context.Context, tenant, symbol string, lines int) (*SymbolContext, error)

	// GetImpact 查询影响面；depth<=0 使用默认深度 1。
	GetImpact(ctx context.Context, tenant, method string, depth int) (*ImpactResult, error)
}

// Resolver 按租户 ID 解析出 SDKProvider（空租户 = 默认租户）。
type Resolver func(tenant string) (SDKProvider, error)

// Request 一次上下文编排请求（SDK 公开 DTO，与 internal/contextsdk.Request 对齐）。
type Request struct {
	Tenant       string   // 目标租户（空 = 默认租户）
	Symbols      []string // 待注入符号全限定名
	ContextLines int      // 上下文裁剪行数（0 = 符号完整内容）
	WithImpact   bool     // 是否附带影响面
	ImpactDepth  int      // 影响面深度（默认 1）
	WithTests    bool     // 是否附带关联单测
}

// Package 编排出的上下文包（SDK 公开 DTO）。
type Package struct {
	Tenant  string   `json:"tenant"`
	Symbols []string `json:"symbols"`
	Impacts []string `json:"impacts,omitempty"`
}
