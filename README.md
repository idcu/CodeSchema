# CodeSchema

> 代码元数据 KV/DB 系统 — 面向 AI 辅助开发的代码元数据索引与上下文裁剪服务

[![CI](https://github.com/idcu/code-schema/actions/workflows/ci.yml/badge.svg)](https://github.com/idcu/code-schema/actions/workflows/ci.yml)
![Go Version](https://img.shields.io/badge/Go-1.25.2-blue)
![Status](https://img.shields.io/badge/status-production--ready-green)

## 系统定位

将仓库中的类、方法、接口、继承关系、调用关系、文件路径、行号、参数、返回值、用途标签等结构化数据，沉淀为「文件存储（权威源）+ 内存索引（热读）+ 向量索引（语义检索）」三层存储，并通过 MCP Server 向 AI Agent 供给精准裁剪后的代码上下文。

## 核心能力

- **精准上下文裁剪**：AI 回答代码问题时，不必喂入整个仓库，大幅节省 token
- **影响面分析**：改一行代码即可反查受影响方法并定位关联单测（⚠️ 默认 tree-sitter 正则/语法树解析路径仅填被调方 `CalleeFQN`、未填调用方 `CallerFQN`，故 `impact`/`tests`/`get_call_graph` 对真实方法默认返回空；需 LSP/SCIP/CodeGraph 适配器或回填 `CallerFQN` 才生效）
- **双路检索**：符号图精确检索 + 向量语义检索（FTS + 向量融合重排）
- **增量监听**：支持 fsnotify 原生文件监听 + 轮询监听，300ms 防抖合并
- **标签分类**：六类标签自动推导（layer/biz/tech/risk/test/lang）
- **向量索引可视化**：Web 可视化仪表盘（`/viz`），默认栈（Persistent/Memory 向量索引）即可启用，支持文档浏览、文本检索、向量索引状态查看
- **可观测性**：结构化日志（log/slog）+ 基础指标（Prometheus 格式）+ 链路追踪
- **配置热重载**：YAML/JSON 配置 + 环境变量覆盖 + 运行时热重载
- **生产级健壮性**：优雅关闭 / 重试机制 / Panic 恢复 / 安全中间件

### 检索低置信度过滤（B8）

「空结果优于误导结果」：检索结果置信度低于阈值时予以过滤，避免向 Agent 返回可能误导的弱匹配。

- **置信度口径（量化、不误杀）**：
  - 语义检索（`semantic` / `both`）：取向量余弦相似度（chromem 返回，绝对量纲 [0,1]，1=完全相同）；
  - 纯 FTS（`exact`）：取按集合最大值归一化的相对得分 [0,1]（仅作相对排序参考，作绝对阈值须谨慎）。
- **阈值 `min_score`**：绝对置信度阈值 [0,1]，默认 `0`（关闭过滤，完全向后兼容）；`Confidence < min_score` 的结果被丢弃。
- **响应（envelope）**：`search_symbols` / `GET /search?min_score=` 返回
  `{ "results": [...], "trim_reason": "below_threshold", "filtered": N }`；`results[].confidence` 为每条结果绝对置信度，
  有结果被过滤时 `trim_reason="below_threshold"`、`filtered` 为被过滤条数。
- 建议起点：语义/融合模式 `min_score=0.3~0.5`（bge-small-zh-v1.5 实测弱匹配常 <0.3）。

## 技术栈

- 语言：Go 1.25.2
- 依赖：modernc.org/sqlite（纯 Go 免 CGO）/ fsnotify / chromem-go / yaml.v3 / onnxruntime_go（**可选**：仅在 `go build -tags onnx` 时引入）/ idcu-go 公共模块 `{trim, ttlcache, pathsafe, retry, graceful, recovery, metrics, log, trace, config}` v0.1.0（其中 log 为草案 α：`WithModule` 形态，API 可破；trace 为轻量 API 形态对齐 OTel）（纯标准库零依赖，托管于 `gitee.com/idcu-go/*`；`go.mod` 以 `require v0.1.0` + `replace ../idcu-go/*` 双模式接线：本地改模块即时生效，CI/Release 按 tag 检出、Docker 按 tag 拉取，去掉 replace 后配 `GOPRIVATE=gitee.com/idcu-go/*` 即可直接构建）；解析适配器为 30 语言（go/java/ts/py/rust/cpp/c/kotlin/swift/php/csharp/ruby/bash/scala/sql/elixir/ocaml/lua/groovy/css/toml/yaml/protobuf/html/hcl/svelte/markdown/dockerfile/elm/cue）正则启发式，无 CGO 依赖（`-tags treesitter` 可切换真语法树）；ONNX 语义模型支持远程分发（`storage.vector.model_download_url` 自动下载 + SHA-256 校验）
- 存储：JSON 文件（默认 fallback）+ SQLite（modernc.org/sqlite 纯 Go 免 CGO，已接线，`storage.driver=sqlite` 启用）；PostgreSQL（`storage.driver=pg|postgres`，需 `go build -tags pg`）+ Redis 热点缓存层（`storage.kv=redis://host:6379/0`，需 `go build -tags redis`）经 `cmd/codeschema` 统一分发接入
- 协议：MCP Server（JSON-RPC 2.0 + SSE）+ HTTP API（RESTful）
- 部署：单二进制 / Docker 容器 / 多平台交叉编译

## 存储后端（多驱动可插拔）

存储层以 `internal/store.Store` 接口统一抽象，后端经 `cmd/codeschema` 的 build-tagged 分发接线（因 sqlite/pg 反向依赖 `internal/store`，分发必须落在 cmd 层而非 `store.NewStore`，否则循环依赖）：

| 驱动 | 配置 | 构建标签 | 说明 |
|---|---|---|---|
| `file`（默认） | `storage.driver=file` | 无 | JSON 文件存储，零依赖 |
| `sqlite` | `storage.driver=sqlite` | 无 | modernc.org/sqlite 纯 Go 免 CGO，关系查询/跨会话一致 |
| `pg` / `postgres` | `storage.driver=pg` + `storage.dsn=postgres://...` | `-tags pg` | PostgreSQL，亿级横向扩展（需 `go get github.com/lib/pq`） |
| Redis 缓存层 | `storage.kv=redis://host:6379/0` | `-tags redis` | 热点类/调用反查 L2 缓存，地址为空则不启用（需 `go get github.com/redis/go-redis/v9`） |

> 默认 `go build ./...` 不含 pg/redis 代码；启用对应后端时加 `-tags pg` / `-tags redis` 并拉取驱动依赖即可，详见 `docs/01-开发者/存储后端.md`。

## 多租户（单实例多仓库）

多个项目都需要 CodeSchema 时，**推荐「单实例多租户」**：一个进程同时服务多个隔离的仓库，按 `project` 标识路由，无需为每个项目各起一个进程。

- 每个租户持有**完全独立的** store + 全文/向量/IDF 索引（索引目录默认按各自 store 目录派生，绝不共享），隔离彻底且不修改 `Store` 接口；
- 接入方式统一：`serve` / `mcp` 共用同一份 `--config`（写 `tenants:` 列表）；HTTP 用 `X-Tenant` 头或 `?tenant=`，MCP 工具用 `project` 参数，`list_projects` / `GET /projects` 枚举全部租户；
- 向后兼容：不写 `tenants` 即为单「default」租户，行为与此前完全一致。

可运行示例见 `build/mt-demo.yaml`，设计细节见 [docs/02-架构师/README.md](docs/02-架构师/README.md)。

## 快速开始

```bash
# 构建（无 CGO，纯 Go 二进制）
make build
# 或直接构建
go build -o codeschema ./cmd/codeschema

# 扫描仓库
./codeschema scan ./repo

# 启动 MCP Server
./codeschema mcp --addr :8080

# 启动 HTTP API
./codeschema serve --http :8081

# 向量索引可视化（serve 启动后访问 http://localhost:8081/viz，默认栈即可用）
# 支持文档浏览、文本检索、向量索引状态查看

# 文件监听（增量更新）
./codeschema watch --fsnotify ./repo

# 全链路基准（扫描/索引/检索指标，单仓或多仓对比）
./codeschema benchmark ./repo --out build/bench.json

# Agent 任务端到端评测（对外可信基准：三档上下文 × 任务集的通过率与 token 节省）
./codeschema agent-bench --repo . --out build/agent-task-bench
# 多仓库评测（按 RepoHint 过滤任务，跨仓对比报告）
./codeschema agent-bench --repos "repo1;repo2" --out build/agent-task-bench

# MCP 原生 stdio 直连（供仅支持 stdio 的客户端，如 Claude Desktop）
./codeschema mcp --stdio --store ./data

# 查看版本
./codeschema version
```

### 5 分钟接入 AI 客户端（MCP）

1. 启动 MCP Server：`./codeschema mcp --addr :8080`
2. 查看当前端点的客户端配置：`./codeschema mcp --print-config`（或见 `docs/00-新人上手/README.md`）
3. 在 VS Code / JetBrains / Claude Code / Cursor 中粘贴对应配置片段，即可调用
   `search_symbols` / `context` / `impact` 等 12 个 MCP 工具。

### Docker 部署

```bash
# 构建镜像（默认免 CGO 纯 Go；模块代理默认 goproxy.cn，国内可复现构建）
docker build -t codeschema:latest .

# 运行
docker run -p 8081:8081 -v ./data:/app/data codeschema:latest
```

## 开发前必读

改动代码**前**请先确认以下前置与红线（均为已核实的工程事实，非约定推测）：

1. **构建前置**：Go 1.25 + **`../idcu-go` 兄弟仓必须存在**（`go.mod` 含 10 条 `replace gitee.com/idcu-go/* => ../idcu-go/*`）；否则 `go build` 解析失败。
2. **能力按 build tag 隔离**（改码前务必 `grep '//go:build'` 确认你动的是哪个变体）：
   - 默认（免 CGO）：正则元数据 + SQLite + **TF-IDF** 语义降级
   - `-tags onnx`：ONNX 语义检索（需 gcc + onnxruntime 动态库 + glibc，alpine 不可用）
   - `-tags 'pg redis'`：PostgreSQL / Redis 存储后端
   - `-tags treesitter`：tree-sitter **真语法树**解析（CGO）
3. **影响面分析可用**：`impact` / `tests` / `get_call_graph` 依赖调用边的 `CallerFQN`；默认解析路径与 AST 路径均已回填，可用（LSP/SCIP/CodeGraph 适配器亦提供调用边）。调用方为空时这些工具对真实方法返回空。
4. **计数类字段禁止手填**：包数（internal=32 / 全仓库=36）、MCP 工具数（12）、HTTP 路由数（16）统一由 `make counts` 生成、`scripts/counts_baseline.json` 锁基线、CI `counts-guard` 漂移断言守护。**改动后 `make counts-check` 必须绿灯**；数字有意为之变时先 `make counts-update` 刷新基线。
5. **文档分层面向不同人群**（文档地图见 [`docs/README.md`](./docs/README.md)）：新人上手 / 开发者 / 架构师 / 运维部署 / 贡献者各有专属文档，勿把某人群内容塞进另一人群文档。
6. **改码必改档，文档不得超前于代码**：任何接口/表/CLI/能力边界变更，先改代码再同步文档；提交前 grep 核查旧路径/旧数字残留。提交遵循 Conventional Commits；**仅 `git commit`，push 需显式授权**。

## 开发指南

按人群分层的开发文档已重构，统一入口见 [docs/README.md](./docs/README.md)（文档地图）：

- 新人 / 快速开始 → [docs/00-新人上手/README.md](./docs/00-新人上手/README.md)
- 开发者（构建 / 架构 / 接口 / 解析 / 存储 / 测试CI）→ [docs/01-开发者/README.md](./docs/01-开发者/README.md)
- 架构师（设计决策 / 成熟度 / 边界）→ [docs/02-架构师/README.md](./docs/02-架构师/README.md)
- 部署运维（Docker / 配置 / 安全 / 多租户 / 可观测）→ [docs/03-部署运维/README.md](./docs/03-部署运维/README.md)
- 贡献者（提交 / 改码必改档 / CI）→ [docs/04-贡献者/README.md](./docs/04-贡献者/README.md)

> 重构前的历史开发文档（原 git 历史（2026-09-02 重构前文档） 00–13、modules P1–P9 等）已归档至 git 历史（2026-09-02 重构前文档），仅供追溯，不再随主文档维护。


## 架构概览

```
┌─────────────────────────────────────────────────────┐
│                      CLI (cmd/codeschema)            │
│  scan  watch  rebuild-kv  mcp  serve  version       │
└──────────┬────────────────────────────────┬──────────┘
           │                                │
     ┌─────▼──────┐                  ┌──────▼───────┐
     │   Scanner   │                  │   Server     │
     │  (增量扫描)  │                  │  HTTP + MCP  │
     └─────┬───────┘                  └──────┬───────┘
           │                                 │
     ┌─────▼─────────────────────────────────▼───────┐
     │                   Service                     │
     │  查询 / 标签 / 测试关联 / 搜索 / 分析           │
     └─────┬──────────┬──────────┬──────────┬─────────┘
           │          │          │          │
     ┌─────▼──┐ ┌────▼───┐ ┌───▼────┐ ┌───▼──────┐
     │ Store  │ │Tagger  │ │Analyzer│ │ Searcher │
     │ 持久化  │ │ 标签推导 │ │ 代码图  │ │ FTS+向量 │
     └────────┘ └────────┘ └────────┘ └──────────┘
```

## 构建

```bash
# 编译
make build

# 运行测试
make test

# 竞态检测
make test-race

# 覆盖率
make test-cover

# 代码检查
make lint

# 交叉编译（5 平台）
make cross

# 清理
make clean
```

## 当前状态

所有 P0-P18 阶段及后续优化项已全部完成，项目达到生产级可运行状态。

| 阶段 | 完成度 | 说明 |
|------|--------|------|
| P0 骨架 | 100% | 项目骨架 + 核心接口 + 存储层 |
| P0 MVP | 100% | 适配器 + 增量更新 + 接口层 |
| P1 | 100% | 反向引用索引 + 类层次父子关系 |
| P2 | 100% | BuildAll 单次遍历 + Go 模块路径解析 |
| P3 | 100% | 多语言 ImportResolver（Java Maven/Gradle） |
| P4 | 100% | Gradle 多模块路径解析 + 可配置前缀 |
| P5 | 100% | 标签分类体系（Tag）与测试关联 |
| P6 | 100% | 可观测性（日志/指标/追踪/安全） |
| P7 | 100% | 配置系统（YAML 解析） |
| P8 | 100% | 语义检索与全文搜索 |
| P9 | 100% | 配置热重载 / 多配置源 |
| P10 | 100% | 遗留问题治理 |
| P11 | 100% | 集成测试与性能压测 |
| P12 | 100% | 生产级健壮性（优雅关闭/重试/Panic 恢复） |
| P13 | 100% | 构建脚本 / CI 配置 / 容器化 / 部署文档 |
| P14 | 100% | 多语言适配器扩展（SCIP/LSP）+ 语义检索精度提升（chromem-go） |
| P15 | 100% | 真实仓库 benchmark 数据采集 |
| P16 | 100% | 生产环境部署验证（Docker/CI 流水线） |
| P17 | 100% | LSP 适配器优化 + 向量索引可视化工具 |
| P18 | 100% | 多仓库 benchmark 对比框架 |
| 后续优化 | 100% | 多仓库 benchmark 实际运行 / LSP 适配器稳定性验证 / 向量可视化增强 / 日志 data race 修复 |

### 最新进展（2026-08-14）

- **多租户（单实例多仓库）已落地**：新增 `internal/tenant`（管理器 + 路由）与 `internal/runtime`（单租户运行期装配），`serve` / `mcp` 通过一份 `--config` 的 `tenants:` 列表同时服务多个隔离仓库；每租户独立 store + 独立 FTS/向量/IDF 索引（默认按各自 `storage.dsn` 目录派生隔离）。HTTP 用 `X-Tenant`/`?tenant=`，MCP 工具用 `project` 参数，`list_projects` / `GET /projects` 枚举租户；无 `tenants` 配置时退化为单「default」租户，完全向后兼容。设计见 [docs/02-架构师/README.md](docs/02-架构师/README.md)，可运行示例见 `build/mt-demo.yaml`。
- **SQLite 权威存储已接线**：新增 `internal/store/sqlite`（基于纯 Go 的 `modernc.org/sqlite`，免 CGO），完整实现 `store.Store` 接口（文件/类/方法/调用/标签 + 反向查询 + `UpsertIR` 增量入库），`storage.driver=sqlite` 即启用，默认仍 JSON 文件存储作 fallback。消除了「文档声称 SQLite、现实仅 JSON」的实现落差。
- **SCIP / LSP 适配器生产验证**：
  - SCIP：新增真实 fixture 端到端测试，覆盖 class/method/**调用关系提取**逻辑，并修复 `ParseAll` 误用「文件存在」校验目录导致目录永远判为不存在的 Bug。
  - LSP：`gopls` 真实语言服务器端到端验证（Go 为主语言，真实返回 `Calculator` 类与 `Add`/`Sub` 方法，`TestLSPAdapter_RealGopls` 已 PASS）；`clangd` 工程上下文真实验证（构造 compile_commands.json 最小工程，clangd 22 真实提取类/方法，`TestLSPAdapter_RealClangd` 已 PASS）；mock 服务器已覆盖 JSON-RPC 传输/超时/取消/多行头/稳定性。并修复两处生产缺陷：`SymbolKind` 映射漏掉 Go 的 Struct(23)/Interface(24)/Function(12) 导致 gopls 返回 0 类；`jsonRPCRequest.ID` 缺 `omitempty` 使 notification 携带 `"id":0` 违反 JSON-RPC 2.0 导致 clangd 拒绝登记文档。
  - 多语言验证/基准框架见 `internal/adapterbench/adapter_validation_test.go`（独立轻量包，仅依赖 lsp/scip 适配器、不引入 onnxruntime 等 cgo 重型依赖，秒级编译运行），输出 `build/adapter-bench.json` 与 `analysis/2026-08-14-adapter-validation.md`；工具缺失则优雅跳过。
- **GitHub Actions CI 已修复（2026-08-16）**：CI 此前因 Node 20 actions 缓存 tar 恢复失败而中断，现已将 `actions/checkout@v7`、`actions/setup-go@v7`、`actions/upload-artifact@v6`、`docker/*-action@v4/v6/v7`、`softprops/action-gh-release@v3` 升级为 Node 24 兼容版本并加固 Windows 任务，8 个 Job（test 跨 ubuntu/macos/windows + bench + nightly-scale + race + treesitter + cross + docker）全部修复通过；同时默认嵌入模型对齐为 `bge-small-zh-v1.5`（离线命中本地 ONNX，语义检索精度恢复到 R@1 1.00）。

## 实际核查备注（2026-08-14）

> 以下为代码级核查结论，供接手/评审参考。详细论证见 `docs/01-开发者/存储后端.md` 与 `DEV_PROGRESS.md`。

- **包数量**：实际 **36** 个 Go 包（`go list ./...`，2026-08-17 实测，含 `internal/tenant`、`internal/runtime`、`contrib/adapterx`、`contrib/contextsdk`、`internal/contextsdk`、`scripts/benchtrend`），本文及 `DEV_PROGRESS.md` 中「23/24/27/31/32/33 个包」等旧表述已过时。
- **默认构建已免 CGO（已修复）**：原 `embedder_onnx.go` 无条件 `import onnxruntime_go` 导致 `go build ./...` 强制需 gcc。现已将 ONNX 嵌入器用 `//go:build onnx` 隔离，默认构建免 CGO/gcc；仅 `go build -tags onnx` 才引入 ONNX 语义检索（仍需 gcc 与 onnxruntime 动态库）。
- **SQLite 批量写入已优化**：`BulkUpsert`（`internal/store`）修复单条 upsert 慢 500 倍的瓶颈，N=10万 级批量写入降至 5~14s（见 `docs/01-开发者/存储后端.md` 与 `analysis/2026-08-14-scale-bench.md`）；超大仓写入走 `BulkUpsert`/PG/chromem。
- **存在但未在本文登记的代码**：`internal/store/pg`（PG 完整实现，564 行，`//go:build pg`）、`internal/store/redis`（热点缓存层，117 行，`//go:build redis`）、`internal/scalebench`（超大仓基准）此前均未接主路。现 PG/Redis 已通过 `cmd/codeschema` 层 build-tagged 统一分发接入主路，`internal/scalebench` 新增 `BenchmarkScaleBulk`（N=1万）与 `BenchmarkSQLiteWALConfigs`（WAL 同步参数定案）固化进 CI（`.github/workflows/ci.yml` 新增 bench job）看护 `BulkUpsert` 回归，详见 `docs/01-开发者/存储后端.md`。
- **开发文档索引**：git 历史（2026-09-02 重构前文档） 实际含 `00`–`13` 共 14 篇，本文「开发指南」已全部列出；模块级文档（P1~P9 分层拆解，含完成度/阻塞项/模块关系）见 git 历史（2026-09-02 重构前文档）。

### 构建变体与能力边界（默认 vs onnx vs 扩展存储）

> 本项目大量能力经 `//go:build` 标签隔离，默认二进制与 `-tags` 构建能力不同，使用前务必确认所在变体。

| 构建命令 | 产物能力 | 说明 |
|---|---|---|
| `go build ./cmd/codeschema`（默认） | 正则/tree-sitter 多语言元数据 + SQLite/文件存储 + TF-IDF 语义（LocalEmbedder 降级）+ 12 MCP / 16 HTTP 工具 + 多租户 | 免 CGO/gcc；**影响面分析 `impact`/`tests`/`get_call_graph` 默认对真实方法返回空**（默认解析路径仅填被调方 `CalleeFQN`，未填调用方 `CallerFQN`），需 LSP/SCIP/CodeGraph 适配器或回填 `CallerFQN` 才生效 |
| `go build -tags onnx ./cmd/codeschema`（产物 `codeschema-onnx`） | 上述 + bge-small-zh-v1.5 ONNX 向量语义（Recall@1=1.00） | 需 gcc + onnxruntime 动态库 + 模型文件 + glibc |
| `go build -tags 'pg redis' ./cmd/codeschema` | 上述 + PostgreSQL（超大仓横向扩展）+ Redis 热点缓存/调用反查 | 需外部 PG / Redis 实例 |

> **构建前置**：`go.mod` 通过 10 条 `replace gitee.com/idcu-go/* => ../idcu-go/*` 指向本地兄弟仓，克隆后须保证 `../idcu-go` 存在才能构建（`go mod vendor` 后 Docker 镜像离线构建同理）。

## 测试

```bash
# 运行全部测试
make test

# 性能基准测试（含 internal/scalebench 的 BulkUpsert 回归看护 BenchmarkScaleBulk）
make bench

# Agent 任务端到端评测（对外可信基准，刷新 build/agent-task-bench/ 快照）
make bench-agent
```

当前全部包通过测试，0 失败（含 -race 竞态检测）。

## 环境要求

| 依赖 | 最低版本 | 说明 |
|------|---------|------|
| Go | 1.25+ | 编译运行 |
| GCC/MinGW | 任一 C 编译器 | **仅 ONNX 语义检索需要**：默认 `go build ./...` 已免 CGO/gcc。`go build -tags onnx` 启用 ONNX 嵌入器（bge-small-zh-v1.5）时需 gcc 与 onnxruntime 动态库；不使用 ONNX 时纯 Go 构建即可（modernc.org/sqlite 亦为纯 Go）。 |
| Docker | 24+ | 容器化部署 |
| onnxruntime | 1.28.0（匹配 `onnxruntime_go v1.32.1`，API 28） | 可选，ONNX 模型语义检索加速（需 `onnxruntime.dll` / `.so` / `.dylib`；Win/Linux/Apple Silicon 用 1.28.0；**macOS Intel(x86_64) 上游自 1.23.2 后不再发布新版本，锁定 1.23.2**，绑定经 `third_party/onnxruntime_go_patch` 适配 API 23，真实嵌入推理 dim=512 已验证） |
| bge-small-zh-v1.5 | — | 可选，ONNX 语义嵌入模型（FP16 量化，~47MB，自动降级到 LocalEmbedder） |

## 许可证

Apache-2.0