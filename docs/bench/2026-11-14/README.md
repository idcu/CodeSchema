# 公开基准（S-2）· 2026-11-14

> 最后更新：2026-11-14

首轮公开基准：在 3 个真实仓库上测「测试关联 P/R/F1」与「调用图结构指标」。
**只报告、不设 CI 阈值**（roadmap R5-4）；原始数据见同目录 `before-*.json`（S-2 首轮）、
`baseline-r59-*.json`（R5-9 口径修正后、召回修复前）、`after-*.json`（R5-11 后）。

## 口径

- **真值独立于被测启发式**：以 IR 中「测试方法 → 生产方法」的**直接调用边**作为真值，
  不使用命名约定构造真值（否则等于用命名约定评估命名约定）。
- **确定性**：文件顺序固定排序、pair 排序固定、等距 stride 采样；同输入两次运行结果一致
  （`TestRun_MultiLanguageFixtures` 断言除时间戳外 DeepEqual）。
- **指标**：对真值中的每个生产方法调用 `FindTestLinks`，把返回的 `TestMethod` 与真值集合比对，
  微平均 TP/FP/FN 与 Precision/Recall/F1；`PredictedLinks=0` 表示策略链**完全无输出**
  （区别于「有输出但 FQN 口径不一致」）。
- **采样口径（R5-9 / S-2c）**：`--max-pairs` 只收缩「待评估生产方法集合」，真值始终取全量 pair。
  否则同一生产方法未进入采样的真实测试会被计为 FP、其余真值被截断为 FN，P/R 同时失真。
- **调用图**：`ResolutionRate = 能落到方法索引的 callee 边 / 全部调用边`。
- **负控**：`TestAffectedNegativeControl_DeleteTest`——删除测试文件前后 `GetAffected` 必须
  从「含该测试」变为「不含」（通过）。

## 环境

| 仓库 | 语言 | commit | 构建变体 |
|---|---|---|---|
| `code-schema`（本仓） | go | 首轮 `3887a42` / 本轮前 `58625af` / after `HEAD`（R5-10/R5-11） | 默认 `regex` / `-tags treesitter` |
| `spring-petclinic` | java | `500158f` | 默认 `regex` / `-tags treesitter` |
| `lytd` | ts | `f5e6604` | 默认 `regex` / `-tags treesitter` |

运行命令（两个变体各执行一次）：

```bash
go build -o /tmp/cs-regex ./cmd/codeschema
go build -tags treesitter -o /tmp/cs-ts ./cmd/codeschema

/tmp/cs-regex release-bench --repo <repo> --lang <lang> --out <dir>
/tmp/cs-ts    release-bench --repo <repo> --lang <lang> --out <dir>
```

> 本仓与 petclinic 的**真值数量会随解析覆盖率变化**（自测仓库、Java 包级可见性方法），
> 前后对比看同一行内的 P/R/F1 与 `PredictedLinks`，不要跨行比较分母。

## 测试关联：本轮前（R5-9 口径）→ 本轮后（R5-11）

同一 `release-bench` 命令、同一语料、同一真值（R5-9 口径：真值全量、采样只收缩评估生产集合）。
本轮前 = `58625af`；本轮后 = `HEAD`（R5-10 命名边界 + R5-11 call_graph）。

| 仓库 | 变体 | 预测链接（前→后） | TP（前→后） | FP（前→后） | Precision（前→后） | Recall（前→后） | F1（前→后） |
|---|---|---:|---:|---:|---:|---:|---:|
| code-schema (go) | regex | 156 → 1282 | 130 → 1144 | 26 → 9 | 0.8333 → 0.9922 | 0.1114 → 0.9803 | 0.1965 → 0.9862 |
| code-schema (go) | treesitter | 174 → 1366 | 128 → 1217 | 46 → 24 | 0.7356 → 0.9807 | 0.1031 → 0.9807 | 0.1809 → 0.9807 |
| petclinic (java) | regex | 1 → 126 | 1 → 123 | 0 → 0 | 1.0000 → 1.0000 | 0.0081 → 1.0000 | 0.0161 → 1.0000 |
| petclinic (java) | treesitter | 1 → 164 | 1 → 160 | 0 → 0 | 1.0000 → 1.0000 | 0.0063 → 1.0000 | 0.0125 → 1.0000 |
| lytd (ts) | regex | 0 → 0 | 0 → 0 | 0 → 0 | — | — | — |
| lytd (ts) | treesitter | 0 → 0 | 0 → 0 | 0 → 0 | — | — | — |

> S-2 首轮（`3887a42`）六组 `PredictedLinks` 均为 0（候选发现缺陷），与本轮数字不同口径，
> 只作为「从零到可见」的起点，不参与 P/R/F1 比较。

### 必须披露的口径耦合

`call_graph` 策略（R5-11）与基准真值**同为直接调用边**：真值来自 IR 直接解析，
策略结果来自「扫描 → 入库 → 反查」链路。因此本轮后接近饱和的数字衡量的是**链路完整性
与 FQN 归一**，不再证明某种独立启发式的判别力。命名策略的独立收益以 R5-10 的中间态衡量
（Go regex P 0.8333 → 0.8630、Java TP 1 → 3），不由本轮 1.0 代表。

### 残余漏召（23/24 条，未静默忽略）

- `Service.FindTestLinks ← service.TestFindTestLinks_*`：callee 为接收者变量形态
  （`svc.FindTestLinks`），短名在库内存在多个声明时无法归一为唯一 FQN。
- `MCPServer.serveStdio ← server.TestMCPServer_Stdio_*`：调用边未被抽取/入库。
- 归属：**调用解析 + FQN 归一**残余，留待下一轮解析精度条目；不在本轮通过放宽阈值掩盖。

## 调用图

| 仓库 | 变体 | calls（前→后） | resolution（前→后） |
|---|---|---|---:|
| code-schema | regex | 16473 → 16473 | 0.2131 → 0.2131 |
| code-schema | treesitter | 18695 → 18695 | 0.2049 → 0.2049 |
| petclinic | regex | 1365 → 1365 | 0.1487 → 0.1487 |
| petclinic | treesitter | 1609 → 1609 | 0.1790 → 0.1790 |
| lytd | regex | 848 → 848 | 0.0425 → 0.0425 |
| lytd | treesitter | 407 → 407 | 0.1425 → 0.1425 |

> R5-10/R5-11 不修改解析器，调用图指标与调用边规模保持不变；分辨率仍 < 0.22，
> 跨类 receiver 消歧是后续条目。

## 结论与根因

1. **Go 包级测试函数不可见（已修，R5-7 / S-2a）。** 测试函数挂在合成 `PACKAGE` 容器
   （容器名=包名），旧 `discoverTestClasses` 只按 `*Test`/`Test*` 类名发现。现按「同目录同包」
   把 `*_test.go` 的 PACKAGE 容器归属到该目录全部生产类，`TestXxx`/`BenchmarkXxx`/`ExampleXxx`
   均进入候选；`GetAffected` 复用 `FindTestLinks`，同步生效。
2. **Java `*Tests` 后缀（已修，R5-8 / S-2b）。** `OwnerControllerTests` 此前不被 `matchTestClass`
   识别（`isTestClass` 已支持，口径不一致）。现已统一为 `Test` 前后缀 + `Tests`/`Spec` 后缀；
   连带修复 Java 包级可见性方法的正则解析（单 tab 缩进 + 无修饰符），否则默认变体不可观测。
3. **TS 的 Jest `it/test` 回调：明确不在本轮范围。** 回调不是 IR 方法，两变体测试方法均为 0，
   真值为 0——这是解析缺口而非「没有测试」；记录保留、不删除。覆盖它需要新的解析/策略条目
   （把 `describe/it/test` 回调建模为方法，或新增文件级测试关联策略）。在此之前 TS 只报告调用图。
4. **基准评估口径（已修，R5-9 / S-2c）。** 旧实现先采样再构造真值：同一生产方法未进入采样的
   真实测试被计 FP、其余真值被截断为 FN。现真值取全量 pair，采样只收缩评估生产集合；
   该修正不放宽任何阈值，Go regex 中间态 P 0.1943 → 0.7386。
5. **命名匹配（已修，R5-10 / S-2d）。** `Contains` 子串兜底让 `Analyze` 命中 `Analyzer`；
   无 `Test` 前缀的 JUnit 描述型方法名（`processCreationFormSuccess`）无法命中。
   现按词边界序列匹配 + 描述型前缀（目标名 <4 禁用），Go 包级 helper 不参与命名。
6. **候选集剪枝（已修，R5-11 / S-2e）。** `discoverTestClasses` 只按类名约定建候选：
   petclinic 的 123 条真值中有 120 条目标方法属于 `NamedEntity`/`BaseEntity` 等无同名测试类的
   基类；Go 的行为型测试名（`TestCollection_Delete` → `DB.CreateCollection`）也不在命名候选内。
   新增 `call_graph` 策略（置信度 95）：按测试文件的直接调用边反查目标方法，与类名约定无关；
   同时做 callee 短名归一（`entity.setName`）与带包名前缀 caller 的后缀归一。
7. **仍偏低/未覆盖（不静默删除）**：TS 的 Jest `it/test` 回调仍不在本轮范围（真值 0）；
   调用图分辨率 < 0.22，跨类 receiver 消歧不足；call_graph 残余 23/24 条 FN 归属
   「调用解析 + FQN 归一」；命名策略的独立判别力需在 call_graph 之外的视图继续跟踪。
