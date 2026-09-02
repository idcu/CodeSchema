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

### 3.1 接口抽象（已解决，2026-08-18）

当前 `contrib/contextsdk` 已实现为**权威契约与编排实现**（自包含，仅依赖标准库）：

- `SDKProvider` 最小契约：`GetContextMode` + `GetImpact`；
- 公开 DTO 全部迁入本包：`ContextOptions` / `SymbolContext` / `ImpactResult` / `TraceEntry` / `Request` / `Package` / `Summary` / `SymbolBlock` / `ImpactBlock`；
- 完整编排实现 `Client.Compose`（多租户解析 → 逐符号注入 → 可选影响面/关联单测 → token 汇总），不再依赖 `internal/service`。

仓库内集成：`internal/contextsdk` 现为纯适配层，`ServiceProvider` 把 `internal/service.Service` 桥接为 `SDKProvider`，`NewClient` 签名保持向后兼容：

```go
// internal/contextsdk
type ResolveService func(tenant string) (*service.Service, error)
func NewClient(resolve ResolveService) *Client // Client = contextsdk.Client（类型别名）
```

验证：`go test ./contrib/contextsdk/...` 11 条 mock 编排测试全通（mockProvider 仅依赖标准库，证明第三方实现契约即可被编排）；`go test ./internal/contextsdk/...` 真实集成测试（Store+Analyzer）全通。

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

> 发布前置验证已就绪（2026-08-17）：`bash scripts/check-contextsdk-publish.sh`
> 把本包复制到临时目录做独立 module 编译 + vet + test，仅标准库依赖即可通过；
> dsh Code mode 集成示例见 `contrib/dsh/README.md` §8。

## 5. 验证纪律

- 发布前 `go test ./contrib/contextsdk/...` 全通
- 发布前 benchmark 记录 token 估算与编排耗时（红线：没有数据就是没做）
- 发布后同步更新 `docs/archive/4-决策层/生态资产发布说明.md` 状态