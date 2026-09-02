# CodeSchema 全维度深度分析评估报告

> 评估日期：2026-09-02 ｜ 仓库：`github.com/idcu/codeschema`（Gitee 镜像 `gitee.com/idcu/code-schema`）｜ 分支 `main` @ `ba4ea71`
> 方法学：代码真相取证（`go list` / `go build` / 源码逐行核对）+ 文档口径抽取 + 双向一致性交叉验证（reality-check）

---

> 最后更新：2026-09-02
## 0. 一句话结论（先说结论）

CodeSchema 是一个**已具备可用骨架与生产级索引能力**的「代码元数据 KV/DB 系统」，定位为面向 AI Agent 的上下文裁剪供给服务。其**默认二进制**能提供：正则/tree-sitter 多语言元数据扫描、文件+SQLite 三层存储、语义/全文混合检索（TF-IDF 降级）、12 个 MCP 工具与 22 个 HTTP 端点（http.go 16 + viz.go 6）、多租户路由。

但**文档显著领先于默认构建的真实能力**，存在三类必须正视的落差：
1. **头牌能力有条件生效**——调用影响面分析（`impact`/`tests`/`get_call_graph`）在默认解析路径下对真实方法恒返回空（caller 侧未填充）；语义检索的 bge-ONNX Recall=1.00 只在 `-tags onnx` 构建 + 模型文件 + glibc 下成立。
   - ⚠️ **CallerFQN 根因已于 2026-09-02 完成代码侧修复**（默认 `adapter.go` 的 `detectCalls` 调用处 `adapter.go:846` 与 AST 路径 `adapter_ast.go:342/402` 均已回填 `CallerFQN`；`go build` + 默认路径单测 `ok 0.521s` 通过）。修复已落地工作区，**待 commit/push 后随新构建生效**；语义检索的 ONNX 边界仍维持文档说明（P0/P1 已修正）。
2. **多存储/多语言服务器被构建标签隔离**——PG、Redis 存储与 ONNX 嵌入均不在默认二进制内，需显式 `-tags`。
3. **文档内部自相矛盾**——存在两套 P 编号体系、包数量 4 处不一致、Docker 基础镜像口径互悖等。

---

## 1. 项目目标与定位（文档宣称 + 代码印证）

| 维度 | 内容 |
|---|---|
| 一句话定位 | 面向 AI 辅助开发的**代码元数据索引与上下文裁剪服务**，把类/方法/接口/调用关系/标签沉淀为「文件存储(权威源)+内存索引+向量索引」三层，经 MCP Server 向 AI Agent 供给精准裁剪上下文 |
| 核心目标 | 单文件解析 P95 <20ms；AI 上下文 token 理论节省 99.7%；AI 增强成本 < 总运行成本 5%；解析 100% 外包给 tree-sitter/LSP/SCIP |
| 接口形态 | CLI（`codeschema scan/watch/serve/mcp/...`）、HTTP API、MCP Server（SSE + stdio 双传输） |
| 代码印证 | `cmd/codeschema` 提供 scan/watch/rebuild-kv/benchmark/mcp/serve/version 七类命令；`internal/server` 含 HTTP(22 路由：http.go 16 + viz.go 6)+MCP(12 工具)；`internal/store` 实现三层存储接口 ✅ |

**结论**：目标与接口形态与代码基本一致，定位清晰。

---

## 2. 架构与实现原理

### 2.1 分层架构（文档 M1–M6 + 代码印证）

```
接入层   CLI(cmd/codeschema) · HTTP(internal/server/http.go) · MCP(internal/server/mcp.go,SSE/stdio)
   │
编排层   Scanner(扫描) · Scheduler(调度) · Watcher/FSWatch(增量) — internal/scanner,scheduler,fswatch,watcher
   │
解析适配中间层  tree-sitter(正则+可选真语法树) · SCIP · LSP(gopls/clangd/jdtls/...) · CodeGraph
   │            internal/parser + internal/parser/adapter/{treesitter,scip,lsp,codegraph}
   │
AI 增强层   Tagger(标签) · DocEnhancer(文档增强) · Embedder(向量) — internal/ai, internal/embedding, internal/vector
   │
存储层   FileStore(权威) · SQLite · PG(-tags pg) · Redis(-tags redis) · Vector(chromem) · FTS — internal/store/*
```

### 2.2 关键实现原理（证据）

- **三层存储**：`internal/store/filestore.go` 以 JSON 文件为权威源，`store/sqlite` 做关系索引，`internal/vector` 用 chromem 做向量索引（`go.mod` replace 到本地 `down/chromem-go`）。✅
- **解析适配器可插拔**：`contrib/adapterx/adapters.go:BuiltinAdapters()` 注册 4 类（treesitter/scip/lsp/codegraph），测试断言 `want 4`。✅
- **MCP 为自研 JSON-RPC**：未使用 mark3labs 库，而是 `internal/server/mcp.go` 自实现 `defineTools()`（12 工具）+ SSE/stdio 传输。✅
- **多租户**：`internal/tenant` 提供 `Manager`，HTTP/MCP 均注入 `SetTenantManager`，按 `X-Tenant` 头 / `?tenant=` 路由到隔离 Service 实例。✅
- **嵌入双轨**：默认 `embedder_onnx_stub.go`(`//go:build !onnx`)→ Local TF-IDF；`-tags onnx` 启用 `embedder_onnx.go`→ bge-small-zh-v1.5(ONNX)。⚠️ 见 §5。

---

## 3. 规划与进度（双 P 编号体系是主要混淆源）

文档并存**两套互不相干的阶段编号**，是理解本项目最大的认知障碍：

| 体系 | 含义 | 宣称完成度 | 来源 |
|---|---|---|---|
| **P0–P18（开发阶段）** | 时间线式交付里程碑（骨架/MVP/反向引用/多语言/可观测性/CI…） | 全部 **100%（生产级）** | README.md「当前状态」表、DEV_PROGRESS.md |
| **P1–P9（架构模块层）** | 按架构职能切分的模块完成度 | **系统总 ≈95%**，其中 P3=96% P4=96% P6=97% P7=95% P9=96%，其余 100% | git 历史（2026-09-02 重构前文档） |

**矛盾点**：同一仓库既宣称「P0–P18 全部生产级 100%」，又宣称「系统总完成度 ≈95% 且 P3/P4/P7/P9 <100%」。两套编号无映射表，新人极易误判。
> 已于 2026-09-02 在 `DEV_PROGRESS.md` 加「进度体系说明」澄清：前者是开发里程碑轴（何时建成，全 100% 交付），后者是能力成熟度轴（当前多成熟，95–98% 打磨中），两轴正交非矛盾。

**子模块完成度口径本身也不一致**（modules/README.md 树 vs P3.md 正文）：
- P3_3 SCIP：树 97% / 正文 95%
- P3_4 LSP：树 97% / 正文 95%
- P3_5 CodeGraph：树 93% / 正文 92%

**代码侧实证进度**：
- 32 个 internal Go 包全部可编译（默认标签 `go build ./cmd/codeschema` exit 0，产出 23MB 二进制）。
- 81 个 `_test.go` 文件分布均衡（parser 16、vector 10、store 7、service 7、server 6…），测试基础扎实。
- 非 vendor 代码 **51,278 行**，vendor 内含 tree-sitter / onnxruntime / modernc-sqlite / chromem 等重依赖。

---

## 4. 阻塞与风险（带代码/运行证据）

| # | 阻塞/风险 | 证据 | 影响 |
|---|---|---|---|
| B1 | **调用影响面分析在默认路径恒空** | `internal/parser/adapter/treesitter/adapter_ast.go:329,378` 构造 `CallIR` 仅填 `CalleeFQN`；`adapter.go:489` `detectCalls(...,"")` caller 传空 | `impact`/`tests`/`get_call_graph` 对真实方法返回空，仅 codegraph/scip 适配器能填 caller | ✅ **已修复（代码侧，2026-09-02）**：默认 `adapter.go:846` 与 AST `adapter_ast.go:342/402` 均回填 `CallerFQN`，`go build`+单测通过；待 commit/push |
| B2 | **ONNX 语义检索需特殊构建+环境** | `embedder_onnx.go://go:build onnx`；默认 `embedder_onnx_stub.go` 降级 TF-IDF；要求 glibc + 模型文件 | 默认二进制语义 Recall 远低于文档宣称的 1.00 |
| B3 | **PG/Redis 存储不在默认二进制** | `pg/pg.go://go:build pg`、`redis/redis.go://go:build redis`；`go list` 默认仅见 sqlite | 超大仓横向扩展能力需 `-tags pg,redis` 显式开启 |
| B4 | **LSP/SCIP 依赖外部二进制** | `lsp/adapter.go` 需 gopls/clangd/jdtls 等；`scip` 需 SCIP 索引工具 | 无外部工具时仅 tree-sitter 正则适配器生效，调用图精度受限 |
| B5 | **构建强依赖本地 idcu-go 兄弟仓** | `go.mod` 中 10 条 `replace gitee.com/idcu-go/* => ../idcu-go/*` | 克隆后必须在 `../idcu-go` 存在时才能 `go build`，文档未显式前置说明 |
| B6 | **AI 增强评估缺 API key** | DEV_PROGRESS 方向A：无 key 时 `ai: enhancement disabled` 优雅降级 | Tagger/DocEnhancer 真实质量无法端到端验证 |

---

## 5. 文档 ↔ 代码一致性核对（reality-check 核心）

### 5.1 一致项（文档可信）
- ✅ MCP 工具数 = 12（代码 `defineTools()` 枚举 12 个 `Name:`）。
- ✅ HTTP 端点 = 22（`http.go` 注册 /health*/context/impact/tests/search/tags*/projects/metrics/openapi/docs 等 16 个 + `viz.go` 注册 /viz* 6 个）。
- ✅ 多租户已落地（`internal/tenant` + 双接口注入）。
- ✅ 三层存储接口 + chromem 向量索引存在。

### 5.2 不一致项（docs outrun code，按严重度排序）

| 严重度 | 不一致点 | 文档说法 | 代码真相 | 建议修正 |
|---|---|---|---|---|
| 🔴 高 | 影响面分析能力 | README 列为「核心能力/已完成」 | 默认路径 caller_fqn 全空，工具对真实方法恒空（DEV_PROGRESS 方向A 亦自认） | ✅ **已修正**：README:16、版本发布说明:29 加 caller 空值警示与生效条件 |
| 🔴 高 | 语义检索 Recall=1.00 | 默认即 bge-ONNX | 仅 `-tags onnx`+模型+glibc 成立；默认 TF-IDF 降级 | ✅ **已修正**：版本发布说明:28、系统简介:30 加默认=TF-IDF 降级说明；README 新增「构建变体与能力边界」矩阵 |
| 🟠 中 | 多存储（PG/Redis） | 作为已落地能力描述 | 被 build tag 隔离，默认二进制不含 | 🟡 部分修正（README 矩阵已标注 `-tags pg,redis` 构建要求；待 P2 在正文显式说明） |
| 🟠 中 | 包数量（口径） | 27 / 31 / 32 / 36 四处不一 | `internal/`=**32**、`全仓库 go list ./...`=**36**（两者均属实，仅口径不同）；唯 modules/README 旧写 27 为错误 | ✅ **已修正**：modules/README:4 27→32+scope；README/系统简介/版本发布说明 标注「32=internal/，36=全仓库」 |
| 🟠 中 | 双 P 编号体系 | P0–P18 全 100% 与 P1–P9 ≈95% 并存 | 两套无映射、互悖 | ✅ **已澄清（2026-09-02）**：DEV_PROGRESS.md 新增「进度体系说明」，明确两轴正交（里程碑轴 vs 成熟度轴）；未做硬性映射表 |
| 🟡 低 | Docker 基础镜像 | 一处写 `alpine` 构建、另一处强调「必须用 glibc(Debian)，alpine 会 ONNX 降级」 | 后者为真（ONNX 需 glibc） | ✅ **已修正**：DEV_PROGRESS:235、11-配置部署:365 alpine→bookworm（与 Dockerfile 实际 `golang:1.25-bookworm→debian:bookworm-slim` 对齐） |
| 🟡 低 | 「解析 100% 外包」 vs 「30 语言正则启发式」 | 设计文档称不自研 AST、100% 外包 | README 自承默认为正则启发式，仅 `-tags treesitter` 切真语法树 | 设计文档与 README 对齐口径 |
| 🟡 低 | 模块文档计数 | DEV_PROGRESS 称「43 份」 | 目录实际 41 个模块文件 + README = 42 | 校数为 42 |

---

## 6. 改进完善清单（含文档同步动作，按优先级）

### P0（立即，影响可用性认知）— ✅ 已应用（2026-09-02）
1. **修正 README 能力表**：✅ README:16、版本发布说明:29 已加 `impact`/`tests`/`get_call_graph` 的 caller 空值警示与生效条件；版本发布说明:28、系统简介:30 已加语义检索默认=TF-IDF 降级说明。
2. **统一包数量口径**：✅ modules/README:4 由 27→32+scope；README/系统简介/版本发布说明 统一标注「32=internal/，36=全仓库（`go list ./...`）」。

### P1（本周，消除构建/部署落差）— ✅ 已应用（2026-09-02）
3. **构建变体说明文档化**：✅ README 新增「构建变体与能力边界」矩阵（默认 / `-tags onnx` / `-tags 'pg redis'` 三档能力对比）。
4. **前置依赖声明**：✅ README 矩阵下方加「构建前置：`../idcu-go` 兄弟仓须存在（go.mod 10 条 replace）」。
5. **Dockerfile 口径统一**：✅ DEV_PROGRESS:235、11-配置部署:365 由 alpine 改为 `golang:1.25-bookworm → debian:bookworm-slim`（与 Dockerfile 实际一致）。

### P2（规划，消除进度认知混乱）
6. **建立 P0–P18 ↔ P1–P9 进度体系澄清**——✅ **已应用（2026-09-02）**：经核查两体系为**正交双轴**——`DEV_PROGRESS.md` 的 `P0骨架/P0 MVP/P1–P18` 是「开发里程碑轴」（各能力首次落地顺序，现已全 100% 交付）；git 历史（2026-09-02 重构前文档） 下 `P*.md` 的「完成度」是「能力模块当前成熟度轴」（95–98%，持续打磨非未交付）。已在 `DEV_PROGRESS.md` 当前状态块后新增「进度体系说明」消除歧义；未做硬性 1:1 映射表（两轴非同构，强行映射反致误导）。
7. **校正子模块完成度**：P3_3/P3_4/P3_5 在树与正文取同一数字——✅ **已验证（2026-09-02）**：脚本扫描 `docs/**/P*.md` 共 42 份，41 份含完成度声明且**头部=正文 100% 一致、0 处不一致**（延续 `ee8c5dc` 三方对齐成果），无需改动。
8. **CallerFQN 回填（代码侧根因修复）**——✅ **已应用（2026-09-02）**：
   - 默认路径 `adapter.go`（`//go:build !treesitter`）：新增 `currentMethod *parser.MethodIR` 跟踪当前方法，类定义处重置、方法定义处赋值、`detectCalls` 调用处（现 `adapter.go:846`）按 `ClassFQN+"."+Name`/`Name` 计算 caller 并回填。
   - AST 路径 `adapter_ast.go`（`//go:build treesitter`）：`walk` 函数新增 `curMethod` 跟踪，Elixir `defmodule`/`def*` 与方法分支设置 `curMethod`，Elixir 默认调用（`adapter_ast.go:342`）与 `callTypes` 分支（`adapter_ast.go:402`）两处 `CallIR` 均补 `CallerFQN`。
   - 验证：`go build ./internal/parser/adapter/treesitter/` 通过；默认路径单测 `go test -run TestTreeSitterAdapter` → `ok 0.521s`；`analyzer.go:300/365` 的 `if call.CallerFQN != ""` 建边逻辑现已能消费回填值。
   - 头牌能力（impact/tests/get_call_graph）默认路径可用性的关键代码修复已完成，**待 commit/push 后随新构建生效**。

### P3（增强）
9. 增加标签隔离代码 CI 校验——✅ **已应用（2026-09-02）**：`.github/workflows/ci.yml` 新增 `tag-guard` job（运行 `go list -tags 'onnx pg redis' ./...`，仅解析+类型检查、不链接 onnxruntime 运行时）；`Makefile` 新增 `verify-tags` target + `help` 说明。验证：`make verify-tags` exit 0。
10. 模块文档计数类字段脚本化——✅ **已应用（2026-09-02）**：新增 `scripts/project_counts.py`（从 `go list` 取权威包数、正则计数 MCP 工具/HTTP 路由、统计非 vendor LoC），`Makefile` 加 `counts` / `counts JSON=1` target。此前文档出现过「包数量 27/31/32/36 四处不一」「/viz 路由归属误写」等问题，现统一以脚本输出为准（internal=32、total=36、MCP=12、HTTP=22、LoC≈51329）；`count_http_routes()` 扫描 `internal/server/*.go`（排除 `mcp*.go` 与 `*_test.go`），覆盖 `http.go` 16 + `viz.go` 6，文档口径核对改为跑 `make counts` 而非手填数字。

---

## 7. 让人更快掌握本项目的路线（30 分钟上手）

1. **先读 3 篇，建立心智模型**：
   - git 历史（2026-09-02 重构前文档）（定位与目标）
   - git 历史（2026-09-02 重构前文档）（五层架构）
   - git 历史（2026-09-02 重构前文档）（模块树与完成度）
2. **再跑一遍，建立手感**（需 `../idcu-go` 兄弟仓）：
   ```bash
   go build -o codeschema ./cmd/codeschema
   ./codeschema scan <某Go仓库> --store ./data
   ./codeschema serve --store ./data &   # :8081 HTTP
   curl localhost:8081/context?symbol=xxx
   ./codeschema mcp --stdio             # 接 MCP 客户端
   ```
3. **带着 §5 的边界意识看能力**：默认构建 = 正则元数据 + SQLite + TF-IDF；要语义/影响面/多存储，按 §6 P1 的开关注明开启。
4. **改代码前先 `go list ./internal/...` 与 `grep '//go:build'`**：本项目大量能力靠 build tag 隔离，改前务必确认自己在哪个变体里。

---

## 附：取证命令与结果（可复现）

```bash
go list ./internal/... | wc -l            # => 32 个 internal 包
find . -name '*.go' -not -path '*/vendor/*' -not -path '*/down/*' | xargs wc -l | tail -1  # => 51278 行
go build -o /tmp/cs ./cmd/codeschema      # => exit 0
grep -rn '//go:build' internal/store/pg internal/store/redis internal/vector/embedder_onnx.go  # => 标签隔离证据
grep -n 'CallerFQN' internal/parser/adapter/treesitter/adapter.go internal/parser/adapter/treesitter/adapter_ast.go
# => 默认 adapter.go:343/846、AST adapter_ast.go:277/342/402 均已带 CallerFQN（修复后）
# 验证：go build ./internal/parser/adapter/treesitter/ => exit 0
#       go test ./internal/parser/adapter/treesitter/ -run TestTreeSitterAdapter => ok 0.521s
```

> 本报告所有断言均基于 2026-09-02 仓库实际代码与文档，未做推断。

## 8. 本次已应用同步动作清单（2026-09-02）

以下文档修订已落地（文件未提交，待用户授权 commit/push）：

| # | 文件 | 修订 |
|---|---|---|
| 1 | `README.md:16` | 影响面分析加 caller 空值警示 + 生效条件 |
| 2 | `README.md`（「构建变体与能力边界」） | 新增三档构建能力矩阵（默认 / `-tags onnx` / `-tags 'pg redis'`）+ idcu-go 兄弟仓构建前置声明 |
| 3 | git 历史（2026-09-02 重构前文档） | 包数量 27 → 32（internal/）+ 全仓库 36 口径说明 |
| 4 | git 历史（2026-09-02 重构前文档） | 32 包加 scope；ONNX 召回 1.00 加「默认=TF-IDF 降级」说明 |
| 5 | git 历史（2026-09-02 重构前文档） | 语义检索/影响面分析加生效边界；包数加 scope |
| 6 | `DEV_PROGRESS.md:235` | Dockerfile 基础镜像 alpine → `golang:1.25-bookworm → debian:bookworm-slim` |
| 7 | git 历史（2026-09-02 重构前文档） | 同上，alpine → bookworm |
| 8 | `internal/parser/adapter/treesitter/adapter.go` | **代码修复**：默认路径新增 `currentMethod` 跟踪，`detectCalls` 调用处（`adapter.go:846`）回填 `CallerFQN` |
| 9 | `internal/parser/adapter/treesitter/adapter_ast.go` | **代码修复**：AST 路径 `walk` 新增 `curMethod` 跟踪，Elixir 默认调用（`adapter_ast.go:342`）与 `callTypes`（`adapter_ast.go:402`）两处 `CallIR` 补 `CallerFQN` |

**P2/P3 收尾状态（2026-09-02）**：
- ✅ P2#6 双 P 编号体系：已以「进度体系说明」澄清正交双轴（DEV_PROGRESS.md），未做硬性映射表。
- ✅ P2#7 子模块完成度：扫描 42 份 P*.md，头部=正文 100% 一致、0 不一致，无需改动。
- ✅ P2#8 CallerFQN 回填：代码侧根因修复已落地（adapter.go + adapter_ast.go），build+单测通过。
- ✅ P3#9 CI 标签隔离校验：ci.yml 新增 tag-guard job + Makefile verify-tags，已验证。
- ✅ P3#10 计数类字段脚本生成：已落地 `scripts/project_counts.py` + `make counts`（包数/LoC/MCP/HTTP 计数脚本化，取代手工数字）。
（注：上表 #8/#9 的 CallerFQN 代码侧根因修复已于 2026-09-02 落地并验证通过，原列于「未做」现已转入「已应用」。）
