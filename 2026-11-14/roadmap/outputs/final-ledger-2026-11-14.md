# CodeSchema 修复总账（2026-11-14）

> 结论先行。本文件是审计基线 `78f00ee3` 之后的最终账本：修了什么、当前基准数字、明确不做什么、还剩什么风险。
> 逐条状态以同目录 `task-backlog-2026-11-14.csv` 为准；本文件只做汇总与结论。

## 0. 结论

1. **审计中可执行的正确性缺陷已全部落地**：62 条中 55 条 `done`、6 条 `cut`、1 条 `blocked`（R0-1 旧账无法对账，见 §4）；deferred 复盘后 5 条延后项均给出了降 cut 的实测理由。收尾阶段新增 R5-15（人工真值扩展）、R3-13（PG 验证边界）两条，均 `done`。
2. **核心承诺已从「可用性」推进到「可度量」**：测试关联自动口径 F1 Go 0.9873–0.9883 / Java 0.9786–0.9801，**人工独立真值扩至 207 对**（Go 167 / Java 40）：Go F1 0.9524（regex）/ 0.9555（treesitter），Java 1.0000；agent-bench 5 个活跃任务 full/minimal 均 100%，token 节省 99.11%。
3. **仍不能宣称的能力**：TS 的 Jest 回调测试关联（真值 0）、调用图全量分辨率（<0.27）、自动真值与 `call_graph` 同源、人工真值仅覆盖 code-schema + petclinic 两仓。这些在 §4 明列，不静默删除。

## 1. 终态快照

| 项 | 值 |
|---|---|
| 基线 HEAD | `78f00ee3`（审计对象） |
| 终态 HEAD | 本文件所在提交（`git log -1 --oneline`） |
| 提交数（基线后） | 93 笔（含本笔总账） |
| 计数 | internal 36 / 全仓 40 包；非 vendor LoC 64492（64.5k）；MCP 12；HTTP 23 |
| 门禁 | `go build` / `go vet` / `go vet -tags "pg redis treesitter"` / `make test`（40 包）/ `make fmt-check` / `make counts-check` 全绿 |
| 清单状态 | 55 done / 5 deferred / 1 cut / 1 blocked + 收尾 R5-15、R3-13（done） |

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

### 2.7 批次 G：人工真值修正、caller 归属与仓内口径

| 项 | 问题 | 提交 |
|---|---|---|
| 人工真值 | `manual-truth.json` 失效引用 `TestFindTestLinks_Coverage`，28 → 27（只修数据有效性） | `f3b3801` |
| 度量缺陷 | 方法索引 `count[fqn]==1` 过严，精确命中不构成歧义（不计引擎收益） | `ae10edb` |
| TS 声明 | 顶层函数与无返回类型方法漏入索引（lytd calls 848 → 385） | `84a8479` |
| 关键字假调用 | `if(`/`len(` 被限定后绕过过滤 | `49afeed` |
| Go 局部变量 | `x := &T{}` / `var x *T` 的 selector 类型消歧 | `58e2e40` |
| TS 模块限定 | 顶层函数按模块限定 FQN，拆开重复裸名 | `5858592` |
| 仓内口径 | 新增「仓内可解析边」并排展示，旧口径分母不变 | `793d187` |
| TS 构造器 | 声明式 `constructor();` 不再计为调用 | `f03f945` |
| Java 注解 | `@Override` 等注解行不再计为调用 | `98d2a3e` |
| Go 原始字符串 | 串内内容不再创建假方法 | `0ed6868` |
| Go 接口签名 | 接口方法签名不再计为调用（caller 空 131 → 25） | `0aa9ce8` |
| Go 指针转换 | AST 路径 `(*T)(nil)` 不再计为调用（空 36 → 30） | `c56e18a` |
| TS 具名箭头 | `const f = () => {}` / `function(){}` 入索引（AST） | `4c5a08b` |
| `.tsx` grammar | JSX 后声明不再丢（lytd 空 270 → 245） | `45dcfcd` |
| C/C++ 方法名 | AST 沿 declarator 链取声明名；变体专属断言按构建跳过 | `a8fd6e7` |

### 2.8 批次 H：人工真值扩展与 PG 验证边界（收尾）

| 项 | 问题 | 提交 |
|---|---|---|
| 人工真值 v2 | 45 → 207 对（Go 167 / Java 40），FQN 补包名、补失效引用；`pairs >= 200` 规模守护 | 本文件所在提交 |
| Java 行内注解 | `public @ResponseBody Vets foo()` 未入索引（真值自检暴露） | `3420f48` |
| 包限定 callee | `pkg.Type.Method` 与索引 `Type.Method` 不匹配，短名多义（Go FN 63） | `37cca60` |
| Go 跨行调用 | 调用起始行 `(` 未闭合于同行时漏边（3 条 FileStore FN） | `6b5927d` |
| Java 嵌套类 FQN | AST 保留嵌套类名，与正则/真值口径不一致（treesitter 真值自检失败） | `2b26b38` |
| Java 构造链式 | `new X().m()` 提取为 `new X`，treesitter 夹具真值 pair=0 | `f14d44c` |
| AST Go 局部类型 | selector 恒裸名，短名消歧失败（treesitter Go recall 0.6287） | `5265541` |
| PG 验证边界 | 补 tags/批量/删除/池/局部直接写入口等路径；未验项逐条列风险 | 本文件所在提交 |

> 以上缺陷均为「扩展真值/补验证」过程中暴露，按先红后绿、一条一提交处理。

## 3. 基准当前数字与口径

> 原始数据：`docs/bench/2026-11-14/README.md` 与同目录 `final-*.json`、`post-*.json`、`manual-pre184-go.json`。以下数字均可在该 README 复核。

### 3.1 测试关联（自动真值，R5-9 口径）

真值 = IR 直接调用边；`PredictedLinks=0` 表示策略链完全无输出；P/R/F1 为微平均。

| 仓库 | 变体 | 预测链接 | TP | FP | Precision | Recall | F1 |
|---|---|---:|---:|---:|---:|---:|---:|
| code-schema (go) | regex | 1677 | 1521 | 16 | 0.9896 | 0.9851 | 0.9873 |
| code-schema (go) | treesitter | 1719 | 1567 | 19 | 0.9880 | 0.9886 | 0.9883 |
| petclinic (java) | regex | 126 | 123 | 0 | 1.0000 | 0.9609 | 0.9801 |
| petclinic (java) | treesitter | 164 | 160 | 0 | 1.0000 | 0.9581 | 0.9786 |
| lytd (ts) | regex/treesitter | 0 | 0 | 0 | — | — | — |

> 解析器补齐后 pair 规模上升（Go regex 2284 / treesitter 2373），新增 pair 中有
> 18–23 条关联未被策略命中，F1 由小样本的 0.9983 降至 0.9873/0.9883；这是覆盖面
> 扩大暴露的真实 FN，既有链路上的 TP 未回退。

**口径耦合（必须披露）**：自动真值与 `call_graph` 策略同为直接调用边，因此接近饱和的数字衡量的是「扫描 → 入库 → 反查」链路完整性与 FQN 归一，不是独立启发式的判别力。TS 的 `PredictedLinks=0` 是因为 Jest `it/test` 回调不是 IR 方法、真值也为 0，属解析缺口，不是「没有测试」。

### 3.2 测试关联（人工独立真值，S-2h / R5-15）

**207 条人工 pair（Go 167 / Java 40）**，覆盖 2 个仓库、3 个 Go 测试文件 + 4 个 Java 测试类；
真值由逐个阅读测试体标注，候选生成用纯文本（花括号配对 + 调用名正则），**不读 IR 调用边**，
标注规则见 `docs/bench/2026-11-14/README.md` §标注规则（v2）。

| 仓库 | 变体 | 规模 | TP | FP | FN | P | R | 95% CI（R） | F1 |
|---|---|---:|---:|---:|---:|---:|---:|---|---:|
| code-schema (go) | regex | 167 | 160 | 9 | 7 | 0.9467 | 0.9581 | 0.9160–0.9796 | 0.9524 |
| code-schema (go) | treesitter | 167 | 161 | 9 | 6 | 0.9471 | 0.9641 | 0.9238–0.9834 | 0.9555 |
| petclinic (java) | regex / treesitter | 40 | 40 | 0 | 0 | 1.0000 | 1.0000 | 0.9124–1.0000 | 1.0000 |

扩展前（45 对：Go 27 / Java 18）两变体均为 1.0000；v1 的饱和是小样本结果，不能外推。
扩展后 Go 的 9 条 FP 集中在命名策略对 `Service.Search`（9 条）的过匹配；7 条 FN 为
接收者类型需跨语句推导（`svc := newTestService(t)`）与嵌套调用
（`TypeOf(NewService(...))`）抽取不到，已在数据集 `note` 逐条记录。

回归捕获证据：R5-12 修复前同一数据集 Go regex 为 TP 6 / FN 22 / Recall 0.2143 / F1 0.3530；而自动基准同期仍显示高 P/R（目标方法从真值一起消失）。

### 3.3 调用图结构

`ResolutionRate = 能落到方法索引的 callee 边 / 全部调用边`。

| 仓库 | 变体 | calls | resolved | resolution |
|---|---|---:|---:|---:|
| code-schema | regex | 16327 | 4315 | 0.2643 |
| code-schema | treesitter | 20452 | 4543 | 0.2221 |
| petclinic | regex | 1183 | 215 | 0.1817 |
| petclinic | treesitter | 1609 | 304 | 0.1889 |
| lytd | regex | 349 | 61 | 0.1748 |
| lytd | treesitter | 413 | 93 | 0.2252 |

**并排口径「仓内可解析边」**（分母只算索引中确有候选的边，旧口径分母不变）：

| 仓库 | 变体 | 仓内候选边 | 仓内已解析 | 仓内 resolution |
|---|---|---:|---:|---:|
| code-schema | regex | 4485 | 4315 | 0.9621 |
| code-schema | treesitter | 4676 | 4543 | 0.9716 |
| petclinic | regex | 218 | 215 | 0.9862 |
| petclinic | treesitter | 307 | 304 | 0.9902 |
| lytd | regex | 61 | 61 | 1.0000 |
| lytd | treesitter | 93 | 93 | 1.0000 |

> treesitter Go 仓内 resolution 由 0.9989 降至 0.9716：AST 路径补齐局部变量类型限定后，
> 更多裸 selector 进入「仓内候选」分母，跨包/接口派发部分仍未解析；已解析边数 4543 不变。

> lytd regex 的 calls 由 848 降至 349：旧 TS 模式把 555 条顶层函数声明误当调用边，
> 修复后这些行成为方法、不再是调用；关键字假调用（`if/for/while` 等）与声明式构造器
> 再删除一批。顶层函数按模块限定 FQN 后重复裸名被拆开，跨模块裸名无 import 信息时
> 如实不可解析（0.2831 → 0.2519 → caller 归属修正后 0.1748），仓内口径保持 1.0000。
> caller 为空：Go regex 341 → 25（接口签名/原始字符串/关键字假调用）、Java regex 60 → 9、
> lytd regex 241 → 205；treesitter 变体 Go 36 → 30、lytd 270 → 245（`.tsx` 25 → 0）。
> 剩余空 caller 均为包级初始化、字段初始化与 `describe/it` 匿名回调，无可归属方法，不伪造归属。

> `count[fqn]==1` 改为 `>0` 属**度量缺陷修正**（精确 FQN 命中不构成歧义），
> 上面的提升不计入引擎收益；两套数字与旧口径均保留。

### 3.4 Agent 端到端（agent-bench）

本仓实测（`--repo .`，8 任务中 5 个活跃、3 个 Skipped）：none 通过率 0、full 1.0、minimal 1.0；平均 token none 0 / full 448 / minimal 4；minimal 相对 full 节省 99.11%。CLI 默认下限 `MinPassRate=0.80`，低于即非零退出。

## 4. 明确砍掉、延后与无法对账

| 项 | 处置 | 原因 / 证据 |
|---|---|---|
| CodeGraph / JCodeIndexer 默认注册 | **砍默认接线**（`3bcb7c7`） | `Parse` 恒返回 `ErrSourceUnavailable`，生产只调 `Parse`；现仅显式 `enabled` 才注册，SCIP 优先 |
| coverage 关联策略 | **已删除**（`85005a7`） | 生产未接线；即使接线也会把测试类全部方法按 90 分登记，放大假阳性 |
| TS Jest `it/test` 回调 | **范围外，保留记录** | 回调不是 IR 方法，两变体测试方法均为 0、真值也为 0；覆盖需新增解析/策略条目，不能当「无测试」删除 |
| SSO / RBAC / 审计 / 配额（X-5） | **降 cut** | 企业能力另开产品线；本轮只证明 token 认证 + 租户隔离 |
| SCIP 导出（X-6） | **降 cut** | 工作量与维护成本大；LSP 路线优先且已落地 |
| viz 转义/锁/死代码（X-2） | 砍 | 调试页非产品面；只修 `P3-1` panic |
| SQLite 大规模性能优化（X-4） | **降 cut** | 默认后端是 file；SQLite 只保证正确性，10 万文件 72s 边界已文档化 |
| 检索融合排序 / BM25（X-3） | **降 cut** | 无公开检索基准证明收益；非本轮押注 |
| GoResolver / `SetModulePath`（X-1） | **降 cut** | 仓内可解析边已达 0.9621–0.9902（TS 1.0000）；剩余不可解析为仓外调用，需 import/模块图而非再写启发式 |
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
2. **人工真值覆盖面有限**：207 条 pair（Go 167 / Java 40）已具备统计意义（Go recall 95% CI 半宽约 ±3pp），但仍只覆盖 code-schema 与 petclinic 两个仓库的 7 个测试文件/类；不能外推为全仓 1.0。TS 无人工真值。
3. **调用图分辨率低**：全量口径最高 0.2643（Go regex），TS 0.1748–0.2252；跨类 receiver 消歧与外部依赖解析仍是缺口。仓内可解析边口径已达 0.9621–1.0000，说明剩余不可解析主要来自外部 API/框架与无 import 信息的跨模块裸名，而非索引失配。
4. **TS 测试关联不可用**：Jest 回调未建模，`PredictedLinks=0`；调用图可报告，测试关联需新条目。
5. **残余 FP**：Go regex 自动口径 16 条，treesitter 19 条，多为「IR 未解析到的真实调用被命名策略命中」，按人工语义并非误报；两套口径不可混用。
6. **P0-1 的 FQN 变化**：历史索引可能需要重建；本轮未提供自动迁移命令，升级需重新扫描（文档已标注）。
7. **PG 验证边界已明确但未消除**：`-tags pg` 下 10 个集成用例在嵌入式 PG 16.3 全绿，覆盖 IR 全链路、批量、标签、变量常量、删除级联、池与索引；TLS、HA/只读副本、并发写、亿级长稳、服务级 PG E2E、Redis+PG 组合仍未验证，逐条原因与风险见 `docs/01-开发者/存储后端.md` §PG 真实实例验证边界。
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
