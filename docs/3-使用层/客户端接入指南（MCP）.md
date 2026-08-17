# 客户端接入指南（MCP）

> 写给谁：使用 CodeSchema 服务 AI 的开发人员 / 运营
> 写什么：将 CodeSchema MCP Server 接入主流 AI 编程客户端，5 分钟可用
> 核心原则：配置即复制粘贴；`codeschema mcp --print-config` 可一键输出
> 优先级：P0
> 最后更新：2026-08-17

---

## 0. 启动 MCP Server

```bash
make build   # 或 go build -o codeschema ./cmd/codeschema
./codeschema mcp --addr :8080                # 默认 :8080；生产加 --auth-token
```
SSE 端点：`http://<host>:8080/sse`。确认：`curl -s http://localhost:8080/sse`（返回 200/SSE 流即就绪；MCP Server 仅注册 `/sse`、`/message`，无 `/health`）。

> 也可运行 `./codeschema mcp --print-config` 直接输出当前端点的各客户端配置片段。

## 1. VS Code

项目根 `.vscode/mcp.json`：
```json
{ "servers": { "codeschema": { "type": "sse", "url": "http://localhost:8080/sse" } } }
```
启用认证时配置 Header：`Authorization: Bearer <token>`。

## 2. JetBrains IDEs（IntelliJ / GoLand / PyCharm）

Settings → Tools → MCP → 添加：
```json
{ "mcpServers": { "codeschema": { "url": "http://localhost:8080/sse" } } }
```

## 3. Claude Code

```bash
claude mcp add codeschema --transport http http://localhost:8080/sse
# 或编辑 ~/.claude.json 的 mcpServers 段
```

## 4. Cursor

项目根 `.cursor/mcp.json`：
```json
{ "mcpServers": { "codeschema": { "url": "http://localhost:8080/sse" } } }
```

## 5. 仅支持 stdio 的客户端（npx mcp-remote 桥接）

```json
{ "mcpServers": { "codeschema": { "command": "npx", "args": ["mcp-remote", "http://localhost:8080/sse"] } } }
```

## 6. 原生 stdio 直连（无需 SSE/HTTP）

```json
{ "mcpServers": { "codeschema": { "command": "codeschema", "args": ["mcp", "--stdio", "--store", "/path/to/data"] } } }
```
> stdio 模式会打印索引构建日志到 stderr（不影响 stdout 协议帧）；首次启动先全量构建索引。

## 7. 可用工具（12 个）与多租户

`context` · `impact` · `tests` · `affected` · `get_call_graph` · `search_config` · `find_dependencies` · `search_symbols` · `get_tags` · `search_by_tag` · `get_all_tags` · `list_projects`。

`context` 支持 `mode` 参数：`full`（默认，注入源码）／`minimal`（仅符号元数据，token 评测基线）。每次 `context` / `impact` 注入附 `_trace` 追溯（来源/裁剪原因/估算 token），便于复盘。

多租户下 11 个检索类工具额外接受 `project` 参数指定目标仓库；省略时路由默认租户（`"default"`）。`list_projects` 返回当前实例所有租户元信息。

> 对接 DeepSeek Harness（dsh）：见 [`contrib/dsh`](../../../contrib/dsh/README.md) 集成指南（stdio/SSE 两种方式 + 配置模板）。

## 8. 验证连通

- `curl http://localhost:8081/health`（HTTP Server :8081）。
- `curl "http://localhost:8081/search?q=UserService&mode=both&limit=5"`。
- 在 AI 工具中提问，观察是否自动调用 `context`/`search_symbols`（出现代码上下文即连通）。

## 9. 修订记录

| 日期 | 说明 |
|---|---|
| 2026-08-17 | 自 docs/MCP接入指南.md 按圈层归位改进 |
| 2026-08-17 | 补 context 的 mode 参数与 _trace 追溯说明；新增 dsh 集成指引 |