# 部署与运维手册

> 写给：要把 CodeSchema 跑上线、配置、监控的运维/SRE

## 快速部署

```bash
# 构建镜像（默认免 CGO 纯 Go；国内走 goproxy.cn）
docker build -t codeschema:latest .
docker run -p 8081:8081 -v ./data:/app/data codeschema:latest

# 或裸二进制
make build && ./codeschema serve --http :8081
```

## 配置（`config.yaml`，优先级：默认 < 文件 < 环境变量`CODESCHEMA_*` < CLI）

```yaml
server:
  mcp_addr: ":8080"      # MCP 监听
  http_addr: ":8081"     # HTTP 监听
  auth_token: ""         # Bearer token；生产必须设置
  rate_limit: 0          # 每分钟上限；0=不限流
storage:
  driver: "file"         # file | sqlite | pg（需 -tags pg）
  dsn: "./data"
  kv: ""                 # redis://host:6379/0（需 -tags redis）
scanner:
  workers: 4
  file_size_limit_mb: 10
  line_count_limit: 50000
watcher:
  debounce_ms: 300
  ignore_dirs: [.git, node_modules, target, build, vendor, dist, .next]
```

完整字段见仓库 `config.yaml.example`（含 `ai` / `context` / `tenants` 多租户 / 向量等）。

## 安全收敛（生产必做）

- **不要全接口绑定**：将 `mcp_addr`/`http_addr` 改为 `127.0.0.1:<port>`，或经 Nginx 内网反代（示例 `build/secure-demo.yaml`）。
- **必须设置 `server.auth_token`**（Bearer）。
- 按需设 `server.rate_limit` 防滥用。
- 多租户：各租户独立存储与索引目录，隔离彻底。

## 多租户部署

单实例多仓库：一份 `--config` 写 `tenants:` 列表；HTTP 用 `X-Tenant` 头或 `?tenant=`，MCP 用 `project` 参数；`list_projects` / `GET /projects` 枚举。可运行示例 `build/mt-demo.yaml`。

## 存储选型

- 默认 `file`：零依赖，小中型。
- `sqlite`：跨会话一致、关系查询（生产化用 `BulkUpsert`）。
- `pg`（`-tags pg`）：亿级横向。
- Redis（`-tags redis`）：热点 L2 缓存，提升大仓反查。

## 健康与可观测

- 健康检查：`/health` `/health/db` `/health/kv` `/health/vector`。
- 指标：`/metrics`（Prometheus）。
- 可视化：`/viz`（向量索引状态，默认栈可用）。
- 日志：结构化（log/slog）；支持链路追踪。

## 构建变体对照（影响镜像）

| 变体 | 标签 | 说明 |
|---|---|---|
| 默认 | 无 | 免 CGO，正则 + SQLite + TF-IDF |
| ONNX 语义 | `-tags onnx` | 需 gcc + glibc |
| PG/Redis | `-tags 'pg redis'` | 对应后端 |
| tree-sitter | `-tags treesitter` | CGO 真语法树 |

> Dockerfile 默认免 CGO 纯 Go 栈；启用 onnx 需 glibc 对齐的基础镜像。
