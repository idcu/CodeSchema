# 公开基准（S-2）· 2026-11-14

> 最后更新：2026-11-14

首轮公开基准：在 3 个真实仓库上测「测试关联 P/R/F1」与「调用图结构指标」。
**只报告、不设 CI 阈值**（roadmap R5-4）；原始数据见同目录 `before-*.json`（S-2 首轮）、
`baseline-r59-*.json`（R5-9 口径修正后、召回修复前）、`after-*.json`（R5-11 后）、
`final-*.json`（R5-12/R5-13 后的历史归档，人工真值仍为 45 对）、`post-*.json`（S-2h 终态，
人工真值 207 对 + 解析/链接修复后）、`manual-pre184-go.json`（人工真值的修复前对照）、
`terminal-*.json`（S-2i 收尾：人工真值 321 对，Go 错例清零 + Java 扩至 152 对后）、
`precision-*.json`（S-2k：TS 方法过匹配收敛 + Go 命名 FP 清零，人工真值 358 对）。

## 口径

- **真值独立于被测启发式**：以 IR 中「测试方法 → 生产方法」的**直接调用边**作为真值，
  不使用命名约定构造真值（否则等于用命名约定评估命名约定）。
- **确定性**：文件顺序固定排序、pair 排序固定、等距 stride 采样；同输入两次运行结果一致
  （`TestRun_MultiLanguageFixtures` 断言除时间戳外 DeepEqual）。
- **指标**：对真值中的每个生产方法调用 `FindTestLinks`，把返回的 `TestMethod` 与真值集合比对，
  微平均 TP/FP/FN 与 Precision/Recall/F1；`PredictedLinks` 为按测试方法去重后的链接数，
  0 表示策略链**完全无输出**（区别于「有输出但 FQN 口径不一致」）。
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

### Caller 归属：Go 原始字符串内容不再创建假方法

反引号原始字符串里的 Go 代码不是源码。旧实现只在 `clean` 后做调用检测，方法解析却用
未清洗的行：串内 `func Ghost() { Fake() }` 被登记为方法，串内 `}` 又提前结束真实方法，
导致串后调用的 caller 变成空（本仓 301 条空 caller 中约 170 条属此类）。

修复：Go 分支中 `clean` 后整行为空的源码行直接跳过方法/类/调用解析。

同语料（含本轮新增单测，calls +71）：code-schema go regex calls 15527 → 15598，
caller 空 301 → 131，resolution 4120/0.2653 → 4139/0.2654，仓内 4300/4139 = 0.9626。
剩余 131 条主要是包级 `var` 初始化（`errors.New`/`regexp.MustCompile`）与接口方法签名，
本身没有方法级 caller；如需归属应引入合成 `<pkg init>` 节点，本轮不做。

### Caller 归属：Java 注解不再计为调用

`@Table(name = "owners")` / `@Column(...)` 这类类/字段注解被 `callPattern` 记成调用，
caller 为空（petclinic 60 条）。注解参数是编译期元数据，不进入调用图；非 Go 语言
以 `@` 开头的行直接跳过调用检测。

同语料 petclinic java regex：calls 1262 → 1184，caller 为空 60 → 9，
resolution 216/0.1712 → 216/0.1824（分母缩小，分子不变）；仓内口径 219/216 不变。
剩余 9 条为字段初始化等模块级语句，无可归属方法。

### Caller 归属：TS 声明式构造器不再计为调用

lytd 的 `.d.ts` 中 `constructor(port: number);` 因无返回类型注解漏过方法模式，
被 `callPattern` 记成 callee=`constructor`、caller 为空的调用边（36 条）。
构造调用在 JS/TS 必须带 `new`，裸 `constructor(...)` 只能是声明，故按关键字过滤。

同语料 lytd ts regex：calls 385 → 349，caller 为空 241 → 205。
resolution 97/0.2519 → 61/0.1748：这 36 条假边此前恰好命中索引里唯一的
`constructor` 方法，属**假解析**，删除后旧口径如实下降；仓内口径仍为 61/61 = 1.0000。
剩余 205 条空 caller 是 Jest `describe/it` 回调（`expect/toBe` 等）与模块级语句，
本身没有可归属的方法，不做伪造归属。

### Caller 归属：Go 接口方法签名不再计为调用

`type Store interface { Open(ctx context.Context) error; ... }` 的每个签名行被
`callPattern` 记为调用边，caller 恒为空（本仓 106 条，集中在 `store.go` 等接口文件）。
接口体是类型契约而非可执行代码，按花括号深度跟踪接口体并跳过调用检测。

同语料（同一诊断命令）Go regex：calls 15597 → 15497，caller 为空 131 → 25。
剩余 25 条为包级 `var` 初始化（`errors.New` / `regexp.MustCompile` / `template.Must`）
与 Go 类型转换，无可归属方法，不做伪造归属。

### Caller 归属：`.tsx` 改用 TSX grammar（treesitter 变体）

`.tsx` 之前复用纯 TypeScript grammar，JSX 之后的声明被吞进 ERROR 节点，同一文件的
后续组件不再登记（lytd `App.tsx` 25 条空 caller）。现在按扩展名选择 `tsx` grammar，
`IRDocument.Language` 仍为 `ts`，下游语言过滤口径不变。

同语料 lytd ts：calls 376 → 382、caller 为空 270 → 245，`App.tsx` 从空 caller
前 8 文件消失（25 → 0）。剩余 245 条集中在 `__tests__` 的 `describe/it` 匿名回调。

### Caller 归属：TS/JS 具名箭头函数入索引（treesitter 变体）

AST 路径只识别 `function_declaration` / `method_definition`，`const f = () => {...}` 与
`const f = function () {...}` 体内的调用没有 caller。现在把初始化为函数表达式的
`variable_declarator` 登记为方法，子树遍历结束恢复外层作用域。

lytd 语料同命令前后均为 calls 376、caller 为空 270 —— 该仓的具名箭头集中在 `.tsx`，
而当前只 vendored 非 TSX 的 TypeScript grammar，解析失败先于本修复暴露。
修复对 `.ts/.js` 生效，由合成用例 `TestASTAdapter_TSNamedArrowFunctionCaller` 守护。

### Caller 归属：Go 指针类型转换不再计为调用（treesitter 变体）

AST 路径把 `var _ Embedder = (*ONNXEmbedder)(nil)` 的 `(*ONNXEmbedder)` 当作
function 节点，产出 callee=`vector.(*ONNXEmbedder)`、caller 为空的假调用边（本仓 6 条）。
括号包裹且以 `(*` 开头的 function 一律按类型转换跳过；`(f)(x)` 间接调用不受影响。

同语料（同一诊断命令，含新增测试代码 calls +9）：calls 20183 → 20192，
caller 为空 36 → 30，净移除 6 条假边。剩余 30 条为包级 `var` 初始化。

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

解读：Go regex 有 161 条「仓内确有定义但严格口径未解析」的边。**后续归因更正**：
这 161 条全部是 callee 多了包/外层类型前缀（`pkg.Type.Method` vs 索引 `Type.Method`），
生产 `resolveCallee` 的逐级后缀规则可链接，并不是「可攻的引擎缺口」；当时未拆分，
按缺口叙述属误读。其余 11246 条属仓外调用。treesitter 变体只剩 5 条仓内未解析。

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

## 人工真值（独立来源，S-2h/S-2i/S-2k；2026-11-14 扩至 358 对）

数据集：`internal/releasebench/testdata/manual-truth.json`（`version: 2`）。
真值由**逐个阅读测试方法体**手工标注，**不来自 IR 调用边**，也不使用
`call_graph` 策略的中间结果。IR 只用于枚举「标注测试文件内的方法」，作为评估范围：

- 范围内的误报计 FP；范围外的预测**忽略**（未标注文件不参与分母）；
- 范围内漏掉的标注测试计 FN；
- 评估前自检：生产方法必须存在于方法索引、真值测试必须位于标注文件内，防止标注漂移。

### 标注规则（v2）

1. **候选生成（纯文本）**：对标注测试文件的每个函数/方法体按花括号配对截取，再用
   `receiver.Method(` / `Method(` 正则抽候选；随后逐条阅读测试体与生产方法签名，
   确认接收者类型。候选生成阶段不读索引、不读 IR 调用边。
2. **纳入**：接收者类型可由声明（结构体字段、局部变量、构造实参）静态确定的直接调用；
   Java 为 MockMvc 触发、且测试体内显式引用被测 controller/handler 的调用，以及标注测试类内
   接收者类型可静态确定的直接调用（validator/formatter/实体与继承访问器，含被 `@Test`
   调用的测试内 helper）。
3. **排除**：接口字段/容器动态派发（`st := store.NewStore("file")` 后的 `st.*`、
   `svc.store.*`、Spring Data repository 接口、`@Mock` 字段）、mock 实现、反射/事件回调、
   只由字符串或注解间接触发的行为。
4. **FQN 口径**：Go 生产方法 `Type.Method`、包级函数 `pkg.Func`，测试 `pkg.TestX`；
   Java 生产 `Class.method`，测试方法（含嵌套类）统一归最外层类。
5. **闭合世界**：只评测标注文件内的方法集合；范围外预测忽略，避免未标注文件稀释 FP。
6. **规模守护**：`TestManualDatasetValid` 要求 `version == 2` 且 `pairs >= 200`，
   防止数据集回退到不足统计意义的规模。
7. **置信说明**：比例指标给出 **Wilson 95% 置信区间**（二项分布，不对称近似）；
   358 对样本下 Go recall（173 对）的区间半宽约 ±2.2pp，Java 152 对约 ±3.5pp。
8. **提交隔离**：标注文件（`manual-truth.json`）的变更必须与解析/链接代码修复分开提交，
   Body 说明新增/删除的 pair 与依据；禁止为让测试通过而删难例或放宽纳入标准。
   判据本身也要分层：负控单测证明行为，人工真值衡量召回，两者不得互相替代。

规模：3 个仓库、**358 条人工 pair（Go 173 / Java 152 / TS 33）**；Go 标注
`internal/store/filestore_test.go`、`internal/service/testlink_test.go`、
`internal/service/service_test.go`，Java 标注 9 个测试类，TS 标注 lytd 的 7 个 Jest
测试文件。命令：

```bash
release-bench --repo <repo> --lang <lang> \
  --manual-truth internal/releasebench/testdata/manual-truth.json --out <dir>
```

### 扩展前后（同命令、同口径）

扩展前为 45 对（Go 27 / Java 18），扩展后为 207 对。扩展过程中发现并修复了
4 个引擎缺陷（Java 行内注解方法、包限定 callee 解析、Go 跨行调用、AST Go 局部变量
类型），另修复 2 个 treesitter 一致性问题（Java 嵌套类 FQN、`new X().m()` 提取）。

| 仓库 | 变体 | 规模 | TP | FP | FN | Precision | Recall | F1 |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| code-schema (go) | regex | 27（v1） | 27 | 0 | 0 | 1.0000 | 1.0000 | 1.0000 |
| code-schema (go) | regex | 167（v2） | 160 | 9 | 7 | 0.9467 | 0.9581 | 0.9524 |
| code-schema (go) | treesitter | 27（v1） | 27 | 0 | 0 | 1.0000 | 1.0000 | 1.0000 |
| code-schema (go) | treesitter | 167（v2） | 161 | 9 | 6 | 0.9471 | 0.9641 | 0.9555 |
| petclinic (java) | regex | 18（v1） | 18 | 0 | 0 | 1.0000 | 1.0000 | 1.0000 |
| petclinic (java) | regex | 40（v2） | 40 | 0 | 0 | 1.0000 | 1.0000 | 1.0000 |
| petclinic (java) | treesitter | 18（v1） | 18 | 0 | 0 | 1.0000 | 1.0000 | 1.0000 |
| petclinic (java) | treesitter | 40（v2） | 40 | 0 | 0 | 1.0000 | 1.0000 | 1.0000 |

Wilson 95% 置信区间（v2）：

| 仓库 | 变体 | Precision（95% CI） | Recall（95% CI） |
|---|---|---|---|
| code-schema (go) | regex | 0.9467（0.9019–0.9717） | 0.9581（0.9160–0.9796） |
| code-schema (go) | treesitter | 0.9471（0.9025–0.9719） | 0.9641（0.9238–0.9834） |
| petclinic (java) | regex / treesitter | 1.0000（0.9124–1.0000） | 1.0000（0.9124–1.0000） |

> v1 的 1.0000 是 27/18 对小样本下的饱和，不能外推。扩展后 Go 的 9 条 FP 集中在
> 命名策略对 `Service.Search`（9 条）的过匹配；7 条 FN 为接收者类型需跨语句推导
> （`svc := newTestService(t)`）与嵌套调用（`TypeOf(NewService(...))`）抽取不到，
> 已在数据集 `note` 中逐条记录。Java 40 对仍全中。

### 收尾轮（S-2i）：Go 错例清零 + Java 扩至 152 对

Go 的 16 条错例按根因拆成三笔修复（先红后绿、一条一提交），Java 从 4 个测试类扩到 9 个。

| 提交 | 根因 | 修复 | 阶段性结果 |
|---|---|---|---|
| `8645db6` | Go 接收者类型跨语句/跨文件不可推导（`svc := newTestService(t)`） | 预扫描同包函数返回类型 + `trackGoLocalTypes` | regex FN 7→3，ts FN 6→2 |
| `1aec628` | `callPattern` 非重叠匹配吞掉内层/链式调用 | `detectGoNestedCalls` + AST `astGoChainedCallee` | regex/ts FN 3/2→0；calls 16419→17592 |
| `c5069b1` | `naming` 对 `TestSearchByTags_*` 公共词过匹配 | `hasCompetingCall`：已有同类调用证据时命名让位 | FP 9→0；169/169，F1 1.0 |
| 本轮 | Java 实参内层调用被外层吞掉（`assertThat(owner.getId())`） | `detectJavaNestedCalls`（`(` / `,` 两种位置） | Java regex TP 106→145，FN 20→7 |
| 本轮 | Java 真值仅 4 个测试类、40 对 | 扩到 9 个测试类、152 对 | 召回 CI 半宽 ±9pp→±3.5pp |

真值扩容后、引擎修复前的中间态（用于证明扩容确实暴露缺陷，而不是先修后测）：

| 仓库 | 变体 | 规模 | TP | FP | FN | P | R | F1 |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| petclinic (java) | regex | 152 | 106 | 24 | 20 | 0.8154 | 0.8413 | 0.8281 |
| petclinic (java) | treesitter | 152 | 119 | 24 | 7 | 0.8322 | 0.9444 | 0.8848 |

> 24 条 FP 全部是标注遗漏的测试内 helper（`george()`/`setup()`/`init()`/`helen()`/`james()`/
> `makePetTypes()`），补齐真值后清零——与 Go 把 `*_test.go` 内 helper 计入的口径一致，
> **属于扩大真值而非收窄标注**。regex 的 20→7 来自实参内层调用抽取；ts 本就能看到内层调用，
> FN 一直是 7。剩余 7 条 FN 为 `Owner.getPet`（3 重载）与 `Owner.addVisit`/`Pet.addVisit`
> 的同名歧义，短名解析无法区分，按已知限制保留并在下方结论中记录。

终态（`terminal-*.json`）：

| 仓库 | 变体 | 规模 | TP | FP | FN | P | R | F1 |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| code-schema (go) | regex | 169 | 169 | 0 | 0 | 1.0000 | 1.0000 | 1.0000 |
| code-schema (go) | treesitter | 169 | 169 | 0 | 0 | 1.0000 | 1.0000 | 1.0000 |
| petclinic (java) | regex | 152 | 145 | 0 | 7 | 1.0000 | 0.9539 | 0.9764 |
| petclinic (java) | treesitter | 152 | 145 | 0 | 7 | 1.0000 | 0.9539 | 0.9764 |

Wilson 95% CI（终态）：

| 仓库 | 变体 | Precision（95% CI） | Recall（95% CI） |
|---|---|---|---|
| code-schema (go) | regex / treesitter | 1.0000（0.9778–1.0000） | 1.0000（0.9778–1.0000） |
| petclinic (java) | regex / treesitter | 1.0000（0.9742–1.0000） | 0.9539（0.9080–0.9775） |

**这套真值确实能挡住回归**：在 R5-12 修复前（临时移除 `EnsureMethodOwners`）用同一数据集
实测 Go regex 为 **TP 6 / FN 22 / Recall 0.2143 / F1 0.3530**，缺口精确落在
`Service.FindTestLinks`（17 条）与 `MCPServer.serveStdio`（5 条）——而自动基准把这两条
目标方法从真值里一起“消失”，反而显示高 P/R。原始数据见 `manual-pre184-go.json`。

### S-2j：Java 重载/同名消歧 + TS Jest 回调建模

#### Java：重载不再算短名歧义，接收者按可见类型限定

Java 方法 FQN 不含参数，`Owner.getPet` 的三重载在方法表中是**同一 FQN 的多条登记**；
测试关联的短名索引直接 append，`len != 1` 被误判为歧义而放弃链接（人工真值 6 条 FN）。
现短名索引按 FQN 去重后再判唯一性。第二处 FN 是 `owner6.addVisit(...)` 与
`pet.addVisit(...)` 跨类同名：两条路径都按方法参数、局部变量声明、增强 for 变量累积
`名字 → 类型`，把接收者限定为 `Owner.addVisit` / `Pet.addVisit`；泛型/变长/数组归一化
（`Page<Owner>` → `Page`）。Java 方法同时回填 `MethodIR.Params`，为重载按实参个数匹配
提供 IR 依据。

| 仓库 | 变体 | 规模 | TP | FP | FN | P | R | F1 | calls | 仓内候选/已解析/前缀差异 | 仓内 resolution |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| petclinic (java) | regex | 152 | 152 | 0 | 0 | 1.0000 | 1.0000 | 1.0000 | 1544 | 287 / 284 / 3 | 0.9895 |
| petclinic (java) | treesitter | 152 | 152 | 0 | 0 | 1.0000 | 1.0000 | 1.0000 | 1609 | 311 / 308 / 3 | 0.9904 |

修复前两变体均为 145/0/7、F1 0.9764。没有删例或改标注；7 条 FN 全部转为 TP。

#### TS：Jest 回调的两种建模方案对比

| 维度 | A. 回调建模为方法（选定） | B. 文件级关联 |
|---|---|---|
| 机制 | `describe/it/test` 回调合成 `MethodIR`，回调体内调用带 `CallerFQN` | 不改 IR，扫描测试文件后直接产出「测试文件 → 生产方法」关联 |
| 粒度 | 方法级，可定位到单条 `it/test` | 文件级，同一文件内所有用例共享一组生产方法 |
| 复用 | 直接复用 `call_graph` 策略、`FindTestLinks`、影响面/context 管道 | 需要新增策略与数据结构，绕过既有 IR 与置信度体系 |
| 自动真值 | IR 直接调用边即可构造，与 Go/Java 同口径 | 真值需另定口径，无法与其它语言并排比较 |
| 精度风险 | 合成 FQN 命名需稳定约定；非 Jest 框架需扩展识别 | 一个文件覆盖多个被测方法时 FP 高，无法区分用例 |
| 解析成本 | 两条解析路径各加回调作用域跟踪 | 解析器零改动，但关联语义弱 |
| 结论 | **采用**：一次投入换来可度量、可复用、与其它语言同口径 | 不采用：指标不可与现有基准并列，收益上限低 |

落地范围：`describe` 路径 + `it/test` 标题清洗为方法名（映射规则见
[解析适配器](../../01-开发者/解析适配器.md)）；正则路径按花括号净深度跟踪回调体，AST 路径按
`call_expression` 子树作用域恢复；测试文件相对路径具名导入按模块 basename 限定
（`../src/utils/math.ts` → `math.add`）；TS 关键字过滤改用本语言控制流表，修掉
`repeat(...)` 被全局 `isKeyword` 误杀的问题。

重建的 TS 人工真值（独立阅读 7 个测试文件的用例体，语料 `/Volumes/Data/lytd` @ `f5e6604`）：

| 仓库 | 变体 | 测试方法 | 真值 pair | TP | FP | FN | P | R | F1 | calls | 仓内候选/已解析 | 仓内 resolution |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| lytd (ts) | regex | 33 | 33 | 33 | 0 | 0 | 1.0000 | 1.0000 | 1.0000 | 408 | 109 / 109 | 1.0000 |
| lytd (ts) | treesitter | 33 | 33 | 33 | 0 | 0 | 1.0000 | 1.0000 | 1.0000 | 369 | 88 / 88 | 1.0000 |

修复前两变体均为「测试方法 0、真值 pair 0、PredictedLinks 0」——该能力在 TS 上不存在。
现在自动真值与人工真值都是 33 对且全部命中；这是**首次可度量**，不是召回突然变好。
需披露：正则路径的生产方法数（859）显著高于 AST（75），是既有 TS 方法模式过度匹配，
与本轮测试关联无关，两行的调用图分母不可直接横比。该过匹配已在 S-2k 收敛到 67
（`.d.ts` 与 `interface` 成员不入方法/调用边），收敛后测试关联仍为 33/33、无召回损失。

### 两套口径的差异（不可混用）

| 维度 | 自动真值（`test_link`） | 人工真值（`manual_truth`） |
|---|---|---|
| 来源 | IR 直接调用边 | 人工阅读测试体（纯文本候选 + 人工确认） |
| 覆盖 | 全仓全部可解析 pair（采样评估 200 个生产方法） | 173（Go）+ 152（Java）+ 33（TS）条 pair、19 个标注测试文件/类 |
| 范围外预测 | 计 FP | 忽略（未标注文件不参与） |
| 主要用途 | 发现链路/FQN 断裂（如 R5-12），衡量规模与吞吐 | 验证判别语义是否正确，防止「自己印证自己」 |
| 已知盲区 | 与 `call_graph` 同源；IR 解析不到的合法调用会**漏入真值**，把正确预测误判为 FP | 只覆盖选定方法；接口派发类调用按规则不计入 |

> 自动口径中剩下的 FP 多为命名策略命中「IR 未解析到的真实调用」（如
> `BenchmarkCollection_Query_* → Collection.Query`），按人工语义并非误报——
> 这也是两套口径需要并排展示、不能互相替代的原因。



## S-2k：方法解析精度收敛（TS 过匹配 + Go 命名 FP）

### TS：859 → 67，多出来的 792 条都是什么

正则路径把 `.d.ts` 声明文件当成生产源码解析：签名行既登记为方法，又被 `callPattern`
读成调用边。逐条量化 859 条的形态（语料 lytd @ `f5e6604`）：

| 形态 | 条数 | 是否有可执行体 | 处理 |
|---|---:|---|---|
| `.d.ts` 顶层函数签名（单行 `;`） | 404 | 否 | `ce46f2b` 跳过 |
| `.d.ts` 顶层函数签名（多行返回类型） | 89 | 否 | `ce46f2b` 跳过 |
| `.d.ts` 接口/类型成员（单行 `;`） | 295 | 否 | `ce46f2b` 跳过 |
| `.d.ts` 接口/类型成员（多行） | 1 | 否 | `ce46f2b` 跳过 |
| 真实源码顶层函数 | 61 | 是 | 保留 |
| 真实源码类/对象字面量方法 | 6 | 是 | 保留 |
| 真实源码 `interface` 成员 | 3 | 否 | `b93e367` 跳过 |
| **合计** | **859** | | **收敛后 67** |

AST 的 75 = 真实源码 67 + `.tsx` 具名箭头属性 3 + `.jsx` 文件方法 5。也就是说，
十倍差异几乎全部来自声明文件，真实源码上两变体只差 8 条（正则少的是 `.jsx` 与
`.tsx` 箭头函数，属 AST 更强项）。逐笔收敛：

| 提交 | 变体 | 生产方法 | calls | 已解析 | 全量 resolution | 假调用边 | test_link / 人工真值 |
|---|---|---:|---:|---:|---:|---:|---|
| `ce46f2b` 前 | regex | 859 | 408 | 109 | 0.2672 | 36（`.d.ts`） | 33/0/0、33/0/0 |
| `ce46f2b` 后 | regex | 70 | 372 | 89 | 0.2392 | 0 | 33/0/0、33/0/0 |
| `b93e367` 后 | regex | 67 | 372 | 89 | 0.2392 | 0 | 33/0/0、33/0/0 |
| `ce46f2b` 前 | treesitter | 75 | 369 | 88 | 0.2385 | 1（`import`） | 33/0/0、33/0/0 |
| `ce46f2b` 后 | treesitter | 75 | 368 | 88 | 0.2391 | 0 | 33/0/0、33/0/0 |

- calls/resolved 下降是**删假边**：36 条 `.d.ts` 声明被当调用，其中 20 条此前恰好命中
  声明里的同名方法与索引，属假解析。仓内口径两变体仍为 89/89 与 88/88（1.0000）。
- 测试关联零变化：自动真值 33 对、人工真值 33 对，TP/FP/FN 均不变（33/0/0）。
- 正则收敛后仍比 AST 少 8 条真实方法（`.jsx`/`tsx` 箭头），属另一类形态，
  未在本轮强行补齐，避免为对齐数字而放宽正则。

### Go：自动口径 15–16 条 FP 的两类根因

修复前（`b93e367`）全部 FP 都是 `strategy=naming`，分两类：

1. **同包跨类型同名**：Go 类 FQN 无包前缀（`JavaResolver` 而非
   `analyzer.JavaResolver`），`Resolve`/`Search`/`Dim`/`Embed`/`Close`/`AddEdge`
   在同一个包里由多个类型实现。包级测试名里的公共词无法确定接收者，
   命名策略却对每个同名方法都发一条链接。
2. **测试名类型锚定被忽略**：`TestMemoryStore_SearchEmpty` 已写明接收者是
   `MemoryStore`，旧实现仍把它链到 `ChromemStore.Search`；`TestLocalEmbedder_Dim`
   同理链到 `ONNXEmbedder.Dim`。

`fe4dd1d` 的两条规则（先红后绿，负控
`TestFindTestLinks_GoPackageAmbiguousNameNotNaming`）：

- 方法名在同目录（Go 包）内由多个类型实现 → **命名策略放弃**，交给 `call_graph`；
- `Test<Type>_<Method>` / `Example<Type>_<Method>` 锚定类型 → 只允许该类型的方法；
- 无类型锚定时，目标方法名必须在测试名剩余部分开头构成完整词
  （`TestResolveImport` 不再命中 `Resolve`）。

| 变体 | 预测/TP/FP/FN（前→后） | P/R/F1（前→后） | 人工真值（前→后） |
|---|---|---|---|
| regex | 1834 / 1819 / 15 / 0 → 1838 / 1838 / 0 / 0 | 0.9918/1/0.9959 → 1/1/1 | 169/2/0 → 173/0/0 |
| treesitter | 1865 / 1849 / 16 / 0 → 1860 / 1860 / 0 / 0 | 0.9914/1/0.9957 → 1/1/1 | 169/2/0 → 173/0/0 |

人工真值分母从 169 增到 173，是因为本轮新增负控与 `CallGraphOverloadSameFQN`
真实调用了 `NewService`/`FindTestLinks`，按「扩大真值、不删例」原则补入数据集；
不是把 FP 改标注洗掉。

### S-2k 终态（Go/TS 复跑，Java 不受影响）

原始数据 `precision-<变体>-<仓库>.json`：

| 仓库 | 变体 | 生产方法 | calls | 已解析 | 全量 resolution | 仓内候选/已解析/前缀 | 自动测试关联 | 人工真值 |
|---|---|---:|---:|---:|---:|---|---|---|
| code-schema (go) | regex | 1456 | 17930 | 4516 | 0.2519 | 5457 / 4516 / 941 = 0.8276 | 1838/0/0 F1 1.0 | 173/0/0 F1 1.0 |
| code-schema (go) | treesitter | 1456 | 21060 | 4661 | 0.2213 | 5584 / 4661 / 923 = 0.8347 | 1860/0/0 F1 1.0 | 173/0/0 F1 1.0 |
| lytd (ts) | regex | 67 | 372 | 89 | 0.2392 | 89 / 89 / 0 = 1.0000 | 33/0/0 F1 1.0 | 33/0/0 F1 1.0 |
| lytd (ts) | treesitter | 75 | 368 | 88 | 0.2391 | 88 / 88 / 0 = 1.0000 | 33/0/0 F1 1.0 | 33/0/0 F1 1.0 |

> 方法精度收敛后，调用图与测试关联的**召回没有下降**：TS 真值/预测仍是 33/33，
> Go 召回保持 1.0（FN 0），人工真值三仓合计 358 对（Go 173 / Java 152 / TS 33）。

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

## 终态复测（人工真值 321 对 + 本轮修复后）

两变体 × 本仓/petclinic，命令与上面一致；原始数据 `terminal-<变体>-<仓库>.json`。
Go 行在 `cfc9ad6` 上复跑（含本轮新增的 `repo_unresolved_*` 诊断字段；新增方法与其测试断言
使自测语料 calls +7，属自指噪声，不改变任何结论）。Java 行经镜像恢复
`spring-petclinic` @ `500158f7`（50 个 `.java`）后**已在同一口径下复跑**，字段与 Go 同源；
lytd 已恢复至 `f5e6604` 并在 S-2j 后复跑（Jest 回调建模 + 导入限定），三仓并列。

| 仓库 | 变体 | 测试对（真值/采样） | 预测链接 | TP | FP | FN | F1 | calls | resolved | resolution |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| code-schema (go) | regex | 2920 / 195 | 1791 | 1776 | 15 | 13 | 0.9921 | 17689 | 4445 | 0.2513 |
| code-schema (go) | treesitter | 2958 / 198 | 1854 | 1838 | 16 | 13 | 0.9922 | 20752 | 4586 | 0.2210 |
| petclinic (java) | regex | 164 / 164 | 164 | 164 | 0 | 0 | 1.0000 | 1544 | 284 | 0.1839 |
| petclinic (java) | treesitter | 168 / 168 | 168 | 168 | 0 | 0 | 1.0000 | 1609 | 308 | 0.1914 |
| lytd (ts) | regex | 33 / 33 | 33 | 33 | 0 | 0 | 1.0000 | 408 | 109 | 0.2672 |
| lytd (ts) | treesitter | 33 / 33 | 33 | 33 | 0 | 0 | 1.0000 | 369 | 88 | 0.2385 |

仓内可解析边（与全量口径并排，旧分母不变）。新增列 `repo_unresolved_prefixed` 给出
「仓内候选但严格口径未解析、去前缀后能精确命中」的条数；短名多义 = 未解析 − 前缀差异，
按当前候选定义恒为 0（推导值，不单独存储）：

| 仓库 | 变体 | 仓内候选边 | 仓内已解析 | 前缀差异 | 仓内 resolution |
|---|---|---:|---:|---:|---:|
| code-schema (go) | regex | 5381 | 4445 | 936 | 0.8261 |
| code-schema (go) | treesitter | 5504 | 4586 | 918 | 0.8332 |
| petclinic (java) | regex | 287 | 284 | 3 | 0.9895 |
| petclinic (java) | treesitter | 311 | 308 | 3 | 0.9904 |
| lytd (ts) | regex | 109 | 109 | 0 | 1.0000 |
| lytd (ts) | treesitter | 88 | 88 | 0 | 1.0000 |

> Java 两行的 `repo_unresolved_prefixed` 为本轮实测字段值（各 3 条），与 Go 同为
> 「去前缀后精确命中」的 FQN 形态差异；短名多义 = 未解析 − 前缀差异，两变体均为 0。
> Java 的「预测链接」同为本轮去重后的实测值（164/168 = TP+FP）。
> TS 的仓内候选边全部命中；全量分辨率仍低是因为 `expect/toBe` 等外部调用占多数。

> 「预测链接」按测试方法去重后计数（同一测试经多个策略命中只算一条）。此前直接累加
> 各策略原始条数，会随策略聚合顺序在 1902–1935 间波动；TP/FP/FN 与 P/R/F1 不受影响。
> 已在 `predicted += len(pred)` 处修正，连续两次复跑均为 1791/1854，字段可复现；
> Java 复跑同为去重口径（156/160），不再保留任何旧口径原始条数。

人工真值（Go 169 / Java 152，共 321 对）：Go 两变体均 TP 169 / FP 0 / FN 0、F1 1.0000；
Java 两变体均 TP 145 / FP 0 / FN 7、F1 0.9764。Go 侧的后续变化（169 → 173、FP 2 → 0）
见 S-2k 终态表，本节保持 S-2i 快照口径。

> 自动口径 F1 0.9921/0.9922（Go）与 0.9781/0.9786（Java），仍低于人工口径的 Go 1.0：
> 自动真值来自 IR 直接调用边，新增语料（并发用例、Java 断言内的属性访问）会带来
> 与人工语义不一致的 pair；两套口径继续并排展示，不互相替代。调用图 calls 随语料
> 增长（Go regex 16419 → 17689），全量 resolution 稳定在 0.22–0.25。

### 归因：仓内 resolution 0.9626 / 0.9989 → 0.8259 / 0.8331

**结论：不是解析缺陷，也不是语料扩张，而是 `8645db6` 改变 callee FQN 形态后，
「仓内候选」与「严格解析」两条规则的落差被放大。** 分解用同一口径分别固定语料与解析器
（`git worktree` + 独立统计每条边属于 精确命中 / 去前缀精确命中 / 短名唯一）：

| 解析器 × 语料 | calls | 仓内候选 | 已解析 | 前缀差异 | 短名歧义 | 仓内 resolution |
|---|---:|---:|---:|---:|---:|---:|
| 旧（`a8fd6e7`）× 旧语料 | 15532 | 4310 | 4149 | 161 | 0 | 0.9626 |
| 旧 × 当前语料 | 15933 | 4379 | 4218 | 161 | 0 | 0.9632 |
| 新（`cfc9ad6`）× 旧语料 | 17244 | 5241 | 4371 | 870 | 0 | 0.8340 |
| 新 × 当前语料 | 17682 | 5377 | 4441 | 936 | 0 | 0.8259 |

- **语料扩张不是主因**：固定旧解析器只换语料，候选 +69、已解析 +69，仓内 resolution
  0.9626 → 0.9632（微升），前缀差异仍是 161。
- **主因是解析器侧的 FQN 形态变化**：固定旧语料只换解析器，候选 +931、已解析 +222、
  前缀差异 +709。按提交拆分（同一旧语料）：`5265541`（AST 局部变量类型）+143/+134/+9；
  `8645db6`（接收者返回类型推导）**+680/+0/+680**；`1aec628`（内层/链式）+108/+88/+20；
  `c5069b1` 与 Java 修复对 Go 为 0。
- **机制**：`svc := newTestService(t)` 这类接收者此前只产出裸短名 `Method`，短名全局
  多义 → 不构成候选，**根本不进分母**；`8645db6` 推导出类型后产出 `pkg.Type.Method`，
  而索引键是 `Type.Method`，去前缀后精确命中 → 进入分母；但基准的严格 `resolve` 刻意
  不做后缀匹配，于是「候选但未解析」。这批边在生产侧一直能链接
  （`internal/service/testlink.go` 的 `resolveCallee` 自 `37cca60` 起支持逐级去前缀），
  Go 人工真值 169/169 也证明没有漏链。
- **新增测试文件贡献剩余 66 条**：当前语料在旧解析器下 161 → 新解析器 870 → 新解析器 +
  新语料 936，增量 66 来自本轮新增的 PG 并发用例/适配器用例（测试里 `store.FileStore.Close`
  一类调用），形态同上，不是解析错误。
- **目标正确性抽样**：未解析边里频次最高的 callee（如 `treesitter.TreeSitterAdapter.Parse` 69、
  `service.Service.GetContextMode` 59、`analyzer.CallGraph.AddEdge` 26、`lsp.LSPAdapter.*` 43）
  去前缀后的键均为该接收者类型的真实方法；没有发现错绑。
- **处理**：不把 `resolve` 改成后缀匹配——仓内候选本来就按后缀定义，那样会恒等于 1.0，
  指标失去含义。改为在报告中固定给出 `repo_unresolved_prefixed`（未解析中前缀差异的条数），
  短名多义由「未解析 − 前缀差异」推导；严格口径数值保持可比。Go 侧短名歧义实测为 **0**，
  仓内解析缺口全部是 FQN 形态差异。



## 结论与根因

1. **Go 包级测试函数不可见（已修，R5-7 / S-2a）。** 测试函数挂在合成 `PACKAGE` 容器
   （容器名=包名），旧 `discoverTestClasses` 只按 `*Test`/`Test*` 类名发现。现按「同目录同包」
   把 `*_test.go` 的 PACKAGE 容器归属到该目录全部生产类，`TestXxx`/`BenchmarkXxx`/`ExampleXxx`
   均进入候选；`GetAffected` 复用 `FindTestLinks`，同步生效。
2. **Java `*Tests` 后缀（已修，R5-8 / S-2b）。** `OwnerControllerTests` 此前不被 `matchTestClass`
   识别（`isTestClass` 已支持，口径不一致）。现已统一为 `Test` 前后缀 + `Tests`/`Spec` 后缀；
   连带修复 Java 包级可见性方法的正则解析（单 tab 缩进 + 无修饰符），否则默认变体不可观测。
3. **TS 的 Jest `it/test` 回调（S-2j 已覆盖）。** 首轮回调不是 IR 方法，两变体测试方法
   均为 0、真值恒为 0——这是解析缺口而非「没有测试」。S-2j 把回调建模为方法后，
   lytd 两变体测试方法 33、真值 33 对，人工/自动 P/R/F1 均 1.0000；方案取舍见上文对比表。
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
9. **人工真值（S-2h/S-2i/S-2k，v2 已扩展）。** 与 `call_graph` 不同源的 **358 条 pair**
   （Go 173 / Java 152 / TS 33），独立给出 P/R/F1 与 Wilson 95% CI：Go 两变体均
   TP 173 / FP 0 / FN 0、F1 1.0000（CI 0.9783–1.0000）；Java 两变体均
   TP 145 / FP 0 / FN 7、F1 0.9764（Recall CI 0.9080–0.9775）。
   数据集已被证明能捕获 R5-12 这类「自动真值一起消失」的回归，并在本轮
   暴露出 Java 实参内层调用抽取缺口。
10. **Java 的 7 条 FN（S-2j 已清零）。** `Owner.getPet` 三重载与
    `Owner.addVisit`/`Pet.addVisit` 同名导致短名歧义。修复分两笔：短名索引按 FQN 去重
    （重载不是不同目标）；接收者按参数/局部变量/增强 for 的可见类型限定，并回填
    `MethodIR.Params`。Java 人工真值从 145/0/7 变为 152/0/0，两变体 F1 均为 1.0000；
    没有删例或改写标注。
11. **仍偏低/未覆盖（不静默删除）**：调用图全量分辨率 < 0.28，跨类 receiver 消歧不足；
    自动口径 FP 已在三仓清零（Go S-2k、Java S-2j、TS S-2j），剩余偏差转为召回与覆盖面；
    人工真值覆盖已扩到 code-schema、petclinic、lytd 三仓（358 对），但 TS 仅覆盖
    一个仓库且非 Jest 测试框架（Mocha/Vitest 扩展语法）尚未建模。TS 正则与 AST
    仍差 8 条真实方法（`.jsx` 与 `.tsx` 箭头函数），未强行对齐。
12. **仓内 resolution 下滑已归因（口径变化，非缺陷）。** `0.9626/0.9989 → 0.8259/0.8331`
    由 `8645db6` 的接收者类型推导导致：裸短名升级为 `pkg.Type.Method` 后，这些边从
    「短名多义、不进分母」变成「去前缀精确命中、进分母」，而严格 `resolve` 不做后缀匹配。
    固定变量实验：语料维度 +69/+69（resolution 微升至 0.9632），解析器维度 +931/+222
    （其中 709 条前缀差异）。Go 侧前缀差异 936 条、短名歧义 0 条，生产 `resolveCallee`
    全部可链接。报告新增 `repo_unresolved_prefixed` 字段（短名多义由差值推导），
    后续复跑可直接核对，不再依赖一次性脚本。

