# FAQ —— 常见问题与排查

> 写给谁：使用者、运营、开发
> 写什么：建/跑/连/查过程的常见问题与解决步骤
> 核心原则：出问题时先查本页，给可复制的命令而非概念
> 优先级：P2
> 最后更新：2026-08-17

---

### Q1: 编译报错 “CGO_ENABLED=0 但需要 CGO”
默认构建应免 CGO。使用：
```bash
make build
# 或 go build -o build/codeschema.exe ./cmd/codeschema
```
若确实需要 ONNX，才用 `-tags onnx` + CGO。

### Q2: MCP Server 启动后 AI 工具连不上
```bash
curl http://localhost:8080/sse        # 检查服务是否监听（MCP Server 无 /health）
netstat -ano | findstr :8080          # 检查端口占用
```
确认客户端 URL 为 `http://localhost:8080/sse`（注意 `/sse` 路径）。

### Q3: 扫描结果为空或索引不正确
```bash
./codeschema scan ./actual-repo-path       # 确认路径正确
./codeschema mcp --store ./data            # 确认与服务同数据目录
```
扫描后 mcp/serve 启动会自动重建索引。

### Q4: 如何重置所有数据
```bash
rm -rf ./data
./codeschema scan ./repo
./codeschema mcp
```

### Q5: Windows 构建报错 “gcc not found”
默认我不需要 gcc。若走 CGO（ONNX/treesitter 真语法树路径），安装 MinGW（`choco install mingw` 或 Git Bash 自带 mingw64 加入 PATH）。

### Q7: 如何确认 CodeSchema 确实在 AI 生成中起作用
观察 AI 回复：生成代码前是否调用了 `context` 或 `search_symbols` 工具（出现"正在查询代码上下文"及方法体/单测上下文即在工作）。

### Q8: 多租户模式下怎么指定仓库 / 为何查不到数据
检索类 MCP 工具传 `project` 参数；HTTP 用 `X-Tenant` 头或 `?tenant=`。若查不到，先用 `list_projects`/`GET /projects` 确认目标租户是否在服务且已扫描，并确认索引目录已按租户隔离（未显式配置时按各自 `storage.dsn` 派生，不会串数据）。

### Q9: 语义检索召回率低
默认 LocalEmbedder（TF-IDF）R@1≈0.42。语义敏感场景请：
```bash
go build -tags onnx -o codeschema ./cmd/codeschema   # 启用 ONNX（bge-small-zh-v1.5，R@1=1.00）
```
并确保模型已下载/分发（`make models-serve` 本地分发或 `model_download_url`）。

### Q10: 全链路基准怎么看
```bash
./codeschema benchmark ./repo --out build/bench.json
```
多仓对比配 `CODESCHEMA_BENCH_REPOS="repo1;repo2"`，产物见 `build/bench-compare.json`。

---

## 修订记录

| 日期 | 说明 |
|---|---|
| 2026-08-17 | 从 DEPLOYMENT_AND_USAGE §8 拆出，补多租户/语义检索/基准条目 |