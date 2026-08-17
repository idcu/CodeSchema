# API 文档

> 写给谁：开发者、技术负责人、AI 编程工具、对接方
> 写什么：HTTP 端点、MCP 12 工具、OpenAPI/Swagger、错误码、鉴权、协议（SSE/stdio）
> 核心原则：接口签名 = 文档事实源；改接口必同步本档
> 优先级：P0
> 最后更新：2026-08-17

---

## 1. 协议概览

- HTTP API：`codeschema serve --http :8081`（RESTful；含 `/openapi.json` + `/docs` Swagger UI、`/viz` 可视化）。
- MCP Server：`codeschema mcp --addr :8080`（SSE，`/sse`、`/message`）；`codeschema mcp --stdio`（原生 stdio，LSP 风格 Content-Length 帧）。
- 多租户：HTTP 用 `X-Tenant` 头或 `?tenant=`；MCP 工具用 `project` 参数；`list_projects`/`GET /projects` 枚举。
- 鉴权：可选 Bearer token（`--auth-token` 或 `server.auth_token`）。

## 2. HTTP 端点

| 端点 | 方法 | 参数 | 说明 |
|---|---|---|---|
| `/health` `/health/db` `/health/vector` `/health/kv` | GET | - | 健康检查（含存储/缓存/向量） |
| `/context` | GET | `symbol`,`context_lines` | 获取符号精准裁剪上下文 |
| `/impact` | GET | `method`,`depth` | 影响面分析 |
| `/tests` | GET | `method`,`min_confidence` | 关联单测 |
| `/search` | GET | `q`,`mode`,`limit` | 双路检索（exact/semantic/both） |
| `/tags` `/tags/search` `/tags/all` | GET | `symbol`/`tag`/- | 标签查询 |
| `/metrics` | GET | - | Prometheus 指标 |
| `/viz` | GET | - | 向量索引可视化（文档浏览/检索/状态） |
| `/openapi.json` `/docs` | GET | - | OpenAPI 规范 + Swagger UI |
| `/projects` | GET | - | 枚举全部租户/仓库 |

> 完整机器可读规范以 `GET /openapi.json` 为准（13 端点）。

## 3. MCP 工具（12 个）

`context`（精准裁剪）· `impact`（影响面）· `tests`（关联单测）· `affected`（受影响方法）·
`get_call_graph`（调用图）· `search_config`（配置检索）· `find_dependencies`（依赖查询）·
`search_symbols`（双路检索）· `get_tags` · `search_by_tag` · `get_all_tags`（六类标签）·
`list_projects`（枚举当前实例服务的全部仓库/租户）。

调用示例：
```json
{ "name": "context", "arguments": { "symbol": "com.example.UserService.login", "context_lines": 10 } }
{ "name": "search_symbols", "arguments": { "q": "用户登录", "mode": "both", "limit": 20 } }
{ "name": "impact", "arguments": { "method": "com.example.UserService.login", "depth": 2 } }
```
多租户下检索类工具额外接受 `project` 参数；省略时路由默认租户（`"default"`）。

## 4. 错误码与边界

- 错误使用 `internal/errors` 体系（适配器/AI/存储/通用 + `errors.ErrBudgetExceeded`、`errors.ErrEnhanceFailed` 等）。
- MCP 传输遵循 JSON-RPC 2.0：请求必须携带合法 id；notification 不得携带 `id`（LSP JSON-RPC `omitempty` 约束，clangd 严格拒绝违反者）。
- HTTP 鉴权失败返回标准错误；路径遍历由中间件拦截。
- 静默空结果模式被禁止（见代码规范）。

## 5. 数据结构与检索模式

- `mode` 取值：`exact`（仅 FTS）、`semantic`（仅语义）、`both`（双路融合重排）。
- 六类标签 `layer / biz / tech / risk / test / lang` 由 Tagger 自动推导；测试关联五策略（naming / same_tag / dependency / explicit `@TestFor` / coverage）。

## 6. 修订记录

| 日期 | 说明 |
|---|---|
| 2026-08-17 | 从 DEPLOYMENT_AND_USAGE/接口层文档提炼，补 /openapi、/viz、/projects、错误码 |

---

> 迁移备注：旧 `docs/DEPLOYMENT_AND_USAGE.md` API 段、`docs/MCP接入指南.md` 的协议细节与此一致；客户端接入配置见 [客户端接入指南](../3-使用层/客户端接入指南（MCP）.md)。