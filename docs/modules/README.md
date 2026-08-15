# CodeSchema 模块总览（乐高积木分解）

> 本项目模块分解总览。按「乐高积木」方式将系统拆为 9 个一级模块、33 个模块单元（含 6 个公共模块），每个单元职责单一、可独立开发交付。
> 依据：实际 Go 包依赖关系（27 包，`go list` 验证）+ 架构分层，非拍脑袋。

## 一、完整模块树（含完成度）

```
CodeSchema 代码元数据 KV/DB 系统（生产级，总完成度 ≈ 95%）
│
├── P1 接入层 Interface Layer ────────── 100%
│   ├── P1_1 CLI 命令行入口 .................... 100%
│   ├── P1_2 MCP Server ........................ 100%
│   └── P1_3 HTTP API + 可视化 ................. 100%
│
├── P2 编排层 Orchestration Layer ──────── 100%
│   ├── P2_1 服务编排 Service ................... 100%
│   ├── P2_2 增量扫描器 Scanner ................. 100%
│   ├── P2_3 文件监听 Watcher ................... 100%
│   └── P2_4 调度器 Scheduler ................... 100%
│
├── P3 解析层 Parsing Layer ───────────── 96%
│   ├── P3_1 解析核心（IR + 注册）【公共契约】.... 100%
│   ├── P3_2 Treesitter 适配器（30 语言） ........ 95%
│   ├── P3_3 SCIP 适配器 ....................... 95%
│   ├── P3_4 LSP 适配器 ........................ 95%
│   └── P3_5 CodeGraph 适配器 .................. 90%
│
├── P4 分析层 Analysis Layer ──────────── 96%
│   ├── P4_1 代码图与影响面分析 ................. 100%
│   └── P4_2 AI 标签推导与测试关联 .............. 85%
│
├── P5 检索层 Search Layer ────────────── 100%
│   └── P5_1 全文检索与融合重排 ................. 100%
│
├── P6 向量索引层 Vector Index Layer ────── 96%
│   ├── P6_1 向量索引核心 ....................... 97%
│   ├── P6_2 嵌入器（Local / ONNX） ............. 95%
│   └── P6_3 模型分发与下载 .................... 97%
│
├── P7 存储层 Store Layer ─────────────── 92%
│   ├── P7_1 Store 接口 + 文件存储 .............. 100%
│   ├── P7_2 SQLite 驱动 ....................... 97%
│   ├── P7_3 PostgreSQL 驱动（-tags pg） ......... 85%
│   └── P7_4 Redis 热点缓存（-tags redis） ....... 85%
│
├── P8 横切公共模块 Cross-cutting 【公共模块】 ── 100%
│   ├── P8_1 配置系统 .......................... 100%
│   ├── P8_2 结构化日志 ........................ 100%
│   ├── P8_3 指标采集 .......................... 100%
│   ├── P8_4 链路追踪 .......................... 100%
│   ├── P8_5 错误体系 .......................... 100%
│   └── P8_6 健壮性组件 ........................ 100%
│
└── P9 测试与基准 Test & Bench ─────────── 95%
    ├── P9_1 Benchmark 框架 .................... 100%
    ├── P9_2 ScaleBench 超大仓基准 .............. 95%
    ├── P9_3 AdapterBench 适配器验证 ............ 95%
    └── P9_4 集成测试 .......................... 95%
```

## 二、公共模块清单与被依赖关系图

```
                     ┌──────────────────────────────┐
                     │          P8 公共模块           │
                     └──────────────────────────────┘

P8_1 配置系统 ──────→ 被依赖: cmd（全部命令）
P8_2 结构化日志 ────→ 被依赖: ai/analyzer/parser/scanner/search/server/
                             service/trace/vector/watcher/robust  (11 包,最广)
P8_3 指标采集 ──────→ 被依赖: analyzer/parser/scanner/server/lsp
P8_4 链路追踪 ──────→ 被依赖: analyzer/scanner/server
P8_5 错误体系 ──────→ 被依赖: ai/lsp/scip/codegraph
P8_6 健壮性组件 ────→ 被依赖: lsp/watcher/scheduler/server/cmd

P3_1 解析核心(IR 契约) → 被依赖: scanner/analyzer/ai/search/service/
                               store(全驱动)/benchmark/integration  (8 包)
```

**依赖方向示意**：

```
写入链路: CLI(scan/watch) → Watcher → Scheduler → Scanner → Parser(适配器) → Store
查询链路: MCP/HTTP → Service → {Analyzer, AI} → Store
                           └→ Search → Vector ─┘
横切注入: 全部业务层 ←── P8 公共模块（config/log/metrics/trace/errors/robust）
```

## 三、全部阻塞项汇总清单

| # | 阻塞项 | 影响模块 | 类型 | 状态 |
|---|---|---|---|---|
| 1 | PG/Redis 驱动缺真实外部服务端到端验证 | P7_3、P7_4 | 外部依赖 | 集成测试代码已完成（优雅 skip），实跑待 Docker 网络恢复 |
| 2 | ONNX 语义检索依赖 gcc + onnxruntime 动态库（`-tags onnx`） | P6_2 | 构建依赖 | 默认构建已隔离；Local 兜底 R@1=0.42 为已知质量取舍 |
| 3 | LSP clangd 适配器需 compile-commands.json 上下文 | P3_4 | 场景受限 | 已解决：测试自动构造工程上下文真实验证（PASS）；独立文件场景优雅降级 |
| 4 | Treesitter 少数语言（bash/sql/css）语法树质量不均 | P3_2 | 质量 | 已通过 adapterbench 量化，回退正则可接受 |
| 5 | 第三方依赖本地化：chromem-go（`down/`）、onnxruntime_go（`third_party/` patch）上游升级需重新验证 | P6_1、P6_2 | 依赖维护 | 持续跟进 |
| 6 | AI 标签/注释增强依赖外部 LLM，真实质量评估未做 | P4_2 | 外部依赖 | 需 API key，阻塞中 |
| 7 | 大规模仓库向量索引内存占用 | P6_1 | 性能 | scalebench 已看护（100k≈169MB） |

## 四、模块文档导航

| 模块 | 文档 | 完成度 | 模块 | 文档 | 完成度 |
|---|---|---|---|---|---|
| **P1 接入层** | [P1.md](./P1.md) | 100% | **P6 向量层** | [P6.md](./P6.md) | 96% |
| ├ P1_1 CLI | [P1_1.md](./P1_1.md) | 100% | ├ P6_1 索引核心 | [P6_1.md](./P6_1.md) | 97% |
| ├ P1_2 MCP | [P1_2.md](./P1_2.md) | 100% | ├ P6_2 嵌入器 | [P6_2.md](./P6_2.md) | 95% |
| └ P1_3 HTTP | [P1_3.md](./P1_3.md) | 100% | └ P6_3 模型分发 | [P6_3.md](./P6_3.md) | 97% |
| **P2 编排层** | [P2.md](./P2.md) | 100% | **P7 存储层** | [P7.md](./P7.md) | 92% |
| ├ P2_1 Service | [P2_1.md](./P2_1.md) | 100% | ├ P7_1 接口+file | [P7_1.md](./P7_1.md) | 100% |
| ├ P2_2 Scanner | [P2_2.md](./P2_2.md) | 100% | ├ P7_2 SQLite | [P7_2.md](./P7_2.md) | 97% |
| ├ P2_3 Watcher | [P2_3.md](./P2_3.md) | 100% | ├ P7_3 PG | [P7_3.md](./P7_3.md) | 85% |
| └ P2_4 Scheduler | [P2_4.md](./P2_4.md) | 100% | └ P7_4 Redis | [P7_4.md](./P7_4.md) | 85% |
| **P3 解析层** | [P3.md](./P3.md) | 96% | **P8 公共模块** | [P8.md](./P8.md) | 100% |
| ├ P3_1 解析核心 | [P3_1.md](./P3_1.md) | 100% | ├ P8_1 配置 | [P8_1.md](./P8_1.md) | 100% |
| ├ P3_2 Treesitter | [P3_2.md](./P3_2.md) | 95% | ├ P8_2 日志 | [P8_2.md](./P8_2.md) | 100% |
| ├ P3_3 SCIP | [P3_3.md](./P3_3.md) | 95% | ├ P8_3 指标 | [P8_3.md](./P8_3.md) | 100% |
| ├ P3_4 LSP | [P3_4.md](./P3_4.md) | 95% | ├ P8_4 追踪 | [P8_4.md](./P8_4.md) | 100% |
| └ P3_5 CodeGraph | [P3_5.md](./P3_5.md) | 90% | ├ P8_5 错误 | [P8_5.md](./P8_5.md) | 100% |
| **P4 分析层** | [P4.md](./P4.md) | 96% | └ P8_6 健壮性 | [P8_6.md](./P8_6.md) | 100% |
| ├ P4_1 Analyzer | [P4_1.md](./P4_1.md) | 100% | **P9 测试基准** | [P9.md](./P9.md) | 95% |
| └ P4_2 AI | [P4_2.md](./P4_2.md) | 85% | ├ P9_1 Benchmark | [P9_1.md](./P9_1.md) | 100% |
| **P5 检索层** | [P5.md](./P5.md) | 100% | ├ P9_2 ScaleBench | [P9_2.md](./P9_2.md) | 95% |
| └ P5_1 检索核心 | [P5_1.md](./P5_1.md) | 100% | ├ P9_3 AdapterBench | [P9_3.md](./P9_3.md) | 95% |
| | | | └ P9_4 集成测试 | [P9_4.md](./P9_4.md) | 95% |

## 五、关键接口契约速查

| 契约 | 定义处 | 消费方 |
|---|---|---|
| `store.Store` 接口 | P7_1（`internal/store/store.go`） | P2/P4/P5 + 全驱动 |
| IR 数据模型 | P3_1（`internal/parser/ir.go`） | Store/Search/Analyzer/AI |
| `VectorStore` / `Embedder` 接口 | P6_1/P6_2（`internal/vector`） | P5_1 检索 |
| MCP 11 工具 | P1_2（`internal/server/mcp.go`） | AI 客户端 |
| `Service` 查询方法集 | P2_1（`internal/service/service.go`） | P1 接入层 |

## 六、本次同步记录（2026-08-15）

对模块文档与实际代码状态做了全量对齐，并推进了未阻塞任务：

- **完成度校准**：多数模块实际完成度高于初版标注。CodeGraph 真实 schema（P3_5 70%→90%）、ONNX 质量定案（P6_2 85%→95%）、30 语言全扩展 + AST/调用图基准（P3_2 90%→95%）、10万+ 全链路压测（P9_2 90%→95%）、chromem 持久化恢复验证（P6_1 97%）等均已落地。
- **SQLite 并发误判纠正**：曾把「reader 无限循环测试超时」误判为 modernc 驱动死锁（一度标记 Skip + 列为头号阻塞项），已纠正——实为测试设计缺陷（reader 需固定迭代次数）。正确设计的读写/多读并发均 `-race` 通过（P7_2 → 97%），并新增 `BenchmarkScaleBulkConcurrent` 固化进 CI。
- **LSP 生产 bug 修复（clangd 场景）**：`jsonRPCRequest.ID` 缺 `omitempty` 导致 notification 携带 `"id":0`，违反 JSON-RPC 2.0；gopls 宽容未暴露，clangd 严格拒绝致 didOpen 不生效、符号提取永远失败（被测试 skip 掩盖）。已修复并新增 clangd 工程上下文真实验证（P3_4 → 95%，阻塞项 #3 解除）。
- **模型下载断点续传（P6_3 → 97%）**：HTTP Range 续传 + `.download.part` 落盘 + 206/200 自适应 + 完整 `.part` 复用，中断重下不浪费已下载部分。
- **剩余阻塞项**：PG/Redis 真实实例实跑（Docker 网络，镜像拉取仍超时）、AI 真实 LLM 评估（API key）。
