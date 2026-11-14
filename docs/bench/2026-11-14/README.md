# 公开基准（S-2）· 2026-11-14

> 最后更新：2026-11-14

首轮公开基准：在 3 个真实仓库上测「测试关联 P/R/F1」与「调用图结构指标」。
**只报告、不设 CI 阈值**（roadmap R5-4）；原始数据见同目录 `before-*.json`（基线）、`after-*.json`（R5-7/R5-8 修复后）。

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
| `code-schema`（本仓） | go | 基线 `3887a42` / after `HEAD`（R5-7/R5-8） | 默认 `regex` / `-tags treesitter` |
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

## 测试关联：修复前 → 修复后

| 仓库 | 变体 | 预测链接（前→后） | TP（前→后） | Precision（前→后） | Recall（前→后） | F1（前→后） |
|---|---|---:|---:|---:|---:|---:|
| code-schema (go) | regex | 0 → 175 | 0 → 34 | 0 → 0.1943 | 0 → 0.1771 | 0 → 0.1853 |
| code-schema (go) | treesitter | 0 → 152 | 0 → 31 | 0 → 0.2039 | 0 → 0.1676 | 0 → 0.1840 |
| petclinic (java) | regex | 0 → 1 | 0 → 1 | 0 → 1.0000 | 0 → 0.0081 | 0 → 0.0161 |
| petclinic (java) | treesitter | 0 → 1 | 0 → 1 | 0 → 1.0000 | 0 → 0.0063 | 0 → 0.0125 |
| lytd (ts) | regex | 0 → 0 | 0 → 0 | — | — | — |
| lytd (ts) | treesitter | 0 → 0 | 0 → 0 | — | — | — |

修复前（基线）各仓库 `PredictedLinks` 均为 0；修复后 Go 两变体召回约 0.17–0.18，
Java 命中 1 条（precision 1.0，recall 受「无 Test 前缀的描述型测试名」严格规则限制）。
Java regex 的 `TestMethods` 由 29 提升到 123，原因是包级可见性方法（JUnit 5 常见）
此前的正则方法模式漏解析，属于 R5-8 的连带根因。

## 调用图

| 仓库 | 变体 | calls（前→后） | resolution（前→后） |
|---|---|---|---:|
| code-schema | regex | 16376 → 16415 | 0.2126 → 0.2125 |
| code-schema | treesitter | 18593 → 18636 | 0.2044 → 0.2043 |
| petclinic | regex | 1451 → 1365 | 0.1234 → 0.1487 |
| petclinic | treesitter | 1609 → 1609 | 0.1790 → 0.1790 |
| lytd | regex | 848 → 848 | 0.0425 → 0.0425 |
| lytd | treesitter | 407 → 407 | 0.1425 → 0.1425 |

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
4. **仍偏低的点（后续条目，未在本轮修）**：Java 描述型测试名（`processCreationFormSuccess`
   对应 `processCreationForm`）受 P2-4 严格规则限制，召回低；非 Go 调用图分辨率仍 < 0.2，
   跨类 receiver 消歧不足。
