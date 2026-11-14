# CodeSchema 修复总账（2026-11-14）

> 结论先行。本文件是审计基线 `78f00ee3` 之后的最终账本：修了什么、当前基准数字、明确不做什么、还剩什么风险。
> 逐条状态以同目录 `task-backlog-2026-11-14.csv` 为准；本文件只做汇总与结论。

## 0. 结论

1. **审计中可执行的正确性缺陷已全部落地**：62 条中 55 条 `done`、5 条 `deferred`、1 条 `cut`、1 条 `blocked`（R0-1 旧账无法对账，见 §4）。
2. **核心承诺已从「可用性」推进到「可度量」**：测试关联自动口径 F1 Go 0.9983 / Java 1.0000（regex），人工独立真值 46 对全部 1.0；agent-bench 5 个活跃任务 full/minimal 均 100%，token 节省 99.11%。
3. **仍不能宣称的能力**：TS 的 Jest 回调测试关联（真值 0）、调用图分辨率（<0.22）、自动真值与 `call_graph` 同源、人工真值规模仍小。这些在 §4 明列，不静默删除。

## 1. 终态快照

| 项 | 值 |
|---|---|
| 基线 HEAD | `78f00ee3`（审计对象） |
| 终态 HEAD | 本文件所在提交（`git log -1 --oneline`） |
| 提交数（基线后） | 72 笔（含本笔总账） |
| 计数 | internal 36 / 全仓 40 包；非 vendor LoC 62976（63.0k）；MCP 12；HTTP 23 |
| 门禁 | `go build` / `go vet` / `go vet -tags "pg redis treesitter"` / `make test`（40 包）/ `make fmt-check` / `make counts-check` 全绿 |
| 清单状态 | 55 done / 5 deferred / 1 cut / 1 blocked |

## 2. 缺陷修复总账（按主题，哈希可直接 `git show`）

### 2.1 批次 A：解析与上下文正确性（P0/P1）

| 审计项 | 问题 | 提交 |
|---|---|---|
| P0-1 | Go 包级函数在三后端被静默丢弃 | `4b7fe61` |
| P1-14 | Go 多行签名提前闭合方法 | `a460fe2` |
| P1-1 | 方法 `EndLine` 恒等于 `StartLine` | `f3919fe` |
| P1-15 | 单行 >64KB 中断整次扫描 | `f5fba75` |
| P3-9 | Go 原始字符串 / const 块 / 泛型解析 | `d0afa9f` |
| P3-1 | `/viz/api/documents` 负数分页 panic | `4ef9e5e`、`009d9b2` |
| 工作底稿 P0-2 | LSP 方法层级/调用边/空结果回退/进程回收 | `1c9b997` |

### 2.2 批次 B：调用图、测试关联与稳定性

| 审计项 | 问题 | 提交 |
|---|---|---|
| P1-7 | import/reference 被当调用边 | `8ece7bd` |
| P1-8 / R2-2 | 后缀匹配遍历 map 导致结果跳变；BFS 重复；裸名归一 | `a940274`、`cf2c57c` |
| P1-12 / P2-4 | explicit 策略不按查询符号过滤、策略过宽 | `82e408e` |
| P1-13 / R2-5 | 每请求多次建图、depth 无上限、ctx 不生效 | `0ed28a6` |
| P1-9 | Redis 快路径 nil 解引用 | `0d0c36e` |
| P1-10 | FTS 共享 map 并发读写 | `365c1f4` |
| P1-11 | 未知租户 nil Service → 500 + 泄露 | `7ac50a0` |
| P1-2 | `mode=exact` 实际执行 fuzzy | `5860481` |

### 2.3 批次 C：多租户与存储

| 审计项 | 问题 | 提交 |
|---|---|---|
| P1-3 | 多租户默认共享 DSN/索引目录 | `4df1b53` |
| P1-4 | Redis 无租户前缀、TTL=0、Flush 全库 | `2381952` |
| P1-5 | PG `method` 表缺 `full_name` | `f159ab3` |
| P1-6 / R3-1 | 重索引孤儿方法与陈旧标签 | `6759621`、`31bea26` |
| P1-17 | `cloneConfig` 丢 AI/Vector 字段 | `f7355e0` |
| P1-18 | `Validate` 无生产调用 | `214feb4` |
| P2-9 / R3-12 | FileStore 崩溃丢增量、损坏静默 | `975b844` |
| P2-13 / R3-7 | IDF 0755、SQLite 0644 | `d2591dc` |
| R3-10 | 按文件清调用与标签反查索引 | `e4be03c`、`a13cc50` |
| R3-11 | 连接池上限可配置 | `942c912` |

### 2.4 批次 D：MCP 与接口契约

| 审计项 | 问题 | 提交 |
|---|---|---|
| P2-20 | MCP stdio/SSE 传输不符合规范 | `5c18c36` |
| P3-14 | OpenAPI 漏项、`/affected` 参数冲突 | `f5f4ab9` |
| P2-5 / R4-3 | `search_config` 描述与实现不一致 | `ca8836c` |
| S-1 / R4-4 | 官方 MCP 客户端 stdio + SSE e2e、stdout 仅 JSON-RPC | `bb45e2f` |
| R6-1 / S-3 | 预算化上下文（`budget_tokens`/`trim_reason`/`truncated`） | `601d67a` |
| R2-10 | 查询缓存 key 含 `IncludeTrace`、命中返回深拷贝 | `7007a77` |
| R3-8 | 认证下 CORS 不再回通配 | `a10e8b4` |
| R3-9 | 健康检查如实上报驱动与向量状态 | `8366b88` |

### 2.5 批次 E：语言精度与公开基准

| 审计项 | 问题 | 提交 |
|---|---|---|
| P1-16 / R5-1 | CodeGraph/JCodeIndexer 移出默认注册，SCIP 前置 | `3bcb7c7` |
| S-2（基准骨架） | release-bench：测试关联 P/R/F1 + 调用图 | `9f57d05` |
| S-2a / R5-7 | Go 包级测试函数进入候选 | `b871ca1` |
| S-2b / R5-8 | Java `*Tests` 后缀口径统一 | `86d278a` |
| S-2c / R5-9 | 采样只收缩评估集，真值全量 | `58625af` |
| S-2d / R5-10 | 命名按词边界 + JUnit 描述型前缀 | `be25fca` |
| S-2e / R5-11 | `call_graph` 策略补行为型/跨类召回 | `17f9ad1` |
| S-2f / R5-12 | 跨文件接收者方法入库 | `270409c` |
| S-2g / R5-13 | Go `Test*` 生产类型不再整文件判测试 | `7880c19` |
| S-2h / R5-14 | 人工独立真值评估 | `cba4911` |
| R5-2 | LSP 路径（同 P0-2） | `1c9b997` |
| R5-3 / S-4 | 调用边来源与置信度分级 | `0e93b00` |
| R5-5 | 非 Go 缩进作用域 + Elixir/C++ 方法名 | `e6b7179` |
| R5-6 / P3-6 | SCIP 懒加载首次 Parse 竞态 | `053ad01` |

### 2.6 批次 F：工程门禁与低风险欠账

| 审计项 | 问题 | 提交 |
|---|---|---|
| R6-2 | AI 查询预算按请求隔离 | `d36a9a3` |
| R6-3 | 向量模型/维度校验、删除生效、Close 后安全 | `1d5aee5`、`08de579`、`42bb9dd` |
| R6-4 | fsnotify 错误可观测 + 目录 rename 重建 | `e8e49a5` |
| R6-5 | 忽略清单同源 + 单文件单次读 + 无适配器不记 hash | `c70dede`、`57fbc15` |
| R6-6 | gofmt 一次性格式化 + CI 格式门禁 | `0f39d6e` |
| R6-7 | `verify-tags` 真编译 + agent-bench 绝对下限 | `711f593` |
| R6-8 | coverage 死代码删除 | `85005a7` |
| R1-6 | 文档纠偏与陈旧清单对账 | `d200c88` |

> 审计基线后、批次开工前的硬化：`60e405e`（AI 并发/429）、`39287bc`（非 Go 裸 callee 包限定）、`120abe9`（标签分类）、`4c33849`（文档与计数）。

## 3. 基准当前数字与口径

> 原始数据：`docs/bench/2026-11-14/README.md` 与同目录 `final-*.json`、`manual-pre184-go.json`。以下数字均可在该 README 复核。

### 3.1 测试关联（自动真值，R5-9 口径）

真值 = IR 直接调用边；`PredictedLinks=0` 表示策略链完全无输出；P/R/F1 为微平均。

| 仓库 | 变体 | 预测链接 | TP | FP | Precision | Recall | F1 |
|---|---|---:|---:|---:|---:|---:|---:|
| code-schema (go) | regex | 1343 | 1181 | 4 | 0.9966 | 1.0000 | 0.9983 |
| code-schema (go) | treesitter | 1380 | 1219 | 16 | 0.9871 | 1.0000 | 0.9935 |
| petclinic (java) | regex | 126 | 123 | 0 | 1.0000 | 1.0000 | 1.0000 |
| petclinic (java) | treesitter | 164 | 160 | 0 | 1.0000 | 1.0000 | 1.0000 |
| lytd (ts) | regex/treesitter | 0 | 0 | 0 | — | — | — |

**口径耦合（必须披露）**：自动真值与 `call_graph` 策略同为直接调用边，因此接近饱和的数字衡量的是「扫描 → 入库 → 反查」链路完整性与 FQN 归一，不是独立启发式的判别力。TS 的 `PredictedLinks=0` 是因为 Jest `it/test` 回调不是 IR 方法、真值也为 0，属解析缺口，不是「没有测试」。

### 3.2 测试关联（人工独立真值，S-2h）

2 个仓库、14 个生产方法、46 条人工 pair（Go 28 / Java 18），真值由阅读测试体标注，不来自 IR。

| 仓库 | 变体 | 真值 pair | TP | FP | FN | P | R | F1 |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| code-schema (go) | regex / treesitter | 28 | 28 | 0 | 0 | 1.0000 | 1.0000 | 1.0000 |
| petclinic (java) | regex / treesitter | 18 | 18 | 0 | 0 | 1.0000 | 1.0000 | 1.0000 |

回归捕获证据：R5-12 修复前同一数据集 Go regex 为 TP 6 / FN 22 / Recall 0.2143 / F1 0.3530；而自动基准同期仍显示高 P/R（目标方法从真值一起消失）。

### 3.3 调用图结构

`ResolutionRate = 能落到方法索引的 callee 边 / 全部调用边`。

| 仓库 | 变体 | calls | resolution |
|---|---|---:|---:|
| code-schema | regex | 16593 | 0.2130 |
| code-schema | treesitter | 18839 | 0.2048 |
| petclinic | regex | 1365 | 0.1487 |
| petclinic | treesitter | 1609 | 0.1790 |
| lytd | regex | 848 | 0.0425 |
| lytd | treesitter | 407 | 0.1425 |

### 3.4 Agent 端到端（agent-bench）

本仓实测（`--repo .`，8 任务中 5 个活跃、3 个 Skipped）：none 通过率 0、full 1.0、minimal 1.0；平均 token none 0 / full 448 / minimal 4；minimal 相对 full 节省 99.11%。CLI 默认下限 `MinPassRate=0.80`，低于即非零退出。

## 4. 明确砍掉、延后与无法对账

| 项 | 处置 | 原因 / 证据 |
|---|---|---|
| CodeGraph / JCodeIndexer 默认注册 | **砍默认接线**（`3bcb7c7`） | `Parse` 恒返回 `ErrSourceUnavailable`，生产只调 `Parse`；现仅显式 `enabled` 才注册，SCIP 优先 |
| coverage 关联策略 | **已删除**（`85005a7`） | 生产未接线；即使接线也会把测试类全部方法按 90 分登记，放大假阳性 |
| TS Jest `it/test` 回调 | **范围外，保留记录** | 回调不是 IR 方法，两变体测试方法均为 0、真值也为 0；覆盖需新增解析/策略条目，不能当「无测试」删除 |
| SSO / RBAC / 审计 / 配额（X-5） | 延后，另开产品线 | 本轮先证明 token 认证 + 租户隔离；企业能力不在本轮 |
| SCIP 导出（X-6） | 延后，仅可行性 spike | 工作量与维护成本大；LSP 路线优先 |
| viz 转义/锁/死代码（X-2） | 砍 | 调试页非产品面；只修 `P3-1` panic |
| SQLite 大规模性能优化（X-4） | 延后，文档标注边界 | 实测 10 万文件 72s；默认后端是 file，SQLite 只保证正确性 |
| 检索融合排序 / BM25（X-3） | 延后 | 非本轮押注；需公开基准证明有效后再做 |
| GoResolver / `SetModulePath`（X-1） | 延后 | 依赖 LSP/SCIP 精度线；当前 heuristic 有假边但非主路径 |
| 向量/ONNX 模型投入 | 冻结，仅修安全 | 向量检索商品化；默认 TF-IDF Recall@1=0.50，投入产出低 |
| 旧 C-16 清单与 `check-deps` | **无法对账（R0-1 blocked）** | 见下 |

### R0-1 核查结论（不编造映射）

已核查范围与结果（均为 0 命中，除 roadmap 自身引用）：

1. **全历史**：`git log --all -S 'C-16'` / `--grep 'C-16'` 仅命中 `b871ca1`（该提交新增 roadmap 文本，不是旧清单）；`git log --all -S 'check-deps'` / `'check_deps'` 同样只命中 roadmap 本身。
2. **已删文件**：`git log --all --diff-filter=D --name-only` 中无 `check-deps` / `deps-check` 类文件。
3. **工作区（含 gitignore）**：全仓 grep（排除 vendor/down/.git）覆盖 `analysis/`、`.trae/documents/`、`build/`、`docs/`，仅命中本轮总账/清单自身（roadmap、CSV、CHANGELOG、本文件），无历史 artifacts。
4. **分支/远端/暂存**：本地仅 `main`；`git ls-remote --heads origin` 仅 `refs/heads/main`；无 stash、无额外 tag（仅 `v0.1.0`）。

结论：**旧 C-16 的 31 条清单与 `check-deps` L0–L6 在本仓历史、工作区与上游均不存在**，无法逐条建立映射。不编造「C-16 → 本轮 P0/P1」的对应关系；本轮的 62 条审计项即为可执行替代总账。工作区基线已记录（本文件 §1 + 审计报告），未修改任何审计判据、阈值或快照。

## 5. 仍存在的风险与已知缺口

1. **自动真值同源**：测试关联自动口径与 `call_graph` 策略共用直接调用边；IR 解析不到的合法调用会漏入真值，把正确预测误判为 FP。缓解：人工真值并排展示；扩充人工数据集。
2. **人工真值规模小**：14 个生产方法 / 46 条 pair，只覆盖选定方法；当前 1.0 不等价于全仓 1.0。
3. **调用图分辨率低**：最高 0.2130（Go regex），TS 仅 0.0425–0.1425；跨类 receiver 消歧与外部依赖解析仍是缺口。
4. **TS 测试关联不可用**：Jest 回调未建模，`PredictedLinks=0`；调用图可报告，测试关联需新条目。
5. **残余 FP**：Go regex 自动口径 4 条，treesitter 16 条，多为「IR 未解析到的真实调用被命名策略命中」，按人工语义并非误报；两套口径不可混用。
6. **P0-1 的 FQN 变化**：历史索引可能需要重建；本轮未提供自动迁移命令，升级需重新扫描（文档已标注）。
7. **PG/Redis 验证边界**：本轮 PG 侧以 `-tags pg` 编译 + 单测校验；真实实例集成在 CI `pg-redis`，本地需自行起服务。
8. **死代码仍保留**：CodeGraph/JCodeIndexer 代码未删除，仅移出默认注册；文档与工具描述已收敛，但维护成本仍在。
9. **向量能力冻结**：默认 TF-IDF；ONNX/语义检索只修安全与生命周期，不作为卖点。
10. **R0-1 无法对账**：旧账 artifacts 缺失，无法证明「旧 31 条是否全部覆盖」；只能保证本轮 62 条自身闭环。

## 6. 复现入口

```bash
make test && make fmt-check && make counts-check && make verify-tags
make bench-agent                       # agent-bench（默认下限 0.80）
make bench-callgraph                   # 调用图精度对照（regex / treesitter）
```

测试关联基准的固定命令与参数见 `docs/bench/2026-11-14/README.md` §运行命令。
