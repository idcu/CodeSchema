# CHANGELOG.internal.md

> 内部追溯日志，不对外发布。记录每次提交的核心改动、验证数据、遗留 TODO。

---

## 提交记录

### Commit 10: feat(analyzer): P4 Gradle 多模块路径解析 + 标准库前缀可配置化

**Commit Hash**: `f0094ca`

**核心改动点**：
- `internal/analyzer/resolver.go` — 新增 `GradleResolver` 实现，支持 `:module:path:to:Class` 格式路径解析、4 种匹配策略、通配符/模块白名单/标准库过滤；`javaStdlibPrefixes` 从全局变量改为 `JavaResolver` 实例字段，新增 `SetStdlibPrefixes`/`AddStdlibPrefix` 方法
- `internal/analyzer/analyzer.go` — `Analyzer` 新增 `gradleResolver` 字段；`NewAnalyzer` 初始化解析器链（GoResolver → JavaResolver → GradleResolver → heuristicResolver）；新增 `SetJavaStdlibPrefixes`/`SetGradleModuleNames` 方法
- `internal/analyzer/analyzer_test.go` — 新增 25 个 P4 测试（GradleResolver 15 个 + 可配置前缀 4 个 + 集成测试 6 个）
- `docs/dev/06-编排层与并发模型.md` — 新增 §9 架构总结（架构图、解析器链、数据结构、扩展性设计、当前状态），更新 P4 完成标准（77 测试通过）

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（11 个包，0 失败）
- analyzer 包 77 个测试全部通过（52 个原有 + 25 个 P4 新增）
- 新增测试覆盖：Gradle 模块路径精确匹配、非 Gradle 路径跳过、源根目录匹配、通配符匹配/不匹配、模块名白名单/空白名单、标准库过滤、Analyzer 集成（含模块名白名单集成）、默认/自定义源根目录、自定义前缀过滤、SetStdlibPrefixes/AddStdlibPrefix/EmptyPrefixes/通过 Analyzer 设置

**遗留 TODO / 风险**：
- Gradle 路径匹配依赖源根目录约定（`{module}/{sourceRoot}/{internalPath}`），若实际项目结构不符需调整默认源根目录
- Python/TypeScript 等多语言解析器尚未实现，可作后续 P5 扩展
- 模块名白名单当前为精确匹配，后续可支持前缀匹配或正则

### Commit 9: feat(analyzer): P3 多语言解析器 — Java Maven/Gradle 包路径解析

**Commit Hash**: `1e8f9f0`

**核心改动点**：
- `internal/analyzer/resolver.go` — 新增文件，定义 `ImportResolver` 接口，实现 4 种解析器：
  - `GoResolver`：模块路径精确解析（`codeschema/internal/store` → `internal/store`）
  - `JavaResolver`：支持 FQCN 导入（`.` 转 `/`）、通配符导入（`com.example.*` → 前缀匹配）、Java 标准库过滤（`java.*`/`javax.*`/`org.springframework.*`/`lombok.` 等 23 个前缀）、Maven/Gradle 源根目录剥离（默认 `src/main/java`、`src/main/kotlin`、`src/test/java`、`src/test/kotlin`）
  - `CompositeResolver`：按注册优先级依次尝试，首个非空结果即返回
  - `heuristicResolver`：最终回退（直接匹配/最后一段匹配/`.` 替换为 `/` 匹配）
- `internal/analyzer/analyzer.go` — `Analyzer` 结构体新增 `resolver`/`goResolver`/`javaResolver` 字段；`NewAnalyzer` 初始化解析器链（GoResolver → JavaResolver → heuristicResolver）；新增 `SetJavaSourceRoots` 方法；`resolveImport` 方法委托给 `CompositeResolver.Resolve`
- 测试文件：analyzer_test.go 新增 22 个 P3 测试（JavaResolver 11 个 + CompositeResolver 3 个 + heuristicResolver 5 个 + 集成测试 3 个）
- 文档同步：docs/dev/06-编排层与并发模型.md 更新 P3 完成标准（多语言解析器体系、JavaResolver 特性、52 测试通过）

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（11 个包，0 失败）
- analyzer 包 52 个测试全部通过（30 个原有 + 22 个 P3 新增）
- 测试覆盖：Java 标准库过滤（7 个类型）、FQCN 精确匹配/不匹配、通配符匹配/不匹配、源根目录 FQCN 匹配、源根目录通配符匹配、默认源根目录（4 个）、自定义源根目录、空源根目录回退、CompositeResolver 优先级链、全部解析器失败、AddResolver 动态添加、heuristicResolver 四种匹配策略、Analyzer 集成测试（自定义源根目录 + 默认源根目录 + 标准库过滤 + 空索引）

**遗留 TODO / 风险**：
- JavaResolver 当前基于路径段匹配，若文件 store 中路径不包含源根目录前缀，需手动设置 `SetJavaSourceRoots`
- 标准库前缀列表当前为硬编码，后续可扩展为配置文件或自动发现
- 未支持 Gradle 多模块项目（如 `:module:submodule` 路径），P4 可扩展

### Commit 8: perf(analyzer): P2 BuildAll 单次遍历 + Go 模块路径 import 解析

**Commit Hash**: `e2db441`

**核心改动点**：
- `internal/analyzer/analyzer.go` — BuildAll 优化为单次遍历：`buildImportIndex` 预构建在循环前，import 解析合并入主循环，消除第二次全量遍历；`resolveImport` 改为 `Analyzer` 方法，新增策略 0（Go 模块路径精确解析，如 `codeschema/internal/store` → `internal/store`），失败时回退到策略 1-3 启发式匹配；`Analyzer` 新增 `modulePath` 字段和 `SetModulePath` 方法
- 测试文件：analyzer_test.go 新增 2 个 P2 测试（TestResolveImport_GoModule 回退到启发式匹配、TestResolveImport_GoModuleExact 模块路径精确匹配），更新 TestResolveImport 为 `a.resolveImport` 方法调用
- 文档同步：docs/dev/06-编排层与并发模型.md 更新 P2 完成标准（单次遍历、Go 模块路径解析、30 测试通过）

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（11 个包，0 失败）
- analyzer 包 30 个测试全部通过（28 个原有 + 2 个 P2 新增）
- 测试覆盖：BuildAll 单次遍历正确性（与原有 28 测试结果一致）、resolveImport Go 模块路径精确匹配、第三方包 import 回退、标准库 import 跳过

**遗留 TODO / 风险**：
- Analyzer 当前仅支持 Go 模块路径解析，P3 可扩展为多语言解析器（如 Java Maven/Gradle 包路径解析）
- 模块路径通过 `SetModulePath` 手动设置，P3 可自动从 `go.mod` 文件读取

### Commit 7: perf(analyzer): P1 反向引用索引 + 类层次父子关系 — 基于 imports 元数据的引用解析

**Commit Hash**: `8b7b025`

**核心改动点**：
- `internal/analyzer/analyzer.go` — 实现 `BuildReverseIndex` 完整逻辑（buildImportIndex 构建多策略查找映射 + resolveImport 三策略匹配），`buildClassHierarchyNode` 接入 `ParentFQNs` 建立父子关系，`BuildAll` 一次遍历同时构建反向索引和文件依赖边
- `internal/store/filestore.go` — `ClassRecord` 新增 `ParentFQNs []string` 字段，`UpsertClasses` 保存父类信息；`UpsertIR` 额外持久化 `FileRecord.Imports`
- `internal/store/store.go` — `FileRecord` 新增 `Imports []string` 字段
- 测试文件：analyzer_test.go 新增 8 个 P1 测试（buildImportIndex/resolveImport/BuildReverseIndex/BuildReverseIndex空imports/BuildClassHierarchy_WithParents/BuildAll_P1/Analyze_P1）
- 文档同步：docs/dev/06-编排层与并发模型.md 更新 P1 完成标准

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（11 个包，0 失败）
- analyzer 包 28 个测试全部通过（20 个原有 + 8 个 P1 新增）
- 测试覆盖：buildImportIndex 索引构建正确性、resolveImport 三策略匹配、BuildReverseIndex 含 imports 数据/空 imports 边界、ClassHierarchy 双向父子关系验证（ServiceImpl → Service + BaseService）、BuildAll P1 集成验证、Analyze P1 统计

**遗留 TODO / 风险**：
- resolveImport 使用基于路径段的启发式匹配，P2 可接入 Go 包路径解析获得精确匹配
- 反向索引当前在 BuildAll 中需要两次遍历（先构建索引、再匹配），大仓库场景可优化为单次遍历
- 文件依赖边（FileGraph.AddEdge）与反向索引共享同一份 import 解析结果，依赖 resolveImport 的正确性

### Commit 6: feat(analyzer): P0 代码图分析器 — 四种图结构 + 影响面分析 + 最短路径

**Commit Hash**: `51cf372`

**核心改动点**：
- `internal/analyzer/graph.go` — 四种图数据结构定义（CallGraph/ClassHierarchy/ReverseIndex/FileGraph），含 AddNode/AddEdge/GetCallers/GetCallees/BFS 遍历方法
- `internal/analyzer/analyzer.go` — 核心分析器，实现 BuildAll/BuildCallGraph/BuildClassHierarchy/BuildFileGraph/BuildReverseIndex，以及 FindImpactNodes（影响面分析）、ShortestPath（BFS 最短路径）、Analyze（模块概要统计）
- `internal/store/store.go` — Store 接口扩展：新增 GetAllFiles、GetClassesByFileID、GetMethodsByClassID、GetCallsByFileID 四个批量查询方法
- `internal/store/filestore.go` — FileStore 实现上述四个查询方法
- 测试文件：analyzer_test.go（20 个测试，覆盖图数据结构 10 个 + 分析器方法 10 个）
- 文档同步：docs/dev/03-存储层实现.md（补充分析器查询接口描述）、docs/dev/06-编排层与并发模型.md（新增第 8 章 Analyzer 分析器，含核心接口/实现步骤/完成标准）

**验证数据**：
- go build ./... — 通过
- go test ./internal/analyzer/... -v -count=1 — 全部通过（20 个测试，0 失败）
- 测试覆盖：CallGraph 节点/边/去重/空图/深度遍历、ClassHierarchy 父子关系、ReverseIndex 引用/导入、FileGraph 依赖边、BuildCallGraph（5 节点 4 边）、BuildClassHierarchy（4 类节点）、BuildFileGraph（3 文件 2 类 3 方法）、BuildAll 一次遍历、FindImpactNodes（2 个调用者）、ShortestPath BFS 路径重建、Analyze 模块概要统计、空存储边界情况

**遗留 TODO / 风险**：
- BuildReverseIndex 当前为 P0 骨架（返回空索引），P1 接入 import 解析后完善
- buildClassHierarchyNode 尚未接入 ParentFQNs 解析，P1 完善父子关系建立
- 热点方法排序中并列场景（相同调用者数）的排序顺序依赖 map 迭代，非确定性（不影响功能正确性）

### Commit 5: feat(adapter): P0 适配器模块 — tree-sitter 文本解析 + CodeGraph 直读骨架

**Commit Hash**: `31e4797`

**核心改动点**：
- `internal/parser/adapter/adapter.go` — 适配器公共工具函数（ExtToLang/IsSourceFile/LangToExtensions/FileExists/SupportedLanguages）
- `internal/parser/adapter/treesitter/adapter.go` — tree-sitter 文本模式解析适配器，基于正则表达式支持 6 种语言（Go/Java/TypeScript/Python/Rust/C++）的类/方法/调用提取
- `internal/parser/adapter/codegraph/adapter.go` — CodeGraph SQLite 直读适配器骨架，实现 BatchParser 子接口，数据库不存在时返回 ErrSourceUnavailable 触发降级
- 测试文件：treesitter/adapter_test.go（10 个测试，覆盖 6 种语言 + 空文件 + 不支持扩展名 + Init/Close）、codegraph/adapter_test.go（8 个测试，覆盖降级/分组/Init/Config）

**验证数据**：
- go build ./... — 通过
- go test ./internal/parser/adapter/... -count=1 — 全部通过（18 个测试，0 失败）
- go test ./internal/parser/... -count=1 — 全部通过（22 个测试，0 失败）
- 测试覆盖：Go 类/方法解析正确性、Java 接口/枚举类型推断、TypeScript interface/class/type 区分、Python 类/方法/调用检测、Rust struct/impl 解析、空文件返回空 IR、不支持扩展名跳过、CodeGraph 数据库不存在降级返回 ErrSourceUnavailable、ParseAll 按扩展名分组

**遗留 TODO / 风险**：
- tree-sitter 适配器当前为 P0 正则实现，P1 可切换为 go-tree-sitter Go binding 获得精确语法解析
- CodeGraph 适配器 P0 为骨架实现（ParseAll 返回空 IR），P1 需接入 go-sqlite3 读取 symbols/edges 表
- 调用关系检测当前仅支持 Go/Python 语言，其他语言 P1 接入
- 文档注释解析为简单实现（仅单行注释），多行注释（Javadoc/PyDoc）P1 完善

### Commit 4: feat(service, server): P0 MVP — Service 服务层 + HTTP API + MCP Server

**Commit Hash**: `b68c206`

**核心改动点**：
- `internal/service/service.go` — Service 业务逻辑层，封装 Store 操作，提供 GetContext/GetImpact/GetTests/Search 等 8 个查询方法，参数校验返回 ServiceError
- `internal/server/http.go` — HTTP API 服务器（5 个端点 + 错误中间件 + CORS + panic recovery）
- `internal/server/mcp.go` — MCP Server（JSON-RPC 2.0 + SSE 传输，8 个工具注册，全部对接 Service 层）
- `cmd/codeschema/main.go` — `mcp` 和 `serve` 命令接入真实服务器实现
- 测试：service_test.go（14 个）、http_test.go（11 个）、mcp_test.go（9 个）

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（8 个包，0 失败）
- 测试覆盖：Service 14 个测试（含参数校验 + 错误码映射）、HTTP 11 个测试（含 CORS + panic recovery）、MCP 9 个测试（含全部 8 个工具调用 + 错误场景）

**遗留 TODO / 风险**：
- Service 层当前返回 P0 占位数据，P1 接入真实 Store 查询
- MCP Server 的 SSE 传输为简化实现，P1 可按 MCP 规范完善
- HTTP API 缺少速率限制中间件（P2）

### Commit 3: feat(scanner, scheduler, watcher): P0 MVP — 增量更新与文件监听

**Commit Hash**: `c743aaa`

**核心改动点**：
- `internal/scanner/hash.go` — SHA-256 哈希闸门函数
- `internal/scanner/scanner.go` — Scanner 核心（ProcessFile + ScanAll + listFiles + detectLang + countLines）
- `internal/store/upsert.go` — 行号区间匹配算法（intervalsOverlap + matchEntity，覆盖 UPDATE/DELETE/INSERT/重定位）
- `internal/scheduler/scheduler.go` — 防抖调度器（300ms 窗口 + 队列阈值 1000 + 降级信号）
- `internal/watcher/watcher.go` — PollWatcher 轮询监听器（mtime+size 检测变更，忽略 .git/node_modules 等）
- 测试文件：hash_test.go（5 个）、scanner_test.go（4 个）、upsert_test.go（2 组 16 场景）、scheduler_test.go（7 个）、watcher_test.go（2 个）
- 更新 DEV_PROGRESS.md、docs/dev/04-增量更新与文件监听.md、docs/dev/06-编排层与并发模型.md 完成标准

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（parser/scanner/scheduler/store/watcher）
- 测试覆盖：哈希闸门命中/未命中、全量扫描 worker pool、防抖合并/刷新/降级、区间重叠 10 场景、匹配判定 6 场景

**遗留 TODO / 风险**：
- 反向引用增量更新尚未实现（P1）
- 大文件旁路尚未实现（P1）
- upsertIR 级联清理依赖 SQLite 实现（P1）
- PollWatcher 轮询监听性能有限，生产环境建议切换 fsnotify

### Commit 2: perf(rename): 二进制名统一为 codeschema，与项目名对齐

**Commit Hash**: `4a64b7c`

**核心改动点**：
- 重命名 `cmd/codameta/` → `cmd/codeschema/`，二进制名从 `codameta` 统一为 `codeschema`
- 更新 `main.go` 中所有用法提示文本和错误信息（`codameta` → `codeschema`）
- 更新 `.gitignore` 中的二进制名（`codameta` → `codeschema`，`codameta.exe` → `codeschema.exe`）
- 更新文档全项目中的 `codameta` 引用为 `codeschema`：
  - `README.md`：快速开始中的构建和运行命令
  - `DEV_PROGRESS.md`：文件路径引用和编译命令
  - `docs/dev/00-项目概述与架构概览.md`：模块依赖图和完成标准
  - `docs/dev/04-增量更新与文件监听.md`：watch 命令示例
  - `docs/dev/05-接口层（CLI+HTTP+MCP）.md`：CLI 命令表示例、开发指南、完成标准
  - `docs/dev/11-配置部署与路线图.md`：配置模型 DSN 和 benchmark 命令

**验证数据**：
- `grep -r codameta` 全项目检查：0 处残留
- `go build ./cmd/codeschema` 编译通过
- `codeschema version` 输出 `CodeSchema v0.1.0` 正常

**遗留 TODO / 风险**：
- 无

---

### Commit 1: docs(dev): 全维度文档分析评估与开发文档分割

**Commit Hash**: `c0d3ec0`

**核心改动点**：
- 对原始文档 `代码元数据KV_DB系统_开发文档.md`（1270行，v7版）进行全维度分析评估
- 识别出 5 类问题：单一巨型文档、缺乏开发顺序引导、缺少前置依赖/完成标准/开发指南、缺少跨文档引用
- 将原始文档按开发顺序分割为 12 个独立文档，存入 `docs/dev/` 目录
- 每个文档增加了"前置依赖"、"完成标准"、"开发指南"三个改善模块
- 文档间建立交叉引用关系，形成完整开发文档体系

**分割文档清单**：

| 序号 | 文件名 | 开发顺序 | 对应原始章节 |
|------|--------|----------|-------------|
| 00 | 00-项目概述与架构概览.md | 第 0 步 | §1, §2 |
| 01 | 01-数据模型与DDL.md | 第 1 步 | §4 |
| 02 | 02-解析适配中间层.md | 第 2 步 | §3 |
| 03 | 03-存储层实现.md | 第 3 步 | §8, §9 |
| 04 | 04-增量更新与文件监听.md | 第 4 步 | §5 |
| 05 | 05-接口层（CLI+HTTP+MCP）.md | 第 5 步 | §12 |
| 06 | 06-编排层与并发模型.md | 第 6 步 | §9 |
| 07 | 07-适配器实现指南.md | 第 7 步 | §3.4, §3.5, §3.7 |
| 08 | 08-测试关联与AI增强.md | 第 8 步 | §6, §7 |
| 09 | 09-语义检索与全文搜索.md | 第 9 步 | §8.3, §8.4 |
| 10 | 10-可观测性与安全设计.md | 第 10 步 | §10, §11 |
| 11 | 11-配置部署与路线图.md | 第 11 步 | §14, §15, 附录 |

**验证数据**：
- 原始文档：1 个文件，1270 行，15 章 + 5 附录
- 改善后：12 个文件，每文件约 200-400 行，覆盖全部原始内容
- 新增改善模块：前置依赖 12 处、完成标准 12 处、开发指南 12 处
- 跨文档引用：约 30+ 处交叉引用关系

**遗留 TODO / 风险**：
- 原始文档 `代码元数据KV_DB系统_开发文档.md` 保留在根目录，建议后续合并到 `docs/dev/00` 或建立索引
- 文档内容尚未经过实际开发验证，需在编码过程中根据实际情况调整
- 建议后续在 `docs/dev/` 下增加 README.md 作为开发文档总索引

---

## 文档版本对照

| 版本 | 文件数 | 总行数 | 说明 |
|------|--------|--------|------|
| v7（原始） | 1 | 1270 | 单一巨型文档 |
| v8（本版） | 12 | ~3600 | 按开发顺序分割，增加前置依赖/完成标准/开发指南 |

---

## 文档结构图

```
docs/dev/
├── 00-项目概述与架构概览.md      ← 第 0 步：先看整体
├── 01-数据模型与DDL.md            ← 第 1 步：先定义数据模型
├── 02-解析适配中间层.md           ← 第 2 步：核心接口定义
├── 03-存储层实现.md               ← 第 3 步：存储层基础实现
├── 04-增量更新与文件监听.md       ← 第 4 步：增量更新逻辑
├── 05-接口层（CLI+HTTP+MCP）.md   ← 第 5 步：接口层实现
├── 06-编排层与并发模型.md         ← 第 6 步：编排层实现
├── 07-适配器实现指南.md           ← 第 7 步：适配器实现
├── 08-测试关联与AI增强.md         ← 第 8 步：测试关联 + AI
├── 09-语义检索与全文搜索.md       ← 第 9 步：语义检索
├── 10-可观测性与安全设计.md       ← 第 10 步：可观测性 + 安全
└── 11-配置部署与路线图.md         ← 第 11 步：配置、部署、路线图
```