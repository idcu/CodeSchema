# CodeSchema 部署与使用指南

> 让 CodeSchema 真正跑起来，在 AI 开发工具中生成代码时用起来。

---

## 目录

1. [项目概述](#1-项目概述)
2. [快速部署](#2-快速部署)
3. [核心工作流](#3-核心工作流)
4. [AI 开发工具集成](#4-ai-开发工具集成)
5. [API 参考](#5-api-参考)
6. [生产部署](#6-生产部署)
7. [运维指南](#7-运维指南)
8. [常见问题与排查](#8-常见问题与排查)

---

## 1. 项目概述

CodeSchema 是一个**代码元数据 KV/DB 系统**，面向 AI 辅助开发场景。它扫描你的代码仓库，提取类、方法、接口、继承关系、调用关系等结构化数据，然后通过 MCP Server 向 AI 工具（Trae、Cursor、VS Code 等）提供精准裁剪后的代码上下文。

**核心价值**：AI 回答代码问题时，不必喂入整个仓库，大幅节省 token，同时提升回答准确度。

### 工作流程

```
你的仓库 ──scan──→ CodeSchema 索引 ──mcp──→ MCP Server ──tools──→ AI 开发工具
                                                                   ↓
                                                          AI 生成精准代码
```

### 三种启动模式

| 模式 | 命令 | 用途 |
|------|------|------|
| **扫描** | `codeschema scan <path>` | 一次性扫描仓库，生成索引后退出 |
| **监听** | `codeschema watch <path>` | 持续监听文件变更，增量更新索引 |
| **服务** | `codeschema mcp` / `codeschema serve` | 启动 MCP/HTTP 服务，供 AI 工具查询 |

---

## 2. 快速部署

### 2.1 环境要求

| 依赖 | 版本 | 说明 |
|------|------|------|
| Go | 1.25+ | 编译运行（仅编译时需要） |
| GCC/MinGW | 任一 C 编译器 | CGO 构建必需（仅 `build-cgo` 时需要） |
| Docker | 24+ | 容器化部署（可选） |

> **Windows 用户**：推荐安装 [Git for Windows](https://git-scm.com/download/win)（自带 MinGW）或 [TDM-GCC](https://jmeubank.github.io/tdm-gcc/)，满足 CGO 编译需求。

### 2.2 从源码构建（推荐）

```bash
# 克隆仓库
git clone <repo-url> codeschema
cd codeschema

# 方式一：纯 Go 构建（无 CGO，推荐生产环境）
make build
# 或：go build -o build/codeschema ./cmd/codeschema

# 方式二：CGO 构建（本地调试）
# 注：build-cgo 仅启用 CGO + 拷贝 ONNX 运行时库；真语法树解析需显式 -tags treesitter 构建
make build-cgo
# 真语法树路径：go build -tags treesitter -o build/codeschema ./cmd/codeschema

# 验证
./build/codeschema version
# 输出：CodeSchema v0.1.0
```

构建产物在 `build/` 目录下：

```
build/
└── codeschema.exe        # Windows
build/codeschema          # Linux/macOS
```

#### 快速启动脚本

项目提供了自动化脚本，一键完成构建、扫描、启动：

```bash
# Linux/macOS
chmod +x scripts/quick-start.sh
./scripts/quick-start.sh /path/to/repo

# Windows PowerShell
.\scripts\quick-start.ps1 -RepoPath D:\repo
```

> 脚本位于 [scripts/](scripts/) 目录，支持 `--docker` 参数直接使用 Docker 部署。

### 2.3 Docker 部署

```bash
# 构建镜像
docker build -t codeschema:latest .

# 运行 MCP Server（供 AI 工具连接）
docker run --rm -p 8080:8080 -v codeschema-data:/app/data codeschema:latest mcp --addr :8080

# 运行 HTTP API
docker run --rm -p 8081:8081 -v codeschema-data:/app/data codeschema:latest serve --http :8081

# 扫描指定目录（挂载宿主机目录）
docker run --rm -v /host/path/to/repo:/repo codeschema:latest scan /repo
```

#### Docker Compose 一键部署

项目提供了生产就绪的 [docker-compose.yml](docker-compose.yml)，包含健康检查、资源限制、持久化存储等配置：

```bash
# 1. 复制配置文件
cp config.yaml.example config.yaml
# 编辑 config.yaml 修改 project.root 等配置

# 2. 启动服务
set CODESCHEMA_REPO_PATH=D:\repo
set CODESCHEMA_AUTH_TOKEN=your-secret-token
docker compose up -d

# 3. 查看日志
docker compose logs -f

# 4. 执行全量扫描
docker compose --profile scan up

# 5. 停止服务
docker compose down
```

> 完整配置参考 [docker-compose.yml](docker-compose.yml) 中的环境变量说明。

### 2.4 配置说明

CodeSchema 支持**零配置启动**（所有参数都有默认值），也可以通过配置文件或环境变量自定义。

#### 默认配置（无需任何配置文件即可运行）

| 参数 | 默认值 | 说明 |
|------|--------|------|
| 存储目录 | `./data` | 索引数据持久化路径 |
| MCP 地址 | `:8080` | MCP Server 监听端口 |
| HTTP 地址 | `:8081` | HTTP API 监听端口 |
| 扫描并发数 | 4 | 同时解析的文件数 |
| 监听防抖 | 300ms | 文件变更防抖窗口 |
| 向量维度 | 1024 | 语义搜索向量维度 |

#### 使用配置文件（可选）

项目提供了完整的配置示例文件 [config.yaml.example](config.yaml.example)，复制即可使用：

```bash
# 复制示例配置
cp config.yaml.example config.yaml
# 编辑后启动
./build/codeschema --config config.yaml mcp
```

```yaml
# config.yaml（完整示例见 config.yaml.example）
project:
  name: my-project
  root: /path/to/repo
  languages: [go, java, typescript, python]

storage:
  dsn: ./data
  search:
    fts: true
    semantic: true
    vector_dim: 1024

server:
  mcp_addr: ":8080"
  http_addr: ":8081"
  auth_token: ""              # 设置 Bearer token 开启认证

scanner:
  workers: 4
  file_size_limit_mb: 10
  line_count_limit: 50000

watcher:
  debounce_ms: 300
  ignore_dirs: [".git", "node_modules", "target", "build", "vendor"]
```

#### 环境变量覆盖

```bash
# 环境变量优先级：默认值 < 配置文件 < 环境变量 < CLI 参数
set CODESCHEMA_SERVER_MCP_ADDR=:9090
set CODESCHEMA_SCANNER_WORKERS=8
set CODESCHEMA_STORAGE_DSN=E:\codeschema-data
./build/codeschema mcp
```

---

## 3. 核心工作流

### 3.1 第一步：扫描仓库

```bash
# 扫描当前目录下的仓库
./build/codeschema scan ./my-repo

# 指定并发数
./build/codeschema scan --workers 8 ./my-repo

# 指定存储目录
./build/codeschema scan --store E:\codeschema-data ./my-repo
```

扫描过程输出示例：

```
scanning repository: ./my-repo (workers=4)
scanning started at 2026-08-13T10:00:00+08:00
scan completed in 1.2s
index built: 152 docs indexed in 50ms
```

### 3.2 第二步：启动服务

扫描完成后，启动 MCP Server 供 AI 工具连接：

```bash
# 启动 MCP Server（供 AI 开发工具连接）
./build/codeschema mcp --addr :8080

# 或同时启动 HTTP API（可浏览器访问）
./build/codeschema serve --http :8081

# 服务端输出示例：
# MCP Server listening on :8080
# index built: 152 docs indexed in 48ms
```

> **注意**：MCP Server 启动时自动加载已扫描的索引数据，无需重新扫描。

### 3.3 第三步：增量监听（可选）

如果仓库频繁修改，可以启动文件监听模式，自动增量更新索引：

```bash
# 轮询模式（默认，零外部依赖）
./build/codeschema watch ./my-repo

# 原生文件系统监听模式（更高效，推荐）
./build/codeschema watch --fsnotify ./my-repo
```

### 完整工作流示意

```bash
# 一条命令完成：扫描 → 启动 MCP 服务
# 先扫描，再启动服务（两个终端）

# 终端 1：扫描
./build/codeschema scan ./my-repo

# 终端 2：启动服务
./build/codeschema mcp --addr :8080
```

---

## 4. AI 开发工具集成

### 4.1 MCP 协议简介

Model Context Protocol（MCP）是一个开放协议，允许 AI 工具通过标准接口与外部数据源交互。CodeSchema 实现了 MCP Server，为 AI 提供 11 个代码查询工具：

| 工具 | 功能 | AI 应用场景 |
|------|------|-------------|
| `context` | 获取符号的精准裁剪上下文 | AI 修改代码时只传相关部分 |
| `impact` | 分析调用影响面 | 改代码前评估影响范围 |
| `tests` | 查询关联单测 | AI 生成/修复单测用例 |
| `affected` | 递归查找受影响的测试 | 改代码后知道哪些测试要跑 |
| `get_call_graph` | 获取调用图 | 理解代码调用链路 |
| `search_symbols` | 双路检索符号 | 语义搜索相关代码 |
| `get_tags` | 获取符号标签 | 按架构分层定位代码 |
| `search_by_tag` | 按标签搜索 | 搜索某层所有代码 |
| `get_all_tags` | 获取所有标签 | 了解代码库架构 |
| `find_dependencies` | 查找依赖关系 | 分析代码依赖 |
| `search_config` | 搜索配置项 | 定位配置定义 |

### 4.2 Trae 集成

#### 配置方法

在 Trae 中配置 MCP Server：

1. 打开 Trae 设置 → MCP 服务器
2. 添加新 MCP 服务器：

```json
{
  "name": "codeschema",
  "type": "url",
  "url": "http://localhost:8080/sse",
  "description": "代码元数据查询服务"
}
```

3. 保存配置，Trae 会自动连接 CodeSchema

#### 使用示例

在 Trae 中提问时，AI 会自动调用 CodeSchema 的工具获取上下文。例如：

- "修改 `UserService.Login` 方法，补充参数校验" → AI 自动调用 `context` 获取方法源码
- "给 `OrderService` 添加一个取消订单的方法" → AI 自动调用 `context` 获取类结构和 `impact` 分析影响面
- "帮我写 `UserService` 的单元测试" → AI 自动调用 `tests` 获取关联测试

### 4.3 Cursor 集成

#### 配置方法

在 Cursor 中配置 MCP Server：

1. 打开 Cursor 设置 → Features → MCP Servers
2. 点击 "Add New MCP Server"：

```json
{
  "name": "codeschema",
  "type": "url",
  "url": "http://localhost:8080/sse",
  "description": "CodeSchema code metadata"
}
```

3. 保存后 Cursor 会自动连接，AI 在生成代码时自动使用 CodeSchema 工具

#### 使用示例

在 Cursor 的 Composer 或 Chat 中：

- "分析 `handleContext` 方法的影响面" → AI 调用 `impact` 工具
- "搜索所有与日志相关的代码" → AI 调用 `search_symbols` 工具
- "给我当前仓库的所有 controller 层代码" → AI 调用 `search_by_tag` 工具

### 4.4 VS Code 集成

#### 通过 VS Code MCP 扩展

VS Code 的 MCP 支持需要通过扩展或 MCP 客户端实现。

**方式一：使用 VS Code MCP 扩展（推荐）**

安装 [MCP Client](https://marketplace.visualstudio.com/items?itemName=ms-mcp.mcp) 扩展，然后在 VS Code 设置中添加：

```json
{
  "mcp.servers": [
    {
      "name": "codeschema",
      "type": "url",
      "url": "http://localhost:8080/sse",
      "description": "CodeSchema code metadata"
    }
  ]
}
```

**方式二：通过 Claude/Copilot + MCP**

如果你使用 GitHub Copilot 或 Claude for VS Code，可以在 VS Code 的 `settings.json` 中配置：

```json
{
  "github.copilot.mcpServers": {
    "codeschema": {
      "type": "url",
      "url": "http://localhost:8080/sse"
    }
  }
}
```

### 4.5 其他 AI 工具集成

任何支持 MCP 协议的 AI 工具都可以连接 CodeSchema。通用配置模板：

```json
{
  "name": "codeschema",
  "type": "url",
  "url": "http://localhost:8080/sse",
  "description": "代码元数据查询 — 提供精准上下文裁剪、影响面分析、语义搜索"
}
```

#### 自定义 MCP 客户端 (Python)

```python
import json
import requests

# 获取工具列表
resp = requests.get("http://localhost:8080/sse", stream=True)
for line in resp.iter_lines():
    if line:
        print(line.decode())

# 调用工具
def call_mcp(tool_name, args):
    payload = {
        "jsonrpc": "2.0",
        "id": 1,
        "method": "tools/call",
        "params": {
            "name": tool_name,
            "arguments": args
        }
    }
    resp = requests.post("http://localhost:8080/message", json=payload)
    return resp.json()

# 示例：搜索符号
result = call_mcp("search_symbols", {"q": "UserService", "mode": "both", "limit": 10})
print(result)

# 示例：获取上下文
result = call_mcp("context", {"symbol": "UserService.Login", "context_lines": 10})
print(result)
```

### 4.6 验证连接是否成功

无论使用哪种工具，连接成功后可以通过以下方式验证：

```bash
# 1. 确认 HTTP API Server 已启动（serve 命令，默认 :8081）
curl http://localhost:8081/health
# 返回：{"status":"ok","uptime":"1m2s","store_ok":true,"store_type":"file"}
# 注：/health 仅存在于 HTTP Server（:8081）；MCP Server（:8080）仅提供 /sse、/message 路由

# 2. 直接调用 HTTP API 测试
curl "http://localhost:8081/search?q=UserService&mode=both&limit=5"

# 3. 在 AI 工具中提问，观察 AI 是否自动调用 MCP 工具
# 如果 AI 的回复中出现了代码上下文信息，说明连接成功
```

---

## 5. API 参考

### 5.1 HTTP API 端点

当使用 `serve` 命令启动时，提供以下 RESTful API：

| 端点 | 方法 | 参数 | 说明 |
|------|------|------|------|
| `/health` | GET | - | 系统健康检查 |
| `/health/db` | GET | - | 存储层健康检查 |
| `/health/vector` | GET | - | 向量索引健康检查 |
| `/context` | GET | `symbol`, `context_lines` | 获取符号上下文 |
| `/impact` | GET | `method`, `depth` | 影响面分析 |
| `/tests` | GET | `method`, `min_confidence` | 关联单测 |
| `/search` | GET | `q`, `mode`, `limit` | 双路检索 |
| `/tags` | GET | `symbol` | 获取符号标签 |
| `/tags/search` | GET | `tag` | 按标签搜索 |
| `/tags/all` | GET | - | 所有标签 |
| `/metrics` | GET | - | Prometheus 指标 |
| `/viz` | GET | - | 向量索引可视化 |

### 5.2 MCP 工具详情

所有 MCP 工具通过 `tools/list` 和 `tools/call` 方法调用。

**工具：`context`** — 获取符号的精准裁剪上下文

```json
{
  "name": "context",
  "arguments": {
    "symbol": "com.example.UserService.login",
    "context_lines": 10
  }
}
```

**工具：`search_symbols`** — 双路检索符号（精确 + 语义）

```json
{
  "name": "search_symbols",
  "arguments": {
    "q": "用户登录",
    "mode": "both",
    "limit": 20
  }
}
```

**工具：`impact`** — 分析方法影响面

```json
{
  "name": "impact",
  "arguments": {
    "method": "com.example.UserService.login",
    "depth": 2
  }
}
```

---

## 6. 生产部署

### 6.1 容器化部署（Docker Compose）

项目提供了生产就绪的 [docker-compose.yml](docker-compose.yml)，包含：

- 健康检查（HEALTHCHECK）
- 资源限制（CPU/内存上限）
- 认证 token 配置
- 持久化数据卷
- 一次性扫描任务（profile scan）
- 系统日志日志驱动

```bash
# 启动服务
docker compose up -d

# 执行全量扫描
docker compose --profile scan up && docker compose logs -f codeschema

# 查看日志
docker compose logs -f

# 停止服务
docker compose down
```

> 完整配置参考 [docker-compose.yml](docker-compose.yml) 和 [config.yaml.example](config.yaml.example)。

### 6.2 性能调优

不同规模仓库的并发、向量维度、内存与监听配置建议，以及基准测试数据，已汇总到运维文档 [性能调优](docs/ops/04-性能调优.md)。要点速览：

- **大仓库（10万+文件）**：`--workers 8-16` + `--fsnotify` 原生监听
- **低内存环境**：`vector_dim: 128` 降低向量维度，或 `semantic: false` 关闭语义搜索
- **高速增量**：`--fsnotify` + `--debounce 200`
- **生产环境**：Docker 部署并配置 `auth_token` 开启认证

### 6.3 监控与可观测性

CodeSchema 内置结构化日志（`log/slog`）、Prometheus 格式指标（`/metrics` 端点）与请求链路追踪。完整的 Prometheus 配置、Grafana 仪表盘、告警规则与日志采集方案，见运维文档 [监控与告警](docs/ops/03-监控与告警.md)。

> 快速验证：`curl http://localhost:8081/metrics` 可查看 `http_requests_total` 等指标。

---

## 7. 运维指南

> 生产环境运维相关文档，详见 [docs/ops/](docs/ops/) 目录。

### 7.1 文档索引

| 文档 | 说明 |
|------|------|
| [生产部署清单](docs/ops/01-生产部署清单.md) | 生产环境部署前逐项检查清单 |
| [备份与恢复](docs/ops/02-备份与恢复.md) | 索引数据备份与恢复操作指南 |
| [监控与告警](docs/ops/03-监控与告警.md) | Prometheus 指标、日志采集、告警规则 |
| [性能调优](docs/ops/04-性能调优.md) | 不同规模仓库的配置建议与优化策略 |

### 7.2 日常运维命令

```bash
# 查看服务状态
docker compose ps

# 查看实时日志
docker compose logs -f --tail=100

# 重启服务
docker compose restart codeschema

# 重新索引（删除数据后重新扫描）
docker compose down
docker compose --profile scan up

# 更新版本
docker compose pull
docker compose up -d
```

### 7.3 生产环境建议

1. **启动扫描**：首次部署时先执行 `--profile scan` 完成全量扫描，再启动主服务
2. **设置认证**：务必配置 `auth_token`，防止未授权访问
3. **配置反向代理**：生产环境建议使用 Nginx 反向代理，配置 HTTPS
4. **定期备份**：每日备份 `./data` 目录，保留最近 7 天（操作见 [备份与恢复](docs/ops/02-备份与恢复.md)）
5. **监控告警**：配置 Prometheus + Grafana 监控关键指标（规则见 [监控与告警](docs/ops/03-监控与告警.md)）

---

## 8. 常见问题与排查

### Q1: 编译报错 "CGO_ENABLED=0 但需要 CGO"

```bash
# 解决方法：使用纯 Go 构建
make build
# 或
go build -o build/codeschema.exe ./cmd/codeschema
```

### Q2: MCP Server 启动后 AI 工具连不上

```bash
# 1. 检查服务是否启动
curl http://localhost:8080/health

# 2. 检查端口是否被占用
netstat -ano | findstr :8080

# 3. 检查 AI 工具配置中的 URL 是否正确
# 正确格式：http://localhost:8080/sse（注意 /sse 路径）
```

### Q3: 扫描结果为空或索引不正确

```bash
# 1. 确认扫描路径是否正确
./codeschema scan ./actual-repo-path

# 2. 确认服务使用的是同一数据目录
./codeschema mcp --store ./data

# 3. 扫描后重建索引（自动在 mcp/serve 启动时执行）
```

### Q4: 如何重置所有数据

```bash
# 删除数据目录后重新扫描
rm -rf ./data
./codeschema scan ./repo
./codeschema mcp
```

### Q5: Windows 上构建报错 "gcc not found"

```bash
# 安装 MinGW-w64（推荐使用 Chocolatey）
choco install mingw

# 或使用 Git Bash 自带的 MinGW
# 将 Git 安装目录下 mingw64/bin 添加到 PATH
```

### Q7: 如何知道 CodeSchema 确实在 AI 生成中起作用了

观察 AI 的回复：如果 AI 在生成代码前先调用了 `context` 或 `search_symbols` 工具，说明 CodeSchema 正在工作。可以在 AI 的对话中看到类似这样的输出：

```
🔍 正在查询代码上下文...
→ 调用 codeschema.context({ symbol: "UserService.Login" })
→ 获取到上下文：方法体 + 相关字段 + 关联单测
```

---

## 附录：快速启动脚本

> 自动化脚本位于 [scripts/](scripts/) 目录，支持 local 和 Docker 两种模式。

### Windows (PowerShell)

```powershell
# 方式一：使用项目脚本
.\scripts\quick-start.ps1 -RepoPath D:\repo

# 方式二：使用 Docker
.\scripts\quick-start.ps1 -RepoPath D:\repo -UseDocker

# 方式三：手动执行
$repoPath = "D:\repo"
$binary = ".\build\codeschema.exe"

# 构建
go build -o $binary .\cmd\codeschema

# 扫描
& $binary scan $repoPath

# 启动 MCP Server
& $binary mcp --addr :8080
```

### Linux/macOS (Bash)

```bash
# 方式一：使用项目脚本
./scripts/quick-start.sh /path/to/repo

# 方式二：使用 Docker
./scripts/quick-start.sh --docker /path/to/repo

# 方式三：手动执行
#!/bin/bash
REPO_PATH="${1:-.}"
BINARY="./build/codeschema"

# 构建
make build

# 扫描
$BINARY scan "$REPO_PATH"

# 启动 MCP Server
$BINARY mcp --addr :8080
```

---

> **文档版本**：v1.0 | **最后更新**：2026-08-13
> **相关文档**：[docs/dev/](dev/) 目录下的开发文档