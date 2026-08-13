# CHANGELOG.internal.md

> 内部追溯日志，不对外发布。记录每次提交的核心改动、验证数据、遗留 TODO。

---

## 提交记录

### Commit 4: feat(service, server): P0 MVP — Service 服务层 + HTTP API + MCP Server

**Commit Hash**: `b68c206`

**核心改动点**：
- `internal/service/service.go` — Service 业务逻辑层，封装 Store 操作，提供 GetContext/GetImpact/GetTests/Search 等 8 个查询方法，参数校验返回 ServiceError
- `internal/server/http.go` — HTTP API 服务器（5 个端点 + 错误中间件 + CORS + panic recovery）
- `internal/server/mcp.go` — MCP Server（JSON-RPC 2.0 + SSE 传输，8 个工具注册，全部对接 Service 层）
- `cmd/codeschema/main.go` — `mcp` 和 `serve` 命令接入真实服务器实现
- 测试：service_test.go（14 个）、http_test.go（11 个）、mcp_test.go（9 个）

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（8 个包，0 失败）
- 测试覆盖：Service 14 个测试（含参数校验 + 错误码映射）、HTTP 11 个测试（含 CORS + panic recovery）、MCP 9 个测试（含全部 8 个工具调用 + 错误场景）

**遗留 TODO / 风险**：
- Service 层当前返回 P0 占位数据，P1 接入真实 Store 查询
- MCP Server 的 SSE 传输为简化实现，P1 可按 MCP 规范完善
- HTTP API 缺少速率限制中间件（P2）

### Commit 3: feat(scanner, scheduler, watcher): P0 MVP — 增量更新与文件监听

**Commit Hash**: `c743aaa`

**核心改动点**：
- `internal/scanner/hash.go` — SHA-256 哈希闸门函数
- `internal/scanner/scanner.go` — Scanner 核心（ProcessFile + ScanAll + listFiles + detectLang + countLines）
- `internal/store/upsert.go` — 行号区间匹配算法（intervalsOverlap + matchEntity，覆盖 UPDATE/DELETE/INSERT/重定位）
- `internal/scheduler/scheduler.go` — 防抖调度器（300ms 窗口 + 队列阈值 1000 + 降级信号）
- `internal/watcher/watcher.go` — PollWatcher 轮询监听器（mtime+size 检测变更，忽略 .git/node_modules 等）
- 测试文件：hash_test.go（5 个）、scanner_test.go（4 个）、upsert_test.go（2 组 16 场景）、scheduler_test.go（7 个）、watcher_test.go（2 个）
- 更新 DEV_PROGRESS.md、docs/dev/04-增量更新与文件监听.md、docs/dev/06-编排层与并发模型.md 完成标准

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（parser/scanner/scheduler/store/watcher）
- 测试覆盖：哈希闸门命中/未命中、全量扫描 worker pool、防抖合并/刷新/降级、区间重叠 10 场景、匹配判定 6 场景

**遗留 TODO / 风险**：
- 反向引用增量更新尚未实现（P1）
- 大文件旁路尚未实现（P1）
- upsertIR 级联清理依赖 SQLite 实现（P1）
- PollWatcher 轮询监听性能有限，生产环境建议切换 fsnotify

### Commit 2: perf(rename): 二进制名统一为 codeschema，与项目名对齐

**Commit Hash**: `4a64b7c`

**核心改动点**：
- 重命名 `cmd/codameta/` → `cmd/codeschema/`，二进制名从 `codameta` 统一为 `codeschema`
- 更新 `main.go` 中所有用法提示文本和错误信息（`codameta` → `codeschema`）
- 更新 `.gitignore` 中的二进制名（`codameta` → `codeschema`，`codameta.exe` → `codeschema.exe`）
- 更新文档全项目中的 `codameta` 引用为 `codeschema`：
  - `README.md`：快速开始中的构建和运行命令
  - `DEV_PROGRESS.md`：文件路径引用和编译命令
  - `docs/dev/00-项目概述与架构概览.md`：模块依赖图和完成标准
  - `docs/dev/04-增量更新与文件监听.md`：watch 命令示例
  - `docs/dev/05-接口层（CLI+HTTP+MCP）.md`：CLI 命令表示例、开发指南、完成标准
  - `docs/dev/11-配置部署与路线图.md`：配置模型 DSN 和 benchmark 命令

**验证数据**：
- `grep -r codameta` 全项目检查：0 处残留
- `go build ./cmd/codeschema` 编译通过
- `codeschema version` 输出 `CodeSchema v0.1.0` 正常

**遗留 TODO / 风险**：
- 无

---

### Commit 1: docs(dev): 全维度文档分析评估与开发文档分割

**Commit Hash**: `c0d3ec0`

**核心改动点**：
- 对原始文档 `代码元数据KV_DB系统_开发文档.md`（1270行，v7版）进行全维度分析评估
- 识别出 5 类问题：单一巨型文档、缺乏开发顺序引导、缺少前置依赖/完成标准/开发指南、缺少跨文档引用
- 将原始文档按开发顺序分割为 12 个独立文档，存入 `docs/dev/` 目录
- 每个文档增加了"前置依赖"、"完成标准"、"开发指南"三个改善模块
- 文档间建立交叉引用关系，形成完整开发文档体系

**分割文档清单**：

| 序号 | 文件名 | 开发顺序 | 对应原始章节 |
|------|--------|----------|-------------|
| 00 | 00-项目概述与架构概览.md | 第 0 步 | §1, §2 |
| 01 | 01-数据模型与DDL.md | 第 1 步 | §4 |
| 02 | 02-解析适配中间层.md | 第 2 步 | §3 |
| 03 | 03-存储层实现.md | 第 3 步 | §8, §9 |
| 04 | 04-增量更新与文件监听.md | 第 4 步 | §5 |
| 05 | 05-接口层（CLI+HTTP+MCP）.md | 第 5 步 | §12 |
| 06 | 06-编排层与并发模型.md | 第 6 步 | §9 |
| 07 | 07-适配器实现指南.md | 第 7 步 | §3.4, §3.5, §3.7 |
| 08 | 08-测试关联与AI增强.md | 第 8 步 | §6, §7 |
| 09 | 09-语义检索与全文搜索.md | 第 9 步 | §8.3, §8.4 |
| 10 | 10-可观测性与安全设计.md | 第 10 步 | §10, §11 |
| 11 | 11-配置部署与路线图.md | 第 11 步 | §14, §15, 附录 |

**验证数据**：
- 原始文档：1 个文件，1270 行，15 章 + 5 附录
- 改善后：12 个文件，每文件约 200-400 行，覆盖全部原始内容
- 新增改善模块：前置依赖 12 处、完成标准 12 处、开发指南 12 处
- 跨文档引用：约 30+ 处交叉引用关系

**遗留 TODO / 风险**：
- 原始文档 `代码元数据KV_DB系统_开发文档.md` 保留在根目录，建议后续合并到 `docs/dev/00` 或建立索引
- 文档内容尚未经过实际开发验证，需在编码过程中根据实际情况调整
- 建议后续在 `docs/dev/` 下增加 README.md 作为开发文档总索引

---

## 文档版本对照

| 版本 | 文件数 | 总行数 | 说明 |
|------|--------|--------|------|
| v7（原始） | 1 | 1270 | 单一巨型文档 |
| v8（本版） | 12 | ~3600 | 按开发顺序分割，增加前置依赖/完成标准/开发指南 |

---

## 文档结构图

```
docs/dev/
├── 00-项目概述与架构概览.md      ← 第 0 步：先看整体
├── 01-数据模型与DDL.md            ← 第 1 步：先定义数据模型
├── 02-解析适配中间层.md           ← 第 2 步：核心接口定义
├── 03-存储层实现.md               ← 第 3 步：存储层基础实现
├── 04-增量更新与文件监听.md       ← 第 4 步：增量更新逻辑
├── 05-接口层（CLI+HTTP+MCP）.md   ← 第 5 步：接口层实现
├── 06-编排层与并发模型.md         ← 第 6 步：编排层实现
├── 07-适配器实现指南.md           ← 第 7 步：适配器实现
├── 08-测试关联与AI增强.md         ← 第 8 步：测试关联 + AI
├── 09-语义检索与全文搜索.md       ← 第 9 步：语义检索
├── 10-可观测性与安全设计.md       ← 第 10 步：可观测性 + 安全
└── 11-配置部署与路线图.md         ← 第 11 步：配置、部署、路线图
```