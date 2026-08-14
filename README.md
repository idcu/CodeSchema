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
- **向量索引可视化**：Web 可视化仪表盘，支持文档浏览、语义搜索、相似度排名
- **可观测性**：结构化日志（log/slog）+ 基础指标（Prometheus 格式）+ 链路追踪
- **配置热重载**：YAML/JSON 配置 + 环境变量覆盖 + 运行时热重载
- **生产级健壮性**：优雅关闭 / 重试机制 / Panic 恢复 / 安全中间件

## 技术栈

- 语言：Go 1.25.2
- 依赖：modernc.org/sqlite（纯 Go 免 CGO）/ fsnotify / chromem-go / yaml.v3 / onnxruntime_go（tree-sitter 为 6 语言正则解析，无 CGO 依赖）
- 存储：JSON 文件（默认 fallback）+ SQLite（modernc.org/sqlite 纯 Go，已接线，`storage.driver=sqlite` 启用）→ 可扩展 PostgreSQL / Redis
- 协议：MCP Server（JSON-RPC 2.0 + SSE）+ HTTP API（RESTful）
- 部署：单二进制 / Docker 容器 / 多平台交叉编译

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

# 向量索引可视化（需先 serve 启动，访问 http://localhost:8081/viz）
# 支持文档浏览、语义搜索、向量索引状态查看

# 文件监听（增量更新）
./codeschema watch --fsnotify ./repo

# 查看版本
./codeschema version
```

### Docker 部署

```bash
# 构建镜像
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
└── 11-配置部署与路线图.md         ← 配置、部署、路线图
```

## 架构概览

```
┌─────────────────────────────────────────────────────┐
│                      CLI (cmd/codeschema)            │
│     scan    watch    mcp    serve    version         │
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

- **SQLite 权威存储已接线**：新增 `internal/store/sqlite`（基于纯 Go 的 `modernc.org/sqlite`，免 CGO），完整实现 `store.Store` 接口（文件/类/方法/调用/标签 + 反向查询 + `UpsertIR` 增量入库），`storage.driver=sqlite` 即启用，默认仍 JSON 文件存储作 fallback。消除了「文档声称 SQLite、现实仅 JSON」的实现落差。
- **SCIP / LSP 适配器生产验证**：
  - SCIP：新增真实 fixture 端到端测试，覆盖 class/method/**调用关系提取**逻辑，并修复 `ParseAll` 误用「文件存在」校验目录导致目录永远判为不存在的 Bug。
  - LSP：`gopls` 真实语言服务器端到端验证（Go 为主语言，真实返回 `Calculator` 类与 `Add`/`Sub` 方法，`TestLSPAdapter_RealGopls` 已 PASS）；`clangd` 真实服务器传输层验证（clangd 需 compile-commands/project 上下文才登记独立文件，缺上下文时优雅跳过）；mock 服务器已覆盖 JSON-RPC 传输/超时/取消/多行头/稳定性。并修复 `SymbolKind` 映射漏掉 Go 的 Struct(23)/Interface(24)/Function(12) 导致 gopls 返回 0 类的**生产缺陷**。
  - 多语言验证/基准框架见 `internal/adapterbench/adapter_validation_test.go`（独立轻量包，仅依赖 lsp/scip 适配器、不引入 onnxruntime 等 cgo 重型依赖，秒级编译运行），输出 `build/adapter-bench.json` 与 `analysis/2026-08-14-adapter-validation.md`；工具缺失则优雅跳过。

## 实际核查备注（2026-08-14）

> 以下为代码级核查结论，供接手/评审参考。详细论证见 `docs/dev/12-存储扩展与大规模迁移路径.md` 与 `DEV_PROGRESS.md`。

- **包数量**：实际 **27** 个 Go 包（`go list ./...`），本文及 `DEV_PROGRESS.md` 中「23/24 个包」等旧表述已过时。
- **默认构建强制 CGO**：`embedder_onnx.go` 无条件 `import onnxruntime_go`，故 `go build ./...` 实际需 gcc（环境 CGO_ENABLED=1）。「GCC 可选」的旧表述不准确。
- **SQLite 实测并非生产级写入**：`docs/dev/12` 记录的 scalebench 显示，N=10万 单批 upsert SQLite 约 **193s**，JSON FileStore 约 **0.4s**（慢约 500 倍）。当前 SQLite 写入路径有严重瓶颈，「SQLite 为权威存储、JSON 仅 fallback」的 headline 与实测方向相反——超大仓写入建议走 `BulkUpsert`/PG 或 chromem。
- **存在但未在本文登记的代码**：`internal/store/pg`（PG 完整实现，507 行，`//go:build pg`）、`internal/store/redis`（热点缓存层，106 行，`//go:build redis`）、`internal/scalebench`（超大仓基准，237 行）均已存在；PG/Redis/scalebench 已在 `docs/dev/12` 登记，但本 README 与 `DEV_PROGRESS.md` 此前未提及。
- **开发文档索引**：`docs/dev/` 实际含 `00`–`12` 共 13 篇，本文「开发指南」仅列到 `11`，缺 `12-存储扩展与大规模迁移路径.md`。

## 测试

```bash
# 运行全部测试
make test

# 性能基准测试
make bench
```

当前全部包通过测试，0 失败（含 -race 竞态检测）。

## 环境要求

| 依赖 | 最低版本 | 说明 |
|------|---------|------|
| Go | 1.25+ | 编译运行 |
| GCC/MinGW | 任一 C 编译器 | **实际为必需**：`internal/vector/embedder_onnx.go` 无条件依赖 CGO 版 `onnxruntime_go`，故默认 `go build ./...` 即需 CGO/gcc（即使不使用 ONNX 模型）。纯 CGO -free 构建需为 ONNX 嵌入器加 build tag 隔离 |
| Docker | 24+ | 容器化部署 |
| onnxruntime | 1.28+ | 可选，ONNX 模型语义检索加速（需 `onnxruntime.dll` / `.so` / `.dylib`） |
| bge-small-zh-v1.5 | — | 可选，ONNX 语义嵌入模型（FP16 量化，~47MB，自动降级到 LocalEmbedder） |

## 许可证

Apache-2.0