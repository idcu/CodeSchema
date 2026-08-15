# CodeSchema MCP 接入指南

> 将 CodeSchema MCP Server 接入主流 AI 编程客户端，5 分钟内可用。
> 也可直接运行 `codeschema mcp --print-config` 输出当前端点的配置片段（无需查阅本文）。

## 0. 启动 MCP Server

```bash
# 构建
make build            # 或 go build -o codeschema ./cmd/codeschema

# 启动 MCP Server（默认 :8080；生产环境务必加 --auth-token）
./codeschema mcp --addr :8080

# 如需认证
./codeschema mcp --addr :8080 --auth-token <token>
```

SSE 端点为 `http://<host>:8080/sse`。确认监听：`curl -s http://localhost:8080/sse`（返回 200/SSE 流即就绪；MCP Server 仅注册 `/sse`、`/message` 两个路由，无 `/health`/`/healthz` 端点）。

## 1. VS Code

项目根 `.vscode/mcp.json`：

```json
{
  "servers": {
    "codeschema": {
      "type": "sse",
      "url": "http://localhost:8080/sse"
    }
  }
}
```

启用认证时，客户端配置加 Header：`Authorization: Bearer <token>`。

## 2. JetBrains IDEs（IntelliJ / GoLand / PyCharm）

Settings → Tools → MCP → 添加：

```json
{
  "mcpServers": {
    "codeschema": {
      "url": "http://localhost:8080/sse"
    }
  }
}
```

## 3. Claude Code

```bash
claude mcp add codeschema --transport http http://localhost:8080/sse
# 或编辑 ~/.claude.json 的 mcpServers 段：
# { "mcpServers": { "codeschema": { "url": "http://localhost:8080/sse" } } }
```

## 4. Cursor

项目根 `.cursor/mcp.json`：

```json
{
  "mcpServers": {
    "codeschema": {
      "url": "http://localhost:8080/sse"
    }
  }
}
```

## 5. 仅支持 stdio 的客户端（npx mcp-remote 桥接）

```json
{
  "mcpServers": {
    "codeschema": {
      "command": "npx",
      "args": ["mcp-remote", "http://localhost:8080/sse"]
    }
  }
}
```

## 6. 原生 stdio 直连（无需 SSE/HTTP）

客户端可直接以子进程方式连接（`codeschema mcp --stdio`，LSP 风格 Content-Length 帧）：

```json
{
  "mcpServers": {
    "codeschema": {
      "command": "codeschema",
      "args": ["mcp", "--stdio", "--store", "/path/to/data"]
    }
  }
}
```

> 注意：stdio 模式会打印索引构建日志到 stderr（不影响 stdout 协议帧）；首次启动会先全量构建索引。

## 可用工具一览（11 个）

`context`（精准裁剪）· `impact`（影响面）· `tests`（关联单测）· `affected`（受影响方法）·
`get_call_graph`（调用图）· `search_config`（配置检索）· `find_dependencies`（依赖查询）·
`search_symbols`（双路检索）· `get_tags` · `search_by_tag` · `get_all_tags`（六类标签）

## 备注

- 端口可在 `config.yaml` 的 `server.mcp_addr` 或 `--addr` 覆盖。
- 生产环境务必加 `--auth-token` 并配合反向代理 TLS。
- 语义检索质量敏感场景：`-tags onnx` 构建 + 模型分发后 `search_symbols` 召回率显著提升
  （见 docs/dev/09）。
