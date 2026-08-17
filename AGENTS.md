# CodeSchema — Agent 接入指南

> 本文件面向 AI 编程工具（Agent），告知其如何参与 CodeSchema 项目的开发协作。
> 编写格式兼容 DeepSeek Harness AGENTS.md 约定。
> 最后更新：2026-08-17

## 1. 项目定位

CodeSchema 是一个**代码上下文供给引擎**，为 AI Agent 提供精准裁剪的代码理解上下文（符号、调用图、影响面、关联单测），以减少 Agent 的工具调用轮次和 token 消耗。

核心价值：**结构图 × 语义检索 × 标签体系 × 测试关联 × 多租户隔离**。

## 2. 开发流程

Agent 在参与 CodeSchema 开发时必须遵循以下流程。完整规范见 [docs/AI协作规范.md](docs/AI协作规范.md)。

### 2.1 读取阶段（读档顺序）

1. `docs/README.md` — 系统定位、技术栈、目录导航
2. `docs/1-生产层/技术设计文档.md` — 架构、模块划分、数据流
3. `docs/1-生产层/代码规范与开发指南.md` — 目录、命名、commit 规范
4. `DEV_PROGRESS.md` / `CHANGELOG.internal.md` — 近期改动、历史坑
5. 当前任务卡 — 目标、验收标准

> 完成读取前禁止写代码。

### 2.2 编码阶段

- 遵循 Go 惯例与代码规范
- 新增模块/接口/表结构 → 先对照现有设计，不重复造轮子
- 破坏性变更（改接口签名、改表、改配置、改 CLI 行为）必须先标注影响范围，等待评审
- **零造假红线**：不做无数据支撑的性能结论；改动必跑对应测试/基准

### 2.3 文档同步（改码必改档）

代码变更后必须同步更新受影响的文档。变更→文档映射表见 [docs/AI协作规范.md §4](docs/AI协作规范.md#4-完成后--同步更新文档改码必改档)。

**文档同步性核验清单**（commit 前逐项过）：
1. 列出本次改动影响的文档，缺一不可
2. grep 核查旧路径/旧数字/旧版本号是否残留
3. 新增/删除文档或目录 → 同步更新 `docs/README.md` 导航
4. 被改文档页首「最后更新」日期刷新
5. 与代码**同次提交**，Body 带验证数据

### 2.4 提交规范（Conventional Commits）

- 类型：`feat / fix / docs / style / refactor / perf / test / chore`
- 示例：`feat(user): 新增批量导出`
- Body 携带关键验证数据（如 `heapUsed +2.20MB / P95 <50ms`）
- 禁止水 commit（如 `fix bug`、`update`）
- 仅 `git commit`；除非明确说 push，否则不 push

## 3. 可用接口

本系统提供 MCP 协议（SSE）和 HTTP API 两种接入方式。

### MCP 工具（12 个）

| 工具 | 说明 |
|---|---|
| `context` | 获取指定符号的精准裁剪上下文 |
| `impact` | 分析指定方法的调用影响面 |
| `tests` | 查询指定方法的关联单测 |
| `affected` | 递归查找受影响的测试 |
| `get_call_graph` | 获取双向调用图 |
| `search_symbols` | 搜索符号（精确/语义/融合） |
| `search_config` | 搜索配置项 |
| `find_dependencies` | 查找依赖关系 |
| `get_tags` / `search_by_tag` / `get_all_tags` | 标签查询 |
| `list_projects` | 枚举多租户仓库 |

### HTTP 端点（14+）

`/health*` / `/context` / `/impact` / `/tests` / `/search` / `/tags*` / `/projects` / `/metrics` / `/openapi.json` / `/docs`

## 4. 约束与安全

- 默认 `:8080`（MCP）和 `:8081`（HTTP）全接口绑定，部署时需将监听地址改为 `127.0.0.1:<port>` 或经 Nginx 反代
- 认证：`server.auth_token` 配置 Bearer token
- 限流：`server.rate_limit` 配置每分钟请求上限（0 不限流）
- 存储：`internal/fsperm` 统一目录 0700 / 文件 0600 权限