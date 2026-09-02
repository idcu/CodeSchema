# 开发者指南

> 写给：要 clone、构建、调试、扩展 CodeSchema 的工程师

## 0. 构建前置（必读）

- **Go 1.25+**
- **`../idcu-go` 兄弟仓必须存在**：`go.mod` 含 10 条 `replace gitee.com/idcu-go/* => ../idcu-go/*`。缺失则 `go build` 直接失败。本地改 idcu-go 即时生效；CI/Release 按 tag 检出。

## 1. 构建变体（能力按 build tag 隔离）

改任何代码前，先 `grep '//go:build'` 确认你动的是哪个变体：

| 变体 | 构建命令 | 说明 |
|---|---|---|
| 默认（免 CGO） | `go build ./...` | 正则元数据 + SQLite + **TF-IDF 语义降级**；零 CGO |
| ONNX 语义 | `-tags onnx` | 真·向量语义检索，需 gcc + glibc（ONNX 运行时） |
| PG / Redis | `-tags 'pg redis'` | PostgreSQL 存储 / Redis L2 缓存；`make verify-tags` 校验 |
| tree-sitter | `-tags treesitter` | 真语法树解析（CGO），替代默认正则 |

双文件互斥实现：`adapter.go`（默认正则）与 `adapter_ast.go`（`//go:build treesitter`）二选一。

## 2. 常用命令

```bash
make build          # 默认构建
make test           # 跑测试
make test-race      # 竞态测试
make lint           # 静态检查
make verify-tags    # 校验 -tags 'onnx pg redis' 全包可解析+类型检查
make counts         # 打印项目计数（包数/工具数/路由数/LoC）
make counts-check   # CI 用：与基线比对，数字漂移则失败
make bench-agent    # agent-bench 端到端评测
```

## 3. 本地开发红线

1. **影响面分析依赖 `CallerFQN`**：默认 tree-sitter 正则/语法树路径**仅填被调方 `CalleeFQN`、未填调用方 `CallerFQN`**，故 `impact` / `tests` / `get_call_graph` 对真实方法**默认返回空**。需 LSP / SCIP / CodeGraph 适配器，或显式回填 `CallerFQN` 才生效。这是已知行为，不是 bug。
2. **计数类字段禁手填**：所有「包数/MCP 工具数/HTTP 路由数」以 `scripts/project_counts.py` 为准，文档从它取值；改了接口后跑 `make counts-update` 刷新 `scripts/counts_baseline.json`。CI `counts-guard` 会断言数字漂移。
3. **改码必改档，且不得超前于代码**：代码变更必须同步受影响文档，同次提交。

## 4. 目录速览

```
cmd/codeschema/       CLI 入口（scan/watch/serve/mcp/benchmark/agent-bench/version）
internal/
  parser/adapter/     treesitter | codegraph | scip | lsp  （+ jcodeindexer 配置）
  scanner/            仓库扫描编排
  store/              file | sqlite | pg(-tags pg) | redis(-tags redis) 统一接口
  service/            context / testlink / coverprofile
  server/             http.go | mcp.go | mcp_stdio.go | middleware.go | openapi_handler.go | viz.go
  analyzer/           调用关系建边（CallerFQN 回填在此）
  search/ retrieval/ vector/ embedding/   检索与语义层
  tenant/             多租户隔离
```

→ 继续阅读：[架构与模块](./架构与模块.md) · [接口层](./接口层.md) · [解析适配器](./解析适配器.md) · [存储后端](./存储后端.md) · [测试与 CI](./测试与CI.md)
