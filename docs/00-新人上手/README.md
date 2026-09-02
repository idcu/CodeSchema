# 新人上手指南

> 写给：第一次接触 CodeSchema 的人（开发 / 运维 / 使用者均可）
> 目标：5 分钟搞清楚「它是什么、能帮我什么、怎么跑起来」

> 最后更新：2026-09-02
## 它是什么

CodeSchema 是一个**代码元数据索引与上下文裁剪服务**：扫描你的代码仓库 → 抽取类/方法/接口/调用关系等结构化数据 → 存入三层存储（文件/SQLite/PG + Redis 缓存 + 向量/全文检索）→ 通过 MCP / HTTP 向 AI Agent 供给精准裁剪后的代码片段，省 token、降噪声。

## 三句话价值

- 给 AI 写代码时，不必把整个仓库塞进上下文。
- 改一行代码，可反查「会影响哪些方法」「有哪些关联单测」。
- 同时支持精确符号检索与语义（向量）检索。

## 5 分钟跑起来

```bash
# 1. 构建（需 Go 1.25；../idcu-go 兄弟仓必须存在，见 01-开发者）
make build
# 或：go build -o codeschema ./cmd/codeschema

# 2. 扫描一个仓库
./codeschema scan ./your-repo

# 3. 启动 MCP（供 AI 客户端连）
./codeschema mcp --addr :8080

# 4.（可选）启动 HTTP API
./codeschema serve --http :8081
```

## 接 AI 客户端（3 步）

1. 启动 MCP：`./codeschema mcp --addr :8080`
2. 打印客户端配置：`./codeschema mcp --print-config`（输出 VS Code / JetBrains / Claude Code / Cursor / npx 五类片段）
3. 在你的客户端粘贴，即可调用 12 个 MCP 工具（`search_symbols` / `context` / `impact` …）

## 下一步看哪里

- 想本地改代码 → [01-开发者/README.md](../01-开发者/README.md)
- 想了解系统设计 → [02-架构师/README.md](../02-架构师/README.md)
- 想部署上线 → [03-部署运维/README.md](../03-部署运维/README.md)
- 想提 PR → [04-贡献者/README.md](../04-贡献者/README.md)
