# 性能基线：索引吞吐、查询延迟与多租户隔离（S-3）

> 最后更新：2026-11-14（追加索引快照流式原子落盘实测，见 §8.5；调用图缓存与内存归因见 §6.1 / §8.4）
>
> **这是基线，不是优化结论。** 数值用于后续回归对比，不构成容量承诺或调优承诺。
> 本轮修复了两处有明确证据的结构性成本：O(n²) 写放大（见 §8.1）与调用图逐请求全量重建
> （见 §6.1）。其余慢点只记录位置与数字，未做无依据优化。

## 1. 结论摘要

| 维度 | 实测（本机单进程） |
|---|---|
| 最大语料 | Go 标准库 `src`：6,786 个 `.go` / 2,276,611 行 / 91,251 个符号 |
| 索引吞吐（large） | scan 1,725.0 files/s、22,460.8 symbols/s；index 1,084.1 files/s、14,115.7 symbols/s |
| 索引内存（large） | 隔离口径堆峰值 **623.6 MiB**（改前 1,382.2 MiB，见 §8.5）；GC 后 live 362.5 MiB；查询期常驻 421.5 MiB。perf 全流程口径改前 1,935.5 MiB，未同批复跑 |
| `/context`（large） | P50 141.5 ms / P95 160.9 ms，0 错误 |
| `/impact`（large） | P50 3.6 ms / P95 5.4 ms，0 错误（改造前 378.5 / 397.8 ms，见 §6.1） |
| `/search`（large） | P50 244.9 ms / P95 256.8 ms，0 错误 |
| 多租户并发 | 顺序总和 6,474.9 ms → 并发墙钟 4,751.3 ms（争用比 0.734） |
| 多租户隔离 | **通过**：store 路径 0 越界、FTS 目录互异、连接池上限生效、Redis 命名空间互异 |
| 查询错误率 | 7,294 次并发查询 / 0 错误 |

发现的明确结构性问题：持久化向量存储的 O(n²) 写放大（§8.1）、调用图逐请求全量重建
（§6.1）、关键字判定每次调用重建 map 的分配放大（§8.4）、索引快照整块 `MarshalIndent`
（§8.5），均已先红后绿修复。`file` 后端的堆峰值经归因为**索引构建期瞬态**（GC 余量 +
快照序列化），常驻仅 362.5 MiB，未发现泄漏或非线性增长；改流式落盘后隔离口径峰值
1,382.2 → 623.6 MiB。

## 2. 硬件与运行环境

| 项 | 值 |
|---|---|
| CPU | Intel Core i9-9980HK @ 2.40 GHz，16 逻辑核 |
| 内存 | 32 GiB（34,359,738,368 B） |
| 操作系统 | macOS 15.8.3（darwin/amd64） |
| Go | go1.26.5 |
| 存储 | 单租户 `file` 后端；多租户 `sqlite` 后端 |
| 进程 | 单进程、进程内 HTTP 服务（真实 TCP 监听 `127.0.0.1:0`） |

## 3. 测量口径（重要，勿跨口径比较）

- **嵌入器**：默认构建未编译 ONNX，基准预置占位模型文件使 `Ensure` 短路，稳定使用
  `LocalEmbedder`。若本机可下载 ONNX 模型，scan/index 会额外计入模型加载与真实推理耗时，**不可与本表直接比较**。
- **向量维度**：`VectorDim=256`（非生产默认值）。降低维度是为了让 2.87M LoC 的 large 语料在单机完成；
  更高维度会线性抬高向量存储内存与语义检索成本。
- **索引参数**：`Scanner.Workers=4`；FTS/向量/IDF 全部落在临时目录；`LSP.Enabled=false`。
- **语料快照**：本仓语料用 `git archive HEAD` 导出干净树（导出时 HEAD=`ab54af0`），
  排除未跟踪目录；因此复跑时若 HEAD 前进，文件数与 LoC 会小幅变化。
- **符号统计**：只统计主语言扩展名（Go `.go`；Java `.java`；TS `.ts/.tsx/.js/.jsx`）。
  Markdown/HTML 等文件会被通用适配器入库，但不计入 scan/index 吞吐与端点评测选样。
- **吞吐定义**：`files_per_sec = 受支持文件数 / 阶段耗时`；`symbols_per_sec = (classes + methods) / 阶段耗时`。
  scan 阶段为 `runtime.ScanRepository`，index 阶段为 `runtime.BuildRuntime`（含 FTS 批量写入 + 向量批量构建 + 原文落盘）。
- **内存口径**：20 ms ticker 读取 `runtime.MemStats`；索引结束后强制 GC 记录 live heap；
  **非 cgroup/系统峰值**，也未含子进程。`live_heap_after_endpoints_bytes` 为三端点各 200 次
  请求结束后再 GC 的常驻堆，用于区分「索引期峰值」与「查询期常驻」（含调用图缓存与检索索引懒加载）。
- **延迟口径**：每端点 10 次预热 + 200 次样本，**串行请求**（非并发压测），最近秩（nearest-rank）百分位；
  客户端超时 20 s。请求参数固定：`/context?max_bytes=4096`、`/impact?depth=2`、`/search?mode=exact&limit=20`。
- **符号选样**：按 `LineCount` 升序取**最小源码文件**中的第一个可调用方法（排除构造器与单字母方法名），
  避免误选跨仓 hub 方法把延迟变成单个极端符号的画像。选样符号见各 JSON 的 `symbols_used`。

## 4. 语料规模

| key | 仓库 | 快照 | 受支持文件 | 主语言文件 | 主语言行数 | classes | methods | calls |
|---|---|---|---:|---:|---:|---:|---:|---:|
| `go` | code-schema（本仓） | `ab54af0` | 293 | 239 | 74,441 | 603 | 2,727 | 18,560 |
| `java` | spring-petclinic | `500158f` | 79 | 50 | 4,297 | 45 | 195 | 1,544 |
| `ts` | lytd | `f5e6604` | 360 | 119 | 11,407 | 522 | 100 | 372 |
| `large` | Go 标准库 `src` | go1.26.5 | 7,008 | 6,786 | 2,276,611 | 16,584 | 74,667 | 466,505 |

说明：

- `large` 的 2,276,611 行约为 `go` 语料的 31 倍、`ts` 的 200 倍，是本轮新增的“明显更大真实仓库”。
- `ts` 的方法数（100）显著低于类数（522）：Jest 回调按合成方法建模、`.d.ts` 与接口/类型成员不登记为方法
  （见 Commit 253/254），类计数包含模块/容器节点。
- `java` 的 50 个 `.java` 与仓库既有基准口径一致（`500158f`）。

## 5. 索引吞吐与内存

| key | scan 耗时 | scan files/s | scan symbols/s | index 耗时 | index files/s | index symbols/s | 堆峰值 | GC 后 live |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| `go` | 216.1 ms | 1,365.0 | 15,477.2 | 295.3 ms | 999.1 | 11,328.3 | 71.1 MiB | 21.0 MiB |
| `java` | 38.2 ms | 2,067.5 | 6,280.9 | 132.5 ms | 596.3 | 1,811.5 | 81.9 MiB | 10.3 MiB |
| `ts` | 191.3 ms | 1,882.0 | 3,251.6 | 351.5 ms | 1,024.1 | 1,769.4 | 102.5 MiB | 20.2 MiB |
| `large` | 4,062.7 ms | 1,725.0 | 22,460.8 | 6,464.5 ms | 1,084.1 | 14,115.7 | 1,935.5 MiB | 363.8 MiB |

补充（`large`）：`HeapInuse` 峰值 2,010.4 MiB；累计分配 6.11 GiB（该值随进程内前序语料累计，
只适合同口径对比）。小语料的堆峰值有 30% 量级的运行间波动，绝对值仅供回归参考。

观察：

- 小语料的 scan 吞吐在 1,365–2,068 files/s，`large` 仍有 1,725 files/s，未出现随规模崩塌的吞吐拐点。
- `large` 的 scan 吞吐由 1,262 提升到 1,725 files/s（+36.7%），来自 §8.4 的关键字 map 复用修复；
  index 吞吐基本持平（1,080 → 1,084 files/s），其成本由 FTS/向量构建主导。
- index 阶段在小语料上比 scan 慢（Java 3.5 倍、TS 1.8 倍），因为要额外完成 FTS 批量写入、
  向量构建（每篇一次 `LocalEmbedder`）与原文批量落盘；`large` 的 index 仅为 scan 的 1.59 倍。
- 堆峰值 1.9 GiB 是**构建期瞬态**而非常驻：GC 后 live 仅 363.8 MiB（IR 131.1 + 文档/向量/FTS 约 232），
  峰值来自索引构建的 JSON 快照与 GC 余量，归因见 §8.4。

## 6. 查询延迟（串行，n=200，0 错误）

| key | `/context` P50 / P95 | `/impact` P50 / P95 | `/search` P50 / P95 |
|---|---:|---:|---:|
| `go` | 5.7 / 7.6 ms | 8.7 / 10.6 ms | 10.4 / 11.8 ms |
| `java` | 4.0 / 5.0 ms | 0.1 / 0.1 ms | 2.0 / 2.5 ms |
| `ts` | 10.1 / 12.1 ms | 0.1 / 0.1 ms | 7.0 / 8.4 ms |
| `large` | 141.5 / 160.9 ms | 3.6 / 5.4 ms | 244.9 / 256.8 ms |

P99 / max：

| key | `/context` P99 / max | `/impact` P99 / max | `/search` P99 / max |
|---|---:|---:|---:|
| `go` | 8.2 / 8.7 ms | 11.2 / 13.2 ms | 12.6 / 13.5 ms |
| `java` | 5.3 / 5.7 ms | 0.2 / 0.3 ms | 2.7 / 4.0 ms |
| `ts` | 13.2 / 14.8 ms | 0.2 / 0.2 ms | 9.3 / 10.9 ms |
| `large` | 163.9 / 166.3 ms | 6.1 / 6.6 ms | 267.8 / 269.5 ms |

说明：`java`/`ts` 的 `/impact` 延迟极低，是因为选样符号位于最小文件中、调用图邻域很小；
不能据此推断这两个语料的全局影响面成本。跨语料比较只看 `large` 与同语料内的端点相对关系。

### 6.1 调用图按索引代次缓存（结构性问题 1，已修复）

**问题**：`/impact` 与 `/call-graph` 每次请求都调用 `Analyzer.BuildCallGraph`，从 Store
全量读取 466,505 条调用边重建图（`large` 每次约 0.36 s），并发请求各自重建，CPU 与分配
随 QPS 线性放大。此前只有「同一请求内 ctx 复用」，没有跨请求缓存。

**修改前 vs 修改后**（`large` 语料，同机同口径，n=200，0 错误；数据取自提交 `d58f861`
的独立复跑，未混入 §8.4 的关键字修复）：

| 指标 | 修改前（`ab54af0`） | 修改后 | 变化 |
|---|---:|---:|---:|
| `/impact` P50 | 378.5 ms | 3.6 ms | **-99.1%** |
| `/impact` P95 | 397.8 ms | 4.5 ms | **-98.9%** |
| `/impact` P99 | 404.6 ms | 5.0 ms | -98.8% |
| `/impact` max | 413.1 ms | 5.2 ms | -98.7% |
| `/context` P50 / P95 | 142.6 / 159.4 ms | 142.2 / 158.9 ms | 无变化（未走调用图） |
| `/search` P50 / P95 | 243.9 / 251.5 ms | 243.9 / 257.8 ms | 无变化（未走调用图） |
| 索引期堆峰值 | 1,935.4 MiB | 1,921.3 MiB | -0.7%（运行间波动内） |
| 索引后 live heap | 363.7 MiB | 363.8 MiB | 持平 |
| 查询期常驻 live heap | 未测 | 423.4 MiB | 新增缓存图 + 检索索引懒加载 |

修改前数据取自提交 `ab54af0` 的 `perf-large.json`（该文件已由本轮复跑覆盖，历史值可从 git 取回）。

**实现**（[analyzer.go](file:///Volumes/Data/code-schema/internal/analyzer/analyzer.go#L436-L537)）：

- **按代次缓存**：`Analyzer` 持有 `callGraphGen`（原子递增的代次）与 `callGraphCache`
  （`atomic.Pointer` 发布不可变图快照）；同代次内 `/impact`、`/call-graph`、`FindDependencies`、
  `FindTestLinks` 的 `call_graph` 策略共享同一张图。
- **失效钩子**：`InvalidateCallGraph()` 先递增代次再清空快照。接线点：
  `Service.BuildIndex`（全量重索引成功后）、`Service.InvalidateDerivedCaches`
  （watcher 的 `OnIndex`/`OnDelete` 增量更新后，`runtime.StartWatchBackground` 与
  `cmd/codeschema watch` 两条路径）。
- **并发正确性**：建图由 `callGraphBuildMu` 串行化并双检，8 个并发请求只做一次全量读盘；
  图只在完整构建后原子发布，读者拿不到半成品。建图期间若代次变化（索引正在更新），
  重试一次，仍不稳定则返回本次结果且**不写缓存**——宁可不缓存，不发布跨代脏图。

**负控（先红后绿）**：

| 测试 | 修前 | 修后 |
|---|---|---|
| [TestBuildCallGraph_CachesByGeneration](file:///Volumes/Data/code-schema/internal/analyzer/analyzer_cache_test.go) | 第二次查询仍 3 次读盘（红，构建失败 `InvalidateCallGraph undefined`） | 首次 3 次、第二次 0 次、失效后 3 次（绿） |
| [TestBuildCallGraph_ConcurrentRequestsBuildOnce](file:///Volumes/Data/code-schema/internal/analyzer/analyzer_cache_test.go) | 8 个并发请求各自建图，24 次读盘（红） | 只建一次，3 次读盘，8 个结果均为 5 节点 / 4 边（绿） |
| [TestService_ImpactSeesNewGraphAfterReindex](file:///Volumes/Data/code-schema/internal/service/service_impact_cache_test.go) | 编译失败（红，`InvalidateDerivedCaches` 未实现） | 改代码 → 重索引 → 失效 → 立即查询看到新边、旧边消失（绿） |

## 7. 多租户并发与隔离（sqlite，三租户）

三租户同时 `scan + BuildIndex`，同时各有独立 goroutine 持续 `/search`（每租户 1 ms 间隔，约 1k QPS 上限）。

| 指标 | 实测 |
|---|---|
| 顺序执行 scan+index 总和 | 6,474.9 ms |
| 并发墙钟 | 4,751.3 ms |
| 争用比（并发 / 顺序） | 0.734 |
| goroutine 峰值 | 28 |
| 并发查询次数 | 7,294 |
| 查询错误 | 0 |

租户明细：

| 租户 | 语料 | scan | index | 入库文件 | classes | methods | 池上限（配置/生效） |
|---|---|---:|---:|---:|---:|---:|---:|
| `go` | 本仓（HEAD 导出） | 1,944.6 ms | 413.2 ms | 361 | 592 | 2,695 | 4 / 4 |
| `java` | spring-petclinic | 1,047.7 ms | 305.5 ms | 132 | 45 | 195 | 6 / 6 |
| `ts` | lytd | 4,045.2 ms | 672.9 ms | 2,038 | 522 | 100 | 8 / 8 |

隔离断言（`isolation.pass=true`）：

| 检查项 | 结果 |
|---|---|
| 租户 store 文件互不交叉 | 通过（go=361 / java=132 / ts=2038，跨租户路径 0） |
| FTS 目录互异且 `fts.json` 存在 | 通过（三个目录互不相同） |
| 连接池上限按租户配置生效 | 通过（`PoolStats().MaxOpenConnections` = 4/6/8） |
| Redis 命名空间前缀互异 | 通过（`codeschema-a96612cbe87f` / `847f00f30128` / `2f5c87f67ee8`） |
| Redis 活实例读写隔离 | **未验证**：未设置 `CODESCHEMA_REDIS_ADDR`，只校验了命名空间派生公式 |

说明：

- Redis 命名空间公式为 `sha256(driver + "|" + DSN)` 前 6 字节；三租户 DSN 互异，派生结果互异。
  读写隔离由 `go test ./internal/store/redis -run Namespace` 覆盖，但**本基准未连真实 Redis**，不虚报。
- 争用比 0.734 表示并发仍有净收益（墙钟比串行总和少 26.6%），同时存在可观测争用。
  本轮**未做锁等待/IO 剖析**，不把争用归因到具体锁或 FTS 写路径。
- 并发阶段的 scan 明显慢于 §5 单租户同语料（如 `go` 1,944.6 ms vs 216.1 ms），
  差异同时来自 sqlite 写入与三租户争用，两者本轮未拆分，不能单归于某一项。

## 8. 瓶颈位置与修复

### 8.1 已修复：持久化向量存储的 O(n²) 写放大

- **位置**：`internal/embedding/persistent.go` 的 `SetContent`——每 10 次变更就把**整个索引**
  `MarshalIndent` 落盘。构建期逐篇写入时，总写入量随文档数平方增长。
- **触发路径**：`internal/search/builder.go` 构建完成后逐篇调用 `SetDocContent`。
- **证据**：`large` 语料构建期收集 96,732 篇文档；修复前 40+ 分钟未完成（中止），修复后
  索引阶段 6.47–6.59 s（同机、同口径、多次运行）。
- **修复**：`Indexer.BatchSetContents` + `PersistentStore.BatchSetContents`，构建期一次加锁、
  一次 `maybeSave`；底层不支持批量时回退逐篇。
- **先红后绿**：`TestIndexBuilder_BuildFromStore_BatchesDocContent` 修复前
  `SetContent=4 / batch=0` 失败，修复后 `SetContent=0 / batch=1` 通过。

### 8.2 未修复：慢但未发现缺陷

| 位置 | 现象（large） | 判断 |
|---|---|---|
| [service.go `SearchWithOptions`](file:///Volumes/Data/code-schema/internal/service/service.go#L1095-L1120) → [search/searcher.go](file:///Volumes/Data/code-schema/internal/search/searcher.go#L67) | `/search` exact P50 244.9 ms（91,251 符号） | FTS 内存结构上的 exact 查询随符号量线性增长；未做 profiling，不归因到具体函数 |
| `file` 后端 / `PersistentStore` | 堆峰值 1,935.5 MiB（2.28M LoC） | 峰值是**构建期瞬态**，常驻仅 363.8 MiB；上限位置见 §8.4。未改默认行为 |
| sqlite 多租户并发 | 争用比 0.734，goroutine 峰值 28 | 并发仍有收益；查询 0 错误、隔离全通过。争用归因需要锁/IO 剖析，本轮不做 |

### 8.3 已修复：调用图逐请求全量重建

原 `GetImpact`/`GetCallGraph` 每次请求全量建图（`/impact` P50 378.5 ms）。改为按索引代次
缓存后 P50 3.6 ms，实现、失效接线与负控见 §6.1。

### 8.4 `file` 后端内存归因（结构性问题 2）

**结论先说**：GOROOT 的 1,935.5 MiB 是**索引构建期瞬态峰值**，不是常驻内存，也未发现泄漏或
随文件数非线性增长。GC 后常驻 363.8 MiB；缓存调用图后查询期常驻 423.0 MiB。
峰值由两部分构成：**IR/文档/向量/FTS 的构建期临时对象**（占大头）与 **GC 余量**（GOGC=100
下允许堆在两次 GC 间继续增长）。上限位置在持久化后端的整索引 JSON 序列化，本轮未改。

测量方法（门控，可复跑）：

```bash
CODESCHEMA_MEM_ATTRIBUTION=1 go test -run '^TestMemoryAttribution_FileBackend$' \
  -v -timeout 1800s ./internal/scalebench
```

- 分阶段 `runtime.GC()` 后读 `HeapAlloc`（3 次取最小）；峰值由 20 ms ticker 采样 `HeapAlloc`；
- 堆快照用 `pprof.WriteHeapProfile`（不强制 GC），按 `inuse_space` 归因常驻对象；
- 分配归因用 `go tool pprof -sample_index=alloc_space` 对比 scan 前后快照；
- `GOGC=20` 重跑作为「峰值是否来自 GC 余量」的敏感性对照。

**常驻内存（索引完成后，GC 后 362.4 MiB）**：

| 类别 | 实测 | 证据 |
|---|---:|---|
| IR（FileStore：文件/类/方法/调用 + 解析期保留的签名与文档字符串） | 131.1 MiB | scan 后 live heap；`UpsertCalls` 45.0、`UpsertMethods` 16.7、解析字符串约 40 |
| 文档原文（contents） | ≈50 MiB | `strings.Builder` 28.5 + `utf8.AppendRune` 23.0 |
| 向量（96,732 × 256 × 4B + map 开销） | 94.1 MiB | `LocalEmbedder.Embed` flat 94.1 |
| FTS 索引（分词 + postings） | 90.7 MiB | `PersistentFTS.BatchIndex` cum 90.7（含 `Tokenize` 49.6） |
| 合计 | 362.4 MiB | 与四项之和（采样误差内）一致 |

调用图缓存再 +59.3 MiB（85,358 节点 / 243,380 边），查询期常驻 421.7 MiB。

**峰值来源（分阶段分配量）**：

| 阶段 | 修复前分配 | 修复后分配 | 说明 |
|---|---:|---:|---|
| scan | 12.46 GB | 2.58 GB | 修复前 `isKeyword` 单函数 9.65 GB（占 scan 分配 77%） |
| index（BuildRuntime） | 3.53 GB | 3.53 GB | `json.MarshalIndent` cum 2.42 GB（69%）、`LocalEmbedder` 0.56 GB、分词 0.26 GB；快照序列化已在 §8.5 改为流式 |
| 全流程峰值（scan 前启动采样） | 1,872.3 MiB | 1,382.2 MiB | 修复后峰值由 index 阶段决定 |
| 全流程峰值（perf 口径：scan/index 连续，不插 GC） | 1,921–1,935 MiB | 1,935.5 MiB | scan 修复不改变该项：峰值由 index 阶段 JSON 快照与 GC 余量叠加 |

**已修复（先红后绿）**：`isKeyword` 每次调用重建 80+ 项 map，GOROOT 扫描累计分配 9.65 GB。
改为包级 `keywordSet` 复用后，单次判定 3 次分配 → 0（
[TestIsKeywordNoPerCallAlloc](file:///Volumes/Data/code-schema/internal/parser/adapter/treesitter/keyword_alloc_test.go)），
scan 吞吐 1,262 → 1,725 files/s（+36.7%），隔离口径峰值 1,872.3 → 1,382.2 MiB（-26.2%）。
但 perf 全流程峰值仍为 1,935.5 MiB：瓶颈已转移到 index 阶段的整索引 JSON 序列化。

**上限位置（已在 §8.5 修复）**：`PersistentFTS.save` 与 `PersistentStore.save` 在构建结束时把
整个索引 `MarshalIndent` 成单个 `[]byte` 再落盘，单轮构建累计分配约 2.4 GB，最大单次缓冲区
决定了峰值下界的一部分。该行为是**线性**的（不随构建次数增长），不属于泄漏；后续按
「流式序列化 + 原子替换」改造并带负控验证，见 §8.5。生产大仓仍应优先 sqlite/pg/redis。

`GOGC=20` 对照（同一修复后代码）：峰值 1,382.2 → 1,108.1 MiB（-19.8%），常驻 362.4 MiB
不变，说明峰值中约 274 MiB 来自 GC 余量，其余约 1.1 GiB 是构建期临时对象（整索引 JSON
快照、文档文本、向量中间值），与数据驻留规模无关。

### 8.5 索引快照流式原子落盘（结构性问题 2 后续，已修复）

**先红后绿**：新增 `internal/atomicfile`，两处 `save()` 由「整索引 `MarshalIndent` →
`fsperm.WriteFile`（直写目标）」改为「按 ID 排序逐条序列化 → 写 `<file>.tmp` → `fsync` →
原子 `rename` → `fsync` 目录」。负控：

- `TestWriteFailureKeepsPreviousVersion`：回调写一半后报错，目标文件保持旧版本且无 `.tmp` 残留；
- `TestPersistentStore_SaveFailureKeepsPreviousVersion`：NaN 触发序列化中途失败，旧索引仍可加载；
- `TestPersistentFTS_StaleTempIgnoredOnLoad` / `TestPersistentFTS_OnlyTempNoMain`：
  中断残留的半份 `.tmp` 不得被当作索引，只有 `.tmp` 时按空索引启动。

**GOROOT 构建期峰值（隔离口径，同机同语料，10,345 文件 / 2,276,611 LoC）**：

| 指标 | 改前 | 改后 | 变化 |
|---|---:|---:|---:|
| build_peak | 1,449,365,920 B（1,382.2 MiB） | 653,910,688 B（623.6 MiB） | **-758.6 MiB（-54.9%）** |
| after_scan | 131.1 MiB | 131.1 MiB | 不变 |
| after_index | 362.4 MiB | 362.5 MiB | 常驻不变 |
| 调用图缓存后常驻 | 421.6 MiB | 421.5 MiB | 常驻不变 |

原始数据：`mem-attribution-large-keyword-fix.json`（改前）、
`mem-attribution-large-stream-atomic.json`（改后）。build_peak 是 scan+index 复合峰值；
本轮只改落盘路径，峰值下降可直接归因于快照序列化缓冲的消失。

**落盘耗时 A/B（真实语料文件、同一份内存数据、5 轮丢弃首轮取中位数）**：

| 组件 | 文件大小 | 旧路径（整索引 MarshalIndent + 直写） | 新路径（流式 + 原子） | 序列化分配 旧 → 新 |
|---|---:|---:|---:|---:|
| 向量 `vector.json` | 86.5 MiB | 2,160 ms | 1,414 ms | 1,015.7 → 100.9 MiB |
| FTS `fts.json` | 44.3 MiB | 695 ms | 518 ms | 307.3 → 55.4 MiB |

采样峰值增量：向量 873.7 → 101.0 MiB（-88.4%）；FTS 307.3 → 55.6 MiB（-81.9%）。
纯序列化（写 `io.Discard`）向量 2,031 → 1,044 ms、FTS 625 → 253 ms。
旧路径为测试内逐字复刻（非生产代码）；时长含 2 ms 堆采样开销，各变体同口径。
原始数据：`save-ab-vector.json` / `save-ab-fts.json`；
复跑：`CODESCHEMA_SNAPSHOT_DIR=<snapshot_dir>` + 对应 `TestPersistentStore_SaveAB` /
`TestPersistentFTS_SaveAB`。

**边界**：原子写只保证**同进程单写者**（调用方持存储写锁），跨进程并发写同一目录不在承诺内；
`maybeSave` 仍是每 ≥10 次变更做一次全量快照，O(n²) 写放大未变（§8.1），增量落盘不在本次范围。

### 8.6 未验证边界

- 未连真实 Redis（见 §7）；未验证 PG/redis 后端的并发性能。
- 未做并发查询吞吐上限测试（查询循环带 1 ms sleep，只用于争用与错误率观测）。
- 未测 ONNX 真实推理、watch 增量索引、冷启动（OS 页缓存未预热）等场景。
- 内存为进程内 `MemStats`，不是容器/cgroup 峰值，也未含 OS 页缓存。

## 9. 复现

### 9.1 一条命令

```bash
make bench-perf
```

等价于：

```bash
CODESCHEMA_PERF_BASELINE=1 go test -run '^TestPerfBaseline' -count=1 -timeout 1800s -v ./internal/scalebench
```

单独跑：

```bash
CODESCHEMA_PERF_BASELINE=1 go test -run '^TestPerfBaseline_IndexAndQuery$' -count=1 -v -timeout 1800s ./internal/scalebench
CODESCHEMA_PERF_BASELINE=1 go test -run '^TestPerfBaseline_MultiTenantConcurrency$' -count=1 -v -timeout 1800s ./internal/scalebench
CODESCHEMA_MEM_ATTRIBUTION=1 go test -run '^TestMemoryAttribution_FileBackend$' -count=1 -v -timeout 1800s ./internal/scalebench
```

默认不参与 `make test`（未设置门控变量时 `t.Skip`）。内存归因的 `GOGC=20` 对照：

```bash
GOGC=20 MEM_ATTRIBUTION_TAG=gogc20 CODESCHEMA_MEM_ATTRIBUTION=1 \
  go test -run '^TestMemoryAttribution_FileBackend$' -count=1 -v -timeout 1800s ./internal/scalebench
```

### 9.2 语料准备

| key | 默认路径 | 准备方式 |
|---|---|---|
| `go` | `/Volumes/Data/code-schema` | 无需准备；测试内用 `git archive HEAD` 导出干净树 |
| `java` | `/tmp/codeschema-perf-corpus/spring-petclinic` | `git clone https://github.com/spring-projects/spring-petclinic /tmp/codeschema-perf-corpus/spring-petclinic && git -C /tmp/codeschema-perf-corpus/spring-petclinic checkout 500158f732419217507c7656904b8e6aa1bcc0d6`（GitHub 不通时按 Commit 249 的镜像方式获取） |
| `ts` | `/Volumes/Data/lytd` | 检出 `f5e6604` |
| `large` | `$(go env GOROOT)/src` | 随本机 Go 工具链存在；无需额外下载 |

路径可用环境变量覆盖：

```bash
PERF_REPO_GO=/path/to/repo \
PERF_REPO_JAVA=/path/to/spring-petclinic \
PERF_REPO_TS=/path/to/lytd \
PERF_REPO_LARGE=/path/to/go/src \
make bench-perf
```

### 9.3 输出

| 文件 | 内容 |
|---|---|
| `perf-go.json` / `perf-java.json` / `perf-ts.json` | 单租户：语料规模、scan/index 吞吐、内存、三端点 P50/P95/P99/max/mean |
| `perf-large.json` | 同上，Go 标准库 `src` |
| `perf-multitenant.json` | 顺序/并发墙钟、争用比、goroutine 峰值、查询次数与错误、逐租户明细、隔离断言 |
| `mem-attribution-large*.json` | file 后端分阶段 live heap、构建峰值、调用图驻留增量、快照导出目录（`before-keyword-fix` / `keyword-fix` / `gogc20` / `stream-atomic`） |
| `save-ab-vector.json` / `save-ab-fts.json` | 真实语料上向量/FTS 快照落盘 A/B：旧 `MarshalIndent` 与流式原子写的时长、堆峰值增量、分配量 |

### 9.4 复跑注意事项

- 机器负载、macOS 版本、Go 版本变化都会改变绝对值；跨机器只比较同一语料内的相对关系。
- `go` 语料取运行时 `HEAD`，本轮为 `ab54af0`；复跑后文件数与 LoC 变化属预期。
- 若启用 ONNX 模型或提高 `VectorDim`，需要重新建立基线，不要与本表混比。

## 10. 原始数据

- [perf-go.json](./perf-go.json)
- [perf-java.json](./perf-java.json)
- [perf-ts.json](./perf-ts.json)
- [perf-large.json](./perf-large.json)
- [perf-multitenant.json](./perf-multitenant.json)
- [mem-attribution-large-before-keyword-fix.json](./mem-attribution-large-before-keyword-fix.json)
- [mem-attribution-large-keyword-fix.json](./mem-attribution-large-keyword-fix.json)
- [mem-attribution-large-gogc20.json](./mem-attribution-large-gogc20.json)
- [mem-attribution-large-stream-atomic.json](./mem-attribution-large-stream-atomic.json)
- [save-ab-vector.json](./save-ab-vector.json)
- [save-ab-fts.json](./save-ab-fts.json)

基准代码：`internal/scalebench/perf_baseline_test.go`、`internal/scalebench/memory_attribution_test.go`、
`internal/embedding/persistent_save_ab_test.go`、`internal/retrieval/persistent_save_ab_test.go`；
门控变量 `CODESCHEMA_PERF_BASELINE=1` / `CODESCHEMA_MEM_ATTRIBUTION=1` / `CODESCHEMA_SNAPSHOT_DIR`。
