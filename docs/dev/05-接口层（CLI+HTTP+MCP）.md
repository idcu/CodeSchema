# 05 — 接口层（CLI + HTTP + MCP）

> 开发顺序：第 5 步
> 前置依赖：[docs/dev/03-存储层实现.md](03-存储层实现.md)
> 对应原始文档章节：§12 接口层

---

## 1. 概述

接口层提供三种接入方式，面向不同使用场景：

- **CLI**：命令行工具，用于索引扫描、文件监听、运维管理。
- **HTTP API**：RESTful 接口，供脚本/CI/Web 前端调用。
- **MCP Server**：AI Agent 接入协议，工具命名对齐 CodeGraph / JCodeIndexer 事实标准。

---

## 2. CLI 命令定义

| 命令 | 用途 | 示例 |
|---|---|---|
| `scan` | 扫描并入库 | `codeschema scan ./repo --lang go,java` |
| `watch` | 文件监听增量 | `codeschema watch ./repo` |
| `rebuild-kv` | 重建 KV 缓存 | `codeschema rebuild-kv` |
| `rebuild-refs` | 重建反向引用索引 | `codeschema rebuild-refs` |
| `mcp` | 启动 MCP Server | `codeschema mcp --addr :8080` |
| `serve` | 启动 HTTP API Server | `codeschema serve --http :8081` |
| `benchmark` | 运行 benchmark | `codeschema benchmark ./repo` |
| `version` | 显示版本信息 | `codeschema version` |

---

## 3. HTTP API

### 3.1 接口表

| 接口 | 方法 | 用途 | 请求参数 | 响应 |
|---|---|---|---|---|
| `GET /context` | GET | 返回方法源码 ± N 行 + 类字段 + 接口 + 单测路径 | `symbol`（必需，全限定名）、`context_lines`（可选，默认 5） | `{symbol, source, class, tests, impacted_by}` |
| `GET /impact` | GET | 返回上游调用方列表 | `method`（必需）、`depth`（可选，默认 1） | `{method, callers: [{method, depth}], callees: [{method, depth}]}` |
| `GET /tests` | GET | 返回关联单测 | `method`（必需）、`min_confidence`（可选，默认 60） | `{method, tests: [{test_method, strategy, confidence}]}` |
| `GET /search` | GET | 精确 + 语义双路检索 | `q`（必需）、`mode`（可选，`exact`/`semantic`/`both`，默认 `both`）、`limit`（可选，默认 20） | `{results: [{symbol, kind, file, score, snippet}]}` |
| `GET /health` | GET | 健康检查 | — | `{status, db, kv, vector}` |

### 3.2 错误响应格式

```json
{
  "error": {
    "code": "ERR_SYMBOL_NOT_FOUND",
    "message": "symbol 'com.x.OrderService' not found in index"
  }
}
```

### 3.3 错误码定义

| 错误码 | HTTP 状态码 | 说明 |
|---|---|---|
| `ERR_SYMBOL_NOT_FOUND` | 404 | 符号未找到 |
| `ERR_INVALID_PARAMETER` | 400 | 参数校验失败 |
| `ERR_INTERNAL` | 500 | 内部错误 |
| `ERR_RATE_LIMITED` | 429 | 请求频率超限 |
| `ERR_UNAUTHORIZED` | 401 | 认证失败 |

---

## 4. MCP Server

MCP Server 提供 8 个工具，命名对齐 CodeGraph 与 JCodeIndexer 事实标准，降低 Agent 迁移成本。

### 4.1 工具表

| 工具 | 入参 | 返回 | 对标 |
|---|---|---|---|
| `context` | `symbol: string`（类/方法全名） | 精准裁剪上下文（方法体 + 字段 + 接口 + 单测） | CodeGraph `codegraph_context` |
| `impact` | `method: string`, `depth?: number` | 上游调用方 + 下游被调 | CodeGraph `codegraph_impact` |
| `tests` | `method: string`, `min_confidence?: number` | 关联单测列表（含 dependency） | CodeGraph `codegraph_affected` |
| `affected` | `symbol: string`, `recursive?: boolean` | 递归 import/include 依赖找受影响测试 | CodeGraph `affected` |
| `get_call_graph` | `symbol: string`, `depth?: number` | 调用图（双向），返回节点 + 边列表 | JCodeIndexer |
| `search_config` | `pattern: string` | 配置项/注解搜索 | JCodeIndexer |
| `find_dependencies` | `symbol: string` | 依赖关系列表 | JCodeIndexer |
| `search_symbols` | `q: string`, `mode?: string`, `limit?: number` | 符号搜索（FTS5 + 向量） | code-context-mcp |

---

## 5. 开发指南

### 5.1 实现步骤

1. **实现 CLI 框架**
   - 使用 `cobra` 库，在 `cmd/codeschema/` 下定义根命令和子命令。
   - 实现 `scan` / `watch` / `rebuild-kv` / `rebuild-refs` / `mcp` / `serve` / `benchmark` / `version` 共 8 个子命令。
   - 集成 viper 配置加载（config.yaml 路径、环境变量覆盖）。

2. **实现 HTTP 路由**
   - 在 `internal/server/http.go` 中实现路由注册。
   - 使用 `net/http` 标准库或轻量路由（如 `chi`）。
   - 实现全部 5 个接口 + 错误中间件。

3. **实现错误处理中间件**
   - 统一错误响应格式（JSON）。
   - 错误码与 HTTP 状态码映射。
   - 参数校验失败返回 `ERR_INVALID_PARAMETER` + 具体字段说明。

4. **实现 MCP 工具**
   - 在 `internal/server/mcp.go` 中实现 MCP Server。
   - 注册全部 8 个工具，每个工具对接 `internal/service` 层。
   - 工具命名对齐 CodeGraph / JCodeIndexer 标准。

5. **集成测试**
   - 启动 HTTP Server 发送请求验证响应。
   - 启动 MCP Server 验证工具列表和调用。

### 5.2 关键接口

```go
package server

type HTTPServer struct {
    service *service.Service
    addr    string
}

func (s *HTTPServer) Start() error
func (s *HTTPServer) Stop() error
```

```go
package server

type MCPServer struct {
    service *service.Service
    addr    string
}

func (s *MCPServer) Start() error
func (s *MCPServer) Stop() error
```

---

## 6. 完成标准

- [ ] CLI 全部 8 个命令可运行，`codeschema version` 输出正确版本号。
- [ ] HTTP API 全部 5 个接口响应正确，返回 JSON 格式。
- [ ] 错误中间件覆盖全部 5 种错误码，响应格式统一。
- [ ] MCP Server 启动成功，所有 8 个工具注册成功。
- [ ] MCP 工具调用返回正确结果，参数校验返回友好错误。
- [ ] 集成测试：HTTP + MCP 一次启动测试全部接口。