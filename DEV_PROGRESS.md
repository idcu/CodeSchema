# CodeSchema 开发进度跟踪

> 更新时间：2026-08-13 13:35
> 当前阶段：P5 已完成 — 标签分类体系（Tag）与测试关联
> 下一个阶段：P6 — 可观测性增强（日志/指标/链路追踪）

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
```

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

### 文档
- [x] `docs/dev/` — 12 个开发文档按开发顺序分割
- [x] `DEV_PROGRESS.md` — 本文件，开发进度跟踪

## 下一步工作

### P6 — 可观测性增强（日志/指标/链路追踪）

| 优先级 | 任务 | 模块 | 依赖 | 估计工时 |
|--------|------|------|------|---------|
| P6 | 结构化日志（zerolog/slog） | `internal/log` | 无 | 2h |
| P6 | 基础指标（请求数/延迟/错误率） | `internal/metrics` | server | 2h |
| P6 | 扫描/分析链路追踪 | `internal/trace` | analyzer | 2h |
| P6 | 健康检查端点增强 | `internal/server` | server | 1h |

### 后续规划

| 阶段 | 任务 | 参考文档 | 依赖 |
|------|------|---------|------|
| P6 | 可观测性增强（日志/指标/链路追踪） | `docs/dev/10-可观测性与安全设计.md` | 无外部依赖 |
| P7 | 配置系统（YAML 解析） | `docs/dev/11-配置部署与路线图.md` | 无外部依赖 |
| P8 | 语义检索 / 全文搜索 | `docs/dev/09-语义检索与全文搜索.md` | 需网络下载 chromem-go |

## 已知问题

1. **网络不可用**：无法下载 `mattn/go-sqlite3` 等外部包。当前使用纯 Go 文件存储（FileStore），SQLite 实现作为 DDL 参考保留。待网络恢复后，可切换为 SQLite 存储。
2. **轮询监听性能**：当前 PollWatcher 基于轮询（1s 间隔），适合开发/小仓库场景。生产环境建议切换为 fsnotify 原生监听（需安装外部包）。
3. **tree-sitter C 绑定**：tree-sitter 适配器需要 CGO 和 tree-sitter C 运行时，需单独安装。
4. **语义检索依赖**：chromem-go / bge-small-zh 等需要网络下载，当前无法实现。

## 接手说明

1. 阅读 `docs/dev/00-项目概述与架构概览.md` 了解整体架构
2. 按 `docs/dev/` 编号顺序阅读对应开发文档
3. 当前所有模块可编译运行：`go build ./cmd/codeschema`
4. 运行测试：`go test ./...`（12 个包，全部通过）
5. 启动 HTTP API：`codeschema serve --http :8081`
6. 启动 MCP Server：`codeschema mcp --addr :8080`
7. 最新提交：`TBD`（P5 标签分类体系 + 测试关联）