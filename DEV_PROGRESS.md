# CodeSchema 开发进度跟踪

> 更新时间：2026-08-13 13:05
> 当前阶段：P0 MVP — 接口层（CLI + HTTP + MCP）完成
> 下一个阶段：P0 MVP — 适配器实现（tree-sitter + CodeGraph 直读）

---

## 当前状态

```
P0 骨架 [████████████████████] 100%
P0 MVP   [████████████████····] 80%
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

### P0 MVP — 接口层（CLI + HTTP + MCP）（第 5 步）
- [x] `internal/service/service.go` — Service 业务逻辑层（8 个查询方法 + 参数校验 + ServiceError）
- [x] `internal/server/http.go` — HTTP API 服务器（5 个端点 + 错误中间件 + CORS + panic recovery）
- [x] `internal/server/mcp.go` — MCP Server（JSON-RPC 2.0 + SSE 传输，8 个工具注册）
- [x] `cmd/codeschema/main.go` — `mcp` 和 `serve` 命令接入真实服务器
- [x] 测试：service_test.go（14 个）、http_test.go（11 个）、mcp_test.go（9 个）

### P0 MVP — 增量更新与文件监听（第 4 步 + 第 6 步部分）
- [x] `internal/scanner/hash.go` — SHA-256 哈希闸门函数
- [x] `internal/scanner/scanner.go` — Scanner 核心（ProcessFile + ScanAll + listFiles + detectLang + countLines）
- [x] `internal/scanner/hash_test.go` — 哈希单测（空文件/一致性/不同内容/文件不存在）
- [x] `internal/scanner/scanner_test.go` — Scanner 单测（哈希命中/未命中/全量扫描/忽略目录）
- [x] `internal/store/upsert.go` — 行号区间匹配算法（intervalsOverlap + matchEntity）
- [x] `internal/store/upsert_test.go` — upsert 单测（区间重叠 10 场景 + 匹配判定 6 场景）
- [x] `internal/scheduler/scheduler.go` — 防抖调度器（300ms 防抖窗口 + 队列阈值 1000 + 降级信号）
- [x] `internal/scheduler/scheduler_test.go` — 调度器单测（防抖合并/刷新/降级/事件处理/清空）
- [x] `internal/watcher/watcher.go` — 轮询监听器（PollWatcher，mtime+size 检测变更）
- [x] `internal/watcher/watcher_test.go` — 监听器单测（新文件检测/忽略目录）

### 文档
- [x] `docs/dev/` — 12 个开发文档按开发顺序分割
- [x] `DEV_PROGRESS.md` — 本文件，开发进度跟踪

## 下一步工作

### P0 MVP — 可用系统（估计仍需 1-2 周）

| 优先级 | 任务 | 模块 | 依赖 | 估计工时 |
|--------|------|------|------|---------|
| P0 | tree-sitter 适配器骨架 | `internal/parser/adapter/treesitter` | parser | 3 天 |
| P0 | CodeGraph 直读适配器骨架 | `internal/parser/adapter/codegraph` | parser | 2 天 |
| P1 | 集成测试 | 全局 | 全部 | 2 天 |

### 详细任务说明

#### 1. 适配器（`internal/parser/adapter/`）
- tree-sitter 适配器骨架（实现 ParserPlugin 接口）
- CodeGraph SQLite 直读适配器骨架
- 降级路径测试
- 参考文档：`docs/dev/07-适配器实现指南.md`

## 已知问题

1. **网络不可用**：无法下载 `mattn/go-sqlite3` 等外部包。当前使用纯 Go 文件存储（FileStore），SQLite 实现作为 DDL 参考保留。待网络恢复后，可切换为 SQLite 存储。
2. **轮询监听性能**：当前 PollWatcher 基于轮询（1s 间隔），适合开发/小仓库场景。生产环境建议切换为 fsnotify 原生监听（需安装外部包）。
3. **tree-sitter C 绑定**：tree-sitter 适配器需要 CGO 和 tree-sitter C 运行时，需单独安装。

## 接手说明

1. 阅读 `docs/dev/00-项目概述与架构概览.md` 了解整体架构
2. 按 `docs/dev/` 编号顺序阅读对应开发文档
3. 当前 P0 MVP 已完成增量更新与文件监听模块 + 接口层（HTTP API + MCP Server），代码可编译运行：`go build ./cmd/codeschema`
4. 运行测试：`go test ./...`
5. 启动 HTTP API：`codeschema serve --http :8081`
6. 启动 MCP Server：`codeschema mcp --addr :8080`
7. 下一步开发：实现 `internal/parser/adapter/` 中的适配器（tree-sitter 和 CodeGraph 直读）
8. 当前提交：`c743aaa`（P0 MVP 增量更新）、`4a64b7c`（P0 骨架）