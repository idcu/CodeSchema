# CodeSchema adapterx 独立发布评估

## 1. 定位

adapterx 是 CodeSchema **解析适配器的对外发布契约层**（A 级生态资产聚合包）。把 4 个解析适配器（tree-sitter / SCIP / CodeGraph / LSP）沉淀为可独立发布的聚合资产：任何第三方可按本契约自行实现 `ParserPlugin`，与 CodeSchema 的解析流水线无缝对接。

当前仓库内实现：`contrib/adapterx/`（自包含，仅依赖标准库）。

## 2. 发布形态

建议以独立 Go 模块 `gitee.com/idcu/codeschema-adapterx` 发布，依赖关系：

```
codeschema-adapterx
  └── 仅标准库（context/fmt/sort/sync 等），无任何外部依赖
```

仓库内集成：`internal/parser/adapterx.go` 提供 `ToAdapterX` / `FromAdapterX` 双向桥接，把内部 IR 与外部契约互转。

## 3. 独立发布前置条件

### 3.1 依赖纯净性（已满足，2026-08-17）

`contrib/adapterx` 仅依赖标准库，无 `github.com/idcu/*` 与 `internal/*` 引用，可直接拷贝为独立仓库。

### 3.2 版本标记

- 契约 API（`ParserPlugin` / `BatchParser` / `IRDocument` / `Registry` / `BuiltinAdapters`）已稳定；
- 独立 Go module 发布应在首个 v* tag 时完成（与 context-sdk 并列）。

## 4. 发布路线图

| 阶段 | 内容 | 依赖 |
|---|---|---|
| P0 | 契约定义 + 双向桥接（已落地，Commit 121） | 当前任务 |
| P1 | 独立 module 编译/测试验证 | 当前 monorepo |
| P2 | 独立 `go.mod` 发布至 `gitee.com/idcu/codeschema-adapterx` | ✅ 已发布 v0.2.0 |
| P3 | 发布示例与第三方接入指南 | 同 P2 |

> 发布前置验证已就绪（2026-08-17）：`bash scripts/check-adapterx-publish.sh`
> 把本包复制到临时目录做独立 module 编译 + vet + test，仅标准库依赖即可通过。

## 4.1 对外消费

独立仓已随 P2 发布至 `gitee.com/idcu/codeschema-adapterx`（模块路径 `gitee.com/idcu/codeschema-adapterx`，tag `v0.2.0`），`go get gitee.com/idcu/codeschema-adapterx@v0.2.0` 即可引入。因 gitee 不在官方 module proxy / sum.golang.org 覆盖范围，消费方 go 环境需：

```bash
go env -w GOPRIVATE=gitee.com/idcu GONOSUMDB=gitee.com/idcu GOSUMDB=off GOPROXY=direct
go get gitee.com/idcu/codeschema-adapterx@v0.2.0
```

## 5. 验证纪律

- 发布前 `bash scripts/check-adapterx-publish.sh` 全通（红线：没有验证就是没做）
- 发布后同步更新 git 历史（2026-09-02 重构前文档） 状态
