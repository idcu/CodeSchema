# CodeSchema 开发进度跟踪

> 更新时间：2026-08-14 00:00
> 当前阶段：全部外部依赖已安装 — chromem-go + go-sqlite3 + go-tree-sitter + onnxruntime_go
> 下一个阶段：P11 — 集成测试与性能压测（已完成）

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
```

---

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
- [x] `internal/parser/adapter/treesitter/adapter.go` — tree-sitter 适配器（6 种语言正则解析）
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

### 文档
- [x] `docs/dev/` — 12 个开发文档按开发顺序分割
- [x] `DEV_PROGRESS.md` — 本文件，开发进度跟踪

## 下一步工作

### 后续规划

| 阶段 | 任务 | 参考文档 | 依赖 |
|------|------|---------|------|
| P12 | 生产级健壮性（错误处理/重试/优雅关闭） | `docs/dev/11-配置部署与路线图.md` | P11 完成 |

## 已知问题

1. ~~**网络不可用**：无法下载外部包。~~ **已解决**：全部外部包已从本地安装（chromem-go + go-sqlite3 + go-tree-sitter + onnxruntime_go + yaml.v3 + fsnotify）。
2. ~~**轮询监听性能**~~ **已解决**：FsWatcher 已实现。
3. ~~**tree-sitter C 绑定**~~ **已解决**：go-tree-sitter 已安装，自带 parser.c 源码，CGO 自编译。
4. ~~**语义检索精度**~~ **部分解决**：onnxruntime_go 已安装，运行时需 onnxruntime.dll。
5. ~~**向量索引为空**：启动时 MemoryStore 和 PersistentFTS 里没有数据，需要 P10 自动构建流程。~~ **已解决**：mcp/serve 命令启动时自动调用 BuildIndex 全量构建索引并持久化 IDF 词典。

## 接手说明

1. 阅读 `docs/dev/00-项目概述与架构概览.md` 了解整体架构
2. 按 `docs/dev/` 编号顺序阅读对应开发文档
3. 当前所有模块可编译运行：`go build ./cmd/codeschema`
4. 运行测试：`go test ./...`（15 个包，全部通过）
5. 启动 HTTP API：`codeschema serve --http :8081`（或 `codeschema --config config.yaml serve`）
6. 启动 MCP Server：`codeschema mcp --addr :8080`（或 `codeschema --config config.yaml mcp`）
7. 最新提交：FsWatcher 原生文件监听（参见 CHANGELOG.internal.md Commit 19）
8. 启动 fsnotify 原生监听：`codeschema watch --fsnotify <path>`