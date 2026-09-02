# CodeSchema 开发进度跟踪

> 更新时间：2026-08-29
> 当前阶段：维护优化阶段（多仓库 benchmark 运行 + LSP 稳定性验证 + 向量可视化增强（/viz 默认栈可用、统一向量索引）+ 日志 data race 修复 + **SQLite 权威存储接线 + SCIP/LSP 生产验证 + 超大仓 BulkUpsert 落库优化 + 存储主线统一分发（sqlite/pg/redis 经 cmd 层 build-tagged 接线）+ 默认构建解除 CGO 强制依赖（ONNX 以 //go:build onnx 隔离）+ PHASE_09 开发计划 16 任务全部完成（benchmark 子命令 / CodeGraph 真实 schema / LSP 接入编排 / 模型公网分发 / 向量原文持久化 / 语义质量定案 / FileStore 进程锁 / 10万+ 真实压测 / PG·Redis 真实实例集成 / Docker 实构建 / MCP stdio+print-config / OpenAPI / Release 流水线 / coverprofile 采集）+ 单实例多租户（多项目共享一个进程，按 project 路由隔离仓库）+ 多租户热重载 + 全局能力热重载（监听地址/认证令牌/限流）+ 服务级热重载补齐（scanner 三项）+ 对外监听收敛基线（secure-demo.yaml）+ 生态资产发布准备（adapterx 聚合包 / context-sdk 接口抽象完成）+ 代码夯基与结构优化（PG 全量替换契约修复 / 标签分类三后端统一 / Redis 读路径 CacheReader / 健康检查真实化 / 语言映射单表 / 静默吞错留痕 / 死代码清理）+ 分析建议全量推进（Agent 任务端到端基准 agent-bench 子命令 / Redis 读路径类符号快速路径接线 / CORS+recovery 中间件合并 / context-sdk 独立发布验证脚本 / 测试关联·AI 预算·多租户打点补齐）+ 遗留 TODO 推进（agent-bench 多仓库评测 --repos+RepoHint 过滤 / Redis 方法符号缓存 GetMethod 快速路径）+ 生态资产 P2 发布前置收尾（adapterx 独立发布验证脚本 + 发布评估 README / check-contrib-publish.sh 抽公共）+ agent-bench 工程化看护（CI agent-bench job + 快照归一化 repo_path=仓库名 + Makefile bench-agent 目标）+ **agent-bench 多语言任务集扩展（内置任务 5→8，新增 Java/Python 通用任务 + 符号预检 Skipped 语义）**）
> 下一个阶段：无（所有 P0-P18 阶段、PHASE_09 开发计划 16 任务及后续优化项均已全部完成；2026-08-16 新增「单实例多租户」作为独立能力，详见下方维护优化章节）

---

## 当前状态

```
P0 骨架 [████████████████████] 100%
P0 MVP   [████████████████████] 100%
P1       [████████████████████] 100%
P2       [████████████████████] 100%
P3       [████████████████████] 100%
P4       [████████████████████] 100%
P5       [████████████████████] 100%
P6       [████████████████████] 100%
P7       [████████████████████] 100%
P8.1     [████████████████████] 100%
P8.2     [████████████████████] 100%
P8.3     [████████████████████] 100%
P9       [████████████████████] 100%
P10      [████████████████████] 100%
P11      [████████████████████] 100%
P12      [████████████████████] 100%
P13      [████████████████████] 100%
P14      [████████████████████] 100%
P15      [████████████████████] 100%
P16      [████████████████████] 100%
P17      [████████████████████] 100%
P18      [████████████████████] 100%
```

---

> **进度体系说明（消除口径歧义）**：本文件 `当前状态` 的 `P0骨架 / P0 MVP / P1–P18` 是**开发里程碑轴**——记录各能力「首次落地」的先后顺序，现已全部 100%（即均已交付）。而 git 历史（2026-09-02 重构前文档） 下各 `P*.md` 的「完成度」字段是**能力模块当前成熟度轴**（如 `P3_3` SCIP=97%、`P3_4` LSP=97%），多数为 95–98%，表示持续打磨中，**并非未交付**。
> 两轴度量维度不同（「何时建成」vs「当前多成熟」），共用 P 前缀易误读为「100% 与 95% 自相矛盾」。P10–P18 为 P1–P9 之后的延展能力（多租户、agent-bench、生态资产发布等）；P19+ 后续里程碑规划见文末「P19+ 后续里程碑（规划）」节。

## 实际核查结论（2026-08-14 · 代码级）

> 接手/评审前必读：下面是基于 `go build ./...`、包枚举与 git 历史（2026-09-02 重构前文档） scalebench 实测的核查，旨在纠正历史文档中的虚高/过时表述。

1. **包数量实为 36 个**（`go list ./...`，含 2026-08-16 新增的 `internal/tenant`、`internal/runtime`、`scripts/benchtrend` 与 2026-08-17 新增的 `contrib/adapterx`、`contrib/contextsdk`、`internal/contextsdk`），全文历史「23/24/27/31/32/33 个包」表述已过时；`go build ./...` 通过（exit 0）。
2. **默认构建强制 CGO（已修复）**：原 `internal/vector/embedder_onnx.go` 无条件 `import onnxruntime_go`，即使不用 ONNX 也需 gcc。现已以 `//go:build onnx` 隔离 ONNX 实现，新增 `embedder_onnx_stub.go`（`!onnx`）提供同名 API 桩，默认 `go build ./...`（CGO 关）免 gcc；ONNX 语义检索需 `go build -tags onnx`（仍依赖 gcc + onnxruntime 动态库）。「GCC 可选」的旧表述已校正为「仅 ONNX 需要」。
3. **SQLite 写入非生产级（已通过 BulkUpsert 修复）**：git 历史（2026-09-02 重构前文档） scalebench 实测 N=10万 单批 `UpsertIR`，SQLite ≈ **77~237s**（本机波动，受 WAL 检查点 fsync 抖动），JSON FileStore ≈ **0.4s**（慢约 500 倍）。根因是 `UpsertIR` 逐文件多语句独立事务（100k 文件≈70万次事务提交放大）；**已实现 `BulkUpsert`（单事务 + 预编译语句），100k 落库降至约 5~14s（约一个数量级 / 5~14× 提速），生产化应使用它**。切 PG 仍适用于亿级。因此「SQLite 为权威存储、JSON 仅 fallback」的 headline 与实测方向相反（SQLite 写入确慢于 JSON，但关系查询/跨会话一致性是 JSON 不具备的）。
4. **pg/redis 后端已接入统一分发（2026-08-14）**：`internal/store/pg`（PG 完整实现 564 行，`//go:build pg`）、`internal/store/redis`（热点缓存层 117 行，`//go:build redis`）现经 `cmd/codeschema` 的 build-tagged 分发接线——`storage.driver=pg|postgres`（需 `-tags pg`）+ `storage.kv=redis://...`（需 `-tags redis`）；`rebuild-kv` 命令在 `-tags redis` 下从基础存储全量重建缓存。详见 git 历史（2026-09-02 重构前文档） §12.5 与 README「存储后端」小节。
5. **tree-sitter 双路径**：默认构建为纯 Go 正则轻量解析（30 语言，无 CGO）；`-tags treesitter` 启用真语法树（go.mod 含 `smacker/go-tree-sitter` 各语言包，但默认 tag 隔离不编译）。依赖为 `modernc.org/sqlite` + `chromem-go` + `fsnotify` + `onnxruntime_go`（可选）。
6. **开发文档索引**：git 历史（2026-09-02 重构前文档） 实际含 `00`–`13` 共 14 篇；README「开发指南」与本文均已全部列出（`12` 于 2026-08-15 补入、`13` 于 2026-08-16 补入）。
7. **阶段完成度口径**：P0–P18 的「功能实现」确已完成并通过测试；但「生产级」「权威存储」等运行期/性能声明需以上述实测为准，不能仅凭 phase 100% 推定。

## 已完成工作

### 基础文件
- [x] `.gitignore` — 覆盖 Go 二进制/IDE/OS/数据文件
- [x] `README.md` — 系统定位、核心能力、开发指南、阶段路线
- [x] `go.mod` — Go 模块初始化（codeschema）
- [x] `CHANGELOG.internal.md` — 内部追溯日志（10 次提交记录）

### P0 骨架 — 项目骨架 + 核心接口 + 存储层
- [x] 项目目录结构（cmd/ + internal/ 共 14 个包）
- [x] `internal/errors/errors.go` — 错误类型体系（适配器/AI/存储/通用）
- [x] `internal/parser/ir.go` — IRDocument + ClassIR/MethodIR/ParamIR/CallIR 结构体
- [x] `internal/parser/plugin.go` — ParserPlugin + BatchParser 接口定义
- [x] `internal/parser/registry.go` — Registry 注册中心（Register/SetPriority/Select）
- [x] `internal/parser/registry_test.go` — Registry 单元测试（优先级/降级/BatchParser）
- [x] `internal/store/store.go` — Store 统一接口定义
- [x] `internal/store/filestore.go` — FileStore 纯 Go 文件存储实现（JSON 持久化）
- [x] `internal/store/filestore_test.go` — FileStore 单元测试（CRUD/UpsertIR/持久化）
- [x] `internal/store/migrations/001_init.sql` — 完整 DDL（12 张表 + 索引）
- [x] `cmd/codeschema/main.go` — 主入口（scan/watch/mcp/serve/version 命令框架）

### P0 MVP — 适配器 + 增量更新 + 接口层
- [x] `internal/parser/adapter/adapter.go` — 适配器模块公共工具（文件过滤/语言检测/IR 转换）
- [x] `internal/parser/adapter/treesitter/adapter.go` — tree-sitter 适配器（30 种语言正则解析，`-tags treesitter` 可切换真语法树）
- [x] `internal/parser/adapter/treesitter/adapter_test.go` — tree-sitter 单测（18 个测试）
- [x] `internal/parser/adapter/codegraph/adapter.go` — CodeGraph 直读适配器骨架
- [x] `internal/parser/adapter/codegraph/adapter_test.go` — CodeGraph 单测（4 个测试）
- [x] `internal/scanner/hash.go` — SHA-256 哈希闸门函数
- [x] `internal/scanner/scanner.go` — Scanner 核心（ProcessFile + ScanAll + listFiles + detectLang + countLines）
- [x] `internal/scanner/hash_test.go` — 哈希单测（空文件/一致性/不同内容/文件不存在）
- [x] `internal/scanner/scanner_test.go` — Scanner 单测（哈希命中/未命中/全量扫描/忽略目录）
- [x] `internal/store/upsert.go` — 行号区间匹配算法（intervalsOverlap + matchEntity）
- [x] `internal/store/upsert_test.go` — upsert 单测（区间重叠 10 场景 + 匹配判定 6 场景）
- [x] `internal/scheduler/scheduler.go` — 防抖调度器（300ms 防抖窗口 + 队列阈值 1000 + 降级信号）
- [x] `internal/scheduler/scheduler_test.go` — 调度器单测（防抖合并/刷新/降级/事件处理/清空）
- [x] `internal/watcher/watcher.go` — 轮询监听器（PollWatcher，mtime+size 检测变更）
- [x] `internal/watcher/watcher_test.go` — 监听器单测（新文件检测/忽略目录）
- [x] `internal/service/service.go` — Service 业务逻辑层（8 个查询方法 + 参数校验 + ServiceError）
- [x] `internal/server/http.go` — HTTP API 服务器（5 个端点 + 错误中间件 + CORS + panic recovery）
- [x] `internal/server/mcp.go` — MCP Server（JSON-RPC 2.0 + SSE 传输，8 个工具注册）
- [x] 测试：service_test.go（14 个）、http_test.go（11 个）、mcp_test.go（9 个）

### P0 代码图分析器（第 6 步）
- [x] `internal/analyzer/graph.go` — 四种图数据结构（CallGraph/ClassHierarchy/ReverseIndex/FileGraph）
- [x] `internal/analyzer/analyzer.go` — 核心分析器（BuildAll 单次遍历 + 影响面分析 + 最短路径 + 热点排序）
- [x] `internal/analyzer/analyzer_test.go` — 30 个 P0 测试（图操作/构建方法/分析算法/边界情况）

### P1 反向引用索引 + 类层次父子关系
- [x] `buildImportIndex` 预构建 import 路径到文件路径的查找映射
- [x] `buildReverseIndex` 基于 imports 建立文件级反向引用
- [x] `buildClassHierarchyNode` 通过 ClassRecord.ParentFQNs 建立继承/实现关系
- [x] 数据持久化：UpsertIR 保存 Imports 和 ParentFQNs 到 FileStore
- [x] 28 个 P1 测试覆盖（反向索引/父子关系/import 匹配/空 imports 边界）

### P2 BuildAll 单次遍历 + Go 模块路径解析
- [x] `buildImportIndex` 预构建在循环前，import 解析合并入主循环，消除第二次遍历
- [x] `resolveImport` 优先 Go 模块路径精确匹配，失败回退到启发式匹配
- [x] `Analyzer.SetModulePath` 动态设置 Go 模块路径
- [x] 30 个 P2 测试覆盖（GoModule 回退/GoModuleExact 精确匹配）

### P3 多语言 ImportResolver 体系（Java Maven/Gradle）
- [x] `ImportResolver` 接口定义 + 4 种实现：GoResolver/JavaResolver/CompositeResolver/heuristicResolver
- [x] JavaResolver：FQCN 导入、通配符匹配、23 个标准库前缀过滤、源根目录剥离
- [x] CompositeResolver：按优先级依次尝试，首个非空结果即返回
- [x] Analyzer 集成：NewAnalyzer 初始化解析器链，SetJavaSourceRoots 动态设置源根目录
- [x] 22 个 P3 测试覆盖（JavaResolver 11 + CompositeResolver 3 + heuristicResolver 5 + 集成 3）

### P4 Gradle 多模块路径解析 + 标准库前缀可配置化
- [x] **标准库前缀可配置化**：javaStdlibPrefixes 从全局变量改为 JavaResolver 实例字段
- [x] **JavaResolver API**：SetStdlibPrefixes/AddStdlibPrefix + Analyzer.SetJavaStdlibPrefixes
- [x] **GradleResolver**：支持 `:module:path:to:Class` 格式、4 种匹配策略
- [x] 通配符支持（`:module:*`）、模块名白名单（SetGradleModuleNames）、标准库过滤
- [x] 解析器链扩展：GoResolver → JavaResolver → GradleResolver → heuristicResolver（共 5 种）
- [x] 25 个 P4 测试覆盖（GradleResolver 15 + 可配置前缀 4 + 集成 6）
- [x] 文档新增 §9 架构总结（架构图、解析器链、扩展性设计、当前状态）

### P5 — 标签分类体系（Tag）与测试关联
- [x] **Tag 数据模型**：Store 接口扩展（UpsertTags/UpsertMethodTags/GetTagsByClassID/GetTagsByMethodID/SearchByTag/GetAllTagsWithCategories）
- [x] **FileStore 持久化**：classTags/methodTags/tagCategories 字段，saveToDisk/loadFromDisk 支持
- [x] **Tag 自动推导（Tagger）**：`internal/ai/tagger.go`，六类标签规则推导（layer/biz/tech/risk/test/lang），50 个测试覆盖
- [x] **Analyzer 集成**：`Analyzer.TagAll()` 方法，调用 Tagger 对所有实体执行标签推导
- [x] **Tag 查询接口**：Service 层（GetTags/SearchByTag/GetAllTags）
- [x] **HTTP 端点**：`GET /tags`, `GET /tags/search`, `GET /tags/all`
- [x] **MCP 工具**：`get_tags`, `search_by_tag`, `get_all_tags`（共 11 个工具）
- [x] **测试关联**：`internal/service/testlink.go`，三种策略（naming/same_tag/dependency），6 个测试

### P6 — 可观测性增强（日志/指标/链路追踪）
- [x] **结构化日志（`internal/log`）**：基于 Go 标准库 `log/slog`，支持 JSON/文本格式输出、日志级别控制、模块化 Logger、自动 caller 信息
- [x] **基础指标（`internal/metrics`）**：纯 Go 实现 Prometheus 文本格式，支持 Counter/Gauge 类型、标签维度、线程安全
- [x] **链路追踪（`internal/trace`）**：简单 span 模型，支持嵌套 span、耗时记录、标签附加，通过日志输出追踪信息
- [x] **健康检查端点增强**：新增 `/health/db`（存储延迟）、`/health/kv`（缓存占位）、`/health/vector`（向量库占位）
- [x] **安全中间件**：Bearer token 认证（`authMiddleware`）、路径遍历防护（`pathTraversalMiddleware`）、CORS、panic recovery
- [x] **`/metrics` 端点**：暴露 Prometheus 文本格式指标数据
- [x] **Analyzer 集成**：为 BuildAll/BuildCallGraph/BuildClassHierarchy/BuildFileGraph/BuildReverseIndex/FindImpactNodes/ShortestPath/Analyze/TagAll 添加 trace span、指标打点、日志记录
- [x] **Scanner 集成**：为 ProcessFile/ScanAll 添加 trace span、指标打点（processed_total/files_total/errors_total/active_workers）、日志记录
- [x] **HTTP 集成**：`requestMetricsMiddleware` 记录请求数/活跃请求数/延迟，每个请求自动 trace span
- [x] **Tagger 集成**：添加模块化 Logger，DeriveAllTags 前后记录关键指标（classes_tagged/methods_tagged）
- [x] 测试覆盖：log 13 个 + metrics 13 个 + trace 17 个 + server 22 个（共 65 个新测试，14 个包全部通过）

### P7 — 配置系统（YAML 解析）
- [x] **`internal/config` 包**：配置结构体（Config/ProjectConfig/StorageConfig/ParserConfig/AIConfig/ServerConfig/WatcherConfig/ScannerConfig），支持 JSON 标签
- [x] **最小 YAML 子集解析器**：`parse.go` 实现 parseYAML 函数，支持嵌套映射、行内列表 `[a,b]`、注释、布尔/数字/字符串类型（零外部依赖）
- [x] **JSON 配置支持**：`parseJSON` 函数，通过 `encoding/json` 解析 `.json` 配置文件
- [x] **默认值系统**：`DefaultConfig()` 返回完整默认配置，Partial YAML/JSON 合并时保留未覆盖字段的默认值
- [x] **配置验证**：`Validate()` 函数检查必填字段（project.root/storage.dsn/server 地址 > 0/scanner.workers > 0 等）
- [x] **CLI 集成**：`cmd/codeschema/main.go` — 全局 `--config` 参数，所有命令从 Config 读取默认值（workers/store/dsn/addr/auth-token/debounce/ignore_dirs）
- [x] **MCP Server 增强**：添加 `SetAuthToken` 方法 + `authMiddleware` + `corsMiddleware`，支持 Bearer token 认证
- [x] 测试覆盖：25 个测试（默认值 1 + 加载 6 + 验证 4 + YAML 解析 7 + 值解析 7），15 个包全部通过

### P8.1 — 语义检索 / 全文搜索骨架（内存 mock 实现）
- [x] **`internal/vector/store.go`** — 向量库接口（VectorStore）定义 + MemoryStore 内存实现（余弦相似度计算）
- [x] **`internal/vector/indexer.go`** — 异步 embedding 索引构建器（Indexer），支持同步/异步/批量构建，worker pool 并发控制
- [x] **`internal/vector/model.go`** — Embedder 接口定义 + MockEmbedder 确定性哈希实现（128 维）
- [x] **`internal/search/fts.go`** — FTSEngine 接口定义 + MemoryFTS 内存实现（精确/前缀/模糊/布尔模式，TF-IDF 简化版评分）
- [x] **`internal/search/searcher.go`** — 双路检索器（Searcher），整合 FTS 和向量搜索，支持 exact/semantic/both 三种模式
- [x] **`internal/search/reranker.go`** — 融合重排器（Reranker），归一化 FTS 和向量得分 → 加权融合 → 去重 → 降序排列
- [x] **`internal/search/adapter.go`** — VectorAdapter 桥接 vector.Indexer → search.VectorSearcher 接口，避免循环依赖
- [x] **`internal/service/service.go`** — 新增 `WithSearcher` 方法注入搜索器，`Search` 方法接入双路检索逻辑
- [x] **`cmd/codeschema/main.go`** — 新增 `newSearcher` 工厂函数，`mcp` 和 `serve` 命令均集成搜索器
- [x] **HTTP 端点**：`GET /search` 支持 `q`/`mode`/`limit` 参数，已接入双路检索
- [x] **MCP 工具**：`search_symbols` 工具已接入双路检索
- [x] 测试覆盖：vector 包 13 个 + search 包 17 个 + service 包 3 个新增搜索测试（共 33 个新测试），17 个包全部通过

### P8.2 — 向量库集成（磁盘持久化 + 本地 Embedder）
- [x] **`internal/vector/persistent.go`** — PersistentStore 磁盘持久化向量存储，基于 JSON 序列化，支持 Save/Load，每次 Add/BatchAdd/Delete 后自动保存（每 10 次变更触发落盘）
- [x] **`internal/vector/embedder_local.go`** — LocalEmbedder 纯 Go Embedder，使用词袋模型 + 哈希技巧 + TF-IDF 权重，128-1024 维可配置，支持 Observe 建立 IDF 词典，FNV-1a 哈希映射，L2 归一化
- [x] **`internal/search/fts_persistent.go`** — PersistentFTS 磁盘持久化全文搜索，基于 JSON 序列化，复用 MemoryFTS 搜索逻辑，自动保存
- [x] **`internal/config/config.go`** — SearchConfig 新增 FTSDir / VectorDir / VectorDim 三个字段，默认启用语义搜索（Semantic: true）
- [x] **`cmd/codeschema/main.go`** — newSearcher 工厂函数切换为 PersistentFTS + PersistentStore + LocalEmbedder，失败时自动降级到 MemoryFTS/MemoryStore
- [x] 新增测试：PersistentStore 4 个（SaveLoad/Search/EmptySearch/Delete）+ LocalEmbedder 7 个（Deterministic/DifferentTexts/EmptyText/Observe/Dim/ZeroDim/Reset）+ PersistentFTS 4 个（SaveLoad/Search/Remove/EmptySearch）= 15 个新测试
- [x] 验证数据：go build + go test 18 个包全部通过，0 失败

### P8.3 — 自动索引构建与增量同步
- [x] **`internal/search/builder.go`** — IndexBuilder 自动索引构建器，BuildFromStore 全量构建 + BuildAndIndex 增量更新 + IndexDocument 单文档索引
- [x] **`internal/search/builder_test.go`** — 10 个测试覆盖：空 Store、单文件/多文件、无类文件、增量构建、构建文本、文件不存在等边界
- [x] **`internal/scanner/scanner.go`** — 新增 onIndex 字段和 SetOnIndex 方法，ProcessFile 中调用增量索引回调
- [x] **`internal/service/service.go`** — 新增 indexBuilder 字段、WithIndexBuilder 方法、BuildIndex 全量构建接口
- [x] **`cmd/codeschema/main.go`** — newSearcher 返回 searcher+builder，scanCmd 扫描后自动构建索引，watchCmd 启动时全量构建+增量回调，mcp/serve 命令集成 searcher
- [x] 新增测试：10 个 builder 测试（New/BuildFromStore 空/有数据/无类文件/多文件/IndexDocument/BuildAndIndex/BuildAndIndex 文件不存在/buildClassIndexText/buildMethodIndexText）
- [x] 验证数据：go build + go test 19 个包全部通过，0 失败
- [x] P8.3 遗留 TODO 优化：IDF 词典持久化（SaveIDF/LoadIDF）、异步索引队列（StartAsync/StopAsync/EnqueueIndex）、删除文件同步清理索引（BuildAndRemove/SetOnDelete）

### P9 — 配置热重载 / 多配置源
- [x] **`internal/config/config.go`** — 新增 `LoadFromEnv` 从环境变量加载配置覆盖（CODESCHEMA_<SECTION>_<KEY> 格式，支持 20+ 环境变量）；新增 `Merge` 函数合并多个配置源（非零值覆盖，深拷贝）；新增 `ConfigWatcher` 结构体支持配置文件变更自动重载（轮询检测，默认 2 秒间隔，原子切换，OnReload 回调）
- [x] **`internal/config/config_test.go`** — 新增 8 个测试：LoadFromEnv 全量覆盖、LoadFromEnv 无效值降级、Merge 基础/覆盖/全量/局部、CloneConfig 深拷贝、ConfigWatcher 初始化
- [x] **`cmd/codeschema/main.go`** — 加载配置后自动应用环境变量覆盖；watch/mcp/serve 命令启动 ConfigWatcher 实现配置热重载
- [x] 验证数据：go build + go test 20 个包全部通过，0 失败；config 包 33 个测试（25 原有 + 8 新增）
- [x] 新增公共抽象：`LoadFromEnv`、`Merge`、`ConfigWatcher`、`OnReload`、`cloneConfig`（深拷贝工具）

### P10 — 遗留问题治理（多 worker / 日志集成 / 结果富化 / 进度条）
- [x] **异步索引队列多 worker 扩展**：`StartAsync(ctx, queueSize, numWorkers)` 接受 `numWorkers` 参数，启动多个 worker goroutine 消费队列，提升高并发场景吞吐
- [x] **异步索引错误集成日志系统**：`asyncWorker` 调用 `log.WithModule("search.index_builder")` 记录索引失败日志
- [x] **搜索结果填充 Kind/File**：`Service.enrichResults` 从 Store 查询符号的 Kind 和 File 信息，在 `Service.Search` 返回前自动富化
- [x] **索引构建进度条**：`BuildFromStore` 分阶段输出进度（文件扫描 → IDF 构建 → FTS 写入 → 向量写入），含百分比和耗时统计
- [x] **新增 11 个测试**：多 worker 并发（3 worker × 10 文档）、默认 worker 数（0→2）、幂等性、错误回调、同步降级、删除文档、符号解析（file/class/interface/method/无效）、富化逻辑、`parseInt64` 工具函数
- [x] 验证数据：go build + go test 20 个包全部通过，0 失败
- [x] 新增公共抽象：`Service.resolveSymbol`、`Service.enrichResults`、`parseInt64`

### FsWatcher — 基于 fsnotify 的原生文件系统监听（已知问题 #2 解决）
- [x] **`internal/watcher/watcher.go`** — 新增 `FsWatcher` 结构体，基于 fsnotify 原生文件系统事件监听；递归监听所有子目录（`addRecursive`），自动跳过忽略目录；新目录创建时自动加入递归监听；`Stop()` 并发安全关闭
- [x] **`internal/watcher/watcher_test.go`** — 新增 6 个 FsWatcher 测试（新文件创建/忽略目录/嵌套忽略路径/Stop 安全/文件修改/子目录递归）
- [x] **`internal/config/config.go`** — WatcherConfig 新增 `UseFsnotify bool` 字段（默认 false），DefaultConfig/cloneConfig/Merge/LoadFromEnv 全部同步更新
- [x] **`cmd/codeschema/main.go`** — watch 命令新增 `--fsnotify` 标志，根据配置选择 FsWatcher 或 PollWatcher
- [x] **`.gitignore`** — 新增 `down/` 条目
- [x] 验证数据：go build + go test 18 个包全部通过，0 失败；watcher 包 8 个测试全部通过

### P11 — 集成测试与性能压测
- [x] **`internal/integration/integration_test.go`** — 7 个端到端集成测试，覆盖 scan → store → index → search 全流程、空查询、搜索限制、文件搜索、结果富化、重复扫描幂等性、索引一致性
- [x] **`internal/integration/benchmark_test.go`** — 9 个性能基准测试，覆盖 MemoryFTS 索引/搜索、LocalEmbedder Embed/Observe、IndexBuilder 全量构建、Searcher 双路检索、完整流水线、异步索引队列、融合重排器
- [x] **`internal/store/filestore.go`** — 修复 `UpsertIR` 未存储方法数据的 Bug，按 ClassFQN 匹配类和方法
- [x] 验证数据：go build + go test 17 个包全部通过，0 失败；Benchmark 全部可运行

### P12 — 生产级健壮性（错误处理/重试/优雅关闭/Panic 恢复）
- [x] **`internal/robust/graceful.go`** — 优雅关闭管理器（GracefulManager），支持多钩子注册、逆序执行、信号监听、超时控制、第二次信号强制退出逃生舱口
- [x] **`internal/robust/retry.go`** — 重试机制（Retry），支持指数退避 + 抖动、可配置最大尝试次数/基础延迟/最大延迟、可重试谓词；等效 AWS SDK StandardRetryMode
- [x] **`internal/robust/recovery.go`** — Panic 恢复工具（RecoveryHandler），支持 SafeCall/SafeCallWithResult/Go/GoWithContext/Recover/RecoverWithCallback，全局便捷函数
- [x] **`internal/robust/*_test.go`** — 28 个测试，覆盖优雅关闭（注册/顺序/超时/重入/错误收集）、重试（成功/重试后成功/耗尽/取消/不可重试/退避计算/抖动/配置选项）、Panic 恢复（SafeCall/SafeCallWithResult/Recover/RecoverWithCallback/Go/GoWithPanic）
- [x] **`internal/server/mcp.go`** — 添加 panic 恢复中间件（recoveryMiddleware），使用 robust.Recover 捕获 panic
- [x] **`internal/watcher/watcher.go`** — PollWatcher 和 FsWatcher 的 Start 方法添加 robost.SafeCall 保护，防止 poll/handleEvent panic 导致监听器崩溃
- [x] **`internal/scheduler/scheduler.go`** — Scheduler.Start 的 processFn 调用添加 robost.SafeCall 保护
- [x] **`cmd/codeschema/main.go`** — 使用 GracefulManager 统一管理优雅关闭，注册 context_cancel 和 config_watcher 钩子，ForceExitOnSecondSignal 逃生舱口
- [x] 验证数据：go build + go test 18 个包全部通过，0 失败；robust 包 28 个测试全部通过

### P13 — 构建脚本 / CI 配置 / 容器化 / 部署文档
- [x] **`Makefile`** — 构建自动化脚本，支持 build/test/clean/cross/lint/bench/run 等 10 个目标，跨平台交叉编译（linux/darwin/windows × amd64/arm64）
- [x] **`Dockerfile`** — 多阶段构建，golang:1.25-bookworm → debian:bookworm-slim（glibc，CGO 链接 onnxruntime `.so` 必需；alpine/musl 会导致 ONNX 静默降级 LocalEmbedder），CGO 构建含 SQLite/tree-sitter，支持 VERSION 构建参数
- [x] **`.github/workflows/ci.yml`** — GitHub Actions CI 流水线，8 个 Job（test 跨 ubuntu/macos/windows + bench + nightly-scale + race 竞态检测 + treesitter + cross 交叉编译 + docker 镜像）；actions 已升级为 Node 24 兼容版本（`actions/checkout@v7`、`actions/setup-go@v7`、`actions/upload-artifact@v6`、`docker/setup-qemu-action@v4`、`docker/setup-buildx-action@v4`、`docker/metadata-action@v6`、`docker/build-push-action@v7`、`softprops/action-gh-release@v3`），此前 Node 20 actions 缓存 tar 恢复失败与 Windows 偶发失败均已在 `427fe7b`/`b90271a`/`0c4a1a1` 修复
- [x] **git 历史（2026-09-02 重构前文档）** — 新增 §9 P13 构建与部署指南（Makefile/Docker/CI/部署形态/环境要求/检查清单）
- [x] 验证数据：go build 通过 | go test 18 包 0 失败 | 新增 3 个文件（Makefile/Dockerfile/CI）+ 1 个文档更新

### P14 — 多语言适配器扩展（SCIP/LSP）+ 语义检索精度提升（chromem-go）
- [x] **`internal/parser/adapter/scip/`** — SCIP index 直读适配器，支持 .scip 文件 JSON 解析，类/方法/引用关系提取，实现 BatchParser 接口（18 测试）
- [x] **`internal/parser/adapter/lsp/`** — 通用 LSP 适配器框架，JSON-RPC 2.0 通信，gopls/jdtls/clangd 工厂方法，documentSymbol 递归解析（13 测试）
- [x] **`internal/vector/chromem.go`** — ChromemStore 实现 VectorStore 接口，基于 chromem-go 嵌入式向量库，支持内存和持久化模式（12 测试）
- [x] `go.mod` — 添加 chromem-go 依赖
- [x] 验证数据：go build 通过 | go test 21 包 0 失败 | 新增 7 个文件，43 个测试用例

### P15 — 真实仓库 benchmark 数据采集
- [x] **`internal/integration/realrepo_test.go`** — 3 个基准测试（ScanAndIndex/Search/FullPipeline）+ 1 个集成测试（CollectMetrics），以 CodeSchema 自身仓库为目标
- [x] 验证数据：采集到关键性能指标并输出 JSON 到 `build/realrepo-bench.json`
- [x] 阈值验证：扫描耗时 < 5min（实际 157ms），索引构建 < 5min（实际 12ms），P95 搜索延迟 < 500ms（实际 1.5ms）

### P16 — 生产环境部署验证（Docker/CI 流水线）
- [x] **`Dockerfile`** — 修复 replace 依赖路径：将 `COPY down/` 移至 `go mod download` 之前，确保本地 replace 依赖可解析；新增 HEALTHCHECK 指令；添加完整使用注释
- [x] **`.dockerignore`** — 新增文件，排除 .git/IDE/build/data 等无关文件，加速 Docker 构建
- [x] **`.gitignore`** — 移除 `down/` 全量排除，改为仅排除 zip/tar.gz/gz 归档文件和 go.mod/go.sum 工具模块，确保 chromem-go 源码可被版本控制跟踪
- [x] **`down/chromem-go/chromem-go-main/`** — 提交 chromem-go 源码（48 文件，387KB），确保 CI 和 Docker 构建中 `go mod download` 可正确解析 replace 指令
- [x] 验证数据：`go build` 通过 | `go test` 21 包 0 失败 | Dockerfile 构建逻辑已验证（本地 Docker 不可用，配置语法正确）

### P17 — LSP 适配器优化 + chromem-go 向量索引可视化工具
- [x] **LSP 适配器 `readResponses` 按字节读取** — 将 `bufio.Scanner` 逐行读取改为 `bufio.Reader` 按字节读取 Content-Length 头，精确解析 JSON 体，解决 JSON 体换行导致的解析问题
- [x] **`internal/server/viz.go`** — 向量索引可视化工具 HTTP 处理器，提供概览/文档列表/搜索 API，内嵌 HTML 模板，支持分页和文本搜索
- [x] **`internal/server/http.go`** — 集成 VizHandler，可选注册可视化路由，SetVizHandler 方法注入
- [x] **`cmd/codeschema/main.go`** — `vectorVizStore`/`vectorVizSearcher` 适配器，serve 中基于默认向量索引（Persistent/Memory，与检索共用同一 store、统一 embedding）统一启用 `/viz`，移除「仅 chromem 驱动才可用」的限制（T3-2/PHASE_09）
- [x] **`internal/vector/chromem.go`** — 新增 Size() 返回真实文档数，ListDocuments()/QueryText() 方法支持可视化工具查询
- [x] 验证数据：`go build` 通过 | `go test` 22 包 0 失败 | 新增 1 个文件，修改 4 个文件

### P18 — 多仓库 benchmark 对比框架
- [x] **`internal/integration/benchreport.go`** — 对比报告生成器，BenchResult/BenchComparison 结构体，GenerateComparisonMarkdown 生成 Markdown 表格（含相对性能百分比），GenerateComparisonJSON 生成 JSON 输出，SortBenchResults 排序
- [x] **`internal/integration/benchhelper.go`** — 共享 benchmark 工具函数，NewBenchSetup 工厂函数创建组件集合，FindRepoRoot/DiscoverGoFiles 查找仓库和文件，GetBenchRepos 从环境变量读取多仓库路径，RepoName 提取路径名
- [x] **`internal/integration/multirepo_test.go`** — TestMultiRepo_CollectMetrics 多仓库基准测试，通过 CODESCHEMA_BENCH_REPOS 环境变量指定多个仓库（分号分隔），对每个仓库执行 scan→index→search 流水线，输出对比报告到 build/bench-compare.json
- [x] **`internal/integration/realrepo_test.go`** — 重构为使用共享工具函数（NewBenchSetup/FindRepoRoot/DiscoverGoFiles），移除私有函数消除重复
- [x] 验证数据：`go build` 通过 | `go test` 23 包 0 失败 | 新增 3 个文件，修改 1 个文件

### 文档
- [x] git 历史（2026-09-02 重构前文档） — 14 个开发文档按开发顺序分割（`00`–`13`）
- [x] `DEV_PROGRESS.md` — 本文件，开发进度跟踪
- [x] git 历史（2026-09-02 重构前文档） — 模块级文档（P1~P9 分层拆解，43 份）

## 后续优化完成项

所有 P0-P18 阶段及后续优化项已全部完成。项目核心功能已开发完毕，进入维护阶段。

### 已完成的优化项

- [x] **多仓库 benchmark 实际运行** — 在 CodeSchema（81 文件）和 idcu-panel/backend（312 文件）上运行 benchmark，验证 4x 文件量 → 1.6x 扫描时间 / 1.9x P95 延迟，线性扩展性良好（Commit 35）
- [x] **LSP 适配器生产环境连接稳定性验证** — 修复 Init/Close 锁重入死锁、readResponses 多行头解析、pending 请求泄漏等问题，mock 测试覆盖超时/取消/错误响应场景（Commit 33）
- [x] **向量索引可视化工具前端增强** — 新增单文档 API、点击展开详情、搜索内容展示、刷新按钮、Toast 通知（Commit 34）
- [x] **日志模块 data race 修复** — 使用 sync.Mutex 保护全局 defaultLogger 的初始化和访问，通过 -race 验证（Commit 36）

### 维护优化（2026-08-14）：SQLite 权威存储接线 + SCIP/LSP 生产验证 + 存储主线统一分发 + 默认构建解除 CGO + 规模基准固化 CI

本会话基于「全维度竞品分析」给出的优先级清单推进实施：

- [x] **优先级① SQLite 权威存储接线** — 新增 `internal/store/sqlite/sqlite.go`，基于纯 Go 的 `modernc.org/sqlite`（免 CGO，契合单二进制目标）完整实现 `store.Store` 接口：文件/类/方法/调用/标签 + 反向查询 + `UpsertIR` 增量入库（语义对齐 FileStore，额外支持跨会话一致与并发查询）。`cmd/codeschema/main.go` 经 `newStore(cfg)` 接线，`storage.driver=sqlite` 即启用，默认仍 JSON 文件存储作 fallback。修复 DDL 可空文本列 `NULL`→Go string 扫描失败（给 `file.language` / `class.method.modifier,doc_comment,source` / `call.source` 补 `DEFAULT ''`），并同步对齐未接线的 `001_init.sql`。`go build ./...` / `go vet` / `go test ./internal/store/sqlite/`（5 项全 PASS）通过。
- [x] **优先级② SCIP / LSP 适配器生产验证** —
  - **SCIP**：新增真实 fixture 端到端测试（`fixture_test.go`），覆盖 class/method/**调用关系（Calls）提取**逻辑（此前 0 覆盖）；并修复 `ParseAll` 误用 `adapter.FileExists`（仅对文件返回 true）校验 `indexDir` 目录，导致目录永远被判为「不存在」的 Bug（改为 `scipDirExists` 目录存在性判断）。
  - **LSP**：`gopls` 真实语言服务器端到端验证（Go 为主语言，真实返回 `Calculator` 类与 `Add`/`Sub` 方法，`TestLSPAdapter_RealGopls` 已 PASS；`clangd` 真实服务器传输层验证（JSON-RPC 实际连通，clangd 因缺少 compile-commands/project 上下文拒绝登记独立文件，已改为优雅跳过并记录缺口）。三处**生产修复**：①`Parse` 的 `didOpen` 改为携带真实文件内容（clangd 等要求，gopls/mock 不受影响）；②新增 `lspPathToOSPath` / `readLSPFileContent` 辅助；③**关键修复**——`addSymbolInfo` / `addDocumentSymbol` 的 `SymbolKind` 映射原仅覆盖 5(Class)/6(Method)/9(Constructor)，漏掉 Go 的 Struct(23)/Interface(24)/Function(12)，导致 gopls 对 Go 源码解析出 0 类 0 方法；补齐 `case 23→STRUCT / 24→INTERFACE / 6,12→方法` 后，gopls 真实返回类与方法（验证由 `TestLSPAdapter_RealGopls` 从 FAIL 转为 PASS）。
  > **2026-08-15 更新**：clangd 场景已补齐工程上下文真实验证（`TestLSPAdapter_RealClangd` 构造 compile_commands.json 最小工程，clangd 22 真实提取类/方法 PASS），并修复其根因——`jsonRPCRequest.ID` 缺 `omitempty` 使 notification 携带 `"id":0` 违反 JSON-RPC 2.0，clangd 严格拒绝（gopls 宽容未暴露）。详见 git 历史（2026-09-02 重构前文档）。
  - **多语言验证/基准框架**：`internal/adapterbench/adapter_validation_test.go`（独立轻量包，仅依赖 lsp/scip 适配器、刻意不引入 onnxruntime 等 cgo 重型依赖，秒级编译运行），对 SCIP（fixture，始终可用）+ LSP（gopls/clangd/jdtls，按工具可用性）逐语言真实解析并记录符号数与延迟，输出 `build/adapter-bench.json` 与 `analysis/2026-08-14-adapter-validation.md`；工具缺失则优雅跳过。
  - **架构调整（绕开 cgo 慢编译）**：原 `internal/integration/adapter_validation_test.go` 因 `package integration` 传递引入完整 scan/index/vector 流水线的 `onnxruntime_go` cgo 依赖，在本机 MinGW 下编译极慢（>50min）。将适配器验证测试迁移到独立包 `internal/adapterbench`（只 import lsp+scip），编译+运行降至 ~1min 且真实验证 gopls（classes=1/methods=2）。旧文件已从索引移除（磁盘锁定副本待杀软释放后清理）。
- [x] **依赖修正说明**：`go.mod` 实际依赖为 `modernc.org/sqlite`（纯 Go）+ `chromem-go` + `fsnotify` + `yaml.v3` + `onnxruntime_go`；tree-sitter 为 30 语言正则解析实现（非 CGO 版 go-tree-sitter，`-tags treesitter` 切真语法树）。此前「已知问题」中「go-sqlite3 / go-tree-sitter 已安装」属历史表述，当前默认构建已无需 CGO。
- [x] **优先级④ 超大仓瓶颈验证 + PG/Redis 迁移路径** —
  - **超大仓基准框架** `internal/scalebench/scale_bench_test.go`：纯 Go 无 cgo，合成每文件 1 类/3 方法/2 调用 IR，压测 N=1k/5k/10k/50k/100k 的插入/落盘/内存；SQLite 每 N 独立 dsn 隔离累积干扰；产物 `build/scale-bench.json` + `analysis/2026-08-14-scale-bench.md`。
  - **实测结论（推翻原假设）**：SQLite(UpsertIR) 是主导瓶颈——100k 文件（≈700k 行）单批插入 **77~237s**（本机波动，≈560× chromem），根因为 `UpsertIR` 逐文件多语句独立事务（100k 文件≈70万次事务提交放大）；FileStore 为内存 O(n)（100k≈1.08GB）、chromem 线性（100k≈169MB）均远快于 SQLite。**已实现 `BulkUpsert`（单事务 + 预编译语句）消除提交放大**：100k 落库降至 **约 5~14s（约一个数量级 / 跨 N 点位稳定 5~14× 提速）**，生产化应使用它（analyzer 整仓重索引时批量灌入）。
  - **PG 适配器骨架** `internal/store/pg/pg.go`（`//go:build pg`）：完整 `store.Store` 接口 + PG DDL；**Redis 缓存骨架** `internal/store/redis/redis.go`（`//go:build redis`）：热点类 HASH + 调用反查 SET + 文件→类索引。均 `go get` 即启用。
  - **文档** git 历史（2026-09-02 重构前文档）：回填实测表格 + 修正结论（SQLite 实为超线性主导瓶颈）+ 迁移路径（SQLite+BulkUpsert / chromem 持久化 / PG 横向 / Redis 缓存）。
  - **环境状态（已解决）**：本机已将仓库加入杀软信任目录——`go.mod` 恢复可写、`go build ./...` 由 50min+ 降至 ~4s、生成目录可写。已 `go get` 拉入 `lib/pq` + `go-redis/v9`，PG/Redis 骨架（`go build -tags pg/redis`）均编译通过；`build/scale-bench.json` + `analysis/2026-08-14-scale-bench.md` 已落盘。
  - **优先级 T2-4 CodeGraph 适配器去骨架（不静默空 IR）** `internal/parser/adapter/codegraph/adapter.go`：原 `ParseAll` 在 DB 存在时**静默吐空 IR 文档**（与「不静默返回空结果」目标相悖，原测试甚至断言「吐了 3 个空文档」）；改为用纯 Go `modernc.org/sqlite` 打开并校验 `symbols`/`edges` 契约表，缺表或非 SQLite 显式返 `ErrSourceUnavailable` 降级，表存在时按文档化契约（symbols: name/qualified_name/kind/file_path/language；edges: caller/callee/type）尽力读取真实类/调用 IR（调用边按 caller 前缀归属文件），列漂移则显式报错——**绝不静默空 IR**。`go test ./internal/parser/adapter/codegraph/...` 全绿（含真实读取 + 显式降级用例）。注：CodeGraph 真实列名未在本仓确认，当前契约为假设列名；若真实列名不同，读取会显式报错并降级到 tree-sitter，需后续按真实 schema 校准列名。
  - **优先级 T2-3 LSP 适配器健壮性（可观测降级 + 失败重试）** `internal/parser/adapter/lsp/adapter.go`：消除"静默丢信息"——① clangd 在 `Init` 显式探测 `compile_commands.json`（缺失则 WARN + `lsp_missing_compile_commands_total`）；② `documentSymbol` 请求经 `requestWithRetry` 包裹（`robust.Retry` 指数退避 + `robust.RetryableError`），瞬时失败自动重试；③ 解析非空 C/C++ 文件却 0 符号时显式 WARN + `lsp_parse_empty_symbols_total{lang="cpp"}`；④ `readResponses` 中原静默丢弃的异常帧（Content-Length 解析失败 / JSON 体失败 / 孤儿响应）改为 WARN + `lsp_malformed_frames_total{kind=...}`；⑤ 子进程 stderr 由 `io.Discard` 改为按行日志（关键字 WARN，其余 DEBUG），暴露 clangd 自身降级原因。`init()` 注册 5 个 LSP 指标；`adapter_test.go` 新增 8 项测试（探测命中/缺失、空符号告警、重试失败/取消、孤儿/畸形 Content-Length/畸形 JSON 帧），`go test -race ./internal/parser/adapter/lsp/...` 全绿。**已接入 Registry 编排主路（PHASE_09/T1-3）**：LSP 适配器经 `cmd/codeschema/parser_registry.go` 的 `newParserRegistry` 统一工厂注册（`parser.lsp.enabled=true` 且工具存在时启用 gopls/jdtls/clangd），以 `parser.FallbackParser` 包装——LSP 解析失败自动回退 tree-sitter，全链路可观测，不再阻塞扫描主路。
  - **优先级 T4-1 测试关联补齐 explicit + coverage 策略（✅ 已实施）** `internal/service/testlink.go` + `internal/service/service.go`：测试关联从 3 策略（naming/same_tag/dependency）补齐到 5 策略——新增 **explicit**（置信度 100，解析测试类/方法 `Doc` 中 `@TestFor(...)` 注解并关联目标类全部生产方法，支持 `@TestFor(OrderService.class)` / `@TestFor com.example.OrderService` / `@testfor: order.OrderService` / `@TestFor=OrderService` 及 FQN 精确/后缀/简单名三类解析，注解可类级或方法级）与 **coverage**（置信度 90，注入式覆盖率报告反查：`Service.SetCoverage` + `LoadCoverageJSON`，格式 `{"testMethodFQN": ["prod.Method", ...]}`）；`FindTestLinks` 接入两策略并按置信度排序。`go test -race ./internal/service/...` 全绿（新增 6 项：explicit 类级/方法级、coverage 反查、JSON 解析、parse/resolve 单元）。**已补真实采集（PHASE_09/T4-4）**：`internal/service/coverprofile.go` 的 `LoadGoCoverProfile`/`ParseGoCoverProfile` 从真实 `go test -coverprofile` 产物自动解析覆盖率块，按行号区间匹配 store 方法记录 → 测试类（命名约定）关联其源类被覆盖方法并注入 coverage 策略（路径后缀匹配兼容相对/绝对路径，合并不覆盖注入式 JSON），端到端测试 PASS。
  - **优先级 T4-1 AI 增强层落地（✅ 已实施）** `internal/ai/enhancer.go` + `budget.go` + `client.go`：按 doc 08 §3 设计实现三件套——① `LLMClient` 接口（`Complete` 补全标签/文档 + `Choose` 同名消歧），隔离 LLM 后端便于 mock；② `Budget` 预算管控（perScan/perQuery **双作用域独立计数**，Reset/Remaining/Exhausted 观测，limit<0 不限，每次扫描/查询开始重置）；③ `Enhancer`（`EnhanceTag`/`EnhanceDoc`/`Disambiguate` + `SetPhase` 切换作用域，`IRable` 接口 + `NewClassEntity`/`NewMethodEntity` 适配 store 记录）——预算超限返 `errors.ErrBudgetExceeded`（不触 LLM），LLM 失败包装 `errors.ErrEnhanceFailed`，失败隔离不影响主流程。`go vet ./...` / `go build ./...` / `go test -race ./internal/ai/...` 全绿（新增 10 项测试）。**已接入生产编排（PHASE_09）**：`cmd/codeschema/main.go` 经 `newAIEnhancer(cfg)` 构建真实 LLM client + budget，`svc.WithAIEnhancer(enh)` 接线查询期同名方法消歧（扫描器/查询处理器已调用，`config.ai` 全字段接线：provider/model/base_url/api_key + 预算）；`ai: enhancement disabled` 日志为未配置 APIKey 时的优雅降级，不影响主流程。

### PHASE_09 开发计划：16 任务全部完成（2026-08-15）

基于 `docs/开发计划-乐高式模块拼装.html` 的 4 阶段 16 任务（先清红再补黄，最后规模化与生态），全部实施并推送（commit `d617a77` → `5bc775e`）：

- [x] **T1-1 benchmark 子命令**（`d617a77`）— `internal/benchmark` 包 + CLI `codeschema benchmark`（--repos 多仓/单仓，Markdown+JSON 报告）；CLI 命令数 6→7（scan/watch/rebuild-kv/benchmark/mcp/serve/version）
- [x] **T1-2 CodeGraph 真实 schema 校准**（`d9aa484`）— 真实 CodeGraph DDL（nodes/edges + source_id/target_id/kind）优先，旧 symbols/edges 契约兼容；缺表显式降级（不静默空 IR）
- [x] **T1-3 LSP 接入 Registry 编排主路**（`ec57a45`）— `parser.FallbackParser` 降级回退包装器 + `newParserRegistry` 统一工厂；**顺带修复 scan/watch 创建空 Registry 导致 CLI 扫描 classes/methods 恒空的隐藏缺陷**
- [x] **T2-1 模型公网分发闭环**（`200a366`）— 注册表 URL 回填 GitHub Releases 约定路径 + `make models-serve` 本地 HTTP 分发 + 真实制品（43MB）HTTP 端到端
- [x] **T2-2 向量索引原文持久化**（`2fcf551`）— `DocContentStore` 可选接口（Persistent/Memory 实现，旧文件向后兼容），IndexBuilder 写入原文，/viz 展示类/方法原文
- [x] **T2-3 语义检索质量定案**（`72bdc93`）— 真实 ONNX 复测：**R@1/@3/@5 = 1.00/1.00/1.00 vs Local(TF-IDF) 0.42/0.58/0.83**；默认策略定案（Local 兜底 / 语义敏感场景 -tags onnx）
- [x] **T2-4 FileStore 权威化与一致性**（`9594342`）— flock 进程锁（Unix）/独占创建（Windows），同目录二次 Open 显式失败；scanner 忽略 store 数据文件
- [x] **T2-5 配置模板与一键体验**（`6ebb93a`）— `codeschema mcp --print-config` 输出 5 类客户端配置 + git 历史（2026-09-02 重构前文档） + README 快速开始
- [x] **T3-1 10万+ 文件真实规模压测**（`efb548e`）— `TestScaleEndToEnd` 真实全链路（真实 .go 文件→Scanner→UpsertIR→BuildFromStore→Searcher）：**N=10万 扫描 8.18s / 索引 9.55s / P95 搜索 1.97s / 内存 1079MB** → 规模决策表（<1万 默认栈 / 1万~10万 SQLite+chromem / >10万 PG+Redis）
- [x] **T3-2 PG/Redis 真实实例集成**（`fa6dec0`）— PG 骨架修 2 个历史 bug（file.imports 列缺失、UpsertIR 从未写 method）；集成测试三档降级（外部实例→localhost→嵌入式 fergusstrange/embedded-postgres 真实内核）；`TestPGStore_EndToEnd` + `TestRedisCache_RealInstance`（docker redis）PASS
- [x] **T3-3 Docker 实构建 + 多平台**（`fa6dec0`）— Dockerfile 三处修复（GOPROXY 固化 goproxy.cn、提前 COPY third_party/、移除非法 shell COPY）；`codeschema:test` 构建成功，容器内 version/scan/serve+viz/mcp stdio 冒烟全 PASS
- [x] **T3-4 CI 规模回归看护升级**（`efb548e`）— `nightly-scale` job（schedule 触发，N=10万 端到端 + 趋势 JSONL 归档）
- [x] **T4-1 MCP stdio 传输**（`4c72dc6`）— `handleRequest` 纯逻辑抽取（HTTP/stdio 复用）+ `StartStdio` LSP 风格 Content-Length 帧 + `codeschema mcp --stdio`；测试 4 项
- [x] **T4-2 HTTP API OpenAPI 完整化**（`13a2048`）— `/openapi.json`（13 端点规范）+ `/docs`（内嵌 swagger-ui）；测试 2 项
- [x] **T4-3 制品发布流水线**（`faef16d`）— `.github/workflows/release.yml`：v* tag → make cross 5 平台 → SHA-256SUMS → action-gh-release；本机 cross 验证通过（13-15MB/平台）
- [x] **T4-4 测试关联真实采集**（`0b427de`）— `LoadGoCoverProfile`/`ParseGoCoverProfile` 从真实 `go test -coverprofile` 产物自动采集（行号区间匹配方法 + 测试类命名约定关联），端到端测试 PASS

### 维护优化（2026-08-16）：单实例多租户（多项目共享一个 CodeSchema 进程）

多项目都需要 CodeSchema 时，落地「单实例多租户」方案（设计见 git 历史（2026-09-02 重构前文档），可运行示例 `build/mt-demo.yaml`）：

- [x] **`internal/tenant`（新包）** — 多租户管理器 `Manager`：持有 N 个隔离的单租户运行实例（每租户独立 `store.Store` + 独立 `runtime.Runtime`），按 `project` 路由请求；`OpenStoreFunc` 由 cmd 层注入以维持 `internal/store` 的循环依赖隔离；`deriveIndexDirs` 为显式 `tenants` 按各自 `storage.dsn` 派生隔离的 FTS/向量/IDF 索引目录。
- [x] **`internal/runtime`（新包）** — 抽取单租户运行期装配（`NewParserRegistry`/`WithImpactAnalyzer`/`NewAIEnhancer`/``RunTagAll`/`NewSearcher`/`BuildRuntime`/`ScanRepository`/`StartWatchBackground`），供单项目（`scan`/`watch`）与多租户（`serve`/`mcp`）共用，消除重复装配。
- [x] **`config`** — `Config.Tenants` + `TenantConfig`（覆盖层）+ `ToConfig(base)` 叠加 + `Validate`（id 唯一/dsn 非空）。
- [x] **`server`（http/mcp）** — 支持 `X-Tenant`/`?tenant=`/`project` 路由；新增 `list_projects` 工具（MCP 工具数 11→12）；`GET /projects` + `handleProjects`。
- [x] **`cmd`** — `serve`/`mcp` 通过 `--config` 的 `tenants:` 列表装配多租户；`scan`/`watch` 走 `runtime` 直装；全局 `--config` 须置于子命令之前。
- [x] **关键修复：索引隔离** — 早期仅隔离主 `store` 的 DSN，FTS/向量/IDF 仍沿用 `DefaultConfig` 共享的 `./data/fts|vector|idf`，多租户按序 `auto_scan` 时后者覆盖前者索引 → 所有租户返回最后扫描者数据。修复：显式 `tenants` 在其**未显式配置**检索/向量目录时自动按各自 `storage.dsn` 派生 `<dsn>/fts|vector|idf`（仅目录型 `file/sqlite`）。判断「是否显式配置」用租户原始配置 `tc.Storage.Search.*`（merged 永远非空）。
- [x] **向后兼容** — 无 `tenants` 配置时退化为单 `default` 租户，所有接口行为与单项目模式完全一致。
- [x] **测试** — `internal/tenant/tenant_test.go`（6 项：deriveIndexDirs 派生/显式覆盖/非目录后端不派生 + Manager 单租户回退/多租户路由/索引目录派生隔离回归）；`internal/runtime/runtime_test.go`（3 项：NewParserRegistry/NewSearcherWithStore 非空 + BuildRuntime 全链路 scan→装配→首轮索引→检索）。
- [x] **文档** — 新增 git 历史（2026-09-02 重构前文档）；更新 `README.md`/git 历史（2026-09-02 重构前文档）/git 历史（2026-09-02 重构前文档）/`docker-compose.yml`（新增 `codeschema-mt` 服务，`--profile mt`）。
- [x] **验证** — `build/mt-demo.yaml`（demo-a→`./cmd`、demo-b→`./internal/config`）端到端：HTTP 与 MCP 两路均正确隔离；`go build ./...`、`go build -tags pg,redis ./...`、`go vet ./...`、`go test ./...`（31 包全绿）通过。
- commit `693bdc4`（14 files, +1253/−257）。

### 维护优化（2026-08-16）：模型制品命名对齐 + 测试隔离竞态修复

- [x] **模型制品命名对齐（已知问题 #6 修复）** — `internal/config/config.go`：`DefaultConfig().Storage.Vector.EmbeddingModel` 由 `bge-small-zh` 改为 `bge-small-zh-v1.5`（与 `config.yaml.example`、真实本地制品 `build/models-bge-small-zh-v1.5.tar.gz`、真实模型目录 `down/models/bge-small-zh-v1.5`、注册表键一致）。`internal/vector/model_download.go`：`resolveLocalArtifact` 精确名未命中时再试 `<model>-v1.5` 后缀（与注册表别名同思路），使显式写旧短名 `embedding_model: bge-small-zh` 也能命中本地打包产物。修复后离线环境默认命中本地 ONNX 模型，语义检索精度由 LocalEmbedder（R@1 0.42）恢复到 ONNX（R@1 1.00）。
- [x] **测试隔离竞态修复** — `internal/parser/adapter/lsp/e2e_test.go`：`writeTempSource`/`writeClangdProject` 原把临时工程写到仓库根 `./cs_lsp_tmp`，与扫描整个仓库根的 `internal/integration/TestRealRepo_CollectMetrics` 在 `go test ./...` 并行（`-p`）执行时竞态——集成扫描途中读到正在被清理的 `compile_commands.json` 导致 `ScanAll failed`。改为使用 `t.TempDir()`（系统临时目录、绝对长路径、仓库外、自动清理），根除竞态。并行复现 3 次稳定通过。
- [x] **测试** — `internal/vector/model_download_test.go` 新增 `TestResolveLocalArtifact_VersionSuffix`；`internal/config/config_test.go` 的 `TestDefaultConfig` 新增默认 `EmbeddingModel` 断言。
- [x] **验证** — `go build ./...`、`go vet ./...`、`gofmt` 干净；`go test ./...` 全绿（此前并行竞态已消除）；`-race` 历史 26 内部包无竞态。

### 维护优化（2026-08-16）：风险与待办落地（benchmark 快照防护 + Windows CI 去掩蔽 + action 版本 pin）

- [x] **benchmark 快照误提交防护（评估风险 #3 修复）** — 新增 `internal/testutil` 包与 `BenchOutPath(tb, name)`：将 `internal/integration`、`internal/scalebench`、`internal/adapterbench` 共 8 处基准报告写入点（bench-compare / realrepo-bench / scale-bench / scale-e2e / embedding-quality / embedding-quality-multi / adapter-bench / treesitter-callgraph-bench）统一路由——`go test ./...` 默认写入 `t.TempDir()` 并自动清理，彻底不再触碰仓库 `build/`；仅当设置 `CODESCHEMA_UPDATE_BENCH=1` 才写回 `build/<name>`，供开发者主动刷新需提交的快照。CI `test` 任务追加 `git diff --exit-code build/bench-compare.json build/realrepo-bench.json` 看护；`nightly-scale` 与 `treesitter` 两个需对外发布快照的任务显式设置该环境变量以写回 `build/`。
- [x] **Windows CI 去掩蔽（评估风险 #2 推进）** — 移除 Windows 测试步骤的 `continue-on-error: true`，失败即红、不再吞掉偶发错误；超时由 300s 提至 600s 降低慢机器抖动；保留详细日志与制品上传用于定位。
- [x] **CI action 版本 pin SHA（评估风险 #5 修复）** — `ci.yml` 与 `release.yml` 中所有 action 引用由浮动词版本（@v3/@v4/@v6/@v7）改为对应 commit SHA（保留 `# vX` 注释），杜绝供应链漂移。
- [x] **验证** — `go vet ./internal/testutil/... ./internal/integration/... ./internal/scalebench/... ./internal/adapterbench/...` 干净；另写临时测试确认 `BenchOutPath` 默认走临时目录、`CODESCHEMA_UPDATE_BENCH=1` 走 `build/`（验证后已删除）；`go test ./...` 后 `build/` 无变更；两 workflow YAML 通过解析校验、无浮动词引用。

### 维护优化（2026-08-17）：服务级热重载补齐 + 监听收敛基线 + 生态资产发布准备

承接「全局能力热重载」后续四项方向批量推进（Commit 121，本地）：

- [x] **服务级配置热重载补齐** — `tenantDirty` 扩展覆盖 `scanner.workers`/`file_size_limit_mb`/`line_count_limit`，与 DSN/Root/Name/autoScan/watch 一并触发租户实例重建；修复 `Apply` 提交阶段 `upsert` map 无序追加导致的多租户顺序随机问题（改按 `targets` 顺序追加）。
- [x] **对外监听收敛（配置形态）** — 新增 `build/secure-demo.yaml` 生产安全基线（`127.0.0.1:<port>` 监听 + 认证 + 限流 + Nginx 反代指引）；代码默认 `:8080/:8081` 保持向后兼容不收敛，安全自查勾选「对外监听收敛」。
- [x] **A 级适配器聚合发布准备** — 新增 `contrib/adapterx/`（自包含）：`ParserPlugin`/`BatchParser` 契约、`IRDocument` DTO、`Registry`、`BuiltinAdapters()`；`internal/parser/adapterx.go` 双向桥接。
- [x] **context-sdk 独立发布评估** — 新增 `contrib/contextsdk/` 契约骨架：`SDKProvider` 接口 + DTO + 发布评估 README（P0→P1→P2）。
- [x] **验证** — `go build ./...`、`go vet ./...` 通过；`go test ./internal/tenant/... ./internal/parser/... ./contrib/adapterx/...` 全绿（tenant 0.923s / parser 4.079s / adapterx 0.508s）。

### 维护优化（2026-08-29）：上下文输出工程全量升级（A/B/C 档）+ idcu-go 公共模块抽取

承接 FastContext 源码级借鉴分析（`analysis/2026-08-29-fastcontext-analysis.md`）的拍板结论
（A 档开工 / B5 用「现有工具加 `symbols[]`」/ B1 字节与 token 预算都支持 / 按 idcu-go/meta 标准抽公共模块）：

- [x] **公共模块抽取（3 个独立仓库，按 meta v1.2 标准）** — `gitee.com/idcu-go/trim`（语义块对齐 +
      双预算自适应降级 + 行级截断，覆盖率 92.9%，快路径 145 ns/op）、`ttlcache`（泛型 TTL+LRU，
      覆盖率 96.6%，命中 107 ns/op 零分配）、`pathsafe`（虚拟根映射与穿越阻断，覆盖率 92.9%）。
      纯标准库零依赖，含中英双版 README / CHANGELOG / LICENSE / Makefile（覆盖率门禁 ≥80%）/ Gitee CI。
- [x] **A 档：输出侧补齐** — 输出预算自适应降级（B1，`MaxBytes`+`MaxTokens` 双轨四级降级链）、
      诊断元数据回传（B2，`_trace` 增 `config`/`actual_bytes`/`actual_tokens`/`degraded` 等）、
      错误随附修复建议（B3，HTTP `error.hint` + MCP `[hint]` 内联 + 批量 `errors[].hint`）、
      行级截断（B6，`MaxLineChars`）、语义块对齐（B7，窗口恒覆盖符号体，不切断半个函数）。
- [x] **B 档：批量入参** — `context`/`impact`/`tests` 三工具（MCP + HTTP）支持 `symbols[]`/`methods[]`，
      一轮返回 N 个符号；单符号失败隔离（落 `errors[]` 带 code + hint），单符号响应形状不变（向后兼容）。
- [x] **C 档：性能与信息面** — 查询级缓存（B4，`ttlcache`，默认关闭，watcher 索引变更后 O(1) 失效）、
      路径虚拟化（B9，`path_style=virtual` 映射 `/codebase`，默认 `absolute` 兼容）。
- [x] **配置与装配** — 新增 `context` 配置段（`context_lines`/`max_bytes`/`max_tokens`/`max_line_chars`/
      `chars_per_token`/`default_path_style`/`query_cache.*`），作为服务端默认值
      （优先级：请求参数 > 服务端默认值 > 不限）。
- [x] **验证** — `go build ./...` 通过；`go test -short ./...` 全绿；`-race` 抽样（service/server/errors）
      通过；`go build -tags pg,redis ./...` 与 `-tags treesitter ./...` 通过；新增 21 组测试。
- [x] **遗留已闭环（Commit 130）** — 三模块已 push 并打 `v0.1.0` tag（`gitee.com/idcu-go/{trim,ttlcache,pathsafe}`）；
      `require` 升到 v0.1.0（保留 `replace` 供本地即时改模块），go.sum 补齐三模块校验和；
      CI（7 个 job）与 Release 新增「Checkout idcu-go modules」按 tag 检出步骤；Dockerfile 改在镜像内
      dropreplace + `GOPRIVATE` 按 tag 拉取。两条构建路径（replace / tag）均实测通过。
- [x] **B8（检索低置信度不返回）** — 量化口径已落地（2026-08-29）：`Search` 增 `MinScore` 阈值，
      置信度取**向量余弦相似度（绝对 [0,1]）**（语义/融合）或归一化 FTS 得分（纯 FTS 回退）；
      `Confidence < MinScore` 的结果过滤，`Service.SearchWithOptions` 回传 `trim_reason=below_threshold` + `filtered` 计数；
      默认 `MinScore=0` 关闭过滤（完全向后兼容）。HTTP `GET /search?min_score=` 与 MCP `search_symbols` 的 `min_score` 参数均已接线，
      响应改为 envelope `{results,trim_reason,filtered}`，每条结果带 `confidence`。新增 8 组测试（search + service）全绿。

### 维护优化（2026-09-01）：idcu-go 多租户扫描（方案 A：SQLite + Docker）+ code-schema 自身完善

承接「如何用 code-schema 将 idcu-go 代码结构化，数据库用 Docker」的诉求，落地多租户方案 A，并同步修复 code-schema 自身三处缺陷：

- [x] **方案 A 落地：idcu-go 41 模块多租户扫描** — 新增 `build/gen_idcu_mt.py`（仅取 idcu-go 41 个 depth-1 module 作租户，排除 `devbox/workspace/modules/*` 嵌套副本避免重复索引），生成 `build/idcu-go-mt.local.yaml`（本地路径 + `build/idcu-mt/<m>` 数据卷）与 `build/idcu-go-mt.docker.yaml`（容器内 `/repo/<m>` + `/app/data/<m>`）；新增 `docker-compose.idcu-go.yml`（复用 `codeschema:onnx` 镜像，挂载 `/Volumes/Data/idcu-go:/repo` + 数据卷）。实测：41 租户启动全量 auto-scan 通过，日志 `[tenant] manager ready: 41 tenant(s) (all auto-scanned)`，`/health` 返回 `store_ok:true, store_type:sqlite`，`/projects` 列出 41 租户，每租户独立 SQLite dsn 目录隔离确认。
- [x] **Fix A（多租户启动可见性）** — `internal/tenant/tenant.go`：新增 `Tenant.ScanErr` 字段；`buildTenant` 自动扫描失败时记录错误并日志 `auto-scan FAILED (tenant will serve with empty data)`；`NewManager` / `Apply` 增加汇总日志（成功 / 失败租户数）。原失败仅静默 log，运维无法感知。
- [x] **Fix B（ONNX 日志澄清）** — `internal/runtime/runtime.go`：`NewSearcherWithStore` 将误导性的 `ONNX model ensured at %s` 改为明确两态：`semantic: ONNX embedder active (bge-small-zh-v1.5, dim=512)` 与 `semantic: ONNX embedder not loaded (need go build -tags onnx + onnxruntime lib; model dir=%s) → using LocalEmbedder (dim=%d)`，避免「模型已就位」误判。
- [x] **Dockerfile 三处实质修正（修复自身缺陷）**：
  1. **基础镜像 alpine/musl → Debian bookworm（glibc）**：onnxruntime 预编译 `libonnxruntime.so` 依赖 `libstdc++.so.6`/`libgomp`（glibc），原 alpine/musl 无法加载该 `.so`，导致 ONNX **静默降级为 LocalEmbedder**（日志 `ONNX embedder not loaded → using LocalEmbedder`）。Dockerfile 已改为 Debian bookworm 基础 + 运行期装 `libstdc++6 libgomp1`（代码修正完成）。**验证说明（已闭环，2026-09-01）**：Debian/glibc 版 `docker build` 经 `Dockerfile.mirror`（daocloud 国内源 `docker.m.daocloud.io/library/...`）成功构建 `codeschema:onnx`（约 1.5min，构建缓存命中）；本地 macOS `-tags onnx` 与容器内均确认 `semantic: ONNX embedder active (bge-small-zh-v1.5, dim=512)`。容器端到端验证：41 租户 `manager ready` + `/health` 200 + `/projects` 41 + 语义检索 `q=解析配置&mode=semantic` 返回真实相关结果（config/docs/api.md、README.md，score 0.61/0.60…）。**ONNX 在 Debian 容器内真实生效，非 musl 降级**。
  2. **vendor 替代 dropreplace+gitee**：原 Dockerfile 构建期 `go mod edit -dropreplace` 去掉 `../idcu-go/*` 本地 replace、改从 gitee 拉发布版，但 `gitee.com/idcu-go/recovery` 与 `trace` **从未发布到 gitee**（去掉 replace 后 `go mod download` 直接 `unknown revision`），构建必失败。改为 `go mod vendor`（把全部本地 replace 固化进 vendor/），Docker 以 `-mod=vendor` 离线构建，代码与本地开发一致、且不依赖 gitee 可用性。修改 go.mod 后须重跑 `go mod vendor`。
  3. **ONNX 默认开启**：`CGO_ENABLED=1 -tags onnx` 为镜像默认（与本地 `make build` 纯 Go 默认不同），语义检索用真实 ONNX 向量化。
- [x] **回归验证** — `go build -mod=vendor ./...` 全树编译通过；`go test ./internal/tenant/... ./internal/runtime/...` 全绿；`Dockerfile.mirror`（daocloud 源）`docker build -t codeschema:onnx` 成功（Debian bookworm + glibc，ONNX 容器内真实生效，非 musl 降级）；alpine 版镜像仅作对照（vendoring + ONNX 尝试，容器内 ONNX 因 musl 降级 LocalEmbedder，结构化/FTS/多租户全链路可用）。本地 macOS `-tags onnx` 构建已确认 ONNX active；容器内 41 租户 `manager ready` + `/health` 200 + `/projects` 41 + 语义检索真实返回已验证。

### 维护优化续（2026-09-01）：多租户 OOM 根因修复 + 配置热重载防御

- [x] **Fix C（多租户 OOM 根因修复：ONNX 嵌入器进程级单例）** — `internal/runtime/runtime.go`：`NewSearcherWithStore` 改用 `vector.GetONNXEmbedderGlobal`（vector 包已有进程级单例，含 `onnxGlobal` 锁保护，`Embed` 内部 `mu` 串行化并发查询），替代原 `NewONNXEmbedderOrFallback` 每租户独立加载 bge 模型 + onnxruntime 会话。**根因**：41 租户各自加载 bge-small-zh-v1.5（~数百 MB/份）→ 启动内存峰值 > 2GB 容器上限 → OOM kill → `restart: unless-stopped` 死循环 → `NewManager` 永远跑不完 → 服务永不监听（`docker inspect` 实锤 `RestartCount` 累增、`1.58GiB/2GiB` 扫描中、每租户 auto-scan 重复 3× = 3 次重启）；本地无内存上限故正常。**修复效果**：模型仅加载 1 次，启动内存从 ~41× 收敛到 ~1×；实测容器 `183MiB/4GiB`、`RestartCount=0`、无 OOM、`manager ready` 正常打印。各租户 `modelDir`/`libDir` 均为默认 `down/models/bge-small-zh-v1.5` + `down/onnxruntime`，共享单例安全。
- [x] **Fix D（配置热重载误触发防御）** — `vendor/gitee.com/idcu-go/config/watcher.go`：`checkAndReload` / `ReloadNow` 在 `mtime+size` 不匹配后追加 **内容 sha256 二次确认**（bind-mount / NFS 等 mtime 不稳定文件系统下避免误判变更 → 触发 `mgr.Apply` 全量 rescan 死循环），仅内容真变才重载并刷新 `lastHash` 作排障指纹。注：本件为 vendor 副本直改（Dockerfile 以 `go mod vendor` 固化，`COPY . .` 不重跑 `go mod vendor`，故镜像内含此加固）；2026-09-01 下半场经用户「授权 全量推进」已将加固**固化到源 `../idcu-go/config/watcher.go`** 并重跑 `go mod vendor`，vendor 副本现已与源逐字节一致（单一可信源），Docker 镜像（`-mod=vendor`）即含此加固。
- [x] **Docker 部署资源上限 2g→4g** — `docker-compose.idcu-go.yml`：`deploy.resources.limits.memory` 由 2g 提到 4g 作安全余量（覆盖每租户 `vector.json` 驻留内存；主因 ONNX 单例已消除重复模型加载，4g 仅为余量）。
- [x] **Fix E（`serve` 同进程起 MCP，消除双进程重复扫描）** — `cmd/codeschema/main.go`：`serveCmd` 新增 `--mcp` 开关（默认取 `cfg.Server.MCPAddr`），与 `--http` **同一进程、共享同一 `tenant.NewManager`** 起 MCP Server（SSE, `mcpSrv.Start(ctx)` 在 goroutine 中运行），并合并进唯一 `OnReload` 回调做 auth-token 热重载。**根因**：原 `mcp` 是独立子命令，若多租户部署另起 `mcp` 容器会再跑一次 41 租户全量扫描 + 再加载一份 ONNX（≈翻倍启动内存，正中此前 OOM 雷区）；且原 compose 只 `serve --http` 导致 18080(MCP) 端口空转。现单进程双协议：一次扫描、零额外模型加载。**验证**：容器日志同时打印 `HTTP API Server listening on :8081` + `MCP Server listening on :8080`；`curl localhost:18081/health`→200、`curl localhost:18080/sse`→200（SSE 可达）；`mcp --print-config` 输出 VS Code/JetBrains 接入片段（`http://localhost:18080/sse`）。`docker-compose.idcu-go.yml` 命令改为 `serve --http :8081 --mcp :8080`，头部补 MCP 接入说明。
- [x] **Fix F（MCP `initialize` 握手缺失：标准客户端连不上）** — `internal/server/mcp_stdio.go`：`handleRequest` 原只处理 `tools/list` + `tools/call`，**缺 `initialize` 分支** → 所有标准 MCP 客户端（VS Code / JetBrains / 官方 SDK）连上来第一步发 `initialize` 收到 `-32601 method not found` 直接断开，端口虽通但协议不干活（由 `scripts/mcp_probe.py` 协议级复现）。新增 `initialize`（回显客户端 `protocolVersion`，capabilities 含 `tools`，返回 `serverInfo`）+ `notifications/initialized` 两分支；`serve --mcp` 的 HTTP/SSE 与 stdio 复用同一纯逻辑分发。**验证**：`scripts/mcp_probe.py` 跑 `initialize`→`tools/list`→`search_symbols`→`context` 全链路，握手现返回 `protocolVersion: 2024-11-05, capabilities.tools, serverInfo(name=codeschema,version=dev)`；容器内 `curl localhost:18080/sse`→200、标准客户端可正常握手。**遗留（已暴露，已修复）**：`search_symbols` 返回的 `symbol` 是索引内部顺序 ID（如 `method:27`），而 `context`/`impact`/`tests` 按 **FQN** 解析（`resolveSymbolLocation`→`GetMethod`/`GetClass`），故 search→context 链路断（HTTP 同款问题）——见下方 Fix G。
- [x] **Fix G（search→context 符号命名空间一致化：search 结果附 `fqn`）** — `internal/search/result.go` + `internal/service/service.go`：原 `search_symbols` 只回索引内部顺序 ID（`method:27` / `class:2`，见 `builder.go` 的 `"method:" + formatClassID(m.ID)`），而 `context`/`impact`/`tests` 按**全限定名**解析，两个命名空间不对齐 → search→context 工作流断。**方案 C（最小侵入、向后兼容）**：`Service.resolveSymbol` 在既有遍历内顺带计算并返回 FQN（类=`c.FullName`，方法=`c.FullName + "." + m.Name`，与 `buildMethodIndexText` / `resolveSymbolLocation` 口径一致），`enrichResults` 写入 `search.SearchResult.QualifiedName`；`search.SearchResult` 与 `service.SearchResult` 均增 `fqn` 字段，并同步进 `projectTo`/`projectFrom` 往返；MCP（`mcpResult` 序列化 `SearchOutcome`）与 HTTP（`/search` 直接 `writeJSON`）自动透出，无需改传输层。
  **验证（同一二进制 + 真实 ONNX 实跑，非推测）**：本机 `go build -mod=vendor -tags onnx` 出新二进制，以单租户（`idcu-go/config`）`serve --http :18085 --mcp :18086` 起服务（日志 `semantic: ONNX embedder active (bge-small-zh-v1.5, dim=512)`、`[tenant] manager ready: 1 tenant(s)`），`scripts/mcp_probe.py`（`MCP_BASE` 可配）全链路结果：① `search_symbols` 首条现返回 `symbol:"method:27"` **+ `fqn:"testCfg.TestWatcher_MultipleStops"`**（`symbol` 保留，向后兼容）；② `context(symbol="method:27")` 仍报 `symbol not found`（内部 ID 本不被 `context` 接受，符合预期、未破坏既有语义）；③ `context(symbol="testCfg.TestWatcher_MultipleStops")`（即 fqn）**成功返回真实上下文**——`file_path=/Volumes/Data/idcu-go/config/watcher_test.go`、`start_line=278`、`_trace.hit_symbols=1`、`trimmed_lines=333`；④ 探针结论行 `[CHAIN search→context via fqn]: OK`。
  **回归测试**：新增 `TestResolveSymbol_FQN`，并为既有 `TestResolveSymbol_Class/Method` 补 `fqn` 断言（`resolveSymbol` 返回值由 2 元改为 3 元，全部调用点已同步）；`internal/service` + `internal/search` 两包测试全绿。
  **备注（环境阻塞，如实记录）**：本次端到端验证在**本机二进制**完成，未走 Docker——`docker build -f Dockerfile.mirror` 期间 daocloud 源 `docker.m.daocloud.io` 拉取 `golang:1.25-bookworm` 基础镜像元数据 `DeadlineExceeded`（11min 超时；此前同命令约 1min 成功，属镜像源临时故障；该基础镜像本地无缓存，故当时无法离线重建）。Fix G 为纯 Go 逻辑、与 ONNX/容器无关，本地实跑结论等价；镜像源恢复后重跑 `codeschema:onnx` 即可（本次改码不含 vendor 变更，重建即生效）。**——已于 2026-09-01 深夜重建并重部署**：daocloud 源恢复（1m30s 完成），`manager ready: 41 tenant(s)`，容器内 MCP+HTTP 双通道验证 search→context fqn 链路 OK（详见文末「部署验证（2026-09-01 深夜）」）。

## 已知问题

1. ~~**网络不可用**：无法下载外部包。~~ **已解决（依赖口径已更正）**：实际本地依赖为 `chromem-go` + `modernc.org/sqlite`（纯 Go，非 `go-sqlite3`）+ `onnxruntime_go` + `yaml.v3` + `fsnotify`。**注：`go-sqlite3` 与 `go-tree-sitter` 从未进入 go.mod**——SQLite 走 modernc 纯 Go 驱动，tree-sitter 适配器为 30 语言正则解析（非 CGO 语法树，`-tags treesitter` 切真语法树）。
2. ~~**轮询监听性能**~~ **已解决**：FsWatcher 已实现。
3. ~~**tree-sitter C 绑定**~~ **已解决（实现方式已更正）**：`internal/parser/adapter/treesitter` 为基于正则表达式的轻量解析（30 语言：go/java/ts/py/rust/cpp/c/kotlin/swift/php/csharp/ruby/bash/scala/sql/elixir/ocaml/lua/groovy/css/toml/yaml/protobuf/html/hcl/svelte/markdown/dockerfile/elm/cue），**默认构建非 CGO 版 go-tree-sitter 语法树（`-tags treesitter` 可切换真语法树）**，因此解析层不依赖 `go-tree-sitter` 与 C 编译器；调用关系检测仅 Go/Python 较准，其余语言为启发式。
4. ~~**语义检索精度**~~ **已解决**：onnxruntime_go 已安装，`bge-small-zh-v1.5` 模型已预转换为 ONNX 格式（FP16 量化，512 维），位于 `down/models/bge-small-zh-v1.5/`。`down/onnxruntime/` 含三平台动态库（`libonnxruntime.dylib`/`.so`/`onnxruntime.dll`），`make build-cgo` 自动复制到输出目录；**本机 x86_64 已端到端验证**——`-tags onnx` 下真实嵌入推理 dim=512（ORT 1.23.2，绑定经 `third_party/onnxruntime_go_patch` 适配 API v23）。应用启动时自动检测 ONNX 模型，优先使用 ONNXEmbedder，失败时降级到 LocalEmbedder。
5. ~~**向量索引为空**：启动时 MemoryStore 和 PersistentFTS 里没有数据，需要 P10 自动构建流程。~~ **已解决**：mcp/serve 命令启动时自动调用 BuildIndex 全量构建索引并持久化 IDF 词典。
6. ~~**本地模型制品命名不匹配（离线必回退 LocalEmbedder）**~~ **已解决（2026-08-16）**：根因是 `DefaultConfig().Storage.Vector.EmbeddingModel` 默认值为 `bge-small-zh`，而真实本地制品 `build/models-bge-small-zh-v1.5.tar.gz`、真实模型目录 `down/models/bge-small-zh-v1.5`、注册表键 `bge-small-zh-v1.5` 均为 `v1.5`；叠加 `resolveLocalArtifact` 仅按精确名 `models-<model>.tar.gz` 查找，无法命中带 `-v1.5` 后缀的本地制品。修复：① 默认 `EmbeddingModel` 对齐为 `bge-small-zh-v1.5`（与 `config.yaml.example` 一致）；② `resolveLocalArtifact` 精确名未命中时再试 `<model>-v1.5` 后缀（与注册表别名同思路，覆盖显式旧短名 `bge-small-zh` 配置）。修复后离线环境默认即可命中本地 ONNX 模型，语义检索精度恢复到 ONNX 水平（R@1 1.00）。新增回归测试 `TestResolveLocalArtifact_VersionSuffix`（vector）+ `TestDefaultConfig` 默认值断言（config）。

## 接手说明

1. 阅读 git 历史（2026-09-02 重构前文档） 了解整体架构
2. 按 git 历史（2026-09-02 重构前文档） 编号顺序阅读对应开发文档
3. 当前所有模块可编译运行：`go build ./cmd/codeschema`
4. 运行测试：`go test ./...`（全部包，0 失败；`-race` 竞态检测通过）
5. 启动 HTTP API：`codeschema serve --http :8081`（或 `codeschema --config config.yaml serve`）
6. 启动 MCP Server：`codeschema mcp --addr :8080`（或 `codeschema --config config.yaml mcp`）
7. 最新提交（HEAD `bdf1b3f`，2026-09-02）：impact/tests/`get_call_graph` FQN 命名空间对齐（T1 adapter 包限定 + analyzer 双向归一化）+ `GetCallGraph` 真实调用图（T2）+ `GET /affected` HTTP 路由（T4）+ `GetImpact` 解析 FQN 回填 + service 层端到端回归 + P19+ 里程碑规划（T8）；此前上下文输出工程全量升级（Commit 129）——A/B/C 三档落地（输出预算自适应降级 / 诊断元数据 / 错误 hint / 行级截断 / 语义块对齐 + 批量入参 `symbols[]` + 查询级缓存 + 路径虚拟化），并抽取出 `idcu-go/{trim,ttlcache,pathsafe}` 三个公共模块（按 meta v1.2 标准，纯标准库零依赖）；此前 agent-bench 多语言任务集扩展（Commit 128）——内置任务 5→8（新增 OrderService 通用任务，Java/Python 语义）+ 符号预检 Skipped（不拉低通过率）+ MD 报告状态列；此前 agent-bench 工程化看护（Commit 127）——CI agent-bench job + 快照归一化（repo_path=仓库名，跨机器 diff 稳定）+ Makefile bench-agent 目标；此前生态资产 P2 发布前置收尾（Commit 126）——adapterx 独立发布验证脚本 + 发布评估 README、check-contrib-publish.sh 抽公共；此前遗留 TODO 推进（Commit 125）——agent-bench 多仓库评测（--repos + RepoHint 过滤 + 跨仓对比报告）、Redis 方法符号缓存（GetMethod 快速路径）；此前分析建议 1-5 全量推进（Commit 124）——agent-bench 子命令（Agent 任务端到端评测，本仓实测 minimal 省 95.2% token）、Redis 读路径类符号快速路径接线、CORS/recovery 中间件合并、context-sdk 独立发布验证脚本、测试关联/AI 预算/多租户打点补齐；此前代码夯基与结构优化（Commit 123）、context-sdk 接口抽象（Commit 122）、服务级热重载补齐 + 监听收敛基线 + 生态资产发布准备（Commit 121 本地）、全局能力热重载（Commit 120）、多租户热重载（Commit 119）、search_by_tag 多标签（Commit 118）、dsh 建议 1-7 全量推进（`59afa36`）、多租户落地（`693bdc4`）、PHASE_09 收尾（`5bc775e`）。运行：`codeschema --config build/mt-demo.yaml serve`（多租户）/ `codeschema --config build/mt-demo.yaml mcp`（多租户 MCP）
8. 启动 fsnotify 原生监听：`codeschema watch --fsnotify <path>`

## 部署验证（2026-09-01 深夜）：Fix G 容器化闭环

- 重建镜像 `codeschema:onnx`：`docker build -f Dockerfile.mirror -t codeschema:onnx .`（daocloud 源已恢复，`golang:1.25-bookworm` 拉取 + `go build -mod=vendor -tags onnx` CGO 链接 onnxruntime 44.6s，总耗时 1m30s）。
- 重部署：`docker compose -f docker-compose.idcu-go.yml up -d --force-recreate`（同 tag 须强重建），容器 `codeschema-idcu` 重新全量扫描 41 租户，`manager ready: 41 tenant(s)`（~105s），`/health`=`{"status":"ok","store_ok":true,"store_type":"sqlite"}`、`/projects` 列 41 租户。
- **容器内 Fix G 端到端验证（MCP + HTTP 双通道）**：`scripts/mcp_probe.py`（`MCP_BASE=http://localhost:18080`）→ `initialize` 握手正常、`search_symbols` 首条返回 `symbol:"method:27"` **+ `fqn:"testCfg.TestWatcher_MultipleStops"`**、`context(fqn)` 成功返回真实上下文（`/repo/config/watcher_test.go:278`，`trimmed_lines=333`），探针结论 `[CHAIN search→context via fqn]: OK`；HTTP `/search?tenant=config&q=Watch` 同样回 `fqn`（如 `method:24`→`testCfg.TestWatcher_StartRespectsContext`）。search→context 工作流在 41 租户多租户场景已打通。
- 至此「授权 全量推进」三项全部落地并上线：Fix G（search→context fqn 一致化）、watcher sha256 加固固化源 `../idcu-go/config/watcher.go` + 重 `go mod vendor`、全量 push（`f34cde2..1380796`）。

## 方向 A 审计（impact/tests/affected FQN 口径，2026-09-02）

> **结论先行**：impact/tests/affected 历史上恒空的根因是**调用图节点 FQN 与查询 FQN 命名空间错位**，而非 API 层流水线错误（FQN in → FQN out 正确，analyzer 已接入）。修复后三者已**真实可用**（端到端测试佐证，见下「修复落地」）。GetAffected 此前已落地为真实实现。

### 历史快照（修复前，config 租户 SQLite 实证）
- `call` 表共 501 行，全部来自真实 Go 文件（`watcher.go`/`env.go`/`loader.go`/`watcher_test.go` 等，language=go）+ 2 个 bash 脚本。
- 早期缺陷（已修复）：`caller_fqn` 在 100% 行中为空 → 边全是 `"" → 碎片`，图中无对应节点；callee 多被截断为标识符碎片。
- 残留真根因（本轮澄清）：callee/caller 缺包前缀（`Watcher.ReloadNow` 而非 `testCfg.Watcher.ReloadNow`），与查询用的包限定全名（`testCfg.Watcher.ReloadNow`）对不上。
- 容器内 `gopls/codegraph/scip/clangd` 全部 MISSING → 默认正则适配器（`//go:build !treesitter`）是 Go 唯一实际解析器。

### 真根因（代码级，本轮澄清）
- `CallerFQN` 空问题已由 `ba4ea71`/`da20964` 修复（enclosing function 跟踪，caller 不再恒空）。
- **残留真根因 = FQN 命名空间错位**：默认正则 `detectCalls` 产出裸名 CallerFQN/CalleeFQN（如 `Watcher.ReloadNow`），而 impact/tests 查询使用包限定全名（如 `config.Watcher.ReloadNow`，来自文件路径合成）→ 命名空间不一致，图中节点永远查不到。
- `GetCallGraph` 此前是硬编码空桩（`nodes:[]`/`edges:[]`），与真实调用图无关；`/affected` 仅 MCP 暴露，HTTP 侧 404。

### 修复落地（2026-09-02，T1/T2/T4）
- [x] **T1 调用图 FQN 命名空间对齐**：
  - adapter（`internal/parser/adapter/treesitter/adapter.go`）Go 路径：以 `package` 声明为前缀，caller/callee 包限定（`config.NewWatcher` / `config.Watcher.ReloadNow` / `config.loadConfig`）；自调用 `w.ReloadNow` 经 receiver 变量识别限定为 `config.Watcher.ReloadNow`。新增 `qualifyCallee`/`goRecvVar` 助手；非 Go 路径保持原启发式（裸名）行为。
  - analyzer（`internal/analyzer/analyzer.go`）：新增 `normalizeImpactSymbol` + `resolveImpactNode` **双向归一化**（去前缀 + 后缀匹配），`FindImpactNodes`/`FindImpactNodesWithDepth`/`ShortestPath` 查询时对齐节点与查询命名空间；并导出 `ResolveImpactNode` 供 service 复用。
  - 佐证：`internal/analyzer/analyzer_impact_e2e_test.go`（`TestAnalyzer_Impact_GoQualifiedFQN` 双向非空、`TestAnalyzer_Impact_NormalizationFallback` 部分限定名兜底、`TestAnalyzer_ShortestPath_Normalization`）。
- [x] **T2 `GetCallGraph` 真实调用图**：`internal/service/service.go` 由硬编码空桩改为基于 `analyzer.BuildCallGraph` 的真实子图——双向归一化定位命中节点后 BFS（callers+callees，受 depth 限制）收集节点与边。佐证：`TestService_GetCallGraph` / `TestService_GetCallGraph_BareQuery` / `TestService_GetCallGraph_NoAnalyzer`。
- [x] **T4 `GET /affected` HTTP 路由**：`internal/server/http.go` 补 `mux.HandleFunc("/affected", h.handleAffected)`，参数 `symbol`（必填）+ `recursive`（bool），复用 service `GetAffected`（已真实实现，向上追溯传递调用者并收集关联单测）。佐证：`TestAffectedEndpoint_EmptySymbol` / `TestAffectedEndpoint_Success`。
- [x] **impact/tests 真实可用**：随 T1 命名空间对齐，查询包限定 FQN（如 `config.Watcher.ReloadNow`）即命中真实节点，callers/callees 双向非空；`get_call_graph` 同步可用。
- [x] **T1 回归加固（`GetImpact` 一致性）**：`Service.GetImpact` 现与 `GetCallGraph` 一致——经 `ResolveImpactNode` 双向归一化定位后将解析后的 FQN 回填 `result.Method`，使 Agent 明确实际分析对象；新增 `internal/service/service_impact_test.go`（`TestService_GetImpact` / `TestService_GetImpact_BareQuery` / `TestService_GetImpact_NoAnalyzer`）锁入 service 层回归。

### 待决策（非阻塞）
- 真语法树 `adapter_ast.go`（`-tags treesitter`）callee 仍可能缺包前缀；当前默认镜像走正则路径，T1 已覆盖。若启用 treesitter tag，需对其 call 抽取做同等包限定对齐（方案 B 范畴，待用户拍板）。
- 非 Go 语言 caller/callee 仍走裸名启发式，双向归一化已保证查询鲁棒；如需更严格 FQN，按语种类别逐步收敛。

## P19+ 后续里程碑（规划，2026-09-02）

> 本节点 P0–P18 开发里程碑轴已全部 100% 交付（均已落地）；各 `P*.md` 的 95–98% 为能力成熟度轴（持续打磨）。以下为 P18 之后的**后续能力里程碑候选**，供下一阶段排期决策（非承诺交付清单）。按优先级与依赖排序，标注所需外部环境。

### P19 — 非 Go 语言调用图 FQN 严格对齐（高优先）
- **目标**：当前非 Go 路径（Java/TS/Python/Rust/C++）caller/callee 走裸名启发式，双向归一化仅兜底；按语种类别做严格 FQN 解析，使 impact/tests 对非 Go 也与 Go 同样精确。
- **验收**：用真实 Java/TS/Python 工程 scan→`GetImpact` 双向非空；`resolveImpactNode` 后缀匹配在跨包消歧场景不再误命中。
- **依赖**：无（纯 analyzer/adapter 侧改造）。

### P20 — 真语法树（tree-sitter AST）多语言扩展（中优先）
- **目标**：默认构建走 regex（精度低）；`-tags treesitter` CGO 构建仅覆盖部分语言且 callee 仍可能缺包前缀（见「方向 A 审计·待决策」）。扩展到更多语言 + 对 `adapter_ast.go` 的 call 抽取做与 T1 同等的包限定对齐。
- **验收**：启用 `treesitter` tag 后，受影响语言的 impact 精度不低于默认正则路径且 FQN 对齐。
- **依赖**：CGO + 各语言 grammar 构建（CI 需 CGO 环境）。

### P21 — AI 标签/文档增强生产化（中优先，依赖 LLM API key）
- **目标**：`internal/ai` 的 Tagger/DocEnhancer 当前在缺 key 时优雅降级（`ai: enhancement disabled`）。定义接入规范、降级策略、质量评估基准；真实质量端到端验证需 API key。
- **验收**：有 key 时标签/文档增强可量化提升 agent-bench 任务通过率；无 key 时行为不变（零造假红线）。
- **依赖**：**LLM API key（用户 2026-09-02 明确暂缓，本里程碑阻塞）**。

### P22 — 单测关联策略增强（中优先）
- **目标**：`FindTestLinks` 五策略当前覆盖度有限；扩展到更多测试框架识别（Go test / JUnit / pytest / Jest），提升「改动一处列出受影响单测」的召回。
- **验收**：多框架示例工程 impact 返回的 `RelatedTests` 召回率可量化提升。
- **依赖**：无。

### P23 — 大规模仓横向扩展生产验证（中优先，依赖 docker 实例）
- **目标**：PG 关系型存储（`-tags pg`）+ Redis 热点缓存/反查（`-tags redis`）已在代码层完整，但真实实例集成测试受 docker 镜像拉取超时阻塞（`redis_integration_test.go` 已优雅 Skip）。需在可拉取环境跑通并建超大仓扫描性能基线。
- **验收**：PG + Redis 真实实例集成测试全绿；10k+ 文件仓扫描时延基线建立。
- **依赖**：docker 实例可达（Redis/PG 镜像拉取；daocloud 源此前已恢复，Redis 拉取需复查）。

### P24 — agent-bench 任务集扩展 + 多语言评测（低优先）
- **目标**：内置任务已从 5→8（新增 OrderService 通用任务，Java/Python 语义）；继续扩语言/场景，建立回归基线。
- **验收**：任务集覆盖 ≥3 语言、跨仓对比报告稳定。
- **依赖**：无（本机轻量评测即可）。

### P25 — 生态资产发布标准化（低优先）
- **目标**：`context-sdk` / `adapterx` / `dsh` 等公共模块按 meta v1.2 标准独立发布（已部分做发布评估/验证脚本）；补全版本管理与破坏性变更规范。
- **验收**：各公共模块可独立 `go get` + 语义化版本发布。
- **依赖**：无。

### P26 — 可观测性与运维完善（低优先）
- **目标**：metrics 维度细化、链路追踪采样、多租户隔离压测；配合 `docker-compose.idcu-go.yml` 多租户部署文档固化。
- **验收**：41 租户场景可观测指标完整、压测报告归档。
- **依赖**：无。

> 决策记录（2026-09-02）：T8（P19+ 里程碑定义）按用户「其它全量推进实施」指令落地为本文档规划节；P21 因 LLM API key 缺失暂缓，P23 因 docker/Redis 实例拉取阻塞需环境就绪后推进；其余 P19/P20/P22/P24/P25/P26 为可自主排期的后续能力，按实际优先级逐步实施。
