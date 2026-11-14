# 公开基准（S-2）· 2026-11-14

> 最后更新：2026-11-14

首轮公开基准：在 3 个真实仓库上测「测试关联 P/R/F1」与「调用图结构指标」。
**只报告、不设 CI 阈值**（roadmap R5-4）；原始数据见同目录 `before-*.json`（S-2 首轮）、
`baseline-r59-*.json`（R5-9 口径修正后、召回修复前）、`after-*.json`（R5-11 后）、
`final-*.json`（R5-12/R5-13 后，含人工真值评估）、`manual-pre184-go.json`（人工真值的修复前对照）。

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
| `code-schema`（本仓） | go | 首轮 `3887a42` / R5-11 前 `58625af` / 本轮前 `17f9ad1` / 本轮后 `HEAD`（R5-12/R5-13） | 默认 `regex` / `-tags treesitter` |
| `spring-petclinic` | java | `500158f` | 默认 `regex` / `-tags treesitter` |
| `lytd` | ts | `f5e6604` | 默认 `regex` / `-tags treesitter` |

运行命令（两个变体各执行一次；人工真值参数可选）：

```bash
go build -o /tmp/cs-regex ./cmd/codeschema
go build -tags treesitter -o /tmp/cs-ts ./cmd/codeschema

/tmp/cs-regex release-bench --repo <repo> --lang <lang> --out <dir> \
  --manual-truth internal/releasebench/testdata/manual-truth.json
/tmp/cs-ts    release-bench --repo <repo> --lang <lang> --out <dir> \
  --manual-truth internal/releasebench/testdata/manual-truth.json
```

> 本仓与 petclinic 的**真值数量会随解析覆盖率变化**（自测仓库、Java 包级可见性方法），
> 前后对比看同一行内的 P/R/F1 与 `PredictedLinks`，不要跨行比较分母。本轮本仓新增了
> 人工真值评估器（`manual.go` + dataset），语料自身变大，Go 行的 calls/TP 含该增量。

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

## 测试关联：本轮前（R5-11）→ 本轮后（R5-12/R5-13）

本轮前 = `17f9ad1`；本轮后 = `HEAD` 工作树（跨文件接收者方法入库 + Go 测试文件判定）。
同一命令、同一 R5-9 口径。本仓语料新增了人工真值评估器，Go 行的 calls/真值含该增量。

| 仓库 | 变体 | 预测链接（前→后） | TP（前→后） | FP（前→后） | Precision（前→后） | Recall（前→后） | F1（前→后） |
|---|---|---:|---:|---:|---:|---:|---:|
| code-schema (go) | regex | 1282 → 1343 | 1144 → 1181 | 9 → 4 | 0.9922 → 0.9966 | 0.9803 → 1.0000 | 0.9862 → 0.9983 |
| code-schema (go) | treesitter | 1366 → 1380 | 1217 → 1219 | 24 → 16 | 0.9807 → 0.9871 | 0.9807 → 1.0000 | 0.9807 → 0.9935 |
| petclinic (java) | regex | 126 → 126 | 123 → 123 | 0 → 0 | 1.0000 → 1.0000 | 1.0000 → 1.0000 | 1.0000 → 1.0000 |
| petclinic (java) | treesitter | 164 → 164 | 160 → 160 | 0 → 0 | 1.0000 → 1.0000 | 1.0000 → 1.0000 | 1.0000 → 1.0000 |
| lytd (ts) | regex | 0 → 0 | 0 → 0 | 0 → 0 | — | — | — |
| lytd (ts) | treesitter | 0 → 0 | 0 → 0 | 0 → 0 | — | — | — |

### 度量缺陷修正：方法索引 `count[fqn]==1` 过严（不计入引擎收益）

`methodIndex.resolve` 原先把「同一 FQN 登记次数 >1」当作歧义。实际同一 FQN 会因
Java 重载、同名多声明被多次登记，精确 FQN 命中本身并无歧义。改为「精确 FQN 命中即解析，
短名歧义仍要求唯一」。这是**度量缺陷修正**：被挡掉的边本来就能被解析器产出，
所以下面的 resolution 提升不计入引擎能力收益。

同一语料（含本节新增单测，本仓 calls 因此 +9）、同一命令，仅回滚/恢复该函数测得：

| 仓库 | 变体 | calls | 可解析边（前→后） | resolution（前→后） | Precision（前→后） | Recall（前→后） | F1（前→后） |
|---|---|---:|---:|---:|---:|---:|---:|
| code-schema (go) | regex | 17641 | 3963 → 4106 | 0.2246 → 0.2328 | 0.9868 → 0.9870 | 1.0000 → 0.9904 | 0.9934 → 0.9887 |
| code-schema (go) | treesitter | 20031 | 4314 → 4472 | 0.2154 → 0.2233 | 0.9871 → 0.9894 | 1.0000 → 0.9881 | 0.9935 → 0.9887 |
| petclinic (java) | regex | 1365 | 203 → 216 | 0.1487 → 0.1582 | 1.0000 → 1.0000 | 1.0000 → 0.9609 | 1.0000 → 0.9801 |
| petclinic (java) | treesitter | 1609 | 288 → 304 | 0.1790 → 0.1889 | 1.0000 → 1.0000 | 1.0000 → 0.9581 | 1.0000 → 0.9786 |
| lytd (ts) | regex | 848 | 36 → 36 | 0.0425 → 0.0425 | — | — | — |
| lytd (ts) | treesitter | 407 | 58 → 91 | 0.1425 → 0.2236 | — | — | — |

`test_link` 的 P/R/F1 变化不是引擎回归：索引放宽后自动真值多出此前无法解析的 pair，
采样评估的生产方法集合随之改变（Go regex 预测 1343 → 1604），原先被隐藏的漏召
（Go regex FN 0 → 14、Java regex 0 → 5）因此显形。两套数字都保留。

### TS 正则路径：顶层函数与无返回类型方法入索引

旧 TS `methodPattern` 强制 `):` 返回类型注解，且没有 `function` 分支：lytd 中 555 条
`export function foo(` / `function foo(` 全部漏解析，无注解的类方法（`start(port: number) {`）
同样漏掉；这些声明行又被 `callPattern` 误读成调用边。修复后方法模式允许省略返回类型
（`;` 分支仍要求注解，避免 `foo(a);` 误判），并新增 `funcPattern` 兜底顶层函数。

同语料、同一命令，lytd ts regex：calls 848 → 385（原先把函数声明当调用），
可解析边 36 → 109，resolution 0.0425 → 0.2831。Go/Java 复核无回退
（Go 0.2328 → 0.2327 的差异来自新增单测使语料 +15 条 calls；Java 0.1582 不变）。

### 关键字假调用：裸名须在包/类限定前过滤

`detectCalls` 原先把 `if (` 这类关键字先经 `qualifyCallee` 限定成 `pkg.Class.if`，
再查 `isKeyword`，限定后的名字永远查不到，控制流因此变成调用边；Go 内建函数
（`len/cap/append/close/make/...`）同样被包限定后绕过过滤。

修复：先按裸名过滤，再做限定（限定后仍保留一次关键字检查，覆盖 `IO.puts` 等）。
补入 Java `try/synchronized/do/else/finally`。

同语料、同一命令：

| 仓库 | 变体 | calls（前→后） | resolution（前→后） |
|---|---|---:|---:|
| petclinic (java) | regex | 1365 → 1262 | 0.1582 → 0.1712 |
| petclinic (java) | treesitter | 1609 → 1609 | 0.1889 → 0.1889（该路径不走正则） |
| code-schema (go) | regex | 17656 → 15486 | 0.2327 → 0.2653 |
| lytd (ts) | regex | 385 → 385 | 0.2831 → 0.2831 |

Go 的降幅主要来自 `len/cap/append/make/close` 等内建伪调用被正确剔除，
Java 的 103 条降幅含 72 条控制流关键字。`test_link` 的 TP/真值同步收缩
（这些伪边此前也进了自动真值），属口径修正而非召回退化。

### Go 局部变量 selector 消歧（CallerFQN 不变）

方法体内 `x := &T{}` / `var x *T` 声明的局部变量，其 `x.Method()` 现限定为
`pkg.T.Method`（此前只跟踪 receiver，保持 `x.Method` 裸 selector）。

单元负控 `TestGoLocalVarCallQualification` 先红后绿。但在**现有严格 resolution 口径**下
数字几乎不动：code-schema go regex 可解析边 4109 → 4111（calls +18 来自新增单测，
rate 0.2653 → 0.2652）。原因是基准 `methodIndex` 的键是 `Type.Method`（无包前缀），
而新 callee 是 `pkg.Type.Method`，只能靠短名回退；`Start/Reload` 这类短名多义时仍判不可解析。
真实收益需要「仓内可解析边」口径（见下节）才能显形，旧口径分母不做调整。

### TS 顶层函数按模块限定 FQN

顶层函数 FQN 从裸名 `foo` 改为 `模块名.foo`（模块名 = 文件名去扩展名），并合成
`MODULE` 容器保证入库；同模块内的裸名调用改写为 `模块.foo`，导入函数保持裸名
（不做 import 解析，避免错误归属）。这修掉了同名跨文件函数在索引中互相覆盖的问题。

代价是严格 resolution 下降：lytd ts regex 可解析边 109 → 97（calls 385 不变，
rate 0.2831 → 0.2519）。原因是被合并的重复裸名被拆开——例如 5 条 `add` 原先都落到
同一个 `add` 键上算作可解析，现在分属不同模块，跨模块的裸名调用在无 import 信息时
如实判为不可解析。这是消歧后的真实数字，不回退、不用旧口径掩盖。

### 新增「仓内可解析边」口径（与旧 resolution 并排）

旧 `resolution_rate = resolved / 全部调用边` 把 `fmt.Println`、`expect(...)` 这类
仓外 API 也算进分母，无法区分「引擎解析不出来」和「本来就没有仓内定义」。
新增并排口径（字段 `repo_resolvable` / `repo_resolvable_resolved` / `repo_resolution_rate`）：

- **分母**：callee 在索引中存在候选的边。候选判定：精确 FQN、逐段去前缀后的后缀
  （`config.Watcher.Reload` → `Watcher.Reload`）、或全局唯一的短名回退。
- **分子**：这些边中按旧严格 `resolve` 成功解析的数量。
- **旧口径的 `calls` / `resolved_calls` / `resolution_rate` 定义与分母完全不变。**

| 仓库 | 变体 | 全部 calls | 全部 resolution | 仓内候选边 | 仓内已解析 | 仓内 resolution |
|---|---|---:|---:|---:|---:|---:|
| code-schema (go) | regex | 15527 | 0.2653 | 4281 | 4120 | 0.9624 |
| code-schema (go) | treesitter | 20119 | 0.2232 | 4495 | 4490 | 0.9989 |
| petclinic (java) | regex | 1262 | 0.1712 | 219 | 216 | 0.9863 |
| petclinic (java) | treesitter | 1609 | 0.1889 | 307 | 304 | 0.9902 |
| lytd (ts) | regex | 385 | 0.2519 | 97 | 97 | 1.0000 |
| lytd (ts) | treesitter | 407 | 0.2236 | 91 | 91 | 1.0000 |

解读：Go regex 有 161 条「仓内确有定义但未解析」的边，是后续真正可攻的引擎缺口；
其余 11246 条属仓外调用。treesitter 变体只剩 5 条仓内缺口。

### 残余漏召（23/24 条）根因更正与清零

此前把 FN 归为「callee 短名多义 + 调用边未抽取」。**该归因错误**：两条调用边
（`svc.FindTestLinks`、`srv.serveStdio`）在 IR 中一直存在，短名在库内也唯一；
真正缺失的是**被调方法本身**——Go 类型与方法可分局同包不同文件，三后端按
「本文件 ClassFQN → classID」映射入库，未命中即丢弃。修复见 R5-12；两变体 FN 均清零。

### 顺带发现：Go 生产文件中的 `Test*` 类型误判为测试（R5-13）

`isTestClass` 只按名称前缀判定，`TestLink`/`TestResult`/`TestBatchItem` 让整个生产文件
（`testlink.go`、`service.go`）被当成测试文件，文件内**生产方法**因此出现在
`call_graph` 的「测试方法」结果里（Go regex 4 条 FP）。现 Go 仅在 `*_test.go` 内做名称判定。

### 必须披露的口径耦合（自动真值）

`call_graph` 策略（R5-11）与自动基准真值**同为直接调用边**：真值来自 IR 直接解析，
策略结果来自「扫描 → 入库 → 反查」链路。因此接近饱和的数字衡量的是**链路完整性
与 FQN 归一**，不再证明某种独立启发式的判别力。这正是下面引入人工真值的原因。

## 人工真值（独立来源，S-2h）

数据集：`internal/releasebench/testdata/manual-truth.json`。真值由**逐个阅读测试方法体**
手工标注（Go 直接调用；Java 为 MockMvc 触发的 controller 方法），**不来自 IR 调用边**。
IR 只用于枚举「标注测试文件内的方法」，作为评估范围（闭合世界）：

- 范围内的误报计 FP；范围外的预测**忽略**（未标注文件不参与分母）；
- 范围内漏掉的标注测试计 FN；
- 评估前自检：生产方法必须存在于方法索引、真值测试必须位于标注文件内，防止标注漂移。

规模：2 个仓库、14 个生产方法、45 条人工 pair（Go 27 / Java 18）。
Go 由 28 降为 27：`TestFindTestLinks_Coverage` 已随测试删除，数据集同步移除该失效引用，
本次只修数据有效性，判分口径未变。命令：

```bash
release-bench --repo <repo> --lang <lang> \
  --manual-truth internal/releasebench/testdata/manual-truth.json --out <dir>
```

| 仓库 | 变体 | 真值 pair | TP | FP | FN | Precision | Recall | F1 |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| code-schema (go) | regex | 27 | 27 | 0 | 0 | 1.0000 | 1.0000 | 1.0000 |
| code-schema (go) | treesitter | 27 | 27 | 0 | 0 | 1.0000 | 1.0000 | 1.0000 |
| petclinic (java) | regex | 18 | 18 | 0 | 0 | 1.0000 | 1.0000 | 1.0000 |
| petclinic (java) | treesitter | 18 | 18 | 0 | 0 | 1.0000 | 1.0000 | 1.0000 |

> 同目录 `final-*-go.json` / `manual-pre184-go.json` 为修复前的归档原始数据，
> 其中的 28 条 pair 含已删除的 `TestFindTestLinks_Coverage`，仅作历史证据，不再作为当前基线。

**这套真值确实能挡住回归**：在 R5-12 修复前（临时移除 `EnsureMethodOwners`）用同一数据集
实测 Go regex 为 **TP 6 / FN 22 / Recall 0.2143 / F1 0.3530**，缺口精确落在
`Service.FindTestLinks`（17 条）与 `MCPServer.serveStdio`（5 条）——而自动基准把这两条
目标方法从真值里一起“消失”，反而显示高 P/R。原始数据见 `manual-pre184-go.json`。

### 两套口径的差异（不可混用）

| 维度 | 自动真值（`test_link`） | 人工真值（`manual_truth`） |
|---|---|---|
| 来源 | IR 直接调用边 | 人工阅读测试体 |
| 覆盖 | 全仓全部可解析 pair（采样评估 200 个生产方法） | 14 个生产方法、2 个测试文件（闭合标注） |
| 范围外预测 | 计 FP | 忽略（未标注文件不参与） |
| 主要用途 | 发现链路/FQN 断裂（如 R5-12），衡量规模与吞吐 | 验证判别语义是否正确，防止「自己印证自己」 |
| 已知盲区 | 与 `call_graph` 同源；IR 解析不到的合法调用会**漏入真值**，把正确预测误判为 FP | 规模小、标注成本高；只覆盖选定方法 |

> 自动口径中剩下的 FP 多为命名策略命中「IR 未解析到的真实调用」（如
> `BenchmarkCollection_Query_* → Collection.Query`），按人工语义并非误报——
> 这也是两套口径需要并排展示、不能互相替代的原因。


## 调用图

| 仓库 | 变体 | calls（前→后） | resolution（前→后） |
|---|---|---|---:|
| code-schema | regex | 16473 → 16593 | 0.2131 → 0.2130 |
| code-schema | treesitter | 18695 → 18839 | 0.2049 → 0.2048 |
| petclinic | regex | 1365 → 1365 | 0.1487 → 0.1487 |
| petclinic | treesitter | 1609 → 1609 | 0.1790 → 0.1790 |
| lytd | regex | 848 → 848 | 0.0425 → 0.0425 |
| lytd | treesitter | 407 → 407 | 0.1425 → 0.1425 |

> 本仓 calls 的增量来自本轮新增的评估器源码（语料变大），分辨率基本不变；
> 仍 < 0.22，跨类 receiver 消歧是后续条目。

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
7. **跨文件接收者方法（已修，R5-12 / S-2f）。** Go 类型与方法可分局同包不同文件；
   三后端按「本文件 ClassFQN」映射会静默丢弃方法。scanner 入库前补最小归属类，
   `Service.FindTestLinks` / `MCPServer.serveStdio` 从「0 条、不可查」变为可查；
   自动基准 FN 23/24 → 0，人工真值修复前 R 0.2143 → 1.0。
8. **Go `Test*` 生产类型误判（已修，R5-13 / S-2g）。** 名称前缀判定让
   `TestLink`/`TestResult` 所在的生产文件整体被当作测试文件；现 Go 仅在 `*_test.go`
   内做名称判定，生产方法的调用不再出现在「测试方法」结果里。
9. **人工真值（新增，S-2h）。** 与 `call_graph` 不同源的 14 个生产方法 / 46 条 pair，
   独立给出 P/R/F1（本轮 1.0），并已被证明能捕获 R5-12 这类「自动真值一起消失」的回归。
10. **仍偏低/未覆盖（不静默删除）**：TS 的 Jest `it/test` 回调仍不在本轮范围（真值 0）；
    调用图分辨率 < 0.22，跨类 receiver 消歧不足；自动口径残余 FP 主要是
    「IR 未解析到的真实调用被命名策略命中」，需继续缩小与人工语义的差距；人工真值规模
    仍小（14 个方法），后续按清单扩充。
