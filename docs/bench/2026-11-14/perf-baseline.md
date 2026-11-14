# 性能基线：索引吞吐、查询延迟与多租户隔离（S-3）

> 最后更新：2026-11-14
>
> **这是基线，不是优化结论。** 数值用于后续回归对比，不构成容量承诺或调优承诺。
> 本轮只修复了一处有明确缺陷证据的 O(n²) 写放大（见 §8.1），其余慢点只记录位置与数字，未做无依据优化。

## 1. 结论摘要

| 维度 | 实测（本机单进程） |
|---|---|
| 最大语料 | Go 标准库 `src`：6,786 个 `.go` / 2,276,611 行 / 91,251 个符号 |
| 索引吞吐（large） | scan 1,246.2 files/s、16,227.0 symbols/s；index 1,063.5 files/s、13,847.6 symbols/s |
| 索引内存（large） | 堆峰值 1,935.4 MiB；GC 后 live 363.7 MiB；累计分配 23.53 GiB |
| `/context`（large） | P50 142.6 ms / P95 159.4 ms，0 错误 |
| `/impact`（large） | P50 378.5 ms / P95 397.8 ms，0 错误 |
| `/search`（large） | P50 243.9 ms / P95 251.5 ms，0 错误 |
| 多租户并发 | 顺序总和 6,474.9 ms → 并发墙钟 4,751.3 ms（争用比 0.734） |
| 多租户隔离 | **通过**：store 路径 0 越界、FTS 目录互异、连接池上限生效、Redis 命名空间互异 |
| 查询错误率 | 7,294 次并发查询 / 0 错误 |

发现的唯一明确缺陷是持久化向量存储的 O(n²) 写放大，已先红后绿修复（§8.1）。

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
- **语料快照**：本仓语料用 `git archive HEAD` 导出干净树（导出时 HEAD=`0fb7a8f`），
  排除未跟踪目录；因此复跑时若 HEAD 前进，文件数与 LoC 会小幅变化。
- **符号统计**：只统计主语言扩展名（Go `.go`；Java `.java`；TS `.ts/.tsx/.js/.jsx`）。
  Markdown/HTML 等文件会被通用适配器入库，但不计入 scan/index 吞吐与端点评测选样。
- **吞吐定义**：`files_per_sec = 受支持文件数 / 阶段耗时`；`symbols_per_sec = (classes + methods) / 阶段耗时`。
  scan 阶段为 `runtime.ScanRepository`，index 阶段为 `runtime.BuildRuntime`（含 FTS 批量写入 + 向量批量构建 + 原文落盘）。
- **内存口径**：20 ms ticker 读取 `runtime.MemStats`；索引结束后强制 GC 记录 live heap；
  **非 cgroup/系统峰值**，也未含子进程。
- **延迟口径**：每端点 10 次预热 + 200 次样本，**串行请求**（非并发压测），最近秩（nearest-rank）百分位；
  客户端超时 20 s。请求参数固定：`/context?max_bytes=4096`、`/impact?depth=2`、`/search?mode=exact&limit=20`。
- **符号选样**：按 `LineCount` 升序取**最小源码文件**中的第一个可调用方法（排除构造器与单字母方法名），
  避免误选跨仓 hub 方法把延迟变成单个极端符号的画像。选样符号见各 JSON 的 `symbols_used`。

## 4. 语料规模

| key | 仓库 | 快照 | 受支持文件 | 主语言文件 | 主语言行数 | classes | methods | calls |
|---|---|---|---:|---:|---:|---:|---:|---:|
| `go` | code-schema（本仓） | `0fb7a8f` | 290 | 237 | 73,380 | 592 | 2,695 | 18,259 |
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
| `go` | 319.3 ms | 908.3 | 10,295.2 | 300.2 ms | 965.9 | 10,948.4 | 64.0 MiB | 20.7 MiB |
| `java` | 40.0 ms | 1,976.3 | 6,004.1 | 148.5 ms | 531.9 | 1,616.0 | 78.3 MiB | 10.2 MiB |
| `ts` | 197.9 ms | 1,819.5 | 3,143.7 | 366.8 ms | 981.5 | 1,695.8 | 90.2 MiB | 20.2 MiB |
| `large` | 5,623.4 ms | 1,246.2 | 16,227.0 | 6,589.7 ms | 1,063.5 | 13,847.6 | 1,935.4 MiB | 363.7 MiB |

补充（`large`）：累计分配 23.53 GiB；`HeapInuse` 峰值 1,996.3 MiB。

观察：

- 小语料的 scan 吞吐在 908–1,976 files/s，`large` 仍有 1,246 files/s，未出现随规模崩塌的吞吐拐点。
- index 阶段在小语料上比 scan 慢（Java 3.7 倍、TS 1.9 倍），因为要额外完成 FTS 批量写入、
  向量构建（每篇一次 `LocalEmbedder`）与原文批量落盘；`large` 的 index 仅为 scan 的 1.17 倍，
  说明批量通道在规模上摊薄了固定成本。
- 堆峰值主要随**符号与向量数量**增长：`large` 1.94 GiB 峰值、364 MiB live，符合 `file` 后端
  全量驻留内存的设计；这是后端选型问题，不是本轮代码缺陷。

## 6. 查询延迟（串行，n=200，0 错误）

| key | `/context` P50 / P95 | `/impact` P50 / P95 | `/search` P50 / P95 |
|---|---:|---:|---:|
| `go` | 6.1 / 7.8 ms | 16.2 / 17.9 ms | 10.1 / 11.7 ms |
| `java` | 3.9 / 5.1 ms | 0.7 / 1.3 ms | 2.0 / 2.4 ms |
| `ts` | 10.0 / 11.4 ms | 0.3 / 0.5 ms | 7.0 / 8.2 ms |
| `large` | 142.6 / 159.4 ms | 378.5 / 397.8 ms | 243.9 / 251.5 ms |

P99 / max：

| key | `/context` P99 / max | `/impact` P99 / max | `/search` P99 / max |
|---|---:|---:|---:|
| `go` | 8.6 / 9.2 ms | 20.5 / 21.3 ms | 12.9 / 14.5 ms |
| `java` | 5.3 / 5.6 ms | 1.7 / 2.2 ms | 2.7 / 3.1 ms |
| `ts` | 12.3 / 13.2 ms | 1.0 / 1.1 ms | 9.3 / 11.0 ms |
| `large` | 161.7 / 179.2 ms | 404.6 / 413.1 ms | 257.8 / 269.4 ms |

说明：`java`/`ts` 的 `/impact` 延迟极低，是因为选样符号位于最小文件中、调用图邻域很小；
不能据此推断这两个语料的全局影响面成本。跨语料比较只看 `large` 与同语料内的端点相对关系。

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
- 并发阶段的 scan 明显慢于 §5 单租户同语料（如 `go` 1,944.6 ms vs 319.3 ms），
  差异同时来自 sqlite 写入与三租户争用，两者本轮未拆分，不能单归于某一项。

## 8. 瓶颈位置（只记录，不优化）

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
| [service.go `GetImpact`](file:///Volumes/Data/code-schema/internal/service/service.go#L948-L1004) | `/impact` P50 378.5 ms，约为 `/context` 的 2.7 倍 | **每次请求构建一次全量调用图**（`BuildCallGraph`）再双向 BFS；466,505 条调用边摊到单请求。属于结构性成本，未看到正确性缺陷，按约束不优化 |
| [service.go `SearchWithOptions`](file:///Volumes/Data/code-schema/internal/service/service.go#L1095-L1120) → [search/searcher.go](file:///Volumes/Data/code-schema/internal/search/searcher.go#L67) | `/search` exact P50 243.9 ms（91,251 符号） | FTS 内存结构上的 exact 查询随符号量线性增长；未做 profiling，不归因到具体函数 |
| `file` 后端 / `PersistentStore` | 堆峰值 1,935.4 MiB（2.28M LoC） | 全量索引驻留内存是后端设计取舍；生产大仓应使用 sqlite/pg/redis。未改默认行为 |
| sqlite 多租户并发 | 争用比 0.734，goroutine 峰值 28 | 并发仍有收益；查询 0 错误、隔离全通过。争用归因需要锁/IO 剖析，本轮不做 |

### 8.3 未验证边界

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
```

默认不参与 `make test`（未设置门控变量时 `t.Skip`）。

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

### 9.4 复跑注意事项

- 机器负载、macOS 版本、Go 版本变化都会改变绝对值；跨机器只比较同一语料内的相对关系。
- `go` 语料取运行时 `HEAD`，本轮为 `0fb7a8f`；复跑后文件数与 LoC 变化属预期。
- 若启用 ONNX 模型或提高 `VectorDim`，需要重新建立基线，不要与本表混比。

## 10. 原始数据

- [perf-go.json](./perf-go.json)
- [perf-java.json](./perf-java.json)
- [perf-ts.json](./perf-ts.json)
- [perf-large.json](./perf-large.json)
- [perf-multitenant.json](./perf-multitenant.json)

基准代码：`internal/scalebench/perf_baseline_test.go`；门控变量 `CODESCHEMA_PERF_BASELINE=1`。
