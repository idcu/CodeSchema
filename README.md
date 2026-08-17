# CodeSchema

> 代码元数据 KV/DB 系统 — 面向 AI 辅助开发的代码元数据索引与上下文裁剪服务

[![CI](https://github.com/idcu/code-schema/actions/workflows/ci.yml/badge.svg)](https://github.com/idcu/code-schema/actions/workflows/ci.yml)
![Go Version](https://img.shields.io/badge/Go-1.25.2-blue)
![Status](https://img.shields.io/badge/status-production--ready-green)

## 系统定位

将仓库中的类、方法、接口、继承关系、调用关系、文件路径、行号、参数、返回值、用途标签等结构化数据，沉淀为「文件存储（权威源）+ 内存索引（热读）+ 向量索引（语义检索）」三层存储，并通过 MCP Server 向 AI Agent 供给精准裁剪后的代码上下文。

## 核心能力

- **精准上下文裁剪**：AI 回答代码问题时，不必喂入整个仓库，大幅节省 token
- **影响面分析**：改一行代码即可反查受影响方法并定位关联单测
- **双路检索**：符号图精确检索 + 向量语义检索（FTS + 向量融合重排）
- **增量监听**：支持 fsnotify 原生文件监听 + 轮询监听，300ms 防抖合并
- **标签分类**：六类标签自动推导（layer/biz/tech/risk/test/lang）
- **向量索引可视化**：Web 可视化仪表盘（`/viz`），默认栈（Persistent/Memory 向量索引）即可启用，支持文档浏览、文本检索、向量索引状态查看
- **可观测性**：结构化日志（log/slog）+ 基础指标（Prometheus 格式）+ 链路追踪
- **配置热重载**：YAML/JSON 配置 + 环境变量覆盖 + 运行时热重载
- **生产级健壮性**：优雅关闭 / 重试机制 / Panic 恢复 / 安全中间件

## 技术栈

- 语言：Go 1.25.2
- 依赖：modernc.org/sqlite（纯 Go 免 CGO）/ fsnotify / chromem-go / yaml.v3 / onnxruntime_go（**可选**：仅在 `go build -tags onnx` 时引入）；解析适配器为 30 语言（go/java/ts/py/rust/cpp/c/kotlin/swift/php/csharp/ruby/bash/scala/sql/elixir/ocaml/lua/groovy/css/toml/yaml/protobuf/html/hcl/svelte/markdown/dockerfile/elm/cue）正则启发式，无 CGO 依赖（`-tags treesitter` 可切换真语法树）；ONNX 语义模型支持远程分发（`storage.vector.model_download_url` 自动下载 + SHA-256 校验）
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

> 默认 `go build ./...` 不含 pg/redis 代码；启用对应后端时加 `-tags pg` / `-tags redis` 并拉取驱动依赖即可，详见 `docs/dev/12-存储扩展与大规模迁移路径.md`。

## 多租户（单实例多仓库）

多个项目都需要 CodeSchema 时，**推荐「单实例多租户」**：一个进程同时服务多个隔离的仓库，按 `project` 标识路由，无需为每个项目各起一个进程。

- 每个租户持有**完全独立的** store + 全文/向量/IDF 索引（索引目录默认按各自 store 目录派生，绝不共享），隔离彻底且不修改 `Store` 接口；
- 接入方式统一：`serve` / `mcp` 共用同一份 `--config`（写 `tenants:` 列表）；HTTP 用 `X-Tenant` 头或 `?tenant=`，MCP 工具用 `project` 参数，`list_projects` / `GET /projects` 枚举全部租户；
- 向后兼容：不写 `tenants` 即为单「default」租户，行为与此前完全一致。

可运行示例见 `build/mt-demo.yaml`，设计细节见 [docs/dev/13-多租户设计文档.md](docs/dev/13-多租户设计文档.md)。

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

# MCP 原生 stdio 直连（供仅支持 stdio 的客户端，如 Claude Desktop）
./codeschema mcp --stdio --store ./data

# 查看版本
./codeschema version
```

### 5 分钟接入 AI 客户端（MCP）

1. 启动 MCP Server：`./codeschema mcp --addr :8080`
2. 查看当前端点的客户端配置：`./codeschema mcp --print-config`（或见 `docs/MCP接入指南.md`）
3. 在 VS Code / JetBrains / Claude Code / Cursor 中粘贴对应配置片段，即可调用
   `search_symbols` / `context` / `impact` 等 12 个 MCP 工具。

### Docker 部署

```bash
# 构建镜像（默认免 CGO 纯 Go；模块代理默认 goproxy.cn，国内可复现构建）
docker build -t codeschema:latest .

# 运行
docker run -p 8081:8081 -v ./data:/app/data codeschema:latest
```

## 开发指南

开发文档按阶段分割在 `docs/dev/` 目录下，请按编号顺序阅读：

```
docs/dev/
├── 00-项目概述与架构概览.md      ← 先看整体
├── 01-数据模型与DDL.md            ← 定义数据模型
├── 02-解析适配中间层.md           ← 核心接口定义
├── 03-存储层实现.md               ← 存储层基础实现
├── 04-增量更新与文件监听.md       ← 增量更新逻辑
├── 05-接口层（CLI+HTTP+MCP）.md   ← 接口层实现
├── 06-编排层与并发模型.md         ← 编排层实现
├── 07-适配器实现指南.md           ← 适配器实现
├── 08-测试关联与AI增强.md         ← 测试关联 + AI
├── 09-语义检索与全文搜索.md       ← 语义检索
├── 10-可观测性与安全设计.md       ← 可观测性 + 安全
├── 11-配置部署与路线图.md         ← 配置、部署、路线图
└── 12-存储扩展与大规模迁移路径.md ← PG/Redis 扩展、规模决策
```

模块级文档（按 P1~P9 分层拆解，含完成度/阻塞项/模块关系）见 `docs/modules/` 总览：[README.md](./docs/modules/README.md)。

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

- **多租户（单实例多仓库）已落地**：新增 `internal/tenant`（管理器 + 路由）与 `internal/runtime`（单租户运行期装配），`serve` / `mcp` 通过一份 `--config` 的 `tenants:` 列表同时服务多个隔离仓库；每租户独立 store + 独立 FTS/向量/IDF 索引（默认按各自 `storage.dsn` 目录派生隔离）。HTTP 用 `X-Tenant`/`?tenant=`，MCP 工具用 `project` 参数，`list_projects` / `GET /projects` 枚举租户；无 `tenants` 配置时退化为单「default」租户，完全向后兼容。设计见 [docs/dev/13-多租户设计文档.md](docs/dev/13-多租户设计文档.md)，可运行示例见 `build/mt-demo.yaml`。
- **SQLite 权威存储已接线**：新增 `internal/store/sqlite`（基于纯 Go 的 `modernc.org/sqlite`，免 CGO），完整实现 `store.Store` 接口（文件/类/方法/调用/标签 + 反向查询 + `UpsertIR` 增量入库），`storage.driver=sqlite` 即启用，默认仍 JSON 文件存储作 fallback。消除了「文档声称 SQLite、现实仅 JSON」的实现落差。
- **SCIP / LSP 适配器生产验证**：
  - SCIP：新增真实 fixture 端到端测试，覆盖 class/method/**调用关系提取**逻辑，并修复 `ParseAll` 误用「文件存在」校验目录导致目录永远判为不存在的 Bug。
  - LSP：`gopls` 真实语言服务器端到端验证（Go 为主语言，真实返回 `Calculator` 类与 `Add`/`Sub` 方法，`TestLSPAdapter_RealGopls` 已 PASS）；`clangd` 工程上下文真实验证（构造 compile_commands.json 最小工程，clangd 22 真实提取类/方法，`TestLSPAdapter_RealClangd` 已 PASS）；mock 服务器已覆盖 JSON-RPC 传输/超时/取消/多行头/稳定性。并修复两处生产缺陷：`SymbolKind` 映射漏掉 Go 的 Struct(23)/Interface(24)/Function(12) 导致 gopls 返回 0 类；`jsonRPCRequest.ID` 缺 `omitempty` 使 notification 携带 `"id":0` 违反 JSON-RPC 2.0 导致 clangd 拒绝登记文档。
  - 多语言验证/基准框架见 `internal/adapterbench/adapter_validation_test.go`（独立轻量包，仅依赖 lsp/scip 适配器、不引入 onnxruntime 等 cgo 重型依赖，秒级编译运行），输出 `build/adapter-bench.json` 与 `analysis/2026-08-14-adapter-validation.md`；工具缺失则优雅跳过。
- **GitHub Actions CI 已修复（2026-08-16）**：CI 此前因 Node 20 actions 缓存 tar 恢复失败而中断，现已将 `actions/checkout@v7`、`actions/setup-go@v7`、`actions/upload-artifact@v6`、`docker/*-action@v4/v6/v7`、`softprops/action-gh-release@v3` 升级为 Node 24 兼容版本并加固 Windows 任务，8 个 Job（test 跨 ubuntu/macos/windows + bench + nightly-scale + race + treesitter + cross + docker）全部修复通过；同时默认嵌入模型对齐为 `bge-small-zh-v1.5`（离线命中本地 ONNX，语义检索精度恢复到 R@1 1.00）。

## 实际核查备注（2026-08-14）

> 以下为代码级核查结论，供接手/评审参考。详细论证见 `docs/dev/12-存储扩展与大规模迁移路径.md` 与 `DEV_PROGRESS.md`。

- **包数量**：实际 **32** 个 Go 包（`go list ./...`，2026-08-17 实测），本文及 `DEV_PROGRESS.md` 中「23/24/27/31 个包」等旧表述已过时。
- **默认构建已免 CGO（已修复）**：原 `embedder_onnx.go` 无条件 `import onnxruntime_go` 导致 `go build ./...` 强制需 gcc。现已将 ONNX 嵌入器用 `//go:build onnx` 隔离，默认构建免 CGO/gcc；仅 `go build -tags onnx` 才引入 ONNX 语义检索（仍需 gcc 与 onnxruntime 动态库）。
- **SQLite 批量写入已优化**：`BulkUpsert`（`internal/store`）修复单条 upsert 慢 500 倍的瓶颈，N=10万 级批量写入降至 5~14s（见 `docs/dev/12` 与 `analysis/2026-08-14-scale-bench.md`）；超大仓写入走 `BulkUpsert`/PG/chromem。
- **存在但未在本文登记的代码**：`internal/store/pg`（PG 完整实现，564 行，`//go:build pg`）、`internal/store/redis`（热点缓存层，117 行，`//go:build redis`）、`internal/scalebench`（超大仓基准）此前均未接主路。现 PG/Redis 已通过 `cmd/codeschema` 层 build-tagged 统一分发接入主路，`internal/scalebench` 新增 `BenchmarkScaleBulk`（N=1万）与 `BenchmarkSQLiteWALConfigs`（WAL 同步参数定案）固化进 CI（`.github/workflows/ci.yml` 新增 bench job）看护 `BulkUpsert` 回归，详见 `docs/dev/12`。
- **开发文档索引**：`docs/dev/` 实际含 `00`–`12` 共 13 篇，本文「开发指南」已全部列出；模块级文档（P1~P9 分层拆解，含完成度/阻塞项/模块关系）见 `docs/modules/`。

## 测试

```bash
# 运行全部测试
make test

# 性能基准测试（含 internal/scalebench 的 BulkUpsert 回归看护 BenchmarkScaleBulk）
make bench
```

当前全部包通过测试，0 失败（含 -race 竞态检测）。

## 环境要求

| 依赖 | 最低版本 | 说明 |
|------|---------|------|
| Go | 1.25+ | 编译运行 |
| GCC/MinGW | 任一 C 编译器 | **仅 ONNX 语义检索需要**：默认 `go build ./...` 已免 CGO/gcc。`go build -tags onnx` 启用 ONNX 嵌入器（bge-small-zh-v1.5）时需 gcc 与 onnxruntime 动态库；不使用 ONNX 时纯 Go 构建即可（modernc.org/sqlite 亦为纯 Go）。 |
| Docker | 24+ | 容器化部署 |
| onnxruntime | 1.23.2（本机已验证） | 可选，ONNX 模型语义检索加速（需 `onnxruntime.dll` / `.so` / `.dylib`；本机 x86_64 经 `third_party/onnxruntime_go_patch` 适配 API v23，真实嵌入推理 dim=512 已验证） |
| bge-small-zh-v1.5 | — | 可选，ONNX 语义嵌入模型（FP16 量化，~47MB，自动降级到 LocalEmbedder） |

## 许可证

Apache-2.0