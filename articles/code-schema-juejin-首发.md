# CodeSchema 开源首发：一个给 AI 编码助手「喂」精准代码上下文的索引服务

> 用 Cursor / Claude Code / Cline 写代码，上下文窗口经常被整仓库塞爆？token 哗哗烧、噪声一大模型还容易跑偏？
> 过去几个月我做了一个小工具 **CodeSchema**：把你的代码仓库「结构化索引」，让 AI 用到哪、取哪块，而不是动辄把整个仓库丢进 prompt。
> **这是它的第一次对外宣传。** 它还不完美，所以更想发出来让真实场景去打磨。下文说实话、给步骤、留反馈入口。

---

## 一、先说痛点

现在的 AI 编码助手（Cursor、Claude Code、Cline、JetBrains AI……）大多靠「把代码塞进上下文窗口」来工作。仓库一大就有三个老问题：

- **token 贵**：动辄把成百上千个文件整仓塞进去，钱烧得肉眼可见；
- **噪声大**：无关文件越多，模型越容易被带偏，改对地方的概率下降；
- **影响面盲区**：改一行，你其实想知道「这会影响哪些方法、哪些单测」——但大多数工具给不出。

CodeSchema 就是冲着这三件事去的。

---

## 二、CodeSchema 是什么

一句话定位：**代码上下文供给引擎**——扫描你的仓库、抽取结构、存进三层存储，再通过 MCP / HTTP 向 AI Agent 供给「按需裁剪好」的代码片段。

三句话价值：

- 给 AI 写代码时，不必把整个仓库塞进上下文（**省 token、降噪声**）；
- 改一行代码，可反查「会影响哪些方法」「关联哪些单测」；
- 精确符号检索 + 向量语义检索，两条腿走路。

技术栈是 **Go 1.25**，单二进制、免 CGO、可 Docker、Apache-2.0 协议。

---

## 三、它怎么工作

```
[你的仓库]
   │  scan / watch（增量监听，300ms 防抖合并）
   ▼
[Scanner + 多语言解析适配器]  ──抽取──►  类 / 方法 / 接口 / 调用关系 / 标签
   │
   ▼
[三层存储]   文件(JSON, 默认) + SQLite(纯 Go) / PostgreSQL + Redis(缓存)
             + 全文检索(FTS) + 向量检索(语义)
   │
   ▼
[Service：查询 / 标签 / 搜索 / 分析]
   │
   ├─► MCP Server（SSE / stdio）──►  VS Code / Cursor / Claude Code / JetBrains
   └─► HTTP API（REST）        ──►  你的脚本 / 内部平台
```

核心思路就一条：**仓库是权威源，AI 是消费者；中间这层索引负责「用到哪、取哪块」**。

---

## 四、5 分钟跑起来

```bash
# 主仓库在 Gitee（建议从这里 clone）；GitHub 为镜像仓，CI 在 GitHub Actions 上跑
git clone https://gitee.com/idcu/code-schema.git && cd code-schema
# （idcu-go 也需 clone 到同级目录，详见 README「开发前必读」）

make build
# 或：go build -o codeschema ./cmd/codeschema

./codeschema scan ./your-repo   # 扫描仓库并入库
./codeschema mcp --addr :8080   # 启动 MCP Server（SSE，端点 /sse）
```

接 AI 客户端只需 3 步：

1. 启动 MCP：`./codeschema mcp --addr :8080`
2. 打印配置：`./codeschema mcp --print-config`（直接输出 VS Code / JetBrains / Claude Code / Cursor / npx 五类接入片段）
3. 在客户端粘贴，即可调用 12 个 MCP 工具（`search_symbols` / `context` / `impact` …）

以 VS Code / Cursor 为例，配置片段长这样（端口按你启动时的 `--addr` 填，SSE 端点为 `/sse`）：

**VS Code**（`.vscode/mcp.json`）：

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

**Cursor / Cline**（`.cursor/mcp.json` 或 `~/.cursor/mcp.json`）：

```json
{
  "mcpServers": {
    "codeschema": {
      "url": "http://localhost:8080/sse"
    }
  }
}
```

> 其他客户端（JetBrains / Claude Code / npx 等）以 `./codeschema mcp --print-config` 输出为准，无需手敲。

Docker 一条命令也能起：

```bash
docker run -p 8081:8081 -v ./data:/app/data codeschema:latest
```

---

## 五、目前能用到的能力

- **12 个 MCP 工具**：`search_symbols` / `context` / `impact` / `tests` / `affected` / `get_call_graph` / `search_config` / `find_dependencies` / `get_tags` / `search_by_tag` / `get_all_tags` / `list_projects`
- **23 个 HTTP 路由**：`/context` `/impact` `/tests` `/search` `/tags*` `/projects` `/metrics` `/openapi.json` `/docs` `/affected` `/viz`（向量索引可视化仪表盘）等
- **双路检索**：符号图精确检索 + 向量语义检索（FTS + 向量融合重排）；低置信度结果默认过滤，避免把弱匹配「误导」给 Agent
- **增量监听**：fsnotify 原生监听 + 轮询，300ms 防抖合并，改完即更新
- **多租户**：一个进程服务多个隔离仓库，按 `project` / `X-Tenant` 路由，无需每项目各起一进程
- **可观测 & 安全**：结构化日志、Prometheus 指标、Bearer 认证、限流、优雅关闭
- **单二进制 / Docker / 多平台交叉编译**

---

## 六、坦诚：当前阶段它还很不完美

> 这正是我为什么现在就发出来——不是为了秀完成度，而是想用真实场景逼出下一版该改什么。

1. **影响面 / 关联测试 / 调用图，Go 路径下已真实可用。** 此前默认解析器只填「被调用方」、没回填「调用方」（CallerFQN），导致 `impact` / `tests` / `get_call_graph` 基本是断的；现已在 tree-sitter `detectCalls` 回填包限定 `CallerFQN`/`CalleeFQN`，并在 analyzer 侧做双向归一化（查询去包前缀 / 节点按后缀匹配），可命中 `search_symbols`/`context` 返回的裸名。非 Go 语言与 LSP / SCIP 适配器仍提供更精确的调用边——这也是下一步继续打磨的方向，欢迎社区一起贡献。
2. **语义检索精度有门槛。** 默认构建免 CGO，语义检索走本地 TF-IDF 降级（R@1≈0.42）；要拿到向量级精度（bge-small-zh-v1.5，R@1=1.00）需要 `go build -tags onnx` + onnxruntime 动态库 + 模型文件。我们提供了内置 ONNX 的 `codeschema:onnx` 镜像，开箱即得。
3. **多语言解析是启发式为主。** 默认 30 语言走正则轻量解析，调用关系抽取对 Go / Python 较准，其余语言偏弱；要更准的调用图建议开 LSP 适配器（gopls / jdtls / clangd）。
4. **仍在快速演进。** P0–P18 里程碑已交付，核心链路已在真实多仓库场景跑通（如 41 模块多租户扫描），但单测关联策略、AI 标签增强、更多语言的真语法树等仍在打磨，文档、性能、易用性也都有提升空间。

---

## 七、为什么现在就发，而不是等「做完」

完美没有尽头。闭门造出来的「完成度」往往和真实需求错位。**先把能用的核心推出去，让大家的真实仓库去跑、去踩坑、去提需求，比自己拍脑袋迭代高效得多。**

我们特别想收到这类反馈：

- 在你的项目里能不能顺利构建、跑起来？哪些语言 / 构建场景踩了坑？
- `impact` / 调用图在你的语言上缺不缺？你期望怎么补（正则 / LSP / 其他）？
- 检索召回准不准？你还想要什么检索 / 上下文能力？
- 接入 AI 客户端的体验如何？12 个 MCP 工具够不够用、命名通不通？

---

## 八、怎么参与 & 反馈

- 主仓库（Gitee，提 Issue / PR 都在这）：👉 https://gitee.com/idcu/code-schema
- 镜像仓库（GitHub，CI 在 GitHub Actions 上跑）：👉 https://github.com/idcu/code-schema
- 文档地图：`docs/README.md`（按新人 / 开发者 / 架构师 / 运维 / 贡献者分层）
- 协议：Apache-2.0，可商用、可改

如果它戳中了你的痛点，**点个 Star 就是最大的鼓励**；如果能跑起来顺手开个 Issue 说说哪里别扭，那对我们下一步开发就是最有价值的输入。

---

*咱们不是来秀完成度的，是来攒真实反馈的。期待你的仓库。*

> 建议标签：`#Go` `#开源` `#AI` `#MCP` `#代码索引`
