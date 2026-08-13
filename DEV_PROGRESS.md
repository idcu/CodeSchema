# CodeSchema 开发进度跟踪

> 更新时间：2026-08-13 10:15
> 当前阶段：P0 骨架 — 完成
> 下一个阶段：P0 MVP — 等待开始

---

## 当前状态

```
P0 骨架 [████████████████████] 100%
P0 MVP   [····················] 0%
P1       [····················] 0%
P2       [····················] 0%
P3       [····················] 0%
```

## 已完成工作

### 基础文件
- [x] `.gitignore` — 覆盖 Go 二进制/IDE/OS/数据文件
- [x] `README.md` — 系统定位、核心能力、开发指南、阶段路线
- [x] `go.mod` — Go 模块初始化（codeschema）
- [x] `CHANGELOG.internal.md` — 内部追溯日志

### P0 骨架 — 项目骨架 + 核心接口 + 存储层
- [x] 项目目录结构（cmd/ + internal/ 共 14 个包）
- [x] `internal/errors/errors.go` — 错误类型体系（适配器/AI/存储/通用）
- [x] `internal/parser/ir.go` — IRDocument + ClassIR/MethodIR/ParamIR/CallIR 结构体
- [x] `internal/parser/plugin.go` — ParserPlugin + BatchParser 接口定义
- [x] `internal/parser/registry.go` — Registry 注册中心（Register/SetPriority/Select）
- [x] `internal/parser/registry_test.go` — Registry 单元测试（优先级/降级/BatchParser）
- [x] `internal/store/store.go` — Store 统一接口定义
- [x] `internal/store/filestore.go` — FileStore 纯 Go 文件存储实现（JSON 持久化）
- [x] `internal/store/filestore_test.go` — FileStore 单元测试（CRUD/UpsertIR/持久化）
- [x] `internal/store/migrations/001_init.sql` — 完整 DDL（12 张表 + 索引）
- [x] `cmd/codeschema/main.go` — 主入口（scan/watch/mcp/serve/version 命令框架）

### 文档
- [x] `docs/dev/` — 12 个开发文档按开发顺序分割
- [x] `DEV_PROGRESS.md` — 本文件，开发进度跟踪

## 下一步工作

### P0 MVP — 可用系统（估计 3-5 周）

| 优先级 | 任务 | 模块 | 依赖 | 估计工时 |
|--------|------|------|------|---------|
| P0 | 增量更新与文件监听 | `internal/watcher` | 无 | 2 天 |
| P0 | 编排层（Scanner） | `internal/scanner` | watcher, store | 2 天 |
| P0 | MCP Server 基础框架 | `internal/server` | 无 | 3 天 |
| P0 | HTTP API 基础框架 | `internal/server` | 无 | 2 天 |
| P1 | tree-sitter 适配器骨架 | `internal/parser/adapter/treesitter` | parser | 3 天 |
| P1 | CodeGraph 直读适配器骨架 | `internal/parser/adapter/codegraph` | parser | 2 天 |
| P1 | 服务层（Service） | `internal/service` | store | 3 天 |
| P2 | 集成测试 | 全局 | 全部 | 2 天 |

### 详细任务说明

#### 1. 增量更新与文件监听（`internal/watcher`）
- 实现 fsnotify 文件监听（需安装 `github.com/fsnotify/fsnotify`）
- 300ms 防抖窗口合并
- 目录过滤（忽略 .git/ node_modules/ 等）
- 队列阈值 1000 触发全量扫描降级

#### 2. 编排层（`internal/scanner`）
- Scanner 实现：全量扫描 worker pool（默认 4 个 worker）
- ProcessFile：SHA-256 哈希闸门 → 选择适配器 → 解析 → upsertIR
- Scheduler：定时/事件驱动的扫描调度

#### 3. MCP Server（`internal/server`）
- 实现 MCP 协议基础框架
- 注册 8-10 个工具（context/impact/tests/affected/get_call_graph 等）
- 命名对齐 CodeGraph / JCodeIndexer 事实标准

#### 4. HTTP API（`internal/server`）
- 使用标准库 `net/http`
- 实现 /context, /impact, /tests, /search, /health 端点
- 错误响应格式 + 错误码定义

#### 5. 适配器（`internal/parser/adapter/`）
- tree-sitter 适配器骨架（实现 ParserPlugin 接口）
- CodeGraph SQLite 直读适配器骨架
- 降级路径测试

## 已知问题

1. **网络不可用**：无法下载 `mattn/go-sqlite3` 等外部包。当前使用纯 Go 文件存储（FileStore），SQLite 实现作为 DDL 参考保留。待网络恢复后，可切换为 SQLite 存储。
2. **外部依赖待安装**：P0 MVP 阶段需要安装 `fsnotify` 等外部包，需网络连接。
3. **tree-sitter C 绑定**：tree-sitter 适配器需要 CGO 和 tree-sitter C 运行时，需单独安装。

## 接手说明

1. 阅读 `docs/dev/00-项目概述与架构概览.md` 了解整体架构
2. 按 `docs/dev/` 编号顺序阅读对应开发文档
3. 当前 P0 骨架已完成，代码可编译运行：`go build ./cmd/codeschema`
4. 运行测试：`go test ./...`
5. 开始 P0 MVP 阶段开发，首选实现 `internal/watcher` 模块