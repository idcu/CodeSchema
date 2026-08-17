# CodeSchema context-sdk 独立发布评估

## 1. 定位

context-sdk 是 CodeSchema 的**上下文编排程序化 API**（B 级生态资产），一次调用组合「多租户 × 多符号 × 影响面 × 关联单测」的上下文包，供任意 Agent 运行时（dsh Code mode、Claude Code、自研 harness）程序化调用。

当前仓库内实现：`internal/contextsdk/`。

## 2. 发布形态

建议以独立 Go 模块 `github.com/idcu/codeschema-contextsdk` 发布，依赖关系：

```
codeschema-contextsdk
  └── codeschema-client（interface 包，当前 internal/service 的抽取）
        └── codeschema 后端（可选，仅全量模式需要后端）
```

## 3. 独立发布前置条件

### 3.1 接口抽象（当前阻塞项）

当前 context-sdk 直接依赖 `internal/service.Service` 的具体类型：

```go
type ResolveService func(tenant string) (*service.Service, error)
```

独立发布前需将 `service.Service` 的上下文方法抽取为轻量接口：

```go
// SDKProvider 是 context-sdk 对外依赖的契约（可独立于 codeschema 后端实现）。
type SDKProvider interface {
    GetContextMode(ctx, symbol, opts) (*SymbolContext, error)
    GetImpact(ctx, method, depth) (*ImpactResult, error)
}
```

同时 `ContextOptions`、`SymbolContext`、`ImpactResult`、`TraceEntry` 等结构体需迁入公共类型包。

### 3.2 版本标记

- 当前 `internal/contextsdk` 的 API（`Compose`、`Request`、`Package`）已是稳定接口，但依赖 `internal/service` 包。
- 接口抽象 + 独立 Go module 发布应在首个 v* tag 之前完成（伴随 `service` 包的接口化重构）。

## 4. 发布路线图

| 阶段 | 内容 | 依赖 |
|---|---|---|
| P0 | 完成接口抽象（SDKProvider + 公共类型），创建独立 module 占位 | 当前任务 |
| P1 | 内部仓库内使用抽象接口编译通过，测试全部通过 | 当前 monorepo |
| P2 | 独立 `go.mod` 发布至 `github.com/idcu/codeschema-contextsdk` | 首个 v* tag |
| P3 | 发布示例与 dsh 集成指南 | 同 P2 |

## 5. 验证纪律

- 发布前 `go test ./contrib/contextsdk/...` 全通
- 发布前 benchmark 记录 token 估算与编排耗时（红线：没有数据就是没做）
- 发布后同步更新 `docs/4-决策层/生态资产发布说明.md` 状态