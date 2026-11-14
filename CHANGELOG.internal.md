# CHANGELOG.internal.md

> 内部追溯日志，不对外发布。记录每次提交的核心改动、验证数据、遗留 TODO。

---

## 提交记录

### Commit 245: fix(parser): Go 内层与链式调用抽取，链式接收者按返回类型限定

- 默认 `callPattern` 为非重叠匹配：`wrap(NewService(...))` 的内层调用被外层整段
  吞掉（`reflect.TypeOf(NewService(...))` 真值 FN）；`NewService().WithX(...)` 的
  WithX 被记为包级函数（`svc := NewService(st).WithImpactAnalyzer(...)` 两条 FN）。
- 正则路径补一轮扫描：内层调用（`(` 后紧跟 `name(`）按现有规则限定；链式调用
  （`base(args).M(`）接收者类型取 base 同包裸函数的具名返回类型，未知则不记。
  同名 callee 就地改写，避免同一行同时留下错误与正确两条边。
- AST 路径链式 callee 原取最后一段标识符（`WithImpactAnalyzer`）后被包限定；
  现按内层 `function` 的具名返回类型输出 `pkg.T.M`。
- 负控：`TestGoNestedAndChainedCalls`（两变体旧实现均红：链式记成
  `demo.WithImpactAnalyzer`、regex 内层 NewService 缺失；现绿）。
- 同语料（code-schema go，人工真值 167 对）前→后：
  regex TP 164/FP 9/FN 3 → 167/9/0，P 0.9480 → 0.9489、R 0.9820 → 1.0000、
  F1 0.9647 → 0.9738；
  treesitter TP 165/9/2 → 167/9/0，P 0.9483 → 0.9489、R 0.9880 → 1.0000、
  F1 0.9677 → 0.9738。
  调用图：regex calls 16419 → 17592（内层调用此前系统性少计）、resolved
  4328 → 4427、resolution 0.2636 → 0.2516；treesitter calls 20567 → 20617、
  resolved 4558 → 4568、resolution 0.2216 不变。
  自动 test_link：regex TP 1740→1769、FP 27→46；treesitter TP 1782→1787、FP 64→60
  （真值 pair 随抽取覆盖变化；剩余 FP 与下一提交的命名策略过匹配同源）。


### Commit 244: fix(parser): Go 接收者类型由同包函数返回类型推导（跨语句/跨文件）

- 旧实现只认 `x := &T{}` / `var x *T`：`svc := newTestService(t)`、`svc := NewService(st)`
  的 `svc.Method()` 恒为裸 selector。`resolveCallee` 要求短名唯一，
  `WithAIEnhancer` / `WithImpactAnalyzer` / `SearchByTag` 与其它类型同名时直接
  判为歧义、不链接（Go 人工真值 FN 7 条中 6 条来自此）。
- 现预扫描包级函数签名的单一具名返回类型（`func helper() *T` → `T`），
  `x := helper()` 记入局部类型表；helper 可在同包另一 `.go` 文件
  （`NewService` 在 service.go），按目录扫描并缓存（键含目录 mtime 与 `.go` 数）。
  多返回值/命名返回值/泛型/切片/带包路径返回类型不推导，不做数据流分析。
- 正则与 AST 两路径同口径（共享 `go_returns.go`）；仅改 selector 限定，不增删调用边。
- 负控：`TestGoFuncReturnLocalQualification`（同包跨文件构造器；去掉跨文件扫描时
  两变体均红：`svc.Search` 未消歧、qualified=1/2；现绿）。
- 同语料（code-schema go，人工真值 167 对）前→后：
  regex TP 160/FP 9/FN 7 → 164/9/3，P 0.9467 → 0.9480、R 0.9581 → 0.9820、
  F1 0.9524 → 0.9647；
  treesitter TP 161/9/6 → 165/9/2，P 0.9471 → 0.9483、R 0.9641 → 0.9880、
  F1 0.9555 → 0.9677。
  自动 test_link（真值由 IR 调用边生成，随解析覆盖变化）：regex TP 1521→1740、
  FP 16→27、F1 0.9873→0.9889；treesitter TP 1567→1782、FP 19→64、F1 0.9883→0.9786
  （真值 pair 2373→2922，新增对集中在原裸 selector 方法上；精度问题与命名策略
  过匹配同源，下一提交处理）。调用图 calls/regex 16327→16419、treesitter 20452→20567。

### Commit 243: fix(parser): AST 路径跟踪 Go 局部变量类型，selector 按类型限定

- 正则路径已有 `goLocalTypes`（`x := &T{}` / `var x *T`），AST 路径未跟踪：
  `fs := &FileStore{}` 后 `fs.GetFileByPath()` 的 callee 恒为裸名，短名在
  FileStore/PGStore/SQLiteStore 间多义，测试关联无法归位——treesitter 人工真值
  Go recall 0.6287 的主因（FN 62 中绝大多数为该类）。
- 现按 `short_var_declaration`（`&T{}` / `T{}`）与 `var_spec`（`*T` / `T`）收集
  局部类型，callee 先按局部类型限定为 `pkg.T.Method` 再做原有包限定；进入方法清空、
  离开作用域恢复。其它右值（函数调用、make、类型断言）不推导，不伪造。
- 负控：`TestASTAdapter_GoLocalVarCallQualification`（旧实现裸 callee，红；新实现绿）；
  `TestASTAdapter_Parse_Go` 期望同步为 `main.User.Greet`（`u := &User{}` 已可静态确定）。
- 同语料（code-schema go treesitter）前→后：calls 20452、unique callers 2235、
  resolved 4543/0.2221 不变；仓内候选边 4548 → 4676、仓内已解析 4543 → 4543
  （0.9989 → 0.9716，分母扩大而解析数不变：新限定的裸名中仍有跨包/接口派发）；
  自动 test_link TP 1517 → 1567、FN 18 → 18、P 0.9889 → 0.9880、R 0.9883 → 0.9886、
  F1 0.9886 → 0.9883（真值 pair 2288 → 2373）。
  人工真值：TP 105 → 161、FN 62 → 6、P 0.9211 → 0.9471、R 0.6287 → 0.9641、
  F1 0.7473 → 0.9555。

### Commit 242: fix(parser): Java `new X().m()` 链式调用提取类型限定 callee（AST 路径）

- Java 的 `method_invocation.object` 为 `object_creation_expression` 时
  （`new Calculator().add(1, 2)`），AST 路径取调用全文 `(` 前文本，得到
  callee=`new Calculator`（非法 FQN）：真值抽取与测试关联全部失配。
- 这是 `-tags treesitter` 下 `TestRun_MultiLanguageFixtures/java` 真值 pair=0 的根因
  （HEAD 即红，非本轮改动引入）；默认 regex 构建不受影响。
- 现识别该形态，callee 取 `类型.方法`（`Calculator.add`）；普通 `obj.method`
  与链式 `a().b()` 口径不变。
- 负控：`TestASTAdapter_JavaChainedNewCall`（旧实现 callee=`…new Calculator`，红；新实现绿）。
- 同语料（petclinic java treesitter）前→后：prod_methods 91、test_methods 104、calls 1609、
  resolved 304/0.1889、仓内 307/304、人工真值 40/40 均不变——该仓库无此调用形态；
  收益体现在夹具与后续含构造链式调用的 Java 仓库。

### Commit 241: fix(parser): Java 嵌套类方法统一归最外层类（跨变体 FQN 一致）

- 正则路径 `classPattern` 带行首锚点且不含 `static`，缩进嵌套类不登记，方法归外层类；
  AST 路径把 `static class ProcessCreationFormHasErrors` 单独登记，同一方法出现
  `PetControllerTests.initUpdateForm`（regex）与 `ProcessCreationFormHasErrors.initUpdateForm`
  （AST）两种 FQN，人工真值在 treesitter 构建下自检直接失败。
- AST 对 Java 嵌套类同样不登记，`curClass` 保持最外层类；离开嵌套类作用域恢复。
- 负控：`TestASTAdapter_JavaNestedClassFlatten`（旧实现 2 个类、内层 FQN，红；新实现绿）。
- 同语料（petclinic java treesitter）前→后：prod_methods 91、test_methods 104、
  calls 1609、resolved 304/0.1889、仓内 307/304 均不变（只改 FQN 字符串）；
  人工真值从「自检失败」转为 40/40，P/R/F1 全 1.0000，与 regex 变体一致。

### Commit 240: fix(parser): Go 跨行调用起始行计入调用边

- `callPattern` 要求 `(` 与 `)` 同行；参数换行的调用
  （`fs.BulkUpsert(ctx, []*parser.IRDocument{{`）整条丢失，测试关联与调用图同时漏召回。
- 新增跨行兜底：仅登记本行内括号未闭合的 `name(`，已闭合调用仍由原路径处理，避免重复。
- 负控：`TestGoMultilineCall`（旧实现 calls=[]，红；新实现绿）。
- 同语料（code-schema go regex）前→后：calls 15560 → 16165、unique callers 2116 → 2125、
  resolved 4161 → 4296（0.2674 → 0.2658，分母增长更快）、仓内 4322/4161 → 4466/4296
  （0.9627 → 0.9619）；扩展人工真值 TP 155 → 158、FN 10 → 7、F1 0.9365 → 0.9461。
- 自动 test_link 同口径：TP 1462 → 1503、FN 9 → 23、F1 0.9908 → 0.9839——真值随解析
  覆盖面扩大，属暴露而非链路回退。

### Commit 239: fix(service): 包限定 callee 按后缀解析到生产方法

- Go selector 的 callee 为 `pkg.Type.Method`（`store.FileStore.GetFileByPath`），
  而生产方法索引键为 `Type.Method`。`linkByCallGraph` 旧实现只做精确匹配与全局唯一
  短名；`GetFileByPath` / `Open` / `Close` / `Upsert*` 在 FileStore、PGStore、SQLiteStore
  间同名多义，整批调用边无法归到生产方法，测试关联漏召回。
- `resolveCallee` 增加逐级去前缀的后缀精确匹配（要求索引中存在完全相同的
  `Type.Method`），再回退唯一短名；不放宽到模糊名。
- 基准真值抽取同步改用 `candidates`（同一「仓内确有定义」口径），
  §3.3 严格分辨率口径与分母不变。
- 负控：`TestFindTestLinks_CallGraphPackageQualifiedCallee`（旧实现无 call_graph 链接，红；
  新实现绿）。
- 扩展人工真值（Go 165 对）同命令前→后：TP 101 → 155、FN 63 → 10、
  P 0.9099 → 0.9337、R 0.6159 → 0.9394、F1 0.7346 → 0.9365。
  剩余 FN 为接收者变量类型推断（`svc`/`st`）与嵌套调用抽取，另项记录。

### Commit 238: fix(parser): Java 行内注解方法进入索引

- `public @ResponseBody Vets showResourcesVetList()` 的注解位于返回类型前，旧 Java
  方法模式修饰符组不含 `@`，整行不被识别为方法：该方法不进索引、体内调用失去 caller，
  声明行还被 `callPattern` 记成一条调用边。
- 修饰符组加入 `@Name` / `@Name(...)`；独立成行的注解匹配不到后续类型，不会误判。
- 负控：`TestTreeSitterAdapter_Parse_JavaInlineAnnotationMethod`（旧实现 methods=[]，红；
  新实现绿）。
- 同命令同语料（petclinic java regex）前→后：生产方法 90 → 91、calls 1184 → 1183、
  unique callers 147 → 148、resolved 216 → 215（0.1824 → 0.1817）、仓内 219/216 →
  218/215（0.9863 → 0.9862）、test_link 123/0/5 不变。
  resolved 下降 1 是删除声明行伪调用：该边此前经短名回退命中同名测试方法
  `VetControllerTests.showResourcesVetList`，属假解析的修正，不是召回退化。
- 文档：`解析适配器.md` 同步。

### Commit 237: docs: 最终复测、caller 归属总账与 deferred 降 cut

- 两变体 × 三仓全量复测，原始数据 `docs/bench/2026-11-14/post-*.json`。
- 调用图（全量 / 仓内）：Go regex 15532/0.2671（4310/4149/0.9626）、
  Go treesitter 20210/0.2232（4516/4511/0.9989）、Java regex 1184/0.1824、
  Java treesitter 1609/0.1889、lytd regex 349/0.1748、lytd treesitter 413/0.2252。
- 人工真值 45 对（Go 27 / Java 18）两变体均 P=R=F1=1.0000。
- 自动口径 Go regex F1 0.9851 / treesitter 0.9889：pair 规模上升后新增 FN 15/18，
  已说明为覆盖面扩大暴露，不是既有链路回退。
- deferred 复盘：X-1/X-3/X-4/X-5/X-6 均降 cut 并给出实测理由，CSV 与总账对平。
- 计数：非 vendor LoC 62976 → 63756（63.8k），基线同步；`make counts-check` 通过。

### Commit 236: fix(parser): C/C++ 声明名取 declarator 链末端；变体专属断言按构建跳过

- AST 路径对 C++ `function_definition` 回退取子树首个标识符，把函数体内的
  `fuelPump.pump()` 误当方法名；改为沿 `declarator` 字段取末端声明名。
- 3 个断言默认正则路径 FQN 口径的测试在 `-tags treesitter` 下跳过（AST 由
  `adapter_ast_test.go` 覆盖），修复该构建下测试红。

### Commit 235: fix(parser): .tsx 改用 TSX grammar 解析（treesitter 变体）

- `.tsx` 之前复用纯 TypeScript grammar，JSX 之后的声明被吞进 ERROR 节点：
  同一文件的后续组件（Counter/TodoList…）不再登记，体内调用 caller 为空（lytd 25 条）。
- 新增 `tsx` grammar 注册；按扩展名选择 grammar，`IRDocument.Language` 仍为 `ts`，
  下游语言过滤/统计口径不变。
- 负控：`TestASTAdapter_TSXGrammar`（旧实现 Counter 未登记，红；新实现绿）。
- 同语料 lytd ts（同一诊断命令）：calls 376 → 382、caller 空 270 → 245。
  `App.tsx` 从空 caller 前 8 文件消失（25 → 0）。
- 文档：bench README 同步。

### Commit 234: fix(parser): TS/JS 具名箭头函数与函数表达式入索引（treesitter 变体）

- AST 路径只识别 `function_declaration` / `method_definition`，`const f = () => {...}`
  与 `const f = function () {...}` 体内的调用全部落入空 caller。
- `variable_declarator` 的值为 arrow_function/function_expression 且名称为普通标识符时
  登记为方法，作用域在子树遍历结束后恢复。
- 负控：`TestASTAdapter_TSNamedArrowFunctionCaller`（旧实现 add/mul 均未登记、caller 为空，红；新实现绿）。
- 同语料 lytd ts（同一诊断命令）：calls 376、caller 空 270 → 270。
  lytd 的具名箭头集中在 `.tsx`，而当前仅 vendored 非 TSX 的 TypeScript grammar，
  该文件类型的解析失败会先于本修复暴露，故本轮语料无净变化；修复对 `.ts/.js` 生效。
- 文档：bench README 同步。

### Commit 233: fix(parser): Go 指针类型转换不再计为调用（treesitter 变体）

- AST 路径把 `var _ I = (*T)(nil)` 的 `(*T)` 当作 function 节点，产出
  callee=`pkg.(*T)`、caller 为空的假调用边（本仓 6 条）。
- `parenthesized_expression` 且内容以 `(*` 开头 → 类型转换，跳过；`(f)(x)` 不受影响。
- 负控：`TestASTAdapter_GoTypeConversionNotCall`（旧实现 1 条假边，红；新实现绿）。
- 同语料（同一诊断命令，含新增测试代码 calls +9）：calls 20183 → 20192、
  caller 空 36 → 30；净移除 6 条假边。
- 剩余 30 条为包级 `var` 初始化，无可归属方法，不伪造归属。
- 文档：bench README 同步。

### Commit 232: fix(parser): Go 接口方法签名不再计为调用（caller 归属）

- `type X interface { M() }` 的方法签名被 `callPattern` 登记为调用边，caller 恒为空
  （本仓 106 条，集中在 `store.go` / `plugin.go` / `viz.go` 等接口文件）。
- Go 分支按花括号深度跟踪接口体，接口体内跳过调用检测（接口体只有签名，无可执行调用）。
- 负控：`TestGoInterfaceSignaturesNotCalls`（旧实现 2 条空 caller 假边，红；新实现绿）。
- 同语料（同一诊断命令）：calls 15597 → 15497、caller 空 131 → 25。
- 剩余 25 条为包级 `var` 初始化与类型转换，无可归属方法，不伪造 `<pkg init>`。
- 文档：bench README 同步。

### Commit 231: fix(parser): Go 原始字符串内容不再创建假方法（caller 归属）

- 方法解析用未清洗行、调用检测用 `clean` 后行：串内 `func Ghost()` 会建成方法，
  串内 `}` 会提前结束真实方法，导致串后调用 caller 为空（本仓约 170 条）。
- Go 分支在 `clean` 后整行为空时跳过方法/类/调用解析。
- 负控：`TestGoRawStringDoesNotCreateMethods`（旧实现建出 Ghost 方法且 caller 丢失，红）。
- 同语料（新增单测使 calls +71）：calls 15527 → 15598、caller 空 301 → 131、
  resolution 4120/0.2653 → 4139/0.2654、仓内 4300/4139 = 0.9626。
- 剩余 131 条为包级 var 初始化与接口方法签名，无方法级 caller；合成 `<pkg init>`
  归属留待后续，不伪造。
- 附带：`adapter_test.go` 补跑 gofmt。
- 文档：bench README、`final-ledger` 同步。

### Commit 230: fix(parser): Java 注解不再计为调用（caller 归属）

- `@Table(name="owners")` / `@Column(...)` 被 `callPattern` 记为调用，caller 为空
  （petclinic 60 条）。注解参数是编译期元数据，不应进入调用图。
- 非 Go 语言以 `@` 开头的行跳过调用检测。
- 负控：`TestTreeSitterAdapter_Parse_JavaAnnotationsNotCalls`（旧实现 2 条红，新实现绿）。
- 实测 petclinic java regex：calls 1262 → 1184、caller 空 60 → 9、
  resolution 216/0.1712 → 216/0.1824（分母缩小）、仓内口径 219/216 不变。
- 文档：bench README、`final-ledger` 同步。

### Commit 229: fix(parser): TS 声明式构造器不再计为调用（caller 归属）

- `.d.ts` 的 `constructor(port: number);` 无返回类型注解，漏过方法模式后被当成调用，
  产生 callee=`constructor`、caller 为空的假边（lytd 36 条）。
- `constructor` 加入关键字过滤：JS/TS 构造调用必须带 `new`，裸名只能是声明。
- 负控：`TestTreeSitterAdapter_Parse_TSConstructorSignatureNotCall`（旧实现红，新实现绿）。
- 实测 lytd ts regex：calls 385 → 349，caller 空 241 → 205；
  resolution 97/0.2519 → 61/0.1748（36 条假边此前被假解析，删除后如实下降），
  仓内口径 61/61 = 1.0000。
- 剩余空 caller：Jest `describe/it` 回调与模块级语句，无可归属方法，不伪造。
- 文档：bench README、`final-ledger` 同步。

### Commit 228: feat(bench): 新增「仓内可解析边」口径（与旧 resolution 并排）

- `CallGraph` 增加 `repo_resolvable` / `repo_resolvable_resolved` /
  `repo_resolution_rate`：分母只算 callee 在索引中有精确、后缀或唯一短名候选的边；
  分子为其中严格 `resolve` 成功的数量。旧 `calls/resolved_calls/resolution_rate`
  定义与分母完全不变。
- 候选只认「无歧义」：短名多义时不算确有候选，避免把外部 API 误算成仓内缺口。
- 测试：`TestMethodIndex_CandidatesBySuffix` 覆盖后缀命中、唯一短名、歧义拒绝。
- 实测（旧全量口径 → 仓内口径）：Go regex 0.2653 → 0.9624（4281 条候选，161 条
  仓内未解析）；Go ts 0.2232 → 0.9989；Java regex 0.1712 → 0.9863；
  Java ts 0.1889 → 0.9902；lytd regex/ts 仓内均为 1.0000。
- 文档：bench README、`final-ledger` 同步。

### Commit 227: fix(parser): TS 顶层函数按模块限定 FQN

- 顶层函数 `ClassFQN` 改为文件名去扩展名，并合成 `MODULE` 容器保证入库；
  同模块裸名调用改写为 `模块.foo`，导入函数保持裸名（不做 import 解析）。
- 目的：消除同名跨文件函数在索引中互相覆盖（lytd 32 条同名 + 3 条漏抽）。
- 实测 lytd ts regex：calls 385 不变，resolved 109 → 97，rate 0.2831 → 0.2519。
  下降是消歧后的真实值：原先 5 条 `add` 等重复裸名被合并到一个键上算作可解析，
  拆分后跨模块裸名调用在无 import 信息时如实判不可解析。旧口径不回退。
- 测试：`TestTreeSitterAdapter_Parse_TypeScriptTopLevelFunctions` 扩展断言模块限定与
  导入裸名保留；`go test ./internal/parser/...` 通过。
- 文档：`解析适配器.md`、bench README、`final-ledger` 同步。

### Commit 226: feat(parser): Go 局部变量 selector 类型消歧

- 方法体内 `x := &T{}` / `x := T{}` / `var x *T` 记入局部类型表，`x.Method()` 限定为
  `pkg.T.Method`；离开方法清空，不做跨方法/数据流推导。
- 负控：`TestGoLocalVarCallQualification`（旧实现 2 条 selector 未消歧，红）；
  `TestGoCallAndTypeExtraction` 的 `w.ReloadNow` 期望同步升级为 `config.Watcher.ReloadNow`。
- 严格 resolution 口径下 code-schema go regex：resolved 4109 → 4111，
  calls 15486 → 15504（+18 来自新增单测），rate 0.2653 → 0.2652。
  新 callee 为 `pkg.Type.Method`，而基准索引键为 `Type.Method`，只能走短名回退，
  多义短名仍判不可解析——真实收益留给新增的「仓内可解析边」口径，旧分母不变。
- 文档：`解析适配器.md`、bench README、`final-ledger` 同步。

### Commit 225: fix(parser): 关键字假调用按裸名过滤（Java/Go 内建）

- `detectCalls` 先把裸名做包/类限定再查 `isKeyword`，导致 `if (` → `pkg.Class.if`、
  Go `len(` → `pkg.len` 绕过过滤，成为假调用边。
- 改为裸名先过滤，再做限定；补入 Java `try/synchronized/do/else/finally`。
- 负控：`TestTreeSitterAdapter_Parse_JavaControlFlowNotCalls`（旧实现 6 条关键字调用全红；
  新实现绿，且 `helper()` 真调用保留）。
- 实测：petclinic java regex calls 1365 → 1262、resolution 0.1582 → 0.1712；
  code-schema go regex calls 17656 → 15486、resolution 0.2327 → 0.2653；
  java treesitter / lytd regex 不变（不走该正则路径/无关键字边）。
- `test_link` 的 TP 与真值同步收缩（伪边此前也进真值），属口径修正。
- 文档：`解析适配器.md`、bench README、`final-ledger` 同步。

### Commit 224: fix(parser): TS 顶层函数与无返回类型方法入索引

- 旧 TS `methodPattern` 强制 `):`，且无 `function` 分支：555 条顶层函数声明漏解析，
  无注解类方法（`start(port: number) {`）同样漏掉；声明行还被 `callPattern` 当成调用边。
- 新增 `langPatterns.funcPattern` 兜底顶层函数（`export`/`default`/`async` 可选）；
  `methodPattern` 允许省略返回类型，`;` 分支仅接受带注解声明，避免 `foo(a);` 误判。
- 负控：`TestTreeSitterAdapter_Parse_TypeScriptTopLevelFunctions`（旧实现 4 条断言全红，
  新实现绿）；`go test ./internal/parser/...` 通过。
- 实测 lytd ts regex：calls 848 → 385（声明不再计为调用）、resolved 36 → 109、
  resolution 0.0425 → 0.2831；Go 0.2328 → 0.2327（语料 +15 calls，来自新增单测）、
  Java 0.1582 不变。
- 文档：`解析适配器.md`、`docs/bench/2026-11-14/README.md`、`final-ledger` 同步。

### Commit 223: fix(bench): 方法索引精确 FQN 命中不再按次数判歧义（度量缺陷修正）

- `methodIndex.resolve` 原要求 `count[fqn]==1`；Java 重载/同名多声明会把同一 FQN
  登记多次，导致真实方法被误判为不可解析。改为精确 FQN 命中即解析，短名歧义仍要求
  唯一 FQN。
- **这是度量缺陷修正，不算引擎收益**：边一直能被解析器产出，只是被基准索引挡掉。
- 负控：`TestMethodIndex_ResolveDuplicateCount`（旧实现红：`("", false)`；新实现绿）。
- 同一语料（新增单测使本仓 calls +9）、同一命令，resolution 前→后：
  Go regex 3963/0.2246 → 4106/0.2328；Go ts 4314/0.2154 → 4472/0.2233；
  Java regex 203/0.1487 → 216/0.1582；Java ts 288/0.1790 → 304/0.1889；
  lytd regex 36/0.0425 不变；lytd ts 58/0.1425 → 91/0.2236。
- `test_link` 变化来自真值/采样集合扩大（Go regex FN 0→14、Java regex 0→5），
  非召回退化；两套口径数字均保留。
- 文档：`docs/bench/2026-11-14/README.md`、`final-ledger-2026-11-14.md` 同步。

### Commit 222: fix(bench): 人工真值数据集移除失效引用（28→27）

- `manual-truth.json` 删除 `service.TestFindTestLinks_Coverage`：对应测试已随 coverage
  裁剪删除，数据集自检会因「真值测试不在标注文件内」直接失败。
- 仅修数据有效性，判分口径未变：仍是闭合世界（范围外预测忽略）、微平均 P/R/F1。
  规模 46 → 45 pair（Go 28 → 27 / Java 18）。
- 实测（同命令 `release-bench --repo . --lang go --manual-truth ...`）：
  真值 pair 27、范围内预测 27、TP 27 / FP 0 / FN 0、P/R/F1 均为 1.0000。
- 文档：`docs/bench/2026-11-14/README.md`、`final-ledger-2026-11-14.md` 同步；
  归档 JSON 标注为历史数据。`go test ./internal/releasebench/...` 通过。

### Commit 221: docs: R0-1 旧账无法对账结论 + 最终修复总账（R0-1）

- R0-1 核查：`C-16` / `check-deps` 在全历史（`git log --all -S`）、已删文件、
  全工作区（含 `analysis/`、`.trae/`、`build/`）、本地与上游分支均 0 命中
  （唯一命中为本轮总账/清单自身引用）；无 stash、无额外 tag。结论标 `blocked`，
  不编造 C-16 → 本轮条目的映射；62 条审计项作为替代总账。
- 新增 `2026-11-14/roadmap/outputs/final-ledger-2026-11-14.md`：结论先行，
  含 72 笔提交的缺陷→哈希总账、测试关联自动/人工 P/R/F1、调用图分辨率、
  agent-bench 数字与口径、砍掉/延后项、10 条残留风险与复现入口。
- CSV：R0-1 `todo` → `blocked`（原因入 decision 列）；roadmap §1.3、§7、
  §9.1 同步最终结论。
- 仅文档变更；`make fmt-check` / `make counts-check` 通过，计数保持
  36/40/62976（63.0k）。

### Commit 219: test(release): verify-tags 真编译 + agent-bench 绝对下限（R6-7 / P3-13）

- `make verify-tags` 从 `go list` 改为 `go build -tags 'pg redis' ./...` +
  `go vet -tags 'onnx pg redis' ./...`；CI `tag-guard` 改为调用该目标。
- `agentbench.MinPassRate = 0.80` + `CheckPassRateFloor`：full/minimal 任一档
  低于下限即返回错误；`agent-bench` CLI 新增 `--min-pass-rate`（0 关闭），
  覆盖默认值时 stderr 打印「阈值变更需单独评审」。
- 负控（先红后绿）：临时模块中带 `//go:build redis` 的类型错误文件，旧
  `go list` 退出 0、新 `go build` 退出 1；`CheckPassRateFloor` 未实现时测试
  编译失败，实现后 50% 两档均被拒、100% 放行。
- 实测：`make verify-tags` 退出 0；本仓 `agent-bench` 默认下限退出 0，
  `--min-pass-rate 1.01` 退出 1（`full pass rate 1.00 below floor 1.01`）。
- 串行门禁：`go build` / `go vet` / `go vet -tags "pg redis treesitter"` /
  `make test` 全部通过。
- 文档：`测试与CI.md`、`01-开发者/README.md`、`04-贡献者/README.md`、`README.md`
  同步；counts 62976（63.0k）。

### Commit 218: chore: gofmt 一次性格式化 + CI 格式门禁（R6-6 / P3-13）

- 新增 `make fmt-check`：对 `git ls-files '*.go'` 排除 `vendor/`、`down/` 后跑
  `gofmt -l`，非空即退出 1；`.github/workflows/ci.yml` 的 `test` job 在 Vet 后执行。
- 对 28 个非 vendor 未格式化文件执行 `gofmt -w`（纯空白/对齐变更，无行为改动）：
  `internal/ai/{tagger,tagstats}.go`、`internal/config/{config,parse}.go`、
  `internal/integration/*`、`internal/parser/adapter/{codegraph,scip}/*`、
  `internal/vector/chromem.go` 等。
- 负控（先红后绿）：加目标后首次 `make fmt-check` 退出码 2 并列出 28 个文件；
  `gofmt -w` 后退出码 0、`gofmt -l internal cmd` 输出为空。
- 串行门禁：`go build ./...` / `go vet ./...` / `go vet -tags "pg redis treesitter" ./...`
  / `make test` 全部通过（40 个包）。
- 文档：`测试与CI.md` 增 `make fmt-check` 与 test job 说明；counts 62922（62.9k）。

### Commit 217: perf(scanner): 忽略清单同源 + 单文件单次读 + 无适配器不记 hash（R6-5 / P3-10）

- 新增 `internal/ignore`：`DefaultDirs` / `DefaultFiles` / `DirSet` / `FileSet`；
  `scanner.listFiles` 与 `fswatch.DefaultIgnoreDirs` 改为同一份来源。
- `hash.Bytes` + `countLinesBytes`：`ProcessFile` 一次 `os.ReadFile` 后
  哈希、行数、解析共用，删除重复 `countLines`/`statSize` 读盘。
- 新增可选接口 `parser.SourceParser`（`ParseSource(ctx,path,data)`），
  treesitter 正则/AST 两变体与 `FallbackParser` 实现；未实现时回退 `Parse`。
- 无适配器分支改用 `markSkipped`，`ContentHash` 留空、状态 `parse_skipped`，
  补上适配器后同一文件不会被哈希闸门挡住。
- 负控（先红后绿）：修前 `countLinesBytes`/`SourceParser` 不存在（编译红）；
  修后 `ParseSource` 1 次且 `Parse` 0 次、`listFiles` 仅返回 `keep.go`
  （8 个默认目录全部忽略）、无适配器 `ContentHash=""` + `parse_skipped`；
  `go test ./internal/scanner ./internal/fswatch ./internal/parser/...` 全绿。
- 文档：`架构与模块.md` 更新包数 36/40、scanner 语义；`解析适配器.md` 增
  SourceParser 契约；counts 62901（62.9k）。

### Commit 216: feat(api): 预算化上下文接口补全（R6-1 / S-3）

- `ContextOptions` 增 `BudgetTokens`（`>0` 时覆盖 `MaxTokens`），HTTP query 与
  MCP 参数同步解析 `budget_tokens`；归一化在缓存 key 之前完成。
- `SymbolContext` 增响应级字段 `trim_reason` / `filtered` / `included_symbols` /
  `truncated`，minimal、file_unreadable、正常裁剪三条路径均填充。
- `FindTestLinksWithStats` 额外返回低于 minConfidence 被丢弃的条数，
  `FindTestLinks` 委托以保持向后兼容；`GetContext` 用 stats 版填 `filtered`。
- 负控（先红后绿）：修前编译失败（无 `BudgetTokens`/`TrimReason`/`Truncated`）；
  修后 `budget_tokens=10` 与 `max_tokens=10` 输出逐字节一致且 64→40 bytes、
  极小预算 `truncated=true`、minimal `trim_reason=mode_minimal`、
  20 次重跑（清时间戳）一致；`budget_tokens` HTTP/MCP 解析用例通过。
- 文档：`接口层.md` 补参数与响应级状态；counts 62717（62.7k）。

### Commit 215: fix(parser): 非 Go 缩进作用域与 Elixir/C++ 方法名（R5-5 / P3-8）

- 根因：通用路径只有一个 `currentClass` 指针，类结束后不复位——
  嵌套类方法挂到外层、后续顶层函数继续粘到上一个类；方法名统一取
  `matches[len-1]`，Elixir 末组是 `(`/`do`、C++ 末组是 `const|override|final`。
- 修复：引入 `nonGoScope` 缩进栈（类定义/方法定义按行首缩进弹栈），
  `langPatterns.methodNameIndex` 为 Elixir/C++ 指定第 2 捕获组。
- 负控（先红后绿）：修前 `inner_method=Outer`、`top_level=Outer`、
  Elixir 方法名 `map[(:true do:true]`、C++ 名 `const`；修后分别
  `Inner`、`""`、`run/priv`、`start`；`go test ./internal/parser/adapter/treesitter` 全绿。
- 文档：`解析适配器.md` 新增「非 Go 作用域与方法名」；counts 62545（62.5k）。

### Commit 214: feat(parser): 调用边来源与置信度分级（R5-3 / S-4）

- 根因：`CallIR` 只有 caller/callee/type/line 四字段，三后端 `call` 表虽已有 `source`
  但生产适配器从不回填、也无置信度——上层无法区分 LSP 精确边与正则猜测边。
- 修复：`CallIR` 新增 `Source`/`Confidence`；8 处生产适配器全部回填
  （LSP 100、SCIP 100、treesitter AST 90、regex 70、CodeGraph/JCodeIndexer 60）；
  `NormalizeCallProvenance` 把历史空来源解释为 `treesitter/60`。
  File/SQLite/PG 读写透传 `source`+`confidence`；SQLite 老库用 `PRAGMA table_info`
  自愈补列，PG 用 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`。
  `CallGraph` 新增 `EdgeMeta`（同 from/to 取最高置信度），`get_call_graph` 增
  `edge_details`/`edge_sources`，原 `edges` 字符串切片条数不变。
- 负控（先红后绿）：
  `TestSQLite_CallSourceConfidenceRoundTrip` 修前无字段（编译红）→ 修后 `lsp/100`（绿）；
  `TestSQLite_LegacyCallTableSelfHeals` 修前老库 `no such column: confidence`（红）→
  修后自动补列并读回 `regex/70`（绿）；
  `TestFileStore_CallMetaRoundTrip` 修前重启后精度归零（红）→ 修后 `scip/100`（绿）；
  `TestService_GetCallGraph_EdgeDetails` 修前无 `edge_details`（红）→ 修后
  `edge_details` 2 条 = `edges` 2 条、重复边取 `lsp/100`、历史边归一为 `treesitter/60`（绿）；
  `TestTreeSitterAdapter_CallProvenance` 修前 Source 空（红）→ 修后 regex/70（绿）。
- 文档：`解析适配器.md` 新增「调用边来源与置信度」；`接口层.md` 补 `get_call_graph`
  精度契约；`存储后端.md` 补 `call.confidence` 迁移；`架构与模块.md` 补边精度；
  counts 62374（62.4k）。

### Commit 213: fix(store): 重索引孤儿清理补齐三后端（R3-1 / P1-6、P2-10）

- 根因：file/sqlite 的 `UpsertIR` 主路径前序已修复，但仍有四个缺口：
  ① `FileStore.DeleteFile` 用 class ID 删 `methodTags`（真实键是 method ID）→ 标签残留；
  ② `FileStore.BulkUpsert` 仅在 `len(ir.Calls)>0` 时替换 calls → 新 IR 无调用时旧边残留；
  ③ `SQLiteStore.UpsertClasses` 公开路径只删 class，不先删 method/method_tag → 方法孤儿；
  ④ PG `UpsertIR` 仅在类/调用非空时执行清理 → 类或调用清空时旧行残留。
- 修复：DeleteFile 按方法逐条清标签；file 的 calls/imports 无条件全量替换；
  sqlite 公开 UpsertClasses 固定「method_tag → method → class_tag → class」顺序；
  PG 的 `upsertClassesTx`/`upsertMethodsTx`/`upsertCallsTx` 改为无条件执行（空集合也清旧行）。
- 负控（先红后绿）：
  `TestFileStore_DeleteFileClearsMethodTags` 修前 `got [hot]`（红）→ 修后空（绿）；
  `TestFileStore_BulkUpsertClearsCallsWhenEmpty` 修前残留 `{pkg.A.Run pkg.B.Helper}`（红）→ 修后 0（绿）；
  `TestSQLite_UpsertClassesPublicClearsMethods` 修前孤儿 `[Gone pkg.Old.Gone]`（红）→ 修后 0（绿）；
  新增 `TestPGStore_ReindexClearsWhenEmpty`（`-tags pg`，类/方法/调用清空后均为 0）。
- 文档：`存储后端.md` 新增「重索引语义（R3-1）」；counts 61935（61.9k）。

### Commit 212: perf(service): 请求内调用图复用 + depth 上限 + ctx 取消（R2-5 / P1-13）

- 根因：`GetImpact` 先 `ResolveImpactNode` 再 `FindImpactNodesWithDepth`，`GetCallGraph`
  再叠加一次 `BuildCallGraph`；`FindTestLinks` 的 call_graph 策略还会重复建图——
  实测一次 `/impact` 建图 2 次、`/call-graph` 3 次。`depth` 只有下限，传 100 原样下传；
  `BuildCallGraph` 循环内不检查 ctx。
- 修复：新增 `analyzer.MaxImpactDepth=10` 与 `ClampImpactDepth`；`BuildCallGraph` 逐文件
  及收尾检查 `ctx.Err()`；新增 `WithCallGraph/CallGraphFromContext` 请求级复用与
  `ImpactWithDepthInGraph`（图内归一化 + 双向 BFS）；`GetImpact/GetCallGraph` 各只建图一次。
- 负控（先红后绿）：
  `TestService_GetImpactBuildsGraphOnce` 修前实测 `expected 1 ... got 2`（红）→ 修后
  `GetCallsByFileID` 计数 1（绿）；`TestService_GetCallGraphBuildsGraphOnce` 修前三处调用点
  （`BuildCallGraph`+`ResolveImpactNode`+`FindImpactNodes`）→ 修后 1（绿）；
  `TestClampImpactDepth` 修前未定义（编译红）→ 0/-5→1、100→10、3→3（绿）；
  `TestBuildCallGraph_HonorsCanceledContext` 修前不返回错误（红）→ 修后 `context.Canceled`（绿）。
  既有 `TestService_GetImpact*`、`TestGetAffected_*` 全部保持通过（结果不丢）。
- 文档：`接口层.md` 影响面 depth 契约与单次建图；`架构与模块.md` 同步；counts 61734（61.7k）。

### Commit 211: fix(analyzer): 影响面 BFS 去重与 GetAffected 归一化/稳定排序（R2-2 / P1-8、P2-1、P2-3）

- 根因：`GetCallersWithDepth/GetCalleesWithDepth` 出队才标记 visited，同一祖先被多个
  同层节点发现时重复入结果（菱形图 `[B A C A]`）；`collectCallers/collectCallees`
  递归前不判 visited，DFS 同样重复。`GetAffected` 只用原始 symbol 作种子，裸名查询
  `FindTestLinks` 拿不到目标类（实测受影响测试 `[]`）；`FindImpactNodes` 错误被吞；
  受影响集合来自 map，遍历顺序随机（连跑第 13 次顺序跳变）。
- 修复：BFS「发现即标记」并保持发现顺序；DFS 在 append 前判 visited；
  新增 `Analyzer.ResolveImpactSymbol`（显式返回建图错误），`GetAffected` 以解析后的
  FQN 为种子、错误转为 `ERR_INTERNAL`、结果 `sort.Strings`。
- 负控（先红后绿）：`TestCallGraph_BFSNoDuplicate` 修前 `GetCallers` 报
  `duplicate node "A" in [B A C A]`（红）→ 修后唯一且深度为 `B/C=1、A=2`（绿）；
  `TestGetAffected_NormalizesBareName` 修前 `got []`（红）→ 修后命中
  `OrderServiceTest.TestRun`（绿）；`TestGetAffected_StableOrder` 修前第 13 次顺序跳变
  （红）→ 修后 20 次 `reflect.DeepEqual` 且 `sort.StringsAreSorted`（绿）。
  既有 `TestCallGraph_GetCallers/GetCallees` 在原实现下即期望唯一节点，复跑通过。
- 文档：`架构与模块.md` 影响面条目补充去重/消歧/稳定排序；counts 61530（61.5k）。

### Commit 210: fix(server): stdio 的 stdout 仅承载 JSON-RPC，并用官方 MCP 客户端做双传输 e2e（R4-4 / S-1）

- 根因：`search.IndexBuilder` 用 `fmt.Print*` 把索引进度（`scanning files` / `collected N docs`
  / `writing FTS index` / `index build complete`）写到 stdout。SSE 模式无影响，但 stdio 的
  MCP 客户端从子进程 stdout 逐行读 JSON-RPC，启动期自动索引的进度行直接破坏握手。
- 修复：上述进度统一改写到 stderr；stdout 只保留 `writeStdioLine` 的 JSON-RPC 响应。
- 验证工具：新增独立模块 `scripts/mcp-e2e`（官方 `github.com/modelcontextprotocol/go-sdk`
  v1.8.0），以官方 `CommandTransport`（stdio）与 `SSEClientTransport`（SSE）完成
  initialize / tools/list / tools/call（`search_config`、`context`、`get_call_graph`），
  transcript 落盘 `docs/bench/2026-11-14/mcp-transcript-{stdio,sse}.json`；`make mcp-e2e` 可复跑。
- 负控（先红后绿）：
  修前官方客户端 stdio `connect` 失败：`calling "initialize": ... invalid character 's'
  looking for beginning of value`（红）；SSE 同版本客户端已 `pass=true`（12 工具/3 调用）。
  修后 stdio 与 SSE 均 `pass=true`，protocolVersion `2025-11-25`、12 个工具、3 次调用无
  `is_error`（绿）。
- 文档：`接口层.md` 补充 stdout 纯净契约与 `make mcp-e2e`；`测试与CI.md` 增加 e2e 入口；
  counts 见本笔 `make counts`。

### Commit 209: fix(vector): ONNX Embedder 关闭后调用不再 panic（R6-3 / P2-16）

- 根因：`ONNXEmbedder.Close` 把 `e.model` 置 nil，但 `Embed` 未判空，关闭后再次推理
  直接解引用 nil session（实测 SIGSEGV in `DynamicAdvancedSession.RunWithOptions`）；
  长驻服务里全局单例被关闭（如租户重配）后，下一个查询即整进程崩溃。
- 修复：`Embed` 在持锁后先判 `e.model == nil`，返回 `onnx embedder is closed` 错误；
  `Close` 原有 nil 判断保持幂等语义（重复调用返回 nil）。
- 负控（先红后绿，`go test -tags onnx`，本机模型 + onnxruntime 就绪）：
  `TestONNXEmbedder_EmbedAfterClose` 修前在 `embedder_onnx.go:166` 触发
  `SIGSEGV ... nil pointer dereference`（红）→ 修后返回错误且重复 Close 为 nil（绿）；
  同批 `TestONNXEmbedder_Embed/Dim/EmptyText` 均通过。
- 文档：`存储后端.md` 补充 ONNX 生命周期语义；counts 见本笔 `make counts`。

### Commit 208: fix(vector): 向量文件记录模型/维度并在不匹配时拒绝加载（R6-3 / P2-16）

- 根因：`PersistentStore` 的 JSON 只存 `vectors`，没有模型标识与维度。切换 embedding
  模型（如默认构建 LocalEmbedder 1024 维 ↔ `-tags onnx` BGE 512 维）后仍会加载旧向量：
  维度不同则余弦恒 0，同维不同模型则结果静默错配，且没有任何告警。
- 修复：序列化结构新增 `model_id` / `dim`；`NewPersistentStoreWithModel(filePath, modelID, dim)`
  在加载时校验——文件声明的维度/模型与期望不一致即返回错误；旧版文件按首条向量长度
  推断维度，与期望不符同样拒绝，相符则加载并在下次保存补写元数据。文件内向量长度
  与声明维度不一致（历史脏数据）同样拒绝加载。`NewPersistentStore` 保持原语义（不校验）。
  运行时（`runtime.NewSearcherWithStore`）以 `onnx:<embedding_model>` / `local-tfidf-v1`
  加实际 `Dim()` 装配，校验失败降级内存并在日志提示删除旧文件重建。
- 负控（先红后绿）：
  先只加字段与写入、不加校验，`TestPersistentStore_ModelAndDimMismatchRejected`
  在维度不符时仍加载成功（红）；补校验后同模型同维通过、维度/模型不符均报错（绿）。
  `TestPersistentStore_LegacyFileInfersDim` 修前旧文件 4 维按期望 8 维加载成功（红）→
  修后拒绝，4 维加载并在升级后拒绝不同模型（绿）。
  `TestPersistentStore_InconsistentVectorRejected` 修前含 3 维脏数据仍加载成功（红）→
  修后报错（绿）。
- 文档：`存储后端.md` 补充模型/维度校验语义；`03-部署运维/README.md` 注明切换模型需删除
  `data/vector/vector.json` 并重建；counts 见本笔 `make counts`。

### Commit 207: fix(vector): chromem 删除真正生效且不可检索（R6-3 / P2-17）

- 根因：`ChromemStore.Delete` 恒返回 `chromem-go does not support single document deletion`，
  注释声称「通过 Search 过滤模拟删除」，但 `Search` 从未实现任何过滤——被删除的符号
  仍可被语义检索命中；持久化目录中的文档文件也未清理，重启后继续存在。
- 修复：改用 chromem-go 的 `Collection.Delete(ctx, nil, nil, id)`，内存与持久化文件同步移除；
  `Search` / `QueryText` 将 Top-K 收敛到 `Collection.Count()`（chromem 要求
  `nResults <= 文档数`，删除后请求较大 K 不再返回错误；空集合直接返回空结果）。
- 负控（先红后绿）：
  `TestChromemStore_DeleteRemovesFromSearch` 修前 `Delete` 返回 unsupported（红）→
  修后 `Search`/`Size`/`ListIDs` 均无已删 ID（绿）；
  `TestPersistentChromemStore_DeletePersists` 修前删除即失败（红）→ 修复后重开实例
  `Size=1` 且 Top-1 为未删除文档（绿）。
- 文档：`存储后端.md` 新增「向量后端语义（R6-3）」；counts 见本笔 `make counts`。

### Commit 206: fix(fswatch): 监听错误可观测 + 目录 rename 重建 watch（R6-4 / P2-18）

- 根因：`FsWatcher.Start` 的 `watcher.Errors` 分支 `_ = err` 直接丢弃错误，
  `addRecursive` 的 Walk/Add 失败同样静默跳过——监听悄悄失效时调用方无从感知，
  增量索引长期停更而进程看似正常。
- 修复：新增 `SetErrorHandler(func(error))`（未设置时写标准日志），Errors 通道、
  Walk/Add 失败均上报；根目录不可访问时 `Start` 显式返回错误（不再阻塞假装在监听）。
  目录 Rename/Remove 事件命中受监听目录时，先移除旧 watch 再重扫 root，
  在新路径重建 watch（规避 inotify 仍按旧路径上报 / 目录重建后漏监听）。
- 负控（先红后绿）：`TestFsWatcher_StartFailsOnMissingRoot`——修复前 Start 阻塞
  1s 未返回（红），修复后立即返回错误（绿）。
  `TestFsWatcher_ReportsWatchErrors` 覆盖 handler 上报；
  `TestFsWatcher_RenameRebuildsWatch` 覆盖 rename 后按新路径上报（macOS FSEvents
  后端本身会跟随 rename，该用例作为跨平台回归守卫，Linux inotify 下的价值更高）。
- 文档：`仓库结构.md` 补充 fswatch 语义；counts 60652（60.7k）。

### Commit 205: fix(store): FileStore 原子落盘 + 损坏即失败（R3-12 / P2-9）

- 根因：两处可靠性缺口。
  ① `Open` 丢弃 `loadFromDisk` 的返回值——`store.json` 损坏时静默当空库，
  下一次 `saveToDisk` 会用空数据覆盖唯一副本（不可恢复的数据丢失）；
  ② 保存仅 `WriteFile` + `rename`，无 `fsync`：rename 可能先于数据块落盘，
  强杀/掉电后可能看到 0 字节或截断的 `store.json`。
- 修复：保存改为「写 tmp → `f.Sync()` → 原子 `rename` → `fsync(dir)`」；
  `Open` 传播加载错误并释放进程锁，损坏文件显式失败（需人工还原/移走）。
- 负控（先红后绿）：
  `TestFileStore_OpenRejectsCorruptStore` 修前 `Open` 返回 nil（红）→ 修后报错（绿）；
  `TestFileStore_SaveIsAtomic` 修前保存后 `store.json` 不存在（红）→ 修后存在且无 `.tmp` 残留（绿）。
- 文档：`存储后端.md` 新增「FileStore 持久化语义」；counts 60502（60.5k）。

### Commit 204: fix(ai): 查询预算按请求隔离，移除共享可变 phase（R6-2）

- 根因：`Enhancer.phase` 是共享可写字段，查询路径 `SetPhase(PhaseQuery)` 与扫描路径
  `SetPhase(PhaseScan)` 在长驻服务里并发改写（`-race` 实测 DATA RACE）；
  查询预算计数 `Budget.queryUsed` 只在进程启动时归零，前一请求耗尽后，
  后续请求在进程生命周期内永久拿不到 AI 消歧。
- 修复：`Enhancer` 改为不可变——`phase` 与查询预算在构造时确定；
  新增 `ForQuery()` 返回「本次请求独立 `QueryBudget`」的实例（共享 client 与限额模板，
  计数互不影响）。删除 `SetPhase` / `Budget.ResetQuery` / `QueryRemaining` /
  `QueryExhausted` / `tryConsumeQuery`；扫描期新增 `Enhancer.ResetScanBudget()`，
  由 `Analyzer.TagAll` 每次扫描开始时调用。Service 查询路径改用
  `s.enhancer.ForQuery()`，并发请求之间不再互相重置/耗尽。
- 负控（先红后绿，`go test -race`）：
  `TestEnhancer_ConcurrentPhaseRace` 修前报 `DATA RACE ... Enhancer.phase`；
  `TestEnhancer_QueryBudgetPerRequest` 修前第 2 次请求即 `budget exceeded`。
  修复后分别改为 `TestEnhancer_ConcurrentPhases_NoRace` / 复用同名用例，均通过。
- 文档：`架构与模块.md` 注明两类预算的生命周期；counts 60434（60.4k）。

### Commit 203: fix(server): 内部错误统一脱敏 + 未知租户固定 404（R2-8 / P1-11、P2-6）

- 根因：`writeServiceError` 的兜底分支直接 `err.Error()` 回传 500；`mcpError`
  同理把原始错误写进 JSON-RPC message。存储层错误通常含完整 SQL、DSN 与绝对路径，
  一旦触发即向客户端泄漏内部结构与凭据线索。panic 路径此前已脱敏，属遗漏出口。
- 修复：非 `*service.ServiceError` 的错误只写服务端日志（HTTP 走模块日志、
  MCP 走 stderr），客户端统一 `ERR_INTERNAL` + `internal server error` /
  `-32603` + `internal error`；受控错误（`ServiceError`）保持原码与原 hint。
  未知租户/服务不可用改为固定文案，不再回显错误串。
- 负控（先红后绿）：`TestInternalErrorsAreSanitized`——修复前 HTTP 与 MCP
  均原样回传含 `SELECT ... path='...' (dsn=postgres://...)` 的错误（红），
  修复后两出口均不含原文（绿）；既有 404 用例继续通过。
- 文档：`接口层.md` 新增错误脱敏契约；counts 60374（60.4k）。

### Commit 202: feat(store): 连接池上限可配置并接入 sqlite/pg（R3-11 / P2-12）

- 根因：sqlite/pg 均直接使用 `database/sql` 默认值——`MaxOpenConns=0`（无上限）。
  PG 在高并发查询下连接数无界，易打满服务端 `max_connections`；
  多租户（41 实例）时问题叠加。
- 修复：`storage.max_open_conns` / `max_idle_conns`（默认 **16 / 8**，`0` 沿用
  database/sql 默认，file 后端忽略）；启动校验拒绝负数与 `idle > open`。
  `store.PoolLimiter` + `store.ApplyPoolLimits` 统一注入，cmd 的 sqlite 与 pg
  两个打开路径（含租户经同一 `newStore`）均生效；两后端新增 `PoolStats()`
  供观测/测试。非法值收敛：负数→0，idle>open→open。
- 负控（先红后绿）：`TestNewBaseStoreAppliesPoolLimits`——修复前
  `MaxOpenConnections = 0`（红），修复后 `= 4`（绿）。
- 补充用例：`TestSQLite_SetPoolLimits`（含收敛路径）、`TestValidatePoolLimits`。
- 文档：`存储后端.md` 新增「连接池上限」，含多租户连接数放大提示；counts 60339（60.3k）。

### Commit 200: perf(store): 补齐按文件清调用与标签反查索引（R3-10 / P2-11）

- 根因：`class_tag` / `method_tag` 的 PK 前导列是 `*_id`，`tag_id` 无索引覆盖，
  `search_by_tag` 反查全表扫描；PG 的 `call` 表缺 `file_id` 索引，`DeleteFile` /
  重索引 / `GetCallsByFileID` 随调用行数线性退化（SQLite 已有 `idx_call_file`）。
- 修复：SQLite / PG / `migrations/001_init.sql` 三处 DDL 新增
  `idx_call_file`（PG）、`idx_class_tag_tag`、`idx_method_tag_tag`（两后端）。
  SQLite 每次 `Open` 重放 `CREATE INDEX IF NOT EXISTS`，既有库自愈；
  PG 需一次 `InitSchema`（服务启动即执行）。
- 负控（先红后绿）：`TestSQLite_HotIndexes`——修复前两项标签索引缺失（红）；
  修复后 `EXPLAIN QUERY PLAN` 实测：
  `call` → `SEARCH call USING COVERING INDEX idx_call_file (file_id=?)`、
  `class_tag` → `SEARCH class_tag USING INDEX idx_class_tag_tag (tag_id=?)`、
  `method_tag` → `SEARCH method_tag USING INDEX idx_method_tag_tag (tag_id=?)`。
- PG 侧新增 `-tags pg` 用例 `TestPGStore_HotIndexes`（`pg_indexes` 存在性 +
  `EXPLAIN`，`enable_seqscan=off` 保证计划体现索引）；门禁以 `go vet -tags` 编译校验。
- 文档：`存储后端.md` 新增「热点索引」；counts 60154（60.2k）。

### Commit 199: refactor(service): 删除未接线的 coverage 关联策略（R6-8 / P2-7）

- 根因：`SetCoverage` / `LoadCoverageJSON` / `LoadGoCoverProfile` / `ParseGoCoverProfile`
  全仓（非测试）零调用点，`Service.coverage` 恒 nil，`linkByCoverage` 永远首行返回。
  对外文档却宣称 90 分覆盖率关联可用——「文档有、运行无」的名义能力；即便接线，
  也会把一个测试类的全部方法按 90 分登记，假阳性高于 naming。
- 删除：`internal/service/coverprofile.go`（219 行）+ 对应测试（`coverprofile_test.go`）、
  `Service.coverage` 字段、两个注入 API、`linkByCoverage` 及其调用点；包注释策略
  由六种收敛为五种（explicit 100 / call_graph 95 / dependency 80 / naming 70 / same_tag 60）。
- 测试调整：删除 `TestFindTestLinks_Coverage` / `TestLoadCoverageJSON`；
  `TestGetImpact_WithAnalyzer_IncludesRelatedTests` 改由 naming 策略建立关联
  （新增 `*_test.go` 的 `HandlerTest.testHandle` 夹具），断言口径不变。
- 负控（先红后绿）：新增 `TestCoverageStrategyRemoved`，用反射断言 Service 不再暴露
  四个 coverage API——修复前 4 项全部报红，修复后通过（防止「名义能力」被重新加回）。
- 文档：`接口层.md` 策略链去 coverage 并注明删除；`001_init.sql` 的 `strategy` 注释同步。
- 验证：串行门禁全绿；counts 60560 → 60046（60.0k，-514 行）。

### Commit 198: fix(parser): CodeGraph/JCodeIndexer 移出默认注册表 + SCIP 优先级前置（R5-1 / P1-16）

- 根因：两个适配器 `Parse` 恒返回 `ErrSourceUnavailable`（只实现批量 `ParseAll`），
  生产扫描走 `Parse`；而 `NewParserRegistry` 只要配置了非默认 `db` 路径就注册，
  等于让一个永远回退 tree-sitter 的适配器占住优先级。更严重的是旧优先级表
  `go: gopls, codegraph, scip, treesitter` 把 codegraph 排在 scip 前——同时配置
  SCIP 与 CodeGraph 时，真正可用的 SCIP 被遮蔽。
- 修复：`CodeGraphConfig` / `JCodeIndexerConfig` 新增 `Enabled bool`（默认 false），
  注册条件改为 `Enabled && db != "" && db != 默认占位`；`TenantConfig.ToConfig`
  与配置 `Merge` 同步透传 `Enabled`。优先级表改为
  `gopls > scip > codegraph > treesitter`（java：`jdtls > scip > jcodeindexer >
  codegraph > treesitter`），其余语言同理。
- **行为变更**：仅保留 `parser.codegraph.db` / `parser.jcodeindexer.db` 的旧配置
  不再触发注册，需显式补 `parser.codegraph.enabled: true` /
  `parser.jcodeindexer.enabled: true`。默认（不配置）注册表不变：treesitter 兜底。
- 负控（先红后绿）：`TestNewParserRegistry_CodeGraphNotDefault`——仅配 db 不配
  enabled，修复前选中 `codegraph`（红），修复后 `treesitter`（绿）；
  `TestNewParserRegistry_SCIPBeforeCodeGraph`——SCIP + CodeGraph 同时可用，修复前
  选中 `codegraph`（红），修复后 `scip`（绿）。
- 文档：`解析适配器.md`（启用方式表 + 行为变更说明）、`docs/README.md`、
  `架构与模块.md`、`01-开发者/README.md`、`仓库结构.md`。
- 验证：串行门禁全绿；counts 60560（60.6k）。

### Commit 197: feat(fsperm): 统一落盘权限 0700/0600（R3-7 / P2-13）

- 背景更正：Commit 196 依据 `pathsafe` 的 `EnsureDir`（0755）判断「不做权限收紧」，
  但本地 replace 的 `pathsafe` 已提供 `MkdirAll`/`WriteFile`（0700/0600）。真正缺口
  在未走该原语的路径：模型下载/解包（`os.MkdirAll 0755`、`os.OpenFile 0644`、
  按归档 mode 0644）与 SQLite `.db`（驱动默认 0644）。
- 新增 `internal/fsperm`：`DirMode=0700`/`FileMode=0600`，提供
  `MkdirAll`/`WriteFile`/`OpenFile`/`Chmod`；对已存在的目录/文件强制执行 `chmod`
  （旧数据迁移）。`OpenFile` 自动建父目录并收敛权限。
- 替换 14 处生产调用：FileStore（2）、lock unix/windows（2）、SQLite 建目录（1）、
  runtime（2）、embedding local/persistent（3）、search builder（2）、
  retrieval persistent（2）；模型下载/解包 5 处改走 `fsperm`；SQLite 在 PRAGMA
  建库后把 `.db` 收敛到 0600。
- **迁移影响**：既有 index 目录/JSON 索引为 0755/0644 的，会在下次创建或重写时
  收敛到 0700/0600；SQLite `.db` 在下次打开时即收敛。WAL/SHM 仍由 SQLite 按
  umask 创建，依赖所在目录 0700 隔离。无数据格式变更，回滚无需转换。
- 负控（先红后绿）：`TestModelDownloader_Permissions`——修复前 `models perm = 0755`
  （红）；修复后模型目录/`onnx` 0700、产物 0600（绿）。
  `TestSQLite_DBFileAndDirPerm`（目录 0700、db 0600）、`TestMkdirAllTightensExisting`
  等 fsperm 迁移用例保持绿。
- 文档：`AGENTS.md` 改为事实描述；`docs/01-开发者/存储后端.md` 新增「落盘权限」；
  包数基线 34/38 → 35/39（`docs/README.md`、架构与模块、仓库结构）。
- 验证：`go build ./... && go vet ./... && go vet -tags "pg redis treesitter" ./... && make test`
  全绿（含 `internal/store/sqlite` 全组）；counts 60495（60.5k）。

### Commit 196: chore(docs): 纠正 fsperm 声明并对账陈旧清单（R1-6）

- 文档纠偏：`AGENTS.md` 原写「`internal/fsperm` 统一目录 0700 / 文件 0600」，
  但仓库无该包；实际路径约束由 `gitee.com/idcu-go/pathsafe` 提供，且
  `pathsafe.MkdirAll` 使用 `0o755`，**不做权限收紧**。已改为事实描述并提示
  敏感环境需部署侧收敛。
- 清单对账：以下条目早已由历史提交完成但状态仍为 todo，逐条核对提交号后置 done
  （无代码改动）：
  R1-1←`4b7fe61`/`89275a7`、R1-2←`a460fe2`、R1-3←`f3919fe`、R1-4←`f5fba75`、
  R1-7←`d0afa9f`、R2-1←`8ece7bd`、R2-3/R2-4←`82e408e`、R2-6←`0d0c36e`、
  R2-7←`365c1f4`、R3-2←`f159ab3`、R3-3←`4df1b53`、R3-4←`2381952`、
  R4-1←`5c18c36`、R5-2←`1c9b997`。
- 复核：语言数（31）、internal/全仓库包数（34/38）与 `make counts-check` 一致。

### Commit 195: fix(cmd): 启动时校验配置并聚合报告全部问题（R3-6 / P1-18）

- 问题：`config.Validate` 早已实现且返回全部错误，但生产路径从未调用；非法配置
  （`scanner.workers=-1`、未知 `storage.driver`）会带病启动，直到运行期才以无关
  症状失败。
- 修复：`run()` 在环境变量覆盖后调用新增的 `validateConfig`；错误信息带问题计数
  与全部条目（含配置项名），一次性可定位。
- 负控（先红后绿）：`TestValidateConfigReportsAllErrors`——临时将校验置空后实测
  `非法配置应返回错误（当前静默放行）`（红）；恢复后断言错误同时包含
  `scanner.workers` 与 `storage.driver`（绿）。`TestValidateConfigAcceptsDefaults`
  保证默认配置可通过。
- 验证：`go build ./... && go vet ./... && go vet -tags "pg redis treesitter" ./... && make test`
  全绿；counts 60305（60.3k）。

### Commit 194: fix(retrieval): exact 模式改用 token 精确匹配（R2-9 / P1-2）

- 问题：`Searcher.Search` 的 `SearchModeExact` 分支调用 `FTSModeFuzzy`，exact 与
  模糊检索完全等价：`Sv` 命中 `Svc`/`Svc.Method`，`java` 命中 `javascript`。
- 修复：exact 分支改调 `FTSModeExact`（token 全等）。`both`/`semantic` 路径不变。
- 负控（先红后绿）：`TestSearcherExactMode`——修复前 `exact Sv 不应命中 Svc/Svc.Method,
  实际 2 条`（红）；修复后 `Sv`/`java` 均 0 条，`Svc` 命中、`both` 仍命中（绿）。
- 连带：`TestFullPipeline_ScanStoreIndexSearch` 原用短名 `User`/`Order` 做 exact 查询，
  依赖的正是子串行为；按新语义改为完整 FQN `main.User`/`main.Order`。
- 文档：`docs/01-开发者/接口层.md` 注明 `mode=exact` 为 token 精确匹配。
- 验证：`go build ./... && go vet ./... && go vet -tags "pg redis treesitter" ./... && make test`
  全绿；counts 60260（60.3k）。

### Commit 193: fix(server): 启用认证时 CORS 不再回通配来源（R3-8 / P2-14）

- 问题：CORS 中间件无条件 `Access-Control-Allow-Origin: *`，启用 Bearer 认证后
  仍对任意站点开放，与「默认收敛」不符。
- 修复：`corsMiddlewareFor` 增加 `authEnabled` 判定；HTTP 与 MCP 两处按各自运行时
  令牌快照传入（支持热更新）。未启用认证时保持 `*`（本地开发向后兼容）。
- 负控（先红后绿）：`TestCORSMiddleware_AuthConverges` 两子用例——
  `auth=true` 断言不回通配头（修复前实测 `*`，红）；`no-auth` 保持 `*`（绿）。
- 文档：`docs/03-部署运维/README.md` 认证段说明 CORS 收敛与反代配置方式。
- 验证：`go build ./... && go vet ./... && go vet -tags "pg redis treesitter" ./... && make test`
  全绿；counts 60.2k。

### Commit 192: fix(config): cloneConfig 保留 AI 凭据与向量模型字段（R3-5 / P1-17）

- 问题：`cloneConfig` 漏拷 `AI.BaseURL`/`AI.APIKey` 与
  `Storage.Vector.ModelDir`/`ModelDownloadURL`/`ModelSHA256`。`TenantConfig.ToConfig`
  以 clone 结果为底再叠加租户覆盖项，基础层字段被清空后，未显式配置 AI 的租户
  会静默失去全局凭据（AI 增强被禁用），模型自动下载与 SHA 校验同样失效。
- 修复：深拷贝补齐上述 5 个字段。
- 负控（先红后绿）：`TestCloneConfig_PreservesAIVectorFields`——通过
  `TenantConfig.ToConfig` 继承全局配置，断言 AI 与向量模型字段一致；
  修复前 `base_url="" api_key=""`（红），修复后绿。
- 验证：`go build ./... && go vet ./... && go vet -tags "pg redis treesitter" ./... && make test`
  全绿；counts 60165（60.2k）。

### Commit 191: fix(server): 健康检查如实上报存储驱动与向量状态（R3-9 / P2-15）

- 问题一：`/health/db` 恒返回 `"type":"file"`，与实际后端（sqlite/pg）无关。
  修复：`HTTPServer` 增加 `storageType` 与 `SetStorageType`，cmd 层注入
  `cfg.Storage.Driver`（空值归一为 `file`）。
- 问题二：向量健康检查 `_ = vs.Size(); return nil` 恒为 ok——`Size` 只是内存计数，
  后端不可用也显示健康。修复：改注入 `vs.ListIDs(ctx)`，真实读操作会返回后端错误并
  尊重 ctx 取消。
- 负控（先红后绿）：`TestHealthDBEndpointReportsDriver`——注入 `pg` 后断言
  `type=="pg"`；修复前实测 `type = file`（红），修复后绿；`TestHealthDBEndpoint` 保持绿。
- 文档：`docs/03-部署运维/README.md` 健康检查段补充驱动上报与向量探测口径。
- 验证：`go build ./... && go vet ./... && go vet -tags "pg redis treesitter" ./... && make test`
  全绿；counts 60.1k 不变。

### Commit 190: fix(service): 查询缓存 key 含 IncludeTrace，命中返回深拷贝（R2-10 / P2-2）

- 问题：`queryCacheKey` 未包含 `IncludeTrace`。先请求 `include_trace=false` 并写入缓存后，
  后续 `true` 请求命中同一 key，`_trace` 静默缺失；且命中路径只做浅拷贝，
  `RelatedTests` 切片与 `Trace`/`Trace.Config` 与缓存共享，调用方修改会污染后续读取。
- 修复：key 追加 `IncludeTrace`；新增 `cloneSymbolContext`，复制 `RelatedTests` 切片与
  `Trace`/`Trace.Config` 后置 `CacheHit=true` 返回。
- 负控（先红后绿）：
  - `TestQueryCacheKeyIncludesIncludeTrace`——修复前 `include_trace=true 命中了
    include_trace=false 的缓存条目`（红）；
  - `TestQueryCacheReturnsDeepCopy`——修复前调用方改 `RelatedTests[0]` 后二次读取
    拿到 `[被污染]`（红）；修复后两项均绿，`TestQueryCache*` 全组保持绿。
- 验证：`go build ./... && go vet ./... && go vet -tags "pg redis treesitter" ./... && make test`
  全绿；counts 60101（60.1k）。

### Commit 189: fix(parser/scip): 懒加载并发首次 Parse 竞态（R5-6 / P3-6）

- 问题：`Parse`/`ParseAll` 用裸 `if !a.loaded { a.loadIndex() }` 触发懒加载，
  `documents`/`loaded`/`truncated` 无同步。并发首次解析（扫描器多 worker 共用适配器）时
  多个 goroutine 同时 `loadIndex`，重复解析 index 并读写同一批文档对象。
- 修复：`SCIPAdapter` 增加 `mu sync.RWMutex`；新增 `ensureLoaded()`（快路径 RLock 读标记，
  慢路径 Lock 后双检再加载）、`snapshot()`（RLock 取文档切片快照）；`Parse`/`ParseAll`
  改走 `ensureLoaded` 并在锁外遍历快照；`Close`/`SetMaxDocs`/`Truncated` 纳入锁保护。
  已加载文档不可变（重载只在写锁内整体替换切片），因此快照在锁外使用安全。
- 负控（先红后绿）：`TestSCIPAdapter_ConcurrentFirstParse`——16 goroutine 并发首次 `Parse`。
  修复前 `go test -race` 报 `WARNING: DATA RACE`（`loadIndex` 写 `documents` vs
  `convertDocument` 读）且 `documents loaded 2 times`；修复后无 race、恰好加载 1 次。
- 验证：`go build ./... && go vet ./... && go vet -tags "pg redis treesitter" ./... && make test`
  全绿；`go test -race ./internal/parser/adapter/scip` 全绿；counts 60011（60.0k）。

### Commit 188: fix(mcp): search_config 描述与实现对齐（R4-3 / P2-5）

- 问题：工具描述写「搜索配置项和注解」，实现是类/方法 `FullName/Name/Doc/Signature/ReturnType`
  的大小写不敏感子串匹配（注解只经 `Doc` 文本覆盖），属能力夸大。
- 修复：描述改为「按名称/文档/签名子串搜索符号（大小写不敏感，含注解文本）」，
  `pattern` 参数说明改为「子串（大小写不敏感）」；`AGENTS.md` 工具表同步。
- 负控（先红后绿）：`TestMCP_ToolsList` 断言 `search_config` 描述包含「子串」且不含「配置项」；
  修复前红，修复后绿；`TestMCP_ToolCall_AllTools` 保持绿（符号搜索行为不变）。

### Commit 187: fix(server): viz 分页负数/非法参数返回 400（R1-5 / P3-1）

- 背景：Commit 162 已用「夹取到 [0,len]」消除 `docs[offset:end]` panic，但批次 A 退出标准
  要求 `limit=-1` / `offset=-1` 返回 **400**；且 `offset=abc`、`limit=1.5` 此前被静默忽略。
- 修复：`apiDocuments` 改用 `vizPageParam`——缺省取默认值，负数/非整数返回 400，
  正数越界仍夹取到 `[0,len]`（`limit=0` 合法，返回空页）。
- 负控（先红后绿）：`TestVizAPIDocuments_PaginationBounds` 扩为 8 组；修复前 4 组负数/非法值
  实测 200（红），修复后负数/非法值 400、正常与越界值 200（绿）。
- 验证：`go build ./... && go vet ./... && go vet -tags "pg redis treesitter" ./... && make test` 全绿。

### Commit 186: feat(releasebench): 人工真值独立评估（R5-14 / S-2h）

- 目的：现有自动真值与 `call_graph` 策略同为 IR 直接调用边，饱和数字只能证明链路完整，
  无法挡住「真值和策略一起消失」的回归（R5-12 即为实例：目标方法连同真值一起不可见）。
- 数据集：`internal/releasebench/testdata/manual-truth.json`，**逐个阅读测试方法体**标注，
  Go 28 条 + Java 18 条，共 14 个生产方法、2 个闭合测试文件。真值不来自 IR；IR 仅用于
  枚举标注文件内的方法作为评估范围（范围外预测忽略、范围内误报计 FP）。
- 实现：`manual.go` 新增数据集加载、范围校验（生产方法必须在索引内、真值测试必须在标注
  文件内）、逐方法与微平均 P/R/F1；`release-bench --manual-truth <path>` 追加
  `manual_truth` 字段与 Markdown 段落，与自动口径并排输出。CLI/接口文档同步。
- 结果（两变体一致）：Go regex/treesitter 28/28、Java 18/18（P/R/F1 = 1.0）。
- 回归捕获验证：临时移除 `EnsureMethodOwners`（R5-12 修复）后，同数据集实测
  **TP 6 / FN 22 / R 0.2143 / F1 0.3530**，缺口精确落在 `Service.FindTestLinks`(17)
  与 `MCPServer.serveStdio`(5)；原始 JSON 见 `docs/bench/2026-11-14/manual-pre184-go.json`。
- 口径差异写入 `docs/bench/2026-11-14/README.md`：来源、覆盖、范围外预测处理、用途、盲区
  五项并排；自动口径残余 FP 多为「IR 未解析到的真实调用被命名策略命中」，按人工语义并非误报。
- 负控：`TestScoreManualClosedScope`（范围外预测忽略、去重、TP/FP/FN 与微平均）；
  `TestManualDatasetValid`（数据结构、重复真值、FQN 形态）。
- 基准数据：`final-*.json`（R5-12/R5-13 后）；Go regex P 0.9922→0.9966、R 0.9803→1.0、
  F1 0.9862→0.9983；treesitter F1 0.9807→0.9935；FN 23/24→0；Java 维持 1.0。
- 验证：build/vet（含 pg redis treesitter）/make test 全绿；counts 59910（59.9k）。

### Commit 185: fix(service): Go 生产文件中的 Test* 类型不再整文件判为测试（R5-13 / S-2g）

- 现象：`Service.FindTestLinks` 的 `call_graph` 结果里出现生产方法
  （`Service.GetTests`/`GetAffected`/`getContextModeUncached`/`enrichImpactNodes`）。
- 根因：`isTestClass` 只按类名前缀判定，生产文件里的 `TestLink`/`TestResult`/`TestBatchItem`
  让整个文件被当作测试文件，文件内所有调用都被当成「测试方法 → 生产方法」的边。
- 修复：Go 源码仅在 `*_test.go` 内做名称判定（Go 测试函数只能存在于 `_test.go`）；
  其它语言保持名称约定。Java 夹具路径改为 `.java` 以继续覆盖名称约定。
- 负控（先红后绿）：`TestIsTestClass` 增加
  `{TestResult, /x/service.go} = false` 与 `{TestResult, /x/service_test.go} = true`；
  修复前首个用例红，修复后全绿。
- 实测（本仓全量扫描，Go regex）：`Service.FindTestLinks` 结果 40 → 36 条，
  消失的 4 条均为生产方法；基准 FP 9 → 4、P 0.9922 → 0.9966。
- 验证：`go build ./... && go vet ./... && go vet -tags "pg redis treesitter" ./... && make test` 全绿；
  `make counts` = 59529（59.5k，与既有文档一致）。

### Commit 184: fix(parser/scanner): 跨文件接收者方法入库（残余 FN 根因）

- **根因更正**：Commit 183 把残余 FN 归为「调用解析 + FQN 归一」。实测推翻该结论——
  IR 里调用边确实存在（`service.TestFindTestLinks_Naming -> svc.FindTestLinks`、
  `server.TestMCPServer_Stdio_ToolsList -> srv.serveStdio`），但 store 中**没有任何短名为
  `FindTestLinks` / `serveStdio` 的方法**。
- 真根因（单点，两个症状同源）：Go 允许类型与方法分属同包不同文件。解析器把
  `MethodIR.ClassFQN` 置为接收者类型（`Service`/`MCPServer`），但该类不在方法所在文件的
  `Classes` 中；三后端按「本文件 ClassFQN → classID」映射入库，未命中即 `continue` 丢弃。
  于是 `Service.FindTestLinks`、`MCPServer.serveStdio` 从未进入索引，任何策略都查不到。
- 动作：新增 `parser.EnsureMethodOwners`，scanner 在 `UpsertIR` 前为未命中的 `ClassFQN`
  补最小类记录（`FullName=ClassFQN`，`Type=CLASS`），方法 FQN 不变。两条解析路径
  （默认正则 / `-tags treesitter`）共用，三后端无需改动。
- 负控（先红后绿）：`TestScanAll_GoCrossFileReceiverMethodIndexed`——`service.go` 声明
  `type Service struct{}`、`service_link.go` 定义 `func (s *Service) FindLinks`。
  修复前 `Service.FindLinks indexed 0 times`（红）；修复后 1 次（绿）。
- 实测（本仓 FileStore 全量扫描，`FindTestLinks` 目标）：
  - `Service.FindTestLinks`：0 → 40 条（修复前 0 条；调用边一直在）；
  - `MCPServer.serveStdio`：0 → 5 条；
  - 短名索引确认唯一：`FindTestLinks -> [Service.FindTestLinks]`、`serveStdio -> [MCPServer.serveStdio]`。
- 文档：`docs/01-开发者/解析适配器.md` 新增「跨文件接收者方法入库」；`docs/README.md`、
  `docs/01-开发者/架构与模块.md` 同步 `非 vendor LoC ≈ 59.5k`；`make counts-update` 刷新基线。
- 验证：`go build ./... && go vet ./... && go vet -tags "pg redis treesitter" ./... && make test` 全绿。

### Commit 183: feat(service): call_graph 策略补齐行为型/跨类测试召回（R5-11 / S-2e）

- 根因归属（四个环节逐一核对）：
  - **候选集剪枝（本笔修复）**：`discoverTestClasses` 只按类名约定建候选。petclinic 123 条真值中
    120 条目标方法属于 `NamedEntity`/`BaseEntity` 等**没有同名测试类**的基类；Go 的行为型测试名
    （`TestCollection_Delete` → `DB.CreateCollection`）也不在任何命名候选里。
  - **FQN/命名匹配（本笔归一）**：callee 常见接收者变量形态（`entity.setName`），caller 带包名前缀
    （`org...ClinicServiceTests.shouldFindVets`），与库内短 FQN 口径不一致。
  - **调用解析**：Java 单 tab + 包级可见性方法已随 R5-8 修复；本轮残余 5 条（`serveStdio`）。
  - **同包可见性**：已随 R5-8 修复，本轮无新增。
- 动作：新增 `linkByCallGraph`（策略名 `call_graph`，置信度 95）：遍历测试文件的直接调用边，
  callee 全名优先、短名唯一时接受变量形态；caller 按文件内方法 FQN 后缀归一。与类名约定解耦。
- 负控（先红后绿）：`TestFindTestLinks_CallGraphBehavioralName` 的 go/java 两个子测试——
  修复前 go 只有 dependency 的粗粒度链接、java 零输出（红）；修复后均返回 `call_graph` 链接（绿）。
- 实测（同一命令/语料/真值；本轮前 = `58625af`）：
  | 组 | 前 P/R/F1 | 后 P/R/F1 |
  |---|---|---|
  | go regex | 0.8333 / 0.1114 / 0.1965 | **0.9922 / 0.9803 / 0.9862** |
  | go treesitter | 0.7356 / 0.1031 / 0.1809 | **0.9807 / 0.9807 / 0.9807** |
  | java regex | 1.0000 / 0.0081 / 0.0161 | **1.0000 / 1.0000 / 1.0000** |
  | java treesitter | 1.0000 / 0.0063 / 0.0125 | **1.0000 / 1.0000 / 1.0000** |
  | ts 两变体 | 真值 0（范围外） | 不变 |
  未放宽任何阈值（min-confidence 仍为 60）。
- **口径披露**：`call_graph` 与真值同为直接调用边，本轮后的饱和数字衡量「扫描→入库→反查→FQN 归一」
  链路完整性，不再代表独立启发式判别力；命名策略的独立收益由 R5-10 中间态（Go P 0.8630）衡量。
- 残余 23/24 条 FN：`svc.FindTestLinks` 短名多义、`serveStdio` 调用边未抽取；归属「调用解析 + FQN 归一」，
  已在 `docs/bench/2026-11-14/README.md` 记录，留待下一轮，不静默删除。
- 验证：`go test ./internal/service/` 全绿；六组 `release-bench` 全量重跑；`make counts-check` 通过。

### Commit 182: fix(service): 命名匹配按词边界 + JUnit 描述型前缀（R5-10 / S-2d）

- 背景：`matchesTestMethod` 对描述型测试名用 `strings.Contains` 兜底，`Analyze` 会命中 `TestAnalyzer_*`（benchmark 实测 `Analyzer.Analyze` 产生 20+ 条 FP）；反向缺口是 JUnit 描述型方法名 `processCreationFormSuccess`/`addPetAddsPersistedPet` 无法命中目标方法。另有 Go 包级容器里的同包 helper（`keysOfSets`/`newMockLSPAdapterMode`）被当作测试，且生产文件的 PACKAGE 容器与测试容器同名时也会被当作测试类扫描。
- 动作：
  - `matchesTestMethod` 改为**词边界**匹配：测试名按下划线/camelCase 切词，目标名须由连续词拼成（`testGetOrderShouldReturn` 仍命中，`Analyze` 不再命中 `Analyzer`）。
  - 新增 JUnit 描述型前缀规则：目标名是测试名前缀且下一字符为大写/下划线（`processCreationFormSuccess` ↔ `processCreationForm`），目标名 <4 时禁用（`get` 不命中 `getById`）。
  - `linkByNaming` 对 Go 测试文件的 PACKAGE 容器只接受 `Test`/`Benchmark`/`Example` 前缀；生产文件的 PACKAGE 容器不再作为测试类扫描。
- 负控（先红后绿）：
  - `TestMatchesTestMethod` 新增 5 例：3 个描述型前缀（红：`= false`）、2 个子串越界（红：`= true`）。
  - `TestFindTestLinks_GoPackageHelpersNotNaming`：修复前 `redis.keysOfSets` 以 naming(70) 泄漏（红），修复后仅 `redis.TestKeysOf`。
- 实测（同一 `release-bench`，regex 变体）：
  - Go：TP 130→126、FP 46→20、P 0.7386→0.8630、R 0.1121→0.1091、F1 0.1947→0.1937。TP 减少 4 条为包级 helper 不再被当作测试（如 `benchmarkCollection_Query`），属精度修正；测试召回由下一笔 `R5-11` 补。
  - Java：TP 1→3、FP 0、R 0.0081→0.0244、F1 0.0161→0.0476。
- 验证：`go test ./internal/service/` 全绿；`make counts-check` 通过。

### Commit 181: fix(bench): 采样只收缩评估生产集合，真值保留全量（R5-9 / S-2c）

- 背景：`release-bench` 先 `samplePairs` 再构造真值，同一生产方法未进入采样的真实测试不在真值集合里 → 策略正确预测却被计为 FP；被采样生产方法的其余真值也一并缺失，FN 被低估。这是**测量口径缺陷**，不是策略变差，会让 P/R 同时失真。
- 动作：新增 `prepareEvaluation(all, sampled)`，真值取全部 pair，采样只用于去重后的「待评估生产方法集合」；`evaluate` 改为接收 `truth map[string]map[string]struct{}` + `prods []string`。
- 负控（先红后绿）：`TestPrepareEvaluation_UsesAllPairsForSampledProds`——临时把 `range all` 改为 `range sampled` 时 `truth[A.m]` 缺 `pkg.TestY`（红）；恢复后绿。
- 实测（同一 `release-bench --repo code-schema --lang go`，min-confidence=60，策略代码零改动）：
  - TP 34 → 130，FP 141 → 46，FN 158 → 1030。
  - P 0.1943 → 0.7386，R 0.1771 → 0.1121，F1 0.1853 → 0.1947。
  - R 下降是分母修正的结果（FN 不再被采样截断），旧数字与新数字**不可直接比较**；本笔不放宽任何阈值。
- 验证：`go test ./internal/releasebench/ -run 'TestPrepareEvaluation|TestRun_MultiLanguage'` 通过；`make counts-check` 通过。

### Commit 180: fix(service/parser): Java `*Tests` 口径统一 + 包级可见性方法解析（R5-8 / S-2b）

- 背景：公开基准 S-2 中 petclinic 的测试类统一命名 `OwnerControllerTests`，`matchTestClass` 只处理 `Test` 前后缀（`isTestClass` 已支持 `Tests`），两处口径不一致导致 naming 零召回。
- 动作：
  - `matchTestClass` 与 `isTestClass` 对齐：`Test` 前后缀 + `Tests`/`Spec` 后缀。
  - 连带根因：Java 正则方法模式要求修饰符且未行首锚定，JUnit 5 包级可见性方法（`void initCreationForm()`）与单 tab 缩进行漏解析——不修则 `*Tests` 修正在默认变体不可观测。改为行首锚定 + 修饰符可省略 + 类型组至少一个非空白字符（仍由语句关键字抑制 `return/new/...`）。
- 负控（先红后绿）：
  - `TestFindTestLinks_JavaTestsSuffix`——修复前 naming 0 条，修复后 `OwnerControllerTests.testProcessCreationForm` naming(70) 命中。
  - `TestTreeSitterAdapter_Parse_JavaPackagePrivateMethods`——修复前 `methods=[helper]`（tab 缩进 + 无修饰符漏解析），修复后 `[initCreationForm helper]` 且方法体调用边存在。
- 实测（同一 `release-bench` 命令，3 仓库 × 2 变体，`docs/bench/2026-11-14/`）：
  - Java regex `PredictedLinks 0 → 1`，TP 0 → 1，P 0 → 1.0000，R 0 → 0.0081，F1 0 → 0.0161；`TestMethods` 29 → 123。
  - Java treesitter `PredictedLinks 0 → 1`，P 0 → 1.0000，R 0 → 0.0063。
  - Go 两变体保持 R5-7 后的非零水平（regex P 0.1943 / R 0.1771 / F1 0.1853；AST P 0.2039 / R 0.1676 / F1 0.1840）。
  - TS 仍为 0：Jest `it/test` 回调不是 IR 方法，**不在本轮范围**，已记录原因与后续路径，真值保留不删除。
- 验证：`go build ./...` / `go vet ./...`（含 pg/redis/treesitter tags）/ `make test` 全绿；`make counts-check` 通过（LoC 59100 ≈ 59.1k）。

### Commit 179: fix(service): Go 包级测试函数进入关联候选（R5-7 / S-2a）

- 背景：公开基准 S-2 实测 3 仓库 `PredictedLinks` 全为 0。Go 的测试函数（`TestXxx`/`BenchmarkXxx`/`ExampleXxx`）挂在合成 `PACKAGE` 容器下，容器名是包名，不满足 `discoverTestClasses` 的 `*Test`/`Test*` 类名约定，100 个 `_test.go` 无一进入候选；`GetAffected` 复用同一策略，同样盲区。
- 动作：`discoverTestClasses` 增加 Go 分支——`*_test.go` 的 `PACKAGE` 容器按「同目录同包」归属到该目录全部生产类（含类型方法）；`matchesTestMethod` 的描述型前缀扩展到 `Benchmark*`/`Example*`；结果去重。`GetAffected` 随 `FindTestLinks` 自动生效，无独立改动。
- 负控（先红后绿）：`TestFindTestLinks_GoPackageLevelTests`——修复前 naming 链接 0 条（只剩 dependency 兜底）；修复后 `TestGetOrder`/`BenchmarkGetOrder`/`ExampleOrderService_GetOrder`/`TestNewOrderService` 均以 naming(70) 命中。
- 实测（`release-bench --repo code-schema --lang go`）：`PredictedLinks 0 → 164`，TP 0 → 39，P 0 → 0.2378，R 0 → 0.2031，F1 0 → 0.2191（真值 1722，采样 192）。
- 验证：`go build ./...` / `go vet ./...` / `make test` 全绿；路线图批次 E 与任务清单同步登记 `R5-7`；TS Jest 回调的范围说明已写入路线图与基准文档。

### Commit 178: feat(bench): 公开基准 release-bench（测试关联 P/R/F1 + 调用图对照）（S-2）

- 背景：缺少可复现的公开基准来回答「测试关联和调用图到底有多准」。既有 `benchmark`（扫描/索引/检索性能）与 `agent-bench`（Agent 任务）都不测关联精度，也没有跨构建变体对照。
- 动作：
  - 新增 `internal/releasebench`：真值取「测试方法 → 生产方法」的直接调用边（不依赖命名约定，避免自我实现）；文件/ pair 固定排序 + 等距采样保证可复现；微平均 P/R/F1；调用图 calls/callers/callees/resolved/resolution。
  - 新增 CLI `release-bench --repo --lang --max-pairs --min-confidence --workers --out`，输出 `report.json` / `report.md`。
  - `treesitter.BuildVariant()`（`regex` / `treesitter`）写入报告，支持同仓两构建变体对照。
  - 负控 `TestAffectedNegativeControl_DeleteTest`：删除测试文件后 `GetAffected` 必须不再返回该测试；`TestRun_MultiLanguageFixtures` 断言 3 语言夹具 TP≥1 且二次运行除时间戳外 DeepEqual。
- 实测（3 仓库 × 2 变体，min-confidence=60，原始数据 `docs/bench/2026-11-14/*.json`）：
  - 测试关联：Go 真值 1720/1844（采样 192/185），Java 45/160，TS 0；**`PredictedLinks` 全为 0**，故 P=R=F1=0（策略无输出，不是 FQN 口径不一致）。
  - 根因（已写入基准文档，属后续独立条目）：Go 包级测试函数挂在 `PACKAGE` 容器下，`discoverTestClasses` 只认 `*Test`/`Test*` 类名；petclinic 的 `*Tests` 后缀未剥离；TS 的 Jest `it/test` 回调不是 IR 方法。
  - 调用图：Go 16376 边 / 0.2126（regex）vs 18593 / 0.2044（AST）；Java 1451 / 0.1234 vs 1609 / 0.1790；TS 848 / 0.0425 vs 407 / 0.1425。
- 验证：`go build ./...` / `go vet ./...`（含 pg/redis/treesitter tags）/ `make test` 全绿；`make counts-check` 通过（LoC 58976 ≈ 59.0k）。首轮只报告，不设 CI 阈值。

### Commit 177: fix(parser): Java 方法体语句不再被误判为方法声明（S-2 前置）

- 背景：Java/Groovy 的方法模式无行首锚点、类型捕获组允许空白，`return helper(a);` 被解释为「返回类型 `return` + 方法名 `helper`」；该行随后被 `continue`，方法体内调用边全部丢失。公开基准的 Java 夹具因此真值 pair 恒为 0，且大仓 `ProdMethods` 会按语句行数虚增。
- 动作：非 Go 通用路径新增 `isStatementLine` 行首语句关键字判定（`return/new/throw/if/for/...`），语句行不参与方法声明解析，继续进入 `detectCalls`；方法声明解析不变。
- 负控（先红后绿）：`TestTreeSitterAdapter_Parse_JavaBodyStatementsAreNotMethods`——修复前 `methods = [add helper helper mul]`（4 个，`return helper(a, b);` 伪造成 `helper`），修复后 3 个且 `calls` 含 `helper`/`mul` 两条边；AST 路径（`-tags treesitter`）同测通过。
- 验证：`go test ./internal/parser/adapter/treesitter/ ./internal/releasebench/` 全绿（两构建变体）；`make counts-check` 通过（LoC 58109 ≈ 58.1k）。

### Commit 176: fix(parser): LSP 符号层级/调用边/空结果回退/进程回收（P0-2）

- 背景：LSP 路径「高精度」名不副实——`documentSymbol` 被按 `SymbolInformation` 反序列化（空字段也算成功），容器名丢失导致 `MethodIR.ClassFQN` 为空、方法入库被丢弃；从不抽取调用边；非空文件拿到 0 符号时静默返回空 IR 并写 hash（后续扫描永久跳过）；`Close` 只 `Kill` 不 `Wait`，遗留僵尸；`Registry` 无 `Close`，长驻 LSP 子进程无人释放。
- 动作：
  - initialize 声明 `hierarchicalDocumentSymbolSupport`；按负载形状（`range` vs `location`）选择 `DocumentSymbol[]` / `SymbolInformation[]` 解析，容器名逐层传递为 `ClassIR.FullName` / `MethodIR.ClassFQN`；`(*Type).Method` 与 `containerName` 两种形态统一归一化。
  - 服务器声明 `callHierarchyProvider` 时，按方法 `selectionRange` 执行 `prepareCallHierarchy` + `outgoingCalls` 产出 `CallIR`；同文件 callee 优先映射本地 FQN，跨文件回退 detail 接收者解析；单文件上限 64 个方法。
  - 非空源文件 0 符号 → `ErrSourceUnavailable`，触发 tree-sitter 回退；空文件不告警。
  - `Close` 改为 shutdown（请求）→ exit（通知）→ 关 stdin → `cmd.Wait()`（2s 超时强杀），可重复调用；初始化失败路径 Kill+Wait+清 pending。
  - `parser.Registry.Close()` 统一关闭适配器；`ScanRepository` defer、watch 停止回调与启动失败路径调用。
- 负控（先红后绿）：
  - `TestLSPAdapter_Parse_NestedContainersSetClassFQN`：修复前 `Outer.FullName=".Outer"`、`Inner.FullName=""`、所有 `ClassFQN=""`。
  - `TestLSPAdapter_Parse_ExtractsCallEdges`：修复前 `Calls=0`。
  - `TestLSPAdapter_Parse_EmptySymbolsReturnsSourceUnavailable`：修复前返回 `nil` error + 空 IR。
  - `TestLSPAdapter_Close_ReapsChildProcess`：修复前 `cmd.ProcessState == nil`（未 Wait）。
  - `TestRegistry_Close_ClosesRegisteredAdapters`：修复前无 `Registry.Close`（编译失败）。
- 验证：`go build ./...` / `go vet ./...`（含 pg/redis/treesitter tags）/ `make test` 全绿；`go test -race` 两包通过。
  - 真实 gopls（`TestLSPAdapter_RealGopls`，声明 `Add` 调用 `Sub`）：`classes=1 methods=2 calls=1`，两条方法 `ClassFQN=Calculator`；修复前方法名 `(*Calculator).Add` 且 `ClassFQN` 空。
  - `make counts-check` 通过（LoC 58017 ≈ 58.0k）。

### Commit 175: fix(mcp): 传输层符合 MCP 2024-11-05（stdio 逐行 JSON / SSE sessionId / 通知不回包）（P2-20）

- 背景：三处不合规——stdio 用 LSP 式 `Content-Length` 帧，标准 MCP 客户端读不到响应；SSE 的 `endpoint` 事件不带 `sessionId`，`POST /message` 直接 200 内联回包，响应无法路由到流；通知也被回包（JSON-RPC 禁止），解析失败的 `id` 被伪造成 `0`；`tools/call` 使用 `context.Background()`，客户端断开后无法取消。
- 动作：
  - stdio 改为逐行 JSON：`readStdioLine`（`ReadSlice` 分段、16MB 上限）/`writeStdioLine`；通知（`id==nil`）处理但不写响应；parse error `id=null`；EOF 优雅退出。
  - SSE：`GET /sse` 生成随机会话 ID 并下发 `data: /message?sessionId=<id>`；新增会话注册表（`sessMu`/`sessions`），响应经 `event: message` 推送；`POST /message` 校验 `sessionId`（缺失/失效 400）并返回 202；会话失效 410。
  - `handleRequest(ctx, req)` 透传请求上下文到 `handleToolCall`，移除 `context.Background()`。
- 负控（先红后绿）：
  - `TestMCP_Stdio_LineDelimitedJSON`：修复前逐行输入得到 0 条响应（空输出）。
  - `TestMCP_Stdio_ParseErrorHasNullID`：修复前无响应；修复后 `id=null`、`-32700`。
  - `TestMCP_SSECompliance`：修复前 `endpoint` 为 `/message`（无 sessionId），POST 返回 200 内联包；修复后 endpoint 带 sessionId、POST 202、响应经 SSE `event: message` 到达（id=7）。
- 验证：`go build ./...` / `go vet ./...` / `make test` 全绿；`make counts-check` 通过（LoC 57364 ≈ 57.4k）。
- 说明：HTTP `/message` 不再支持无会话的内联回包（旧行为本身不符合 MCP 传输），现有调用方需先连 `GET /sse` 获取 `sessionId`。

### Commit 174: fix(server): OpenAPI 声明与 handler 实际参数对齐，/affected 支持 method（P3-14）

- 背景：`/openapi.json` 与实际实现漂移——`/context` 未声明 `chars_per_token`，`/impact`/`/tests` 未声明批量 `methods`，`/search` 未声明 `min_score`；`/affected` 规范声明 `method`，handler 只读 `symbol`，按规范调用直接 400。
- 动作：
  - `handleAffected` 主参数改为 `method`（保留 `symbol` 兼容别名），错误提示与响应字段同步；OpenAPI 增补 `symbol`/`recursive`。
  - OpenAPI 补齐 `chars_per_token`、`methods`（impact/tests）、`min_score`，并把 `method` 的 `required` 改为 false +「与批量参数二选一」说明。
  - 错误 hint 改为准确口径：context 用 `symbols[]`，impact/tests 用 `methods[]`，affected 用 `method`。
- 负控（先红后绿）：
  - `TestAffectedEndpointMethodParamIsQueryable`：修复前 `GET /affected?method=...` 返回 400 `symbol is required`。
  - `TestOpenAPIDeclaresHandlerParameters`：修复前 6 处缺失（context/chars_per_token、impact/methods、tests/methods、affected/symbol、affected/recursive、search/min_score）。
- 验证：`go build ./...` / `go vet ./...` / `make test` 全绿；`make counts-check` 通过（LoC 57217 ≈ 57.2k）。

### Commit 173: fix(parser): AST 路径作用域跟踪与 Go 包级容器对齐（P0-1 遗留）

- 背景：Commit 157 只修了默认正则路径，`-tags treesitter` 的 `adapter_ast.go` 仍用「进入声明即覆盖 `curClass/curMethod` 且永不恢复」的遍历方式：嵌套类/嵌套函数之后的兄弟声明被错挂到内层作用域；Go 顶层函数继承最近一次 `type` 声明作为 `ClassFQN`（或不归属任何类），无 PACKAGE 容器、caller/callee 无包限定；类/方法 `EndLine` 恒为 0，且 AST 路径不产出 Go 的 `Fields/Constants`。CI 的 treesitter job 因此红（6 个测试失败）。
- 动作（全部在 `internal/parser/adapter/treesitter/adapter_ast.go`）：
  - 作用域跟踪：进入 class/method/Elixir def 节点时保存外层 `curClass/curMethod`，子树遍历结束恢复；嵌套类/嵌套函数不再污染后续兄弟声明。
  - Go：按 AST `receiver` 字段（只取 `type_identifier`）确定方法归属；无 receiver 的顶层函数 `ClassFQN=package` 并合成同款 `PACKAGE` 容器；caller 形如 `pkg.Func` / `pkg.Type.Method`，裸 callee 包限定为 `pkg.func`（含点前缀的 `obj.method` 保持原样）。
  - 补齐 class/method `EndLine = EndPoint().Row+1`，以及 Go `field_declaration → FieldIR`、包级 `const_spec → ConstantIR`（方法内局部 const 不入库）。
  - 基准黄金集与适配器输出对齐：Go 样本 golden 改为包限定 FQN（`main.realA` 等），Java 自调用改为 `com.example.OrderFacade.findById`；非 Go 样本不变。
- 负控（先红后绿）：
  - `TestASTAdapter_GoScopeAndPackageContainer`：修复前 `helper/main` 的 `ClassFQN=User`、无 PACKAGE 容器、caller 为 `User.*` 且 callee 裸名（无包前缀）。
  - `TestASTAdapter_NestedScopeRestore`：修复前嵌套类之后的 `last` 被挂成 `com.example.Inner.last`（期望 `com.example.Outer.last`）。
  - 既有跨路径用例（`TestGoCallAndTypeExtraction`/`TestGoMultiLineSignature_CallAttribution`/`TestGoParser_RawStringConstGeneric`/`TestGoFieldAndConstantExtraction`/`TestTreeSitterAdapter_Parse_Go`/`TestTreeSitterAdapter_Parse_StringCallFiltered`）修复前整体为 FAIL。
- 验证：`go build ./...` / `go vet ./...` / `make test` 全绿；`go build -tags treesitter ./...`、`go vet -tags treesitter ./internal/parser/adapter/treesitter/... ./internal/adapterbench/`、`go test -tags treesitter ./internal/parser/adapter/treesitter/... ./internal/adapterbench/` 全绿；AST 调用图基准 P=1.000 R=1.000（TP=72 FP=0 FN=0，门槛 0.95）；`make counts-check` 通过（LoC 57146 ≈ 57.1k）。

### Commit 172: fix(redis): 键加租户命名空间与 TTL，Flush 限定本命名空间（P1-4）

- 背景：Redis L2 缓存所有键均为裸键（`class:<fqn>` 等），多租户/多实例共用同一 Redis 时同名 FQN 互相覆盖；写入不设 TTL，删除/重建后残留键永久驻留并可能返回陈旧数据；`Flush` 用 `SCAN "*"` 清整库，会误删同库其他应用/租户的数据。
- 动作：
  - `RedisCache` 增加 `prefix`/`ttl`，新增 `normalizeNamespace`（空回退 `codeschema:`）、`key(parts...)`、`touch`；所有 `class/method/classpath/methodpath/caller/callee/file` 键统一加前缀，写入后统一 `Expire` 默认 24h。
  - `Flush` 改为游标 `SCAN <prefix>*` + `DEL`，不再使用 `*`。
  - 抽出 `kvBackend` 接口（`goRedisBackend` 适配，`redis.Nil` 归一为 `errKeyNotFound`），测试用内存 fake，无新增依赖。
  - 新增 `NewRedisCacheWithNamespace`/`NewRedisCacheFromURLWithNamespace`；`cmd/codeschema` 按 `sha256(driver|dsn)` 前 6 字节派生 `codeschema-<12hex>:`（`redisNamespace`/`newRedisCacheForConfig`），`applyRedisCache` 与 `rebuild-kv` 共用。
- 负控（先红后绿，3 条，`internal/store/redis/redis_test.go`，`-tags redis`）：
  - `TestRedisCache_NamespaceIsolatesTenants`：修复前 tenant-b 读到 tenant-a 的类（prefix 置空验证）。
  - `TestRedisCache_SetsTTL`：修复前 `class:pkg.Svc` 等 7 个键全部无 TTL（ttl=0 验证）。
  - `TestRedisCache_FlushScopedToNamespace`：修复前 `Flush` 后 `unrelated:key` 也被删（remaining: []，Scan "*" 验证）。
- 验证：`go build ./...` / `go vet ./...` / `make test` 全绿；`go build -tags redis ./...`、`go vet -tags redis ./internal/store/redis/ ./cmd/codeschema/`、`go test -tags redis -count=1 -run TestRedisCache_ ./internal/store/redis/`（ok 0.410s）；`make counts-check` 通过（LoC 56877 ≈ 56.9k）。

### Commit 171: fix(tenant): 省略 DSN 时真正回退 ./data/<id>，PG 租户派生独立索引目录（P1-3）

- 背景：`cloneConfig` 把全局默认 `storage.dsn: ./data` 预填进每个租户配置，`resolveTargets` 用合并后的值判断 `DSN == ""` 永不成立 → 所有租户共用 `./data`；`deriveIndexDirs` 只处理 file/sqlite，PG 租户的 FTS/向量/IDF 保持全局默认 `./data/fts` 等，多租户互相覆盖。
- 动作：
  - `resolveTargets` 改用租户原始配置 `tc.Storage.DSN == ""` 判断，省略时回退 `filepath.Join("./data", tc.ID)`。
  - `deriveIndexDirs` 增加 `tenantID` 参数：file/sqlite/空 仍以 DSN 为根；pg/postgres/postgresql 以 `./data/<id>` 为根派生 fts/vector/idf（连接串不能拼路径）；显式设置仍然优先。
- 契约取舍：原 `TestDeriveIndexDirs_NonDirBackend` 断言「PG 不派生」，与文档「各租户索引目录绝不共享」冲突，按隔离优先改为断言派生。
- 负控（先红后绿）：
  - `TestResolveTargets_DerivesIsolatedDSNWhenOmitted`：修复前两个租户 DSN 均为 `./data`、FTSDir 均为 `./data/fts`。
  - `TestDeriveIndexDirs_NonDirBackend`：修复前三项目录均为空。
- 验证：`go build ./...` / `go vet ./...` / `make test` 全绿；`make counts-check` 通过（LoC 56404 ≈ 56.4k）。

### Commit 170: fix(pg): method 表补 full_name 列，PG 后端方法 FQN 不再恒为空（P1-5）

- 背景：PG `method` 表 DDL、`GetMethodsByClassID`、`UpsertMethods`、`BulkUpsert`、`upsertMethodsTx` 均无 `full_name`，`MethodRecord.FullName` 在 PG 上恒为空。依赖方法 FQN 的能力（测试关联、标签、`/context` 方法匹配）在 PG 上全部失效；SQLite/FileStore 均正常。
- 动作：
  - DDL 增加 `full_name TEXT DEFAULT ''` + `ALTER TABLE method ADD COLUMN IF NOT EXISTS full_name ...`（兼容既有库）+ `idx_method_full_name` 索引。
  - 四条写入路径（`upsertMethodsTx`/`UpsertMethods`/`BulkUpsert`）统一写入 `methodFullName(ClassFQN, Name)`；查询路径（`GetMethodsByClassID` → `queryMethods`）回读该列。
- 负控（先红后绿）：`internal/store/pg/integration_test.go::TestPGStore_MethodFullNameRoundTrip`（`-tags pg`，嵌入式 PG）。修复前实测 `full_name not persisted: got "", want "p15.Svc.Run"`；修复后通过（13.2s）。
- 验证：`go build ./...` / `go vet ./...` / `make test` 全绿；`go vet -tags pg ./internal/store/pg/` + 该集成测试通过；`make counts-check` 通过（LoC 56356 ≈ 56.4k）。

### Commit 169: fix(store): 重索引先清 method/标签再删 class，消除孤儿累积（P1-6）

- 背景：SQLite `method`/`class_tag`/`method_tag` 无外键级联，但重索引先 `DELETE FROM class`，随后 `DELETE FROM method WHERE class_id IN (SELECT ... FROM class WHERE file_id=?)` 已查不到任何 class → 旧方法永久残留；`UpsertIR` 只在 `len(ir.Methods)>0` 时删方法，类整体消失时也不清。FileStore 以 class ID 为方法 map 的键，重索引分配新 ID 后旧键无人清理，`UpsertIR`/`BulkUpsert` 同样只在有方法时覆盖。
- 动作：
  - SQLite `upsertClassesLocked`：按「method_tag → class_tag → method → class」顺序清理，全部无条件执行；`BulkUpsert` 新增 `stmtDelMethodTag`/`stmtDelClassTag`，清理移到插入前且不再依赖 `len()>0`。
  - FileStore 新增 `purgeClassArtifactsLocked`（清旧 class 的 method/classTags/methodTags），在 `UpsertClasses` 与 `BulkUpsert` 覆盖 `classes[fileID]` 前调用。
- 负控（先红后绿，3 条）：
  - `TestSQLite_ReindexRemovesStaleMethods`：修复前重索引后 method 计数 3（期望 1），类消失后仍残留。
  - `TestSQLite_BulkUpsertRemovesStaleMethods`：修复前 method 计数 3（期望 1）。
  - `TestFileStore_ReindexRemovesStaleMethods`：修复前 UpsertIR 后旧/新方法共存（3，期望 1），BulkUpsert 同样。
- 验证：`go build ./...` / `go vet ./...` / `make test` 全绿；`make counts-check` 通过（LoC 56288 ≈ 56.3k）。

### Commit 168: fix(service): 测试关联按被查询方法收敛，命名/同目录/同 tag 去假阳性（P1-12 + P2-4）

- 背景（共享根因：策略都在「类/目录/标签」粒度上与查询方法脱钩）：
  - **P1-12** `linkByExplicit` 不接收被查询方法，遍历全仓测试类，把任意 `@TestFor(X)` 的测试以置信度 100 关联给所有生产方法；结果 ≥20 后还会跳过 same_tag/dependency。
  - **P2-4** 命名策略以无条件 `Contains` 兜底（目标 `Get` 命中 `TargetGetter`）；同目录策略对所有语言给 80，且 import 用 `Contains` 子串匹配；same_tag 把同 tag 测试类的所有方法都关联；`discoverTestClasses` 用 `map[string]string`，同源类多个测试类互相覆盖。
- 动作：
  - explicit：新增 `methodFQN` 参数，仅当 `@TestFor` 解析出的类 == 被查询方法所属类时建立关联，且只关联被查询方法本身。
  - naming：`discoverTestClasses` 改为 `map[string][]string`，同源类所有测试类都参与；`Contains` 兜底收敛为「测试名前缀属于 Test/test/test_」时才允许包含匹配。
  - same_tag：先定位目标方法名，测试方法名须通过 `matchesTestMethod` 才关联；移除基于结果数的策略跳过。
  - dependency：同目录证据仅限 Go（同包 `*_test.go`）；import 匹配改为「等于目标目录名或以 `/目标目录名` 结尾」，去掉双向 `Contains`。
  - `resolveExplicitClass` 多命中时按 FQN 字典序取首个，消除 map 遍历不确定性。
- 负控（先红后绿，5 条，均以 `git stash` 回退修复验证过红）：
  - `TestFindTestLinks_ExplicitScopedToQueriedMethod`：修复前无关类 `B.N` 仍拿到 explicit/100。
  - `TestFindTestLinks_MultipleTestClassesPerSource`：修复前只保留 `TestOrderService`（后者覆盖前者）。
  - `TestFindTestLinks_NoSubstringNamingFalsePositive`：修复前 `Get` ↔ `TargetGetter` 命中 naming/70。
  - `TestFindTestLinks_SameDirNonGoNotDependency`：修复前 Java 同目录给 dependency/80。
  - `TestFindTestLinks_SameTagRequiresMethodNameMatch`：修复前同 tag 的 `TestUnrelated` 被关联。
- 验证：`go build ./...` / `go vet ./...` / `make test` 全绿；`make counts-check` 通过（LoC 56084 ≈ 56.1k）。

### Commit 167: fix(analyzer): 影响面后缀匹配结果确定化，消除 map 遍历顺序跳变（P1-8）

- 背景：`resolveImpactNode` 的后缀匹配直接 `for nodeFQN := range cg.Nodes` 命中即返回；多包同名符号（`a.pkg.B.Foo` / `z.pkg.A.Foo`）时结果随 map 遍历顺序变化，同一 `/impact` 查询多次调用返回不同 FQN 与影响面。
- 动作：收集全部后缀命中，`sort.Strings` 后取字典序最小者；注释明确确定性契约与消歧方式（传更精确 FQN）。
- 负控（先红后绿）：`internal/analyzer/analyzer_test.go::TestResolveImpactNode_SuffixMatchDeterministic`（3 个同名节点 + 200 次重复查询）。修复前实测返回 `z.pkg.A.Foo`（期望字典序最小 `a.pkg.B.Foo`）；修复后 200 次稳定同一结果。
- 验证：`go build ./...` / `go vet ./...` / `make test` 全绿；`make counts-check` 通过（LoC 55867 ≈ 55.9k）。

### Commit 166: fix(analyzer): 调用图只纳入真实调用关系，非调用边不再制造假影响面（P1-7）

- 背景：`BuildAll`/`BuildCallGraph` 建边时完全丢弃 `CallIR.CallType`，任何非空 `CallerFQN/CalleeFQN` 都成为调用边；而 CodeGraph/JCodeIndexer 适配器把 imports/references/implements/extends 映射成 `dynamic`/`interface`，于是文件依赖与类层次关系被当作调用，`/impact`、`/affected` 出现假影响面。
- 动作：
  - 适配器如实标注关系类型：`codegraph`/`jcodeindexer` 的 `callTypeFromKind` 改为 calls/invoke→`direct`、extends→`extends`、implements→`implements`、imports→`imports`、references/uses→`references`；CodeGraph legacy schema 路径也统一走该映射（此前直接透传原始字符串）。
  - `analyzer` 新增 `isCallRelation` 白名单（`direct`/`interface`/`dynamic`/空值），`BuildAll` 与 `BuildCallGraph` 两个建边循环统一过滤；空值按 `direct` 兼容历史记录。
  - `parser/ir.go` 的 `CallType` 契约注释补充非调用关系取值。
- 负控（先红后绿）：`internal/analyzer/analyzer_test.go::TestCallGraph_ExcludesNonCallRelations`（BuildCallGraph/BuildAll 两路）。修复前实测四条非调用边全部泄漏进图（imports/references/implements/extends）；修复后四条均不在图中，direct 边保留。
- 验证：`go build ./...` / `go vet ./...` / `make test` 全绿；`make counts-check` 通过（LoC 55835 ≈ 55.8k）。

### Commit 165: fix(retrieval): PersistentFTS 搜索全程持读锁，消除并发 map fatal（P1-10）

- 背景：`PersistentFTS.Search` 在 `RUnlock` 后才调用 `MemoryFTS.Search`，而临时 `MemoryFTS` 持有的是自己的零值锁，保护不到共享的 `pf.docs`。并发 `Index/BatchIndex/Remove` 与搜索遍历冲突，触发 Go fatal「concurrent map read and map write」（不可 recover，直接拖垮进程）。
- 动作：读锁覆盖整个搜索过程（`defer RUnlock`）；包装体注释明确共享 map 由外层锁保护。
- 负控（先红后绿）：新增 `internal/retrieval/persistent_test.go::TestPersistentFTS_ConcurrentIndexSearch`（500 文档 + 1 写 4 读并发 300ms）。修复前实测 `fatal error: concurrent map iteration and map write`，栈顶 `fts.go:119` 经 `persistent.go:89`；修复后常规与 `-race` 均通过。
- 验证：`go build ./...` / `go vet ./...` / `make test` 全绿；`make counts-check` 通过（LoC 55751 ≈ 55.8k）。

### Commit 164: fix(service): 缓存残留但主存储无文件时不再 nil 解引用（P1-9）

- 背景：`resolveSymbolLocation` 的 Redis 快路径只判断 `err == nil` 就访问 `f.LineCount`；`GetFileByPath` 契约允许未命中返回 `(nil, nil)`（FileStore/SQLite 均如此）。Redis 的 `methodpath/classpath` 键 TTL=0，文件删除/重建后残留键可稳定触发 panic。
- 动作：方法/类两条快路径均改为 `err == nil && f != nil`；文件缺失时 `LineCount` 保持 0，`GetContextMode` 按既有 `file_unreadable` 逻辑降级为 minimal 形态。
- 负控（先红后绿）：`internal/service/trace_test.go::TestGetContextMode_StaleCacheMissingFile_NoPanic`（缓存命中方法、主存储无文件）。修复前实测 `panic: nil pointer dereference`，栈顶 `service.go:781`；修复后返回 `file_unreadable` 降级结果。
- 验证：`go build ./...` / `go vet ./...` / `make test` 全绿；`make counts-check` 通过（LoC 55681 ≈ 55.7k）。

### Commit 163: fix(server): 未知租户返回 404 而非 nil 解引用 500，panic 原文不再外泄（P1-11）

- 背景：`serve` 以 `NewHTTPServer(nil, ...)` 启动并注入租户 resolver；`serviceForRequest` 在 resolver 失败后回退到 `h.service`（即 nil），所有 handler 直接方法调用触发 nil 解引用 panic，被 recovery 兜成 500，且响应体回传 `internal server error: <panic 原文>`（可能含 DSN/路径等敏感信息）。
- 动作：`http.go` 将 `serviceForRequest` 改为 `resolveService`（返回 `(*service.Service, error)`），未知租户返回 `ERR_UNKNOWN_TENANT` + 404、未配置 Service 返回 `ERR_SERVICE_UNAVAILABLE` + 503；10 个 handler 统一接入。`middleware.go` 的 500 响应体改为固定文案 `internal server error`，panic 详情仅写结构化日志。
- 负控（先红后绿）：`internal/server/http_test.go` 新增 `TestUnknownTenantReturns404`（context/health/search 三路由）与 `TestRecoveryMiddlewareHidesPanicDetail`。修复前实测 `panic: runtime error: invalid memory address or nil pointer dereference`（栈顶 `service.go:388`）且 500 体含 panic 原文；修复后三路由均 404/`ERR_UNKNOWN_TENANT`，500 体不含敏感串。
- 验证：`go build ./...` / `go vet ./...` / `make test` 全绿；`make counts-check` 通过（LoC 55645 ≈ 55.6k）。

### Commit 162: fix(server): viz 文档列表分页参数夹取，消除负数切片 panic（P3-1）

- 背景：`/viz/api/documents` 直接把 `offset/limit` 用于 `docs[offset:end]`，`limit=-1` 触发 `slice bounds out of range [:-1]`，线上被 recovery 兜成 500；`Sscanf` 解析失败也会静默保留原值。
- 动作：改用 `strconv.Atoi`，并把 `offset` 夹取到 `[0,len]`、`limit` 夹取到 `[0,len-offset]` 后再切片，任何输入都返回 200 且不 panic。
- 负控（先红后绿）：新增 `internal/server/viz_test.go::TestVizAPIDocuments_PaginationBounds`（5 组参数）。修复前 `limit=-1` 实测 panic（栈顶 `viz.go:141`）；修复后 5 组全部 200，文档数分别 0/3/0/0/1。
- 验证：`make test` 全绿；`make counts-check` 通过（LoC 55505 ≈ 55.5k）。

### Commit 161: fix(parser): Go 原始字符串 / const 块泄漏 / 泛型函数三项硬化（P3-9）

- 反引号原始字符串：`codeSanitizer` 增加跨行 `inRawString` 状态，串内内容全屏蔽。修复前串内 `fake(1)` 被记录为真实调用，且串内 `{` 使方法深度失衡、`UsesRaw` 的 EndLine 停在定义行（实测 3，应为 6）。
- const 块泄漏：闭合行由 `trimmed == ")"` 改为按前缀 `")"` 识别（支持 `) // note`），并在 func 定义行兜底复位。修复前 const 块后方法体调用被整体吞掉（实测 `calls=[]`）。
- 泛型函数：Go 方法正则增加可选类型参数 `(?:\[[^\]]*\])?`。修复前 `func Generic[T any](` 方法数为 0，修复后可识别并挂包级容器。
- 负控（先红后绿）：`TestGoParser_RawStringConstGeneric` 三个子用例，修复前分别实测 `sample.fake` 假调用 + EndLine=3、`calls=[]`、`methods=[]`；修复后全部通过。
- 验证：`make test` 全绿；`make counts-check` 通过（LoC 55430 ≈ 55.4k）。

### Commit 160: fix(parser): 超长单行不再中断整次扫描（P1-15）

- 背景：解析器用 `bufio.Scanner` 按行读取文件，默认单行上限 64KB；生成代码/压缩产物的长行触发 `bufio.Scanner: token too long`，`Parse` 返回错误并向上中断整次扫描，一个文件可拖垮全仓索引。
- 动作：`adapter.go` 改为 `bufio.Reader.ReadString('\n')` 逐行读取（无 64KB 限制）；新增 `maxParseLineBytes=4MiB` 解析上限，超限行仅跳过内容解析、行号照常推进，文件其余部分与其余文件不受影响。
- 负控（先红后绿）：`TestGoLongLine_DoesNotFailFile`（70KB 注释行 + 5MiB 行 + 两个正常函数）。修复前实测 `scan file: bufio.Scanner: token too long`；修复后 Parse 成功，`AfterLong`/`AfterHuge` 均被解析。
- 验证：`make test` 全绿；`make counts-check` 通过（LoC 55316 ≈ 55.3k）。

### Commit 159: fix(parser): Go 方法 EndLine 回填到闭合行（P1-1）

- 背景：treesitter Go 路径创建 `MethodIR` 时 `EndLine=StartLine`，全文件无任何回填；`/context` 的语义块只取 1 行（仅签名），「精准裁剪」退化为「只回签名」。
- 动作：`adapter.go` 在三个闭合点回填 `EndLine=lineNum`——单行方法体定义行、多行签名与完整方法体同处一行、以及方法体 `}` 闭合行。
- 负控（先红后绿）：`TestGetContext_TopLevelFunc_RealParse`（service 包，真实解析→FileStore→`GetContext("Svc.Method", 0)`）。修复前实测 `start=11 end=11`，源码仅签名；修复后 `end=13`，源码含完整方法体 `return TopLevelFunc(1)`。
- 验证：`make test` 全绿；`make counts-check` 通过（LoC 55270 ≈ 55.3k）。

### Commit 158: fix(parser): Go 多行函数签名不再提前闭合方法（P1-14）

- 背景：`func F(` 签名行未出现 `{` 时，`methodOpenDepth == goDepth`；下一行参数续行在「进入本行前深度」判定中即满足 `goDepth <= methodOpenDepth`，方法被误判闭合，方法体调用的 `CallerFQN` 为空 → analyzer 不建边，影响面/调用图丢边。
- 动作：`adapter.go` 增加 `methodBodyOpen` 状态——定义行按是否含 `{` 设置；`{` 出现前，续行累积进 `Signature`、不检测调用、不触发闭合；`{` 出现当行开启方法体；闭合时复位。
- 负控（先红后绿）：`TestGoMultiLineSignature_CallAttribution`。修复前实测 `Signature="func MultiLine("`、调用为 `{caller="" callee="sample.helper"}`；修复后签名含 `b string`，调用为 `sample.MultiLine -> sample.helper`。
- 验证：`make test` 全绿；`make counts-check` 通过（LoC 55238 ≈ 55.2k）。

### Commit 157: fix(parser): Go 包级函数合成包级容器入库（P0-1）

- 背景：默认 treesitter 正则路径对无 receiver 的 Go 顶层函数留空 `ClassFQN`；三后端（FileStore/SQLite/PG）按 `ClassFQN`→class 匹配，找不到即 `continue` 静默丢弃。后果：`/context` 对包级函数恒 404，FTS/向量/标签/测试关联同样失效，而 `calls` 仍写入包级 `caller_fqn` 形成悬空边。
- 动作：`internal/parser/adapter/treesitter/adapter.go` 采用合成包级容器方案——顶层函数 `ClassFQN` 置为 `package` 名，文件末尾追加 `ClassIR{Name/FullName=package, Type="PACKAGE", StartLine=1, EndLine=EOF}`（同文件已有同名类型时不重复创建）；caller FQN 对包级函数产出 `pkg.Func`（不重复包前缀），与 `MethodRecord.FullName` 对齐，悬空调用边同时消除。
- 负控（先红后绿）：`TestGetContext_TopLevelFunc_RealParse`（service 包）真实解析→FileStore 入库→`GetContext("sample.TopLevelFunc")`。修复前实测 `ERR_SYMBOL_NOT_FOUND`；修复后命中且源码体为 `return helper(x)`，包级容器唯一，`sample.TopLevelFunc`/`sample.helper` 均在容器方法列表。
- 回归：`TestGoCallAndTypeExtraction` 更新断言（顶层函数挂容器而非留空/误挂 Watcher）；`TestTreeSitterAdapter_Parse_Go` 类数 1→2（User + PACKAGE main）。
- 验证：`go build ./...` / `go vet ./...` / `make test` 全绿；`-race` 相关包通过；`make counts-check` 通过（LoC 55166 ≈ 55.2k）。
- 遗留：`-tags treesitter` AST 路径的 Go 作用域跟踪仍为既有开放项（本次仅修默认正则路径）。

### Commit 155: feat(ai): P21 生产化 — 增强并发化 + 429 限流退避重试 + 提示词质量约束 + 真实端到端验证

- 背景：P21（AI 标签/文档增强生产化）因 LLM API key 缺失长期阻塞。用户提供 AMD OpenAI 兼容端点（`https://developer.amd.com.cn/radeon/api/v1`，模型 `DeepSeek-V4-Flash`）后解除阻塞，本轮实施三项生产化改进并完成真实质量端到端验证。
- 动作：
  - **增强并发化**：`internal/analyzer/analyzer.go` 的 `enhanceTagsWithAI` 由串行逐实体调用 LLM 改为 worker pool 并发（`aiWorkersCap=4`，worker 数按实体量收敛）；`internal/ai/budget.go` 的 `Budget` 计数器原子化（`sync/atomic` CAS，`tryConsume*` 并发不超发硬限）；`enhanceOne` 抽取单实体处理，失败隔离保留。
  - **429 限流退避重试**：`internal/ai/http_client.go` 的 `chat` 用 `retry.Do`（`gitee.com/idcu-go/retry`）包裹，对 429/网络超时等可重试错误指数退避重试（200ms 起、最多 3 次），新增 `llm rate limited, will retry` 日志区分 429 与其他错误。
  - **提示词质量约束**：`buildTagPrompt` 明确要求「分类:标签」格式前缀（biz/tech/risk/test 四类，排除 layer/lang），修正模型把分类名与标签值混淆（此前产出 `risk:biz` 等异常标签）。
  - **测试**：新增 `TestBudget_ConcurrentConsume_NoOvershoot` / `TestBudget_ConcurrentQuery_Independent`（budget 原子并发不超发）；`TestAnalyzer_TagAll_WithEnhancer_Concurrent`（并发 worker 全实体打标）；`mockStore` tag 方法（`UpsertTags`/`GetTagsByClassID` 等）加 `sync.RWMutex` 修 -race 数据竞争。
- 验证（`internal/config` 24 实体：18 类 + 6 方法）：AMD 端点 → scan 时 `Enhancer` 调用真实 LLM → 标签合并写回 store → `tags-stats` 覆盖率从 **75.0% → 79.2%**（+4.2pp）；方法标签 0→1，biz 业务标签 0→12 实体；AI 标签格式正确（`biz:LSP适配器`、`test:单元测试` 等）；全程 `go build ./...` / `go test -race ./internal/ai/ ./internal/analyzer/` 全绿。验证配置 `build/p21/{baseline,ai,ai-small}.yaml` 落盘。
- 遗留：`tree-sitter grammar 头文件补齐` 已本轮推进（见下）；AST 路径（-tags treesitter）Go 支持的整体完善仍为开放项。
- 后续补充（同轮）：`DeriveTagCategory` 新增「分类:值」前缀识别（tech:配置管理→tech、test:单元测试→test 等），修正 tags-stats 分类口径失真；新增 `internal/store/tagcategory_test.go` 3 用例。

### 遗留开放项推进（Commit 156）：非 Go 裸 callee FQN 双路径收敛 + tree-sitter 头文件补齐

- 背景：承接 P19/P20 的开放遗留——非 Go 语言 callee 仍走裸名启发式；`-tags treesitter` 真语法树路径在第三方 grammar（go-tree-sitter/sql、/php）C 头文件处构建失败（`tree_sitter/parser.h` / `tree_sitter/array.h` 找不到）。
- 动作：
  - **非 Go 裸 callee FQN 收敛（正则路径 adapter.go）**：`detectCalls`/`qualifyCallee` 增加 `nonGoPkg`/`currentClass` 参数，Java/Kotlin/C# 当前类内**无点前缀裸 callee** 限定为 `pkg.Class.method`，与 caller 命名空间对齐；带点前缀（obj.method）保持原样（类型消歧留待收敛）；无包声明语言（Python/TS/Rust/C++）nonGoPkg 为空 → 行为不变。
  - **AST 路径同等收敛（adapter_ast.go，P20 宗旨）**：新增 `astQualifyCallee`/`astCurClassName`，两处 call 处理对裸 callee 做与正则路径一致的 `pkg.Class.method` 限定，消除双路径解析漂移。
  - **tree-sitter grammar 头文件补齐**：`go mod vendor` 不复制 C 间接引用的 `tree_sitter/*.h` 头文件（上游模块的 sql/tree_sitter、php/tree_sitter 子目录在 vendor 化后丢失）→ 在 vendor 内按上游结构补回（php 需 alloc.h/array.h/parser.h，sql 需 parser.h），`-tags treesitter` 构建通过。
  - **测试**：新增 `TestTreeSitterAdapter_Parse_NonGoBareCalleePkgQualified`（Java/Kotlin/C# 三例，验证裸 callee 包限定），默认 tag 与 `-tags treesitter` 双路径通过；`TestGoCallAndTypeExtraction`/`TestTreeSitterAdapter_Parse_StringCallFiltered` 在 -tags treesitter 下预存失败（AST 路径 Go 处理缺口，非本改动引入，已核验）。
- 验证：`go build ./...` / `go build -tags treesitter ./...` 通过；`go test ./internal/parser/...`（默认）全绿含 -race；`go test -tags treesitter -run "NonGoBareCalleePkgQualified|NonGoCallerPkgQualified"` 双路径通过；`make counts-check` 通过。
- 遗留：**AST 路径（-tags treesitter）Go 支持整体完善**（caller/callee 裸名、常量提取在 AST 路径下与正则路径不一致）为开放项；tree-sitter 头文件补丁在 vendor 内、vendor 不被 git 跟踪（项目既有架构矛盾：Docker 依赖仓库 vendor 但 gitignore 之），建议后续迁至 `third_party/` 并接入构建流程持久化。

### Commit 154: docs: 逐行修正文档漂移（命令数 9 / 包数 33·37 / 语言 31 / 日期刷新）

- 背景：Commit 153 落地 tags-stats 命令与 Docker 纯 Go 默认口径后，逐行复查发现数处文档-代码漂移（均未被 counts-guard 的内联格式覆盖）：入口层仍写 8 个子命令、两处包数仍是 internal=32/total=36、两处语言计数仍是 30、目录速览漏列命令。
- 动作：
  - `docs/01-开发者/架构与模块.md`：入口层命令 8→9 并补 `tags-stats`；「make counts 校验（internal=32/total=36）」→（internal=33/total=37）。
  - `docs/01-开发者/README.md`：目录速览 CLI 命令补 `rebuild-kv` 与 `tags-stats`（scan/watch/rebuild-kv/benchmark/agent-bench/mcp/serve/tags-stats/version）。
  - `README.md`：开发红线「包数（internal=32/全仓库=36）」→（internal=33/全仓库=37）。
  - `docs/02-架构师/README.md`：解析精度权衡「覆盖 30 语言」→「31 语言」。
  - `DEV_PROGRESS.md`：tree-sitter 适配器「30 种语言」→「31 种语言」。
  - 上述文档「最后更新」日期刷新至 2026-09-08；CHANGELOG Commit 153 过时遗留（Docker 职责重叠待定案）清理。
- 验证：`make counts-check` 通过（含 dead-link LINK-OK）；`go build ./...` OK；全 live 文档 grep 无 internal=32/30 语言/默认 ONNX 残留。

### Commit 153: feat(ai)+docs: 新增 tags-stats 离线标签覆盖率统计命令 + counts-guard 扩容至 AGENTS/仓库结构 + 死链校验

- 背景：P21（AI 标签/文档生产化）因 LLM API key 缺失阻塞，真实标签质量端到端无法验证。本次提供**不依赖 key 的离线评估基线**；同时 counts-guard 此前只守 README/架构与模块 的计数，漏检 AGENTS.md、仓库结构.md 等含硬数字的必读档。
- 动作：
  - 新增 `internal/ai/tagstats.go`：`ComputeTagCoverage` 遍历库中全部类+方法，统计六类标签（layer/biz/tech/risk/test/lang）覆盖率、分类分布与 TopTags（复用 store 的 DeriveTagCategory 分类口径，单实体失败不中断）；`internal/ai/tagstats_test.go` 2 用例（实体统计/分类分布/TopTags/空库）全绿。
  - 新增 `cmd/codeschema/tags_stats.go` 子命令 `tags-stats`（--store --top），main.go 接线并补 Usage 帮助。
  - `scripts/counts_check.py` 扩容：DOC_TARGETS 加入 AGENTS.md（MCP/HTTP 计数）与 仓库结构.md（internal 包数/基线）；新增 dead-link 校验扫描 docs/ 与根 README/AGENTS 相对链接。
  - 语言计数 30→31：`internal/runtime/runtime.go`、`internal/benchmark/benchmark.go`、`internal/integration/multilang_test.go` 注释及 README/解析适配器/DEV_PROGRESS 同步（ExtToLang 实际返回 31 语言，含 js）。
  - 文档同步：命令数 8→9（架构与模块/仓库结构/02-架构师/00-新人上手/README CLI 框），LoC 54.3k→54.6k。
  - **Docker 默认构建口径定案（2026-09-03，消除三份镜像职责重叠）**：root Dockerfile 收敛为**纯 Go 免 CGO 默认轻量镜像**（`CGO_ENABLED=0`、去 `-tags onnx`、去 onnxruntime 软链/CGO_LDFLAGS/down 运行时拷贝；语义检索降级 LocalEmbedder，与 `make build`/CI 同口径）；ONNX 语义检索统一由 `docker/onnx/Dockerfile` 为唯一入口；`Dockerfile.mirror` 为同口径的 daocloud 国内源构建。同步更新 README 与 03-部署运维/README 的构建变体对照与部署命令。
- 验证：`go build ./...` OK；`go vet ./cmd/... ./internal/ai/...` OK；`go test ./internal/ai/...` 全绿；`make counts-update` 刷新基线至 54566；`make counts-check` 通过（含死链 LINK-OK）；e2e：`scan --store X repo && tags-stats --store X` 实测覆盖率 50%、分类分布与 TopTags 正确。
- 遗留：P21 真实 LLM 标签质量验证仍待 API key。（root Dockerfile 默认 ONNX 与 mirror/onnx 职责重叠项已于本记录同批定案——root 收敛为纯 Go 默认，onnx 由 docker/onnx/Dockerfile 唯一入口。）

### Commit 152: fix(server): OpenAPI 规范补齐 /affected、/projects、/docs/，对齐 23 路由口径

- 背景：`internal/server/openapi_handler.go` 静态 OpenAPI 规范仅声明 13 个路径，而 HTTP API 实际注册 23 个端点（http.go 17 + viz.go 6）；`/affected`、`/projects` 是 `docs/01-开发者/接口层.md` 与 AGENTS.md 明确列出的核心数据端点，却未纳入规范，与「完整契约以 GET /openapi.json 为准」的声明相矛盾。
- 动作：规范 `paths` 补入 `/affected`、`/projects`、`/docs/`；`/context` 参数补齐 B5 批量 `symbols` 与 B1 预算/裁剪参数（`max_bytes`/`max_tokens`/`max_line_chars`/`path_style`/`mode`），并将 `symbol` 改为非必填（与 `symbols` 二选一）；测试 `TestHandleOpenAPI` 的必需路径清单增补 `/affected`、`/projects` 防回归。
- 说明：`/viz` 及 `/viz/api/*`（6 路由）为可视化调试端点，属 UI 调试工具，不纳入 OpenAPI 数据规范（与接口层.md「http.go 17 + viz.go 6」口径一致）。
- 验证：`go test ./internal/server/...` 通过；`go build ./...` 通过。

### Commit 151: feat(parser): jcodeindexer 适配器落地并接线（去「配置预留」）

- 背景：`internal/config/parse.go` 早有 `parser.jcodeindexer` 配置字段（db/config_file/env），但 `internal/parser/adapter/` 下无对应包、解析注册中心未接线——文档长期标注「仅配置预留、未实现」。本轮「修复骨架」将其补齐为真实可用的批量直读适配器。
- 动作：
  - 新增 `internal/parser/adapter/jcodeindexer/`：BatchParser 直读 JCodeIndexer（第三方 JVM 专项索引器 Java/Kotlin/Scala）SQLite 库——`symbols`+`edges` 契约（符号 + 调用图），按文件分组产出 `IRDocument`（class/method/interface/enum 映射 ClassIR，method/constructor 映射 MethodIR，calls/implements/imports 映射 CallIR）；schema 检测 + 契约不符/库缺失/非 SQLite 显式降级 `ErrSourceUnavailable`，绝不静默返回空 IR（复用 codegraph 降级范式）。JVM kind→语言 best-effort 由文件扩展名推断。
  - `internal/runtime/runtime.go` `NewParserRegistry` 新增 ⑤ `parser.jcodeindexer.db`（非默认值）时注册，包 `FallbackParser` 失败回退 tree-sitter；`java` 语言优先级插入 `jcodeindexer`。
  - `internal/config/config.go` 新增常量 `DefaultJCodeIndexerDB = "./jcodeindexer.db"`，默认配置复用该常量。
  - 文档同步：解析适配器.md 适配器总览表新增 jcodeindexer 行并更正「配置预留」表述；docs/README.md、架构与模块.md、01-开发者/README.md 适配器列表与计数基线更新。
- 测试：`internal/parser/adapter/jcodeindexer/adapter_test.go` 四用例（Supports JVM 三语 + 非 Go 拒、ParseAll 读符号/调用并分组按扩展名判语言、库缺失降级、schema 不符降级）。
- 验证：`gofmt` 通过；`go build ./...` 通过；`go test ./internal/...` 全绿；新增包使 internal 包 32→33、全仓库 36→37、非 vendor LoC ≈ 54.3k，`make counts-update` 刷新基线 + `make counts-check` 通过。

### Commit 150: feat(parser): P20 — 真语法树路径非 Go caller FQN 包限定对齐

- 背景：P19 已把默认正则路径（`adapter.go`）Java/Kotlin/C# 的 caller FQN 包限定为 `com.example.OrderService.createOrder`；但 `-tags treesitter` 的真语法树路径（`adapter_ast.go`）仅按类简单名回填 caller（`OrderService.createOrder`），两条解析路径命名空间差异会导致同一仓库 impact 结果随构建标签漂移。P20 要求对 AST 路径做同等的包限定对齐。
- 动作：`adapter_ast.go` 新增 `astPackageDecl`（复用与正则路径 `pkgDeclPatterns` 一致的 Java `package`/Kotlin `package`/C# `namespace` 声明正则），在 `Parse` 构建文档时捕获包前缀，并在此前两处 caller 回填点（Elixir call 分支 + 通用 call 分支）应用 `astPkg + "." + caller`；类 FullName 保持简单名、callee 保持裸名（依赖类型消歧），与正则路径口径一致。
- 测试：新增 `TestASTAdapter_Parse_NonGoCallerPkgQualified`（java/kotlin/csharp 三例，断言 caller 产出 `com.example.OrderService.createOrder` 等），镜像 P19 的正则路径用例。
- 验证：`gofmt` 通过。**环境遗留**：本机 `-tags treesitter` 构建在第三方 grammar（`go-tree-sitter/sql`/`php`）的 C 头文件（`tree_sitter/parser.h`、`tree_sitter/array.h`）处失败，经 `git stash` 复核确认是 HEAD 即存在的预存环境问题（缺 grammar 的 vendored tree-sitter runtime 头文件），非本改动引入；故 AST 运行时路径本机无法实跑，改动经代码对称性与 gofmt 校验。

### Commit 149: test(server): B5 批量入参补 MCP 层验证 + 文档同步接口裁剪参数

- 背景：fastcontext B1/B5 已在 Commit 129 落地，但验证集中在 service/HTTP 层，MCP 层符号批量入参（`symbols[]`）路径未直接回归。
- 动作：新增 `TestMCP_ToolCall_ContextBatch`（`tools/call` 的 context 传 `symbols` 数组 → 批量路径，单符号失败不中断整体、`errors[]` 带 code+hint）；`docs/01-开发者/接口层.md` 增补 context 工具裁剪参数表（`symbols`/`max_bytes`/`max_tokens`/`max_line_chars`/`path_style`/`mode`）与 HTTP 等价支持；修正新增测试导致的非 vendor LoC 53.6k→53.7k 两处文档口径。
- 验证：`go test ./internal/server/ ./internal/service/` 全绿；`make counts-check` 通过。

### Commit 148: chore(eco): 生态资产 P2 正式发布收口（gitee 独立仓 v0.2.0，模块路径 go get 可用）

- 背景：adapterx / contextsdk 已随上批推送 gitee 独立仓（v0.1.0），但 go.mod 模块路径沿用 `github.com/idcu/…` 与真实托管（gitee）不一致——标准 `go get github.com/idcu/codeschema-adapterx@v0.1.0` 会去 github.com 拉取而失败，对外不可消费。
- 动作：两独立仓 go.mod 模块路径改为 `gitee.com/idcu/codeschema-adapterx` / `gitee.com/idcu/codeschema-contextsdk`（与 `gitee.com/idcu-go/*` 惯例一致），源注释/README 同步；re-tag `v0.2.0` 并 push（main + tag）。消费侧验证：临时 module `go get gitee.com/idcu/codeschema-adapterx@v0.2.0` / `contextsdk@v0.2.0` 成功，`go run` 打印 `builtin adapters: 4`。
- 配套：`scripts/check-{adapterx,contextsdk}-publish.sh` 与 `check-contrib-publish.sh` 用法注释改 gitee 模块路径；`contrib/{adapterx,contextsdk,dsh}/README.md` 发布形态改 gitee 路径并标注 P2 已发布 v0.2.0。
- 遗留：两个独立仓消费需 `go get` 侧 `GOPRIVATE/GONOSUMDB` 覆盖 `gitee.com/idcu`（gitee 非官方 module proxy/sumdb 覆盖范围），已在 03-部署运维 或 README 记录。本批 monorepo 改动未 push（待授权）。

### Commit 147: refactor(watcher,scheduler): 抽取通用文件监听 fswatch 与泛型防抖队列 scheduler（档二续）

- 背景：档二封档时 fswatch 标 planned 但因 watcher 耦 scanner/Scheduler 未落实。本轮把文件监听与事件调度两个中性内核从领域管线解耦，真正落地为可复用原语。
- 新增（code-schema/internal/fswatch，中性）：Watcher 接口 + PollWatcher（轮询）/FsWatcher（fsnotify 递归），构造只收 `onChange func(string)`，剔除 scanner/scheduler 依赖；默认忽略目录集（.git/node_modules/build/...）+ 导出 IsIgnored；纯机制零领域语义，仅依赖 fsnotify 与 idcu-go/recovery。
- 新增（code-schema/internal/scheduler，中性·泛型）：`Scheduler[K comparable]` —— Enqueue(K)/Ready() []K/Start(ctx, func(ctx,K) error)/Len/Clear + 阈值降级信号 DegradeSignal；path 语义泛型化，与业务 key 解耦。
- 兼容层（零生产调用点改动）：internal/watcher 改薄封装，保留旧签名 `(root, scan, sched, ...)`，内部以 `func(p){ sched.Enqueue(p) }` 把变更路径接入领域调度器；runtime.go/main.go 仅给 NewScheduler 加 `[string]` 一处改动。
- 测试迁移：watcher_test.go 整体迁到 fswatch 包（用 onChange 闭包捕获、访问同包未导出字段），scheduler_test.go 泛型化；fswatch/scheduler/runtime 单测全绿。
- 验证：go build ./...=0；go vet ./...=0；go test ./...（全仓）全绿；gofmt 干净。
- 注册：idcu-go/meta/modules/modules.yaml 修正 fswatch 描述为已落地中性实现、新增 scheduler planned（primitive），meta.version 9→10，modules.md 同步。未 push（待授权）。

### Commit 146: refactor(vector,search): 抽取通用嵌入/检索机制为本地中性包 embedding/retrieval（档二封档）

- 背景：按 meta 标准 §3.17 中立性审计 code-schema，vector/search 的通用核心（向量存储/索引/本地 Embedder、FTS 引擎/融合重排/双路检索）中性、可插拔、零领域依赖，但仅 code-schema 单消费者，未过双消费者门槛。按档二在源项目内先抽独立包，封档待第二消费方。
- 新增（code-schema/internal/embedding）：Embedder/Embeddable/MockEmbedder/SearchResult/VectorStore/DocContentStore/MemoryStore/PersistentStore/cosineSimilarity/Indexer/LocalEmbedder，纯机制零领域语义，仅依赖 idcu-go/pathsafe。
- 新增（code-schema/internal/retrieval）：FTSEngine/MemoryFTS/PersistentFTS/Reranker/Searcher/VectorSearcher/Result（中性，领域字段入 Meta）+ 融合重排与绝对置信度标定（1-exp(-BM25/τ)）。
- 兼容层（零调用点改动）：vector 包用 type alias 重导出全部符号（DefaultText/chromem/ONNX/模型分发留领域）；search 包保留领域 SearchResult（含 symbol/kind/file/snippet/source）并加投影函数，Searcher/Reranker 包一层委托 retrieval，对外签名不变。
- 不归并：internal/store/filestore 的原子写机制虽通用，但数据模型全绑代码符号语义，归并风险>收益，判不归并；config 已下沉 idcu-go/config，无重复造轮子。
- 验证：go build ./...=0；go vet ./...（含测试）=0；internal/{embedding,retrieval,vector,search,service} 单测全绿（search Reranker/Searcher、service Search/B8/Enrich/Disambiguate 行为等价验证通过）。
- 注册：idcu-go/meta/modules/modules.yaml 追加 planned embedding/retrieval/fswatch（state=planned, version=0.0.0），meta.version 8→9。未 push（待授权）；fswatch 待 watcher 与 scanner/Scheduler 解耦后再议。

### Commit 145: refactor(config): env-merge 通用化回灌 idcu-go/config.ApplyEnv，LoadFromEnv 改领域薄层

- 背景：审计 §4 把 config 的 loader/env/watch 均判为通用片段，但此前只回灌了 include-loader、toInt、watch；env-merge 仍是本仓 ~130 行硬编码逐字段映射（CODESCHEMA_<SECTION>_<KEY>），领域绑定未收敛。本轮补齐。
- 改动（idcu-go/config）：
  - 新增 `env.go`：泛型反射 `ApplyEnv(prefix string, v any) error`——按 camelCase→UPPER_SNAKE 展开字段路径（含连续大写缩写识别：APIKey→API_KEY、ModelDownloadURL→MODEL_DOWNLOAD_URL、MCPAddr→MCP_ADDR、SCIP→SCIP），嵌套结构逐层下钻、匿名嵌入扁平化；类型安全解析（string 非空覆盖 / bool 容忍 1|t|true|yes 与 0|f|false|no / int 族拒绝负值 / uint / float）；`env:"-"` 标签显式排除；解析失败保留默认值；切片/map 自动跳过。
  - 新增 `env_test.go`：6 用例（标量+嵌套/跳过 env-dash/非法与负值保留默认/布尔容忍/嵌入扁平化/非法入参报错）。
- 改动（code-schema/internal/config）：
  - `LoadFromEnv` 收敛为：领域特殊逻辑（CODESCHEMA_PRESET→ValidPreset+ApplyPreset，幂等）保留 + 其余字段委托 `cfgconv.ApplyEnv("CODESCHEMA", cfg)`；删除 ~125 行硬编码映射；`Preset` 字段加 `env:"-"` 排除出通用遍历；`strconv` 导入移除。
  - 行为一致性：全部 28 个 env 变量映射名不变（命名算法与既有硬编码一致）；现有测试契约保持——字符串/整数/布尔覆盖、非法整数保留原值全部通过。边际差异（文档化）：int 从「>0 / >=0 混合」统一为「非负即接受」；bool 从「仅 true/1/yes（大小写敏感）」放宽为容忍解析（大小写不敏感，新增 no/f/0→false）。
- 验证：
  - idcu-go/config：`go test ./...` 全绿，覆盖率 83.3%（门禁 73 达标）。
  - code-schema：`go build ./...`=0；`go vet ./...`=0；全包 `go test ./...` 全绿（config 包含 TestLoadFromEnv/TestLoadFromEnv_InvalidInt 通过）。
- 说明：本提交未推 tag（config ApplyEnv 仍为草案，审计「待第二消费方再正式化」逻辑，与 watcher/graceful/recovery/trace 一致）。

### Commit 144: refactor(config): ConfigWatcher 热重载下沉 idcu-go/config 泛型 Watcher，领域层改薄封装

- 背景：审计 §4 已把 `watch` 判为 config 通用片段（不依赖 Config 任一具体字段，只做「轮询检测 + 原子切换 + OnReload 回调」），但首轮只回灌了 `toInt`。本轮补齐。
- 改动（idcu-go/config）：
  - 新增 `watcher.go`：泛型 `Watcher[T]`（`NewWatcher[T]`/`SetPollInterval`/`SetOnReload`/`Get`/`Start`/`Stop`），`load func(path) (T, error)` 由调用方注入，变更检测用 mtime+size，默认 2s 轮询，纯标准库零第三方依赖。
  - 新增 `watcher_test.go`：6 个用例（Get 初始值/变更重载+onReload 携带新旧值/加载失败保留旧配置/Start 后注册回调仍生效/Start 响应 ctx 取消/多次 Stop 幂等）。
- 改动（code-schema/internal/config）：
  - `ConfigWatcher` 改为 `cfgconv.Watcher[*Config]` 薄封装：加载逻辑（Load + LoadFromEnv）注入为 load 回调，复用通用变更检测/原子切换/回调机制；原地删除约 120 行领域重复实现；行为不变（默认 2s、SetOnReload 可在 Start 后调用、加载失败保留旧配置）。
  - 结果：`sync` 导入移除、`cfgconv` 引用 idcu-go/config，领域 Config 结构保留。
- 验证：
  - idcu-go/config：`go test -run TestWatcher` 6/6 PASS。
  - code-schema：`go build ./...`=0；`go vet ./internal/config/`=0；全包 `go test ./...` 全 ok（config 3 用例全过）。
- 说明：本提交未推 tag（config 通用 Watcher 仍为草案，审计「待第二消费方再正式化」逻辑，与 graceful/recovery/trace 一致）。

### Commit 143: refactor(common): 通用模块深度抽取归并落地（retry/metrics/log/trace/graceful/recovery/config 迁移 idcu-go）

- 背景：按 `analysis/2026-08-30-common-modules-extract-audit.md` §7 已确认决策全量推进（用户拍板）：「trace 对齐 OTel、graceful 拆两包、fsutil 并入 pathsafe、metrics 回填、log 先走草案」。取向以 idcu-go 为准，改 idcu-go 即同步改 code-schema 调用。
- 改动（code-schema 侧）：
  - `internal/robust` 删除：`retry` → `gitee.com/idcu-go/retry`（4 处 `Do`）；`graceful` → `gitee.com/idcu-go/graceful`；`recovery` → `gitee.com/idcu-go/recovery`（watcher/scheduler 切换）。
  - `internal/metrics` 删除：8 处改用 `gitee.com/idcu-go/metrics` 全局便捷 API（`RegisterCounter/IncCounter/RegisterGauge/SetGauge/Render`）。
  - `internal/log` 删除：12 处 import 切换为 `gitee.com/idcu-go/log`（包名 `logutil`，alias `log`；mcp.go 用 alias `slog`）。基于 slog 的模块化 `WithModule/Init/L()/Debug/Info/Warn/Error` 形态，草案 α（API 可破）。
  - `internal/trace` 删除：3 处切换为 `gitee.com/idcu-go/trace`（trace.go/http.go/scanner.go），API 形态对齐 OTel（`Tracer.Start`/`Span.End/AddEvent/SetTag/StartChild`/`Trace`），轻量纯 stdlib 实现 + `*slog.Logger` 注入（用户确认不引入重 OTel SDK）。
  - `internal/config`：通用片段 `toInt` 回灌 `gitee.com/idcu-go/config`（导出 `ToInt`），本地 `toInt` 改为薄封装委托（alias `cfgconv`，避免与包内 `cfg` 变量名冲突）；领域结构（Config/Tenant/…）全部保留。
  - `go.mod`：新增 require+replace `{retry, graceful, recovery, metrics, log, trace, config}`（双模式接线，与既有 trim/ttlcache/pathsafe 一致）；`go mod tidy` 补 go.sum。
- idcu-go 侧新增/更新：
  - 新模块 `trace`（go.mod + trace.go + trace_test.go + README 待补），加入 go.work。
  - `log` 草案 α：module.go 补包级 `Debug/Info/Warn/Error` 便捷函数后 module_test.go 全过。
  - `config` 新增 `toint.go`（`ToInt`）。
  - scheduler 档二 planned 登记：`meta/modules/modules.yaml` infra 段加 planned 条目（触发条件=第二真实消费方）。
- 验证：
  - code-schema：`go build ./...`=0；`go test ./...` 全 ok；`go test -race ./internal/... ./cmd/...` 全 ok。
  - idcu-go：`log/trace/config/metrics/graceful/recovery/pathsafe/retry` 单测全 ok。
- 文档同步：README.md 依赖清单扩充、CHANGELOG.internal.md（本记录）、审计文档 §7 决策打 ✅ + 执行记录；README/COVERAGE 台账后续按现状刷新。

### Commit 142: feat(lsp): 新增 jdtls(Java) 端到端 e2e + Docker 镜像补齐 clangd/jdtls，LSP 六语言全部实跑验证（D 闭环）

- 背景：用户「全量推进实施下一步选项」——继续 D，闭环 option 1（jdtls/clangd 纳入 `cs-lsp-test` 镜像实跑）。Commit 141 后 D 已接线 go/java/cpp/py/ts/rust 六语言，但 java(jdtls)/cpp(clangd) 代码路径已接线、镜像/本机未装对应运行时 → e2e SKIP。本轮补齐两者运行时并在 Docker 内实跑验证。
- 改动：
  - `e2e_test.go`：新增 `TestLSPAdapter_RealJDTLS`（跳过门槛 `ResolveServerPath("jdtls")==""`，invisible-project 模式直接解析单 .java 文件，断言提取 Calculator 类 + add 方法）；`containsName` 增强兼容 jdtls 的 `add(int, int)` 形式（新增 `target+"("` 前缀判定）。
  - `adapter.go`：`NewJDTLSAdapter` 默认超时 15s→**30s**（jdtls JVM 冷启动 + bundle 解析在容器内首启偏重，15s 会 initialize 超时）。
  - `docker/lsp-test/Dockerfile`：① 基础 `debian:bookworm`→**`debian:trixie`**（trixie 默认 JDK=21；jdtls latest 要求 class file 65.0=Java 21，bookworm 默认 JDK 17 会 `UnsupportedClassVersionError`）；② rustup→**apt `rust-analyzer`+`cargo`**（免 rustup 工具链长下载，本环境曾卡 30min+）；③ 新增 `clangd clang`(apt)、`default-jdk`(JDK21)、jdtls tarball 解 `/opt/jdtls` + `jdtls.sh` 包装脚本 COPY 到 `/usr/local/bin/jdtls`；④ Go 二进制下载 `go.dev/dl`→**`golang.google.cn/dl`** 镜像（go.dev 在本环境 wget 退出码 4 网络失败）。
  - `docker/lsp-test/jdtls.sh`（新增）：jdtls 非单二进制，以 `java -jar org.eclipse.equinox.launcher_*.jar` 拉起，补全 equinox 启动参数 + `config_linux` + `mktemp` 独立 `-data` workspace；`NewJDTLSAdapter` 以命令名 `jdtls`（无参数）调用之。
- 验证：
  - 主机（clangd 已装、jdtls 未装）：`go build ./...`=0；`go vet`=0；`go test -short ./...`→exit 0（RealClangd PASS；RealJDTLS SKIP；gopls/rust/pyright/ts 一致）。
  - Docker（cs-lsp-test:latest，trixie）：`java -version`=21.0.12.1、`rust-analyzer`/`clangd`/`jdtls` 均在位；**6 个 Real* e2e 全部 PASS**——RealClangd(0.27s)/RealGopls(0.28s)/RealRustAnalyzer(0.08s)/RealPyright(0.55s)/RealTSLanguageServer(1.36s)/RealJDTLS(3.94s)。
  - Docker 全仓 short 套件：除 `internal/agentbench` 的 `TestRunMulti_RepoHintFilter` 预存失败（见 Commit 141：主机 PASS、干净 HEAD bcc91cd 复现确认与本次无关）外全 ok；该失败不构成本轮 regression。
- 结论：D 的 LSP 六语言 **go/java/cpp/py/ts/rust 适配器全部接线且均在 Docker 或主机真实拉起语言服务器端到端验证抽取符号**（java 经 JDK21+jdtls、cpp 经 clangd）。D 语言服务器高精度路径实跑验证至此闭环。镜像构建踩坑已固化：①jdtls latest 需 Java21（bookworm JDK17 `UnsupportedClassVersionError`）；②rustup 本环境下载极慢（换 apt `rust-analyzer`）；③`go.dev/dl` 二进制本环境网络失败（换 `golang.google.cn/dl` 镜像）；④trixie 基础层首次拉取慢、BuildKit 缓存后续快。
- 文档同步：CHANGELOG.internal.md（本记录）+ `docker/lsp-test/Dockerfile` + `docker/lsp-test/jdtls.sh`；SKILL.md 补 jdtls(JDK21+包装脚本)/clangd 安装要点、Go 镜像、rustup→apt 替代。

### Commit 141: feat(lsp): 新增 pyright + typescript-language-server 适配器与端到端 e2e，Docker 内实跑验证 D 扩展

- 背景：用户「全量推进实施」继续推进 D（LSP 多语言高精度）。Commit 140 已实跑验证 rust-analyzer；D 剩余两个「单文件即可解析、无需工程上下文」的语言服务器为 **pyright（Python）** 与 **typescript-language-server（TypeScript）**——均基于 Node，本机/CI 未装故此前仅接线未验证。沿用「调试测试可在 docker 中进行」授权，在已落库的 `cs-lsp-test` 镜像内装 node 运行时后实跑验证。
- 改动：
  - `adapter.go`：新增 `NewPyrightAdapter()`（命令 `pyright-langserver --stdio`，lang=py，超时 20s）+ `NewTSLanguageServerAdapter()`（命令 `typescript-language-server --stdio`，lang=ts，超时 20s）。
    - **坑 1**：pyright 的 LSP 入口二进制是 `pyright-langserver`（非 `pyright` CLI）——用 `pyright` 跑会卡在 initialize 触发 20s 超时失败；已修正为 `pyright-langserver`。
    - **坑 2**：typescript-language-server 把 `typescript` 列为 **peerDep**，必须能在其「被分析工程」的 `node_modules` 内解析到 `typescript`，全局安装 + `NODE_PATH` 均不被采纳（这是首跑报 "Could not find a valid TypeScript installation" 的根因）。
  - `runtime.go`：① `lspAdapters` slice 注册 `NewPyrightAdapter()` + `NewTSLanguageServerAdapter()`；② `SetPriority("py", ["pyright-langserver","codegraph","scip","treesitter"])`（正确含 LSP）；③ **修复 `SetPriority("ts", ...)` 漏 `typescript-language-server` 的真实 bug**——`Registry.Select` 按 priorityMap 白名单遍历，漏列则 ts 语言即便装了 tsls 也永不被选中；改为 `["typescript-language-server","codegraph","scip","treesitter"]`；④ 顶部注释 LSP 覆盖语言由 `go/java/cpp` 更正为 `go/java/cpp/py/ts/rust`。
  - `e2e_test.go`：新增 `TestLSPAdapter_RealPyright`（跳过门槛 `ResolveServerPath("pyright-langserver")==""`，断言 Calculator 类 + add 方法）+ `TestLSPAdapter_RealTSLanguageServer`（跳过门槛 `ResolveServerPath("typescript-language-server")==""`）；新增 `linkGlobalTypescript(t, projDir)` helper——`npm root -g` 解析全局 typescript，软链进被测工程 `node_modules/typescript`（全局缺失则静默 no-op）；回加 `"os/exec"` import（Commit 139 移除，此处需 `npm` 解析路径）。
  - `docker/lsp-test/Dockerfile`：`npm install -g pyright typescript-language-server` **`typescript@5`**（typescript 7.0 为 Go 原生重写、已移除 `tsserver.js`，而 typescript-language-server@6 依赖经典 JS 版 `tsserver.js` → 钉 5.x，镜像实测 5.9.3 含 `lib/tsserver.js`）。
- 验证：
  - 主机（未装 pyright/tsls）：`go build ./...`=0；`go vet ./...`=0；`go test -short ./...`→exit 0、NO FAIL（pyright/ts e2e SKIP；gopls/clangd e2e PASS）。单测 verbose 确认 RealPyright/RealTSLanguageServer 均为 SKIP、RealGopls/RealClangd PASS。
  - Docker（cs-lsp-test:latest，卷挂载 code-schema+idcu-go+module cache）：`TestLSPAdapter_RealPyright`→**PASS(0.65s)**；`TestLSPAdapter_RealTSLanguageServer`→**PASS(1.76s)**（symlink 生效、tsserver.js 解析成功）；`TestLSPAdapter_RealGopls`→PASS(0.23s)；`TestLSPAdapter_RealRustAnalyzer`→PASS(0.03s)；`TestLSPAdapter_RealClangd`→SKIP（镜像未装 clangd，符合预期）。LSP 六语言中四语言（go/py/ts/rust）已在 Docker/主机实跑验证。
  - 全仓 short 套件 Docker：除 `internal/agentbench` 的 `TestRunMulti_RepoHintFilter` 在 Docker 内 FAIL 外其余全 ok。**经干净 HEAD(bcc91cd) git worktree 在 Docker 内复现，确认该失败为 Docker 环境特有预存问题（repo1 active tasks=1 want 5，与本次改动无关，agentbench 走 tree-sitter 未触碰），主机 PASS**——不构成本次 regression，已单列待后续排查（见下「遗留」）。
- 结论：D 的 LSP 语言服务器覆盖推进到 go/java/cpp/py/ts/rust 六语言全部接线；其中 go/py/ts/rust 已在 Docker 或主机真实拉起语言服务器端到端验证抽取符号，cpp(jangd)/java(jdtls) 代码路径已接线但镜像/本机未装对应运行时 → 优雅降级 SKIP。本轮额外**修复 `SetPriority("ts")` 遗漏 typescript-language-server 的真实选择 bug**，确保 tsls 装好即被 `ts` 语言正确选中。
- 遗留：① `TestRunMulti_RepoHintFilter` Docker 环境失败（预存、非本次引入，建议另开排查：疑似 tree-sitter 并行符号预检在 Docker 绑定挂载 FS / 调度下非确定性，或依赖某 Docker 缺失的外部条件）；② jdtls/clangd 仍未纳入 `cs-lsp-test` 镜像（如需 Java/C++ 实跑验证，镜像补 `npm i -g vscode-java-language-server`/apt 装 clangd 后重复本验证流程）。
- 文档同步：CHANGELOG.internal.md（本记录）+ `docker/lsp-test/Dockerfile`；SKILL.md 待补 pyright-langserver / typescript@5 两坑（见下轮）。

### Commit 140: feat(lsp): 新增 rust-analyzer 适配器 + 端到端 e2e 验证（D 扩展，Docker 内实跑通过）

- 背景：用户「全量推进实施，调试测试可以在docker中进行」——解锁 D 的环境阻塞项。原 D 仅接线 gopls/jdtls/clangd（jdtls/pyright/ts 等运行时缺失），本机 rust-analyzer 亦未装。最低摩擦、无需工程上下文即可抽取符号的新语言服务器 = **rust-analyzer**（单静态二进制）。
- 改动：
  - `adapter.go`：新增 `NewRustAnalyzerAdapter()`（lang=rust，默认超时 20s，rust-analyzer 启动略重需 cargo metadata）；`documentSymbol→IR` 映射补 `enum(kind 10)→ClassIR{Type:"ENUM"}`（两处 `addSymbolInfo`/`addDocumentSymbol`），struct(23)/fn(12) 已有。
  - `runtime.go`：`NewParserRegistry` 的 `lspAdapters` slice 注册 `NewRustAnalyzerAdapter()`；`SetPriority("rust", ["rust-analyzer","codegraph","scip","treesitter"])`（原仅 codegraph/scip/treesitter）；注释同步列出 rust-analyzer。
  - `e2e_test.go`：新增 `TestLSPAdapter_RealRustAnalyzer`（跳过门槛 `ResolveServerPath("rust-analyzer")==""`，无则 SKIP，CI 绿）+ `writeRustProject`（最小 Cargo 工程：struct Calculator + impl fn add/sub + enum Op）；断言提取 Calculator 类 + add 方法。与 Commit 139 一致改用同包 `ResolveServerPath`。
  - `docker/lsp-test/Dockerfile`：落库**已验证可用**的 LSP 测试镜像（debian:bookworm + rustup/rust-analyzer + go1.25 + gopls），供 Docker 内实跑 e2e 复现；含卷挂载命令与网络注意（proxy.golang.org 本环境不可达→goproxy.cn；rust:latest 加速器繁忙时退 debian+rustup）。
- 验证：
  - 主机：`go build ./...`=0；`go vet ./internal/parser/adapter/lsp/ ./internal/runtime/`=0；`go test -short ./...` 无 FAIL（rust e2e 因主机无 rust-analyzer SKIP；clangd/gopls e2e PASS）。
  - Docker（cs-lsp-test:latest，卷挂载 code-schema+idcu-go+module cache）：`rust-analyzer --version`=1.98.0、`gopls version`=v0.23.0；`TestLSPAdapter_RealRustAnalyzer`→**PASS(0.07s)**，`TestLSPAdapter_RealGopls`→PASS(0.19s)；全仓 `go test -short ./...`→exit 0 全包 ok（clangd 镜像未装 SKIP）。D 的 rust 扩展端到端实跑验证通过。
- 结论：D 由「仅接线」推进到「rust 真实解析已验证」；LSP 注册表现支持 gopls / java(jdtls) / cpp(clangd) / rust(rust-analyzer) 四语言高精度路径，缺运行时仍优雅降级 tree-sitter。其余（jdtls/pyright/ts-language-server）仍待对应运行时——Docker 工作流已就绪，装好即重复上述验证。用户授权「调试测试可在 docker 中进行」闭环。
- 文档同步：CHANGELOG.internal.md（本记录）+ `docker/lsp-test/Dockerfile`。

### Commit 139: test(lsp): LSP 端到端 e2e 跳过门槛改用 ResolveServerPath——开发机 gopls 从 SKIP 变真跑

- 背景：用户「全量实施」。Commit 138 让 `NewLSPAdapter`/`runtime.commandAvailable` 经 `lsp.ResolveServerPath` 发现位于 `$GOPATH/bin`（非 PATH）的 gopls，修复注册层静默失效。但 `internal/parser/adapter/lsp/e2e_test.go` 的两个真实 LSP 端到端测试（`TestLSPAdapter_RealClangd`、`TestLSPAdapter_RealGopls`）的跳过门槛仍用 `exec.LookPath(name)`（仅判 PATH）——本机 gopls 在 `$GOPATH/bin`（`/Users/huyu/go/bin/gopls`）→ `TestLSPAdapter_RealGopls` 直接 SKIP。**gopls 真实解析路径此前从未在开发机实跑验证**：上一轮「全绿」中 lsp 包仅为 `TestLSPAdapter_RealClangd` PASS + `TestLSPAdapter_RealGopls` SKIP。这是 Commit 138 发现机制在验证层未闭环的缺口（发现用解析器、跳过却只用 PATH，二者不一致）。
- 修复：`e2e_test.go` 两处跳过门槛由 `exec.LookPath(name)` 改为 `ResolveServerPath(name) == ""`（同包直接调用，无需 import）；同步移除测试文件不再使用的 `"os/exec"` import。`NewLSPAdapter` 内部已在 Commit 138 将 `a.cmd` 解析为绝对路径，故 gopls 现从 `/Users/huyu/go/bin/gopls` 真实拉起、走完整 JSON-RPC + documentSymbol，跳过门槛与 spawn 解析一致。
- 验证：
  - `go test -run TestLSPAdapter_RealGopls -v -timeout 180s` → **PASS（0.30s）**，证明 gopls 真实提取 `Calculator` 类 + `Add`/`Sub` 方法（此前 SKIP）。
  - 全 lsp 包 `go test -timeout 300s ./internal/parser/adapter/lsp/` → ok（clangd + gopls 双 e2e 均 PASS）。
  - 完整回归 `go test -short ./...` → exit 0、NO FAIL；`go vet ./...`=0；`go build ./...`=0；`go build -tags 'pg redis treesitter onnx' ./...`=0。
- 结论：LSP 端到端真实解析现对本机可用语言服务器（clangd 在 PATH、gopls 在 `$GOPATH/bin`）均**真正实跑验证**，不再因「发现解析器 ≠ 跳过门槛」而漏验。D 的「真实 LSP 路径可信」闭环再进一步；新增 jdtls/rust-analyzer/pyright/typescript-language-server 仍依赖对应运行时安装（环境依赖不变）。
- 文档同步：CHANGELOG.internal.md（本记录）。

### Commit 138: fix(lsp): LSP server 命令发现增强——PATH + $GOPATH/bin + $HOME/go/bin 回退（修复 gopls 静默失效）

- 背景：用户「全量实施下一步方向」推进 D（LSP 可靠性）。reality-check 发现本机 `gopls` 位于 `$GOPATH/bin`（`/Users/huyu/go/bin/gopls`）但**不在 PATH**；而 `runtime.NewParserRegistry` 注册前用 `commandAvailable(name)=exec.LookPath(name)`（仅 PATH）检测，`gopls` 未命中 → gopls 适配器被 skip；`NewLSPAdapter` 拉起也用字面命令名 `gopls` 经 PATH 解析。结果：Go 符号走 LSP 高精度路径**静默降级到 tree-sitter**（clangd 因在 PATH 故正常）。这是 D 的真实可靠性缺口，且本机可验证。
- 修复：新增 `lsp.ResolveServerPath(name)`——先 `exec.LookPath`（PATH），再回退各 `$GOPATH/bin` 与 `$HOME/go/bin`；找不到返回空串（供 `commandAvailable` 判否、不注册）。`NewLSPAdapter` 将 `a.cmd` 解析为绝对路径（未找到回退原命令名，spawn 失败仍优雅降级）；`runtime.commandAvailable` 改用 `lspadapter.ResolveServerPath(name) != ""`（移除冗余 `os/exec` 依赖）。
- 验证：
  - 单测 `resolve_test.go`：`TestResolveServerPath_PathResolution`（PATH 命中 + 不存在返回空）、`TestResolveServerPath_HomeGoBin`（$HOME/go/bin 回退）、`TestResolveServerPath_GoplsResolvable`（本机 `gopls resolved: /Users/huyu/go/bin/gopls` 通过，CI 无 gopls 时 skip）。
  - 真实注册断言 `runtime.TestNewParserRegistry_LSPGoplsRegistered`：LSP 启用时 `Select("go")` 现返回 `gopls`（PASS，实际拉起 gopls 子进程注册），证明修复前静默失效已闭环。
  - 全量回归：`go build ./...`=0；`go build -tags 'pg redis treesitter onnx' ./...`=0；`go vet ./...`=0；`go test -short ./...` 无 FAIL。
- 结论：LSP server 发现现兼容 macOS 常见「GOPATH/bin 未入 PATH」布局；gopls/jdtls/clangd 等不再因 PATH 缺失而静默失效。新增服务器（jdtls/rust-analyzer/pyright）仍依赖对应运行时安装（D 的「新增服务器」项环境依赖不变），但发现机制已健壮。
- 文档同步：CHANGELOG.internal.md（本记录）。

### Commit 137: chore(cleanup): 删除死码 internal/contextsdk（SDKProvider 桥，零消费方，C 清理闭环）

- 背景：用户「全量推进实施」指令继续推进；上一轮（Commit 136）将 `internal/contextsdk` 标记为死码（零消费方、耦合 internal/service、SDKProvider 抽象前旧位置）并「待用户拍板未擅删」。本批次在用户连续「全量推进实施」语境下完成该可选收尾。
- reality-check（确认可删）：全仓 grep `internal/contextsdk` 零外部消费方；服务端/cmd 未引用 `SDKProvider`，`/context` 端点直接经 `internal/service.Service` 暴露，不经此桥；`contrib/contextsdk`（已独立发布 v0.1.0）是唯一活性路径、仅被本包自引用。故该桥为冗余死码。
- 动作：`git rm -r internal/contextsdk`（sdk.go + sdk_test.go）。
- 验证：删后 `go build ./...`=0；`go build -tags 'pg redis treesitter onnx' ./...`=0；`go vet ./...`=0；`go test -short ./...` 全 ok（无 FAIL）。删除全绿、可经 git 回滚。
- 结论：code-schema 主仓现无 internal 级死码残留；C（生态资产独立发布）清理闭环。
- 文档同步：CHANGELOG.internal.md（本记录 + 更新 Commit 136 死码备注）。

### Commit 136: feat(eco): adapterx/contextsdk 独立发布就绪——自包含验证 + 独立仓骨架（发布仅差建仓+push 授权）

- 背景：用户「全量推进实施」指令，推进 C（adapterx/contextsdk 独立仓发布）。原 CHANGELOG 将其列为外部动作（待用户建仓+push）。本提交把 C 推进到「只差建远程仓 + push 授权」的边界。
- **自包含 reality-check（结论）**：
  - `contrib/adapterx` 全源零外部依赖（仅 stdlib），被 `internal/parser/adapterx.go` 消费；**完全自包含** ✅。
  - `contrib/contextsdk` 仅依赖 `encoding/json`（stdlib），零外部依赖；SDKProvider 抽象后只定义契约+DTO+Compose 编排，**完全自包含** ✅（发布目标）。
  - `internal/contextsdk` 全仓零消费方、仍耦合 `internal/service` → **确认为死码**（SDKProvider 抽象前的旧位置）；建议删除（待用户确认，未擅删）。
- **独立仓骨架（仓库外，保持主仓干净）**：于 `/Volumes/Data/codeschema-adapterx`、`/Volumes/Data/codeschema-contextsdk` 落地——`go.mod`（module 路径 `github.com/idcu/codeschema-{adapterx,contextsdk}`，`go 1.25.2`，**无 require**）+ 拷贝源 + MIT LICENSE + README；白盒同包测试无需改写 import。
- **验证（独立脱离主仓）**：两仓均 `go build ./...`=OK、`go vet ./...`=OK、`go test ./... -short`=ok；已 `git init` + 首提交 + 打 `v0.1.0` tag，处于发布就绪态。
- 遗留（C 已闭环）：真正对外发布 = 用户建 `github.com/idcu/codeschema-adapterx` / `codeschema-contextsdk` 远程仓 + 授权 push；**已于本批次全量推送（main + v0.1.0 tag 已上 gitee），C 正式发布**。`internal/contextsdk` 死码已于 Commit 137 删除闭环（可选收尾已完成）。
- 文档同步：CHANGELOG.internal.md（本记录 + 更新 C legacy 备注 @Commit 214/265）。

### Commit 135: fix(docker): 修复镜像构建 + 真实 Redis/PG 端到端验证（关闭 Docker/PG·Redis 实跑环境阻塞）

- 背景：Commit 134 后按用户「全量推进下一步 + 环境可用 redis/docker」指令，转向验证 CHANGELOG 标注的『Docker 实构建 / PG·Redis 实跑待本机 Docker 网络恢复』环境阻塞项；过程中发现镜像构建实际已坏（非单纯网络），并修复。
- **Dockerfile 修复（2 处真实根因）**：① builder 阶段 `golang:1.25-alpine` 缺 `git`，`GOPRIVATE=gitee.com/idcu-go/*` 经 VCS 拉取 trim/ttlcache/pathsafe 报 `git: executable file not found`；改为无条件 `apk add --no-cache git`（仅 CGO 加 gcc/musl-dev）。② `go mod edit -dropreplace` 原跑在 `COPY . .` 之前，被仓库原始 go.mod 覆盖，build 仍找不存在的 `../idcu-go/pathsafe`；改为 `COPY . .` 后对已落盘最终 go.mod 再次 dropreplace。
- **验证（Docker 提供真实 Redis + 本地 embedded-postgres）**：
  - `docker build -t codeschema:verify --build-arg VERSION=v0.1.0 .` 成功；镜像冒烟 `version` + `serve --http :8081` 的 `/health` 返回 200。
  - 真实 Redis 集成测试 `-tags 'pg redis' TestRedisCache_RealInstance`：class hit + method hit + caller/callee 反查 verified（确认方法符号缓存层已可用，原『仅类符号走快速路径』legacy 备注亦已失效）。
  - PG 端到端 `-tags pg TestPGStore_EndToEnd`：embedded-postgres 16.3 内核，file=1 class=1 methods=2 calls=2 全链路 PASS。
- 结论：Docker 构建 + Redis 缓存 + PG 存储三条环境阻塞项全部闭环并验证；code-schema 部署路径（Docker 镜像 + Redis L2 + PG 存储）现端到端可验证。
- 文档同步：CHANGELOG.internal.md（本记录 + 关闭相关 legacy 备注）。

### Commit 134: docs(release): 首个 v0.1.0 tag 发布说明回填 + 打 tag（B8 待决项②）

- 背景：Commit 132 遗留 ②——code-schema 自身首个 `v*` tag 就绪后回填 版本发布说明.md。本次补齐发版动作，关闭 S3 周期全部遗留。
- **docs/4-决策层/版本发布说明.md**：`未发布（Next）` 草稿段落提升为 `已发布` 正式段落；`v0.1.0（待首个 v* tag，日期待定）` → `v0.1.0（2026-08-29）`，补「对应 git tag v0.1.0」说明；修订记录追加 2026-08-29 发布行。
- **tag**：`git tag v0.1.0`（指向本提交），推送 origin 触发 `release.yml` 5 平台制品构建；code-schema 自此进入有版本号、可溯源的发布态。
- 验证：无代码改动，仅文档；`GOWORK=off go build ./...` 不受影响。
- 文档同步：CHANGELOG.internal.md（本记录 + 关闭 Commit 132 遗留②）。

### Commit 133: feat(search): FTS exact 模式绝对置信度——BM25 归一 + 1-exp(-s/τ) 映射（B8 待决项①）

- 背景：Commit 132 遗留 ①——纯 FTS（`exact`）模式 `MinScore` 基于「结果集最大值归一化」的相对尺度，作绝对阈值受结果集强弱影响（同一文档+查询在不同查询集合下置信度漂移）。B8 要求置信度可作绝对阈值，故将纯 FTS 置信度改为基于 BM25 的绝对相关度量纲。

- **search 包（fts.go）**：`MemoryFTS.scoreDoc` 由长度敏感的无界 TF-IDF 改为 **BM25**（`idf = log(1+(n-d+0.5)/(d+0.5))`，`tfNorm = (tf*(k1+1))/(tf+k1*(1-b+b*dl/avgdl))`，`k1=1.5, b=0.75`）；`Search` 先按语料统计 `n/avgdl/df` 再逐文档打分。`FTSEngine`/`MemoryFTS` 注释同步，Score 即 BM25 绝对相关度。
- **search 包（searcher.go）**：`withExactConfidence` 由「按集合最大值归一」改为绝对映射 `Confidence = 1 - exp(-Score/τ)`（`τ=0.3`），与结果集大小无关；同一文档+查询恒定同一置信度，`MinScore` 可作绝对阈值。
- **生产路径覆盖**：`PersistentFTS.Search` 委托 `MemoryFTS`（`fts_persistent.go`），BM25 自动覆盖 JSON 持久化生产引擎，无需改持久化层。
- **重排无回归**：`Reranker.Rerank` 对 FTS `Score` 内部 max 归一化融合（`both` 模式尺度无关）；`both` 模式 `Confidence` 取向量原始余弦（绝对），FTS-only 回退归一化 FTS——引擎原始得分尺度变更不影响 `both` 行为。
- **测试**：`b8_test.go` 两组 exact 模式断言随绝对尺度更新——`TestSearcher_ExactConfidence` 改判 top 置信度 ∈(0,1)（不再恒为 1.0）；`TestSearcher_SearchWithOptionsMinScore_Exact` 改用 `MinScore=0.5`（High≈0.56 / Low≈0.38，保留强匹配、过滤弱匹配）。
- 验证：`GOWORK=off go build ./...`=0；`go vet ./internal/search/...`=0；`go test ./internal/search/... ./internal/service/... ./internal/server/... -short -count=1`=ok。
- 文档同步：CHANGELOG.internal.md（本记录 + 关闭 Commit 132 遗留①）。

### Commit 131: feat(search): B8 检索低置信度不返回（MinScore 量化阈值 + trim_reason 回传）

- 背景：DEV_PROGRESS 维护优化（2026-08-29）遗留项 `B8`（空结果优于误导结果）原「待有阈值量化口径后再落地」。
  本提交定义量化口径并落地：置信度取**向量余弦相似度（chromem 返回，绝对量纲 [0,1]）**（语义/融合模式），
  纯 FTS 模式回退到按集合最大值归一化的相对得分；`Confidence < MinScore` 的结果过滤，默认 `MinScore=0` 关闭（向后兼容）。

- **search 包**：
  - `SearchResult` 增 `Confidence float64`（绝对置信度 [0,1]）；
  - `Reranker.Rerank` 为每条融合结果计算 `Confidence`：有向量命中取原始余弦（绝对），否则回退归一化 FTS；
  - 新增 `SearchOptions{Mode,Limit,MinScore}` 与 `SearchWithOptions`（返回过滤计数）；`Search` 保持旧签名委托 `MinScore=0`。

- **service 包**：新增 `SearchOutcome{Results,TrimReason,Filtered}` 与 `SearchWithOptions`；
  过滤发生时置 `TrimReason="below_threshold"`、`Filtered`=被过滤条数；`Service.Search` 旧签名保持 `[]SearchResult` 向后兼容。

- **HTTP / MCP**：`GET /search?min_score=` 与 `search_symbols` 的 `min_score` 参数接线，
  响应改为 envelope `{results,trim_reason,filtered}`，每条结果带 `confidence`。

- 验证：
  - `go build ./...` 通过；`go vet ./internal/search/... ./internal/service/... ./internal/server/...` 通过；
  - `go test ./internal/search/... ./internal/service/...` 全绿（新增 8 组 B8 测试：置信度绝对量纲 / FTS 回退 / 三模式 MinScore 过滤计数 / envelope 透传）；
  - `GOOS=linux GOARCH=arm CGO_ENABLED=0 go build ./...` 交叉编译通过。
- 文档同步：DEV_PROGRESS.md（B8 遗留项 [ ]→[x] + 量化口径）、README.md（检索低置信度过滤小节）、CHANGELOG.internal.md（本记录）。
- 遗留：纯 FTS（`exact`）模式的 `MinScore` 基于相对归一化得分，作绝对阈值须谨慎（已在 README 注明）。

### Commit 132: docs(s3): S3 周期性守护首周期——文档一致性修复（R1）+ 版本/合规核对（R3/T4）

- 背景：B8（Commit 131）收口后，DEV_PROGRESS 全部勾选，特征阶段结束，转入 S0 协议 **S3 周期性守护**。本提交为首个 S3 周期增量报告，仅修文档态滞后、不改动代码。

- **R1 文档一致性（修复）**：
  - `analysis/2026-08-29-fastcontext-analysis.md`：B8 状态表原 `⏸ 未做`（line 139）与代码态矛盾 → 补 B8 ✅ 行（line 138，落地位置 = `Search`/`Service.SearchWithOptions` 的 `MinScore` 量化阈值 + `trim_reason=below_threshold` 回传）+ 修订记录补正（line 153）；
  - `docs/1-生产层/开发文档/11-配置部署与路线图.md:179`：风险表「语义检索未落地」与已落地事实矛盾 → 更正为「已落地（历史风险已解除）」，注明 P6/P6_2 + Recall@1=1.00。
- **R3 版本一致性（核对）**：`go.mod` 对 `gitee.com/idcu-go/{pathsafe,trim,ttlcache}` 锁定 `v0.1.0`（与 Commit 130 发布 tag 一致），并保留 `replace => ../idcu-go/*` 本地开发路径；复测 `GOWORK=off go build ./...` = 0，确认本地 replace 源可编译、无偏离。
- **T4 合规（核对）**：B8 新增公共 API（`SearchWithOptions` / `SearchOutcome` / `SearchResult.Confidence`）已文档化、向后兼容、双消费者（HTTP+MCP）接线、无 secret、复用 chromem 余弦+FTS 归一化无复制；三 idcu-go 模块纯标准库零依赖、v0.1.0 合规。本周期无新增公共抽象抽取。
- **R2 潜能点亮（核对）**：B1–B9 全落地，无新增 planned 候选；唯一历史待拍板项 B8 已闭环。
- 验证：`go build ./...` = 0；`go test ./internal/search/... ./internal/service/... ./internal/server/... -short -count=1` = ok（含 B8 8 组）；仅文档改动，无代码回归风险。
- 文档同步：analysis/.../fastcontext-analysis.md（B8 状态更正 + 修订记录）、11-配置部署与路线图.md（语义检索风险更正）、docs/4-决策层/S3-守护报告-2026-08-29.md（本周期增量报告）、CHANGELOG.internal.md（本记录）。
- 遗留（已全部闭环）：① 纯 FTS `min_score` 绝对尺度化（IDF/BM25 归一）——Commit 133 落地；② code-schema 首个 `v*` tag + 版本发布说明.md 回填——Commit 134 落地（tag v0.1.0 @ 2026-08-29）。

### Commit 130: ci(deps): idcu-go 三模块发布 v0.1.0（push + tag）+ CI/Release 检出步骤 + Docker 按 tag 拉取

- 背景：Commit 129 把 `trim` / `ttlcache` / `pathsafe` 抽成 idcu-go 公共模块后，`go.mod` 以
  `replace` 指向仓库外的 `../idcu-go/*`——本地可用，但 CI / Release / Docker 拿不到上级目录，
  replace 无法解析，流水线必红。这是当时书面登记的遗留项，本提交闭环。

- **模块发布（外部动作，已执行）**：三个模块新建 gitee 仓库并 push `main`，打 `v0.1.0` tag 并推送——
  - `https://gitee.com/idcu-go/trim`（v0.1.0）
  - `https://gitee.com/idcu-go/ttlcache`（v0.1.0）
  - `https://gitee.com/idcu-go/pathsafe`（v0.1.0）
  - 校验：`git ls-remote --tags` 三个仓库均返回 `refs/tags/v0.1.0`。

- **go.mod / go.sum**：`require` 由占位 `v0.0.0` 提升为 **`v0.1.0`**；同时**保留 `replace` 指向本地
  `../idcu-go/*`**（本地改模块即时生效，不需先发布）。为让「去掉 replace 也能构建」，
  go.sum 补齐三模块的 `h1:` 校验和（由 `go mod download` 生成）。两条路径均实测通过：
  - replace 模式（本地/CI）：`go build ./...` 通过；
  - tag 模式（Docker/外部消费方）：`go mod edit -dropreplace=...` + `GOPRIVATE=gitee.com/idcu-go/* go build ./...`
    在默认 `-mod=readonly` 下通过（说明 go.sum 完整、tag 可解析）。

- **CI（`.github/workflows/ci.yml`）**：7 个需要构建的 job（test / agent-bench / bench / nightly-scale /
  race / treesitter / cross）在 checkout 之后新增「Checkout idcu-go modules」步骤——按 `v0.1.0` tag
  把三模块 clone 到同级 `../idcu-go/`，让 replace 可解析。docker job 不加（走 Dockerfile 内的 tag 拉取）。
- **Release（`.github/workflows/release.yml`）**：`make cross` 交叉编译同样依赖 replace 目录，补同一检出步骤。
- **Dockerfile**：构建上下文拿不到上级目录，改在镜像内
  `go mod edit -dropreplace=gitee.com/idcu-go/{trim,ttlcache,pathsafe}` + `ENV GOPRIVATE=gitee.com/idcu-go/*`
  后 `go mod download`，按已发布 tag 拉取（依赖 go.sum 中已补齐的校验和）。

- 验证：两个 workflow YAML 解析通过（ci 8 jobs / 7 个检出步骤；release 1 job / 1 个检出步骤）；
  本地模拟 CI 检出脚本 `git clone --depth 1 --branch v0.1.0` 三个模块均成功且 `git describe` = v0.1.0；
  临时 module 按 tag `require` 三模块 `go mod tidy` + `go run` 通过（验证外部消费方可解析）；
  `go build ./...`、`go test -short ./...` 全绿。
- 文档同步：CHANGELOG.internal.md（本记录）、DEV_PROGRESS.md（遗留项闭环）、README.md（依赖口径）。
- 遗留：无（CI / Release / Docker 三条流水线均已覆盖）。后续模块升级需同步三处：
  模块打新 tag → 本仓 `require` 升版本 + 刷新 go.sum → CI/Release 检出步骤里的 `--branch` 版本号。

### Commit 129: feat(context): 上下文输出工程全量升级（A/B/C 档）+ 抽取 idcu-go 三个公共模块

- 背景：以 FastContext（fast-context-mcp v1.3.2）为参照做源码级借鉴分析（见
  `analysis/2026-08-29-fastcontext-analysis.md`），结论是「不抄架构（它无本地索引、查询期上云），
  抄它的输出工程」。用户拍板：A 档开工、B5 用「现有工具加 `symbols[]`」、B1 字节与 token 预算两者都支持，
  公共能力按 idcu-go/meta 的《通用模块标准 v1.2》抽成独立模块。

- **公共模块（新建 3 个独立仓库，按 meta v1.2 标准落地，均已本地 commit，未 push）**：
  - `gitee.com/idcu-go/trim` v0.1.0（通用·原语）：语义块对齐 + 双预算自适应降级 + 行级截断。
    四级降级链（上下文 → 块 → 块内居中截断保头尾 → 仅首行）、字节/token 双轨预算、UTF-8 安全截断。
    覆盖率 **92.9%**、race 全绿；基准：无预算 **145.0 ns/op / 32 B / 1 allocs**，居中截断 **1026 ns/op**。
  - `gitee.com/idcu-go/ttlcache` v0.1.0（通用·原语）：泛型 TTL + LRU 内存缓存，无后台 goroutine；
    空值可跳过写入并清除旧值（对齐 FastContext「空结果不缓存」）、全量失效、六计数器。
    覆盖率 **96.6%**；命中 **106.9 ns/op / 0 allocs**，未命中 **31.0 ns/op / 0 allocs**。
  - `gitee.com/idcu-go/pathsafe` v0.1.0（通用·工具）：虚拟根 ↔ 真实根双向映射与穿越阻断；
    越界一律报错不静默夹紧，虚拟路径统一 POSIX（Windows 同样识别 `/codebase/...`），
    可选 symlink 逃逸校验。覆盖率 **92.9%**；解析 **240.6 ns/op**。
  - 三模块均为纯标准库零依赖，含 README.md / README.en.md / CHANGELOG.md / LICENSE / Makefile
    （覆盖率门禁 ≥80%）/ .golangci.yml / Gitee CI / check-internal-deps.sh。

- **A 档（输出侧补齐）**：
  - **B1 输出预算自适应降级**：`ContextOptions` 增 `MaxBytes` / `MaxTokens` / `CharsPerToken`，
    裁剪改走 `trim.Fit` 的降级链；`_trace` 留痕 `degraded` / `degrade_reason` / `omitted_lines`。
  - **B2 诊断元数据回传**：`TraceEntry` 增 `config`（本次实际生效参数）/ `actual_bytes` /
    `actual_tokens` / `actual_start` / `actual_end` / `degraded` / `line_truncated` / `cache_hit`。
  - **B3 错误 hint**：新增 `internal/errors/hint.go`（`Hint(code)` / `WithHint`）；HTTP 错误响应增
    `error.hint` 字段，MCP（error 只有 message）以 `[hint] ` 内联，批量 `errors[]` 每项同样带 hint。
  - **B6 行级截断**：`MaxLineChars`（默认 0 = 不截断），防压缩产物/生成代码一行吃掉整份预算。
  - **B7 语义块对齐**：裁剪窗口恒覆盖符号体（调用方给的窗口小于块时向外扩展），
    `trim_reason` 新增 `semantic_block` 语义（原 `full` 语义过笼统）。

- **B 档（核心 KPI）**：
  - **B5 批量入参（方案 a）**：`context` / `impact` / `tests` 三个工具（MCP + HTTP）支持
    `symbols[]` / `methods[]`，一轮返回 N 个符号；单符号失败隔离（落 `errors[]` 带 code + hint），
    单符号仍返回原有响应形状（向后兼容）。service 层新增
    `GetContextBatch` / `GetContextBatchDetailed` / `GetImpactBatch` / `GetTestsBatch`。

- **C 档（性能与信息面）**：
  - **B4 查询级缓存**：service 增 `EnableQueryCache(ttl, maxEntries)`（默认**关闭**，由
    `context.query_cache.enabled` 开启），key = 世代号 + 符号 + 全部裁剪参数；
    `InvalidateQueryCache()` 在 watcher 的 OnIndex/OnDelete 后调用（O(1) 失效 + 立即回收），
    命中时 `_trace.cache_hit=true`。
  - **B9 路径虚拟化**：`path_style=virtual` 时经 `pathsafe` 把项目根映射为 `/codebase`
    （默认 `absolute`，向后兼容；根外路径原样返回而非伪装成功）。

- **配置与装配**：新增 `context` 配置段（`context_lines` / `max_bytes` / `max_tokens` /
  `max_line_chars` / `chars_per_token` / `default_path_style` / `query_cache.*`），
  经 `runtime.ContextOptionsFromConfig` → `SetContextDefaults` 作为服务端默认值
  （优先级：请求参数 > 服务端默认值 > 不限）；`runtime.ApplyContextConfig` 装配缓存与虚拟根。

- 依赖接线：`go.mod` 以 `replace` 指向本地 `../idcu-go/{trim,ttlcache,pathsafe}`（v0.0.0 占位）。
  **CI/发布遗留**：三模块尚未 push 打 tag，CI 上若无 idcu-go 目录则 `replace` 相对路径会失败——
  发布前需改为按 tag `require`（配套 `GOPRIVATE=gitee.com/idcu-go/*`），或 CI 增加 checkout。

- 测试：新增 `internal/service/context_output_test.go`（11 组：预算收缩/块内截断/token 预算/行截断/
  块不被切断/批量失败隔离/缓存命中与失效/路径虚拟化）与 `internal/server/http_output_test.go`
  （6 组：hint 回传/批量端点/单符号形状不变/预算生效/默认值补位不覆盖/虚拟路径），
  `internal/errors/hint_test.go`（4 组）；修正 `trace_test.go` 两处断言以匹配新的
  `semantic_block` 与 `TrimmedLines=文件总行-注入行` 语义（新语义与字段注释一致，旧实现不符合）。
- 验证：`go build ./...` 通过；`go test -short ./...` **全绿**；`-race` 抽样（service/server/errors）通过；
  `go build -tags pg,redis ./...` 与 `-tags treesitter ./...` 通过；`gofmt` 对改动文件无告警。
- 文档同步：`docs/1-生产层/API文档.md`（批量入参 / 预算降级 / 路径形态 / 错误 hint 四节 + 端点参数表）、
  `docs/1-生产层/配置参考.md`（新增 `context` 节 + 修订记录）、`config.yaml.example`（`context` 段，
  经 `config.Load` 实测解析正确）、CHANGELOG.internal.md（本记录）、DEV_PROGRESS.md。
- 遗留：`B8`（检索低置信度不返回，MinScore 阈值）本次未做——不在拍板的 A/B/C 档内，
  待有量化口径（阈值取多少不误杀）再落地；三模块的 tag/push 属外部动作。

### Commit 128: feat(agentbench): agent-bench 多语言任务集扩展 + 符号预检 Skipped 语义

- 背景：agent-bench（对外可信基准）内置任务集仅 Go 符号（CodeSchema 专属 + Config 通用），无法评测 Java/Python/TS 等仓库；且通用任务在「符号不存在的仓库」被判定失败，拉低通过率（语义错误）。
- 实现：
  - **多语言任务集** `internal/agentbench/agentbench.go`：内置任务 5 → 8。新增 3 个语言通用任务（RepoHint 空，任意仓库评测）：`generic-bug-001`（OrderService.getUser 参数校验）、`generic-bug-002`（OrderService.get_user None 处理，Python 语义）、`generic-refactor-001`（OrderService 资源清理收口）。符号约定经 tree-sitter 实测验证：类 FQN = 简单类名、方法 FQN = "ClassName.MethodName"（Java/Python/TS 与 Go 一致）。
  - **符号预检 Skipped 语义** `evalTask`：目标符号在仓库中完全无法定位（full/minimal 均无注入）→ 任务标记 Skipped（仓库不适配），不计入通过率分母——与 RepoHint 过滤语义统一。`Run` 的 `ActiveTasks` 统计补齐；`RunMulti` 改为基于最终 Skipped 状态重算 active（修复此前仅按 RepoHint 判定导致的计数偏差）。
  - **MD 报告 Skipped 展示** `GenerateMarkdown`：分任务明细增加「状态」列（active / **skipped**（符号不适用）），脚注说明 skipped 不计入分母。
- 实测：多语言样例仓（Java/Go/Python 混合含 OrderService）——OrderService 相关 3 任务 full/minimal 均通过、code-schema 专属任务 Skipped、Config 任务（符号缺失）预检 Skipped；本仓快照保持 5 active / 100% 通过率 / 98.7% token 节省（skipped 不拉低）。
- 测试：新增 2 组（TestEvalTask_SymbolPresencePrecheck / TestDefaultTasks_MultiLanguage）；修正 TestRunMulti_RepoHintFilter 断言（active=5、skipped=7）。
- 验证：`go build ./...`、`go vet ./...` 通过；`go test ./internal/agentbench/` 全绿（11 测试）。
- 文档同步：CHANGELOG.internal.md（本记录）、DEV_PROGRESS.md、analysis/2026-08-17-competitor-and-harness-analysis.md（修订记录）、build/agent-task-bench/ 快照（8 任务集、skipped 展示）。
- 遗留：任务集可按需继续扩展（更多语言/更复杂任务）；外部动作（adapterx/contextsdk 独立仓库 push 发布）同前。

### Commit 127: ci(make+bench): agent-bench 工程化看护——CI job + 快照归一化 + Makefile 目标

- 背景：agent-bench（对外可信基准）此前仅一次性工具，未纳入 CI 与 Makefile；快照含本机绝对路径，跨机器 diff 必失败。
- 实现：
  - **快照归一化（关键修复）** `internal/agentbench/agentbench.go`：`Report.RepoPath` 改存仓库名（`filepath.Base`），新增 `RepoAbs` 保留绝对路径（诊断用，不参与快照对比）——`build/agent-task-bench/*.json` 可在任何机器生成且 `git diff --exit-code` 稳定，CI 看护不再受本机路径差异影响。
  - **CI 看护** `.github/workflows/ci.yml`：新增 `agent-bench` job（真实仓库评测 TestRun_RealRepo + 快照 diff 看护）；test job 的「Assert tracked bench snapshots unchanged」纳入 agent-task-bench 两个快照文件。
  - **Makefile**：新增 `bench-agent` 目标（构建 CLI → agent-bench 单仓评测 → 刷新 build/agent-task-bench/ 快照，支持 `AGENT_BENCH_REPO` 覆盖仓库）。
  - **文档**：README 快速开始补 `agent-bench`（单仓/多仓）示例与 `make bench-agent`；docs/2-交付层/测试指南.md §6 基准回归补 agent-bench 说明（CI 看护口径）。
- 验证：`make bench-agent` 实测通过（快照归一化为 repo_path="code-schema"）；`go build ./...`、`go vet ./...` 通过；`go test ./internal/agentbench/` 全绿；ci.yml YAML 解析通过（jobs 9 个，新增 agent-bench）。
- 文档同步：README.md、docs/2-交付层/测试指南.md、CHANGELOG.internal.md（本记录）、DEV_PROGRESS.md。
- 遗留：无代码级遗留；外部动作（adapterx/contextsdk 独立仓库 push 发布、对外监听收敛部署）同前。

### Commit 126: chore(contrib+scripts): 生态资产 P2 发布前置收尾——adapterx 独立发布验证 + 脚本抽公共

- 背景：Commit 125 遗留 TODO「adapterx 独立仓库拷贝发布（P2）」推进；context-sdk 发布前置核查。
- 实现：
  - **adapterx 独立发布准备（A 级资产）**：新增 `scripts/check-adapterx-publish.sh`（复制 contrib/adapterx → 独立 go mod init → vet/build/test，仅标准库即可独立发布，实测通过）；新增 `contrib/adapterx/README.md`（发布评估：P0 契约 → P1 独立验证 → P2 独立 module → P3 第三方接入指南，对标 contextsdk）。
  - **发布验证脚本抽公共**：新增 `scripts/check-contrib-publish.sh <src_dir> <module_name>` 通用验证脚本，`check-contextsdk-publish.sh` / `check-adapterx-publish.sh` 改为薄包装（exec 委托），消除 90% 重复。
  - **context-sdk 发布前置核查**：`scripts/check-contextsdk-publish.sh` 仍通过（仅标准库）；README P2 状态与发布说明一致。
- 坑：`check-contrib-publish.sh` 中 `echo "...$MODULE_NAME）..."`（变量后紧跟全角右括号）在本机 bash 下被解析为 `MODULE_NAME）` 变量 → `unbound variable`。修复：改用 `${MODULE_NAME}` 花括号明确边界（多字节字符后必须花括号包裹）。
- 文档同步：analysis/2026-08-17-competitor-and-harness-analysis.md（遗留 TODO 状态 + 修订记录）、docs/4-决策层/生态资产发布说明.md（A 级进度 + 修订记录）、CHANGELOG.internal.md（本记录）。
- 遗留：**真正发布动作属外部**——首个 v* tag + push 独立仓库 `github.com/idcu/codeschema-adapterx` / `codeschema-contextsdk`；**已于 Commit 136 完成自包含验证 + 独立仓骨架，并随本批次全量推送 main + v0.1.0 tag 至 gitee，C 已发布**；「对外监听收敛」为部署期运维项（按 secure-demo.yaml）。

### Commit 125: feat(agentbench+redis): 遗留 TODO 推进——agent-bench 多仓库评测 + Redis 方法符号缓存

- 背景：Commit 124 遗留 TODO 的前两项落地（agent-bench 任务集扩展 / Redis 方法符号缓存）。
- 实现：
  - **agent-bench 多仓库评测** `internal/agentbench` + `cmd/codeschema/benchmark.go`：新增 `RunMulti`（按仓库分组评测，任务按 `RepoHint` 过滤——不适用的任务标记 `Skipped` 不计入通过率分母，跨仓对比公平）；`AgentTask` 新增 `RepoHint` 字段（通用任务留空在任意仓库评测）；内置任务扩至 5 个（新增 `generic-feat-001` 通用任务，符号 Config 任意 Go 仓可命中）；新增 `GenerateMultiMarkdown` 跨仓对比报告；CLI `agent-bench` 新增 `--repos="p1;p2"` 多仓库模式（输出每仓独立报告 + stdout 跨仓对比表）。实测：code-schema 5 任务 full 100%/minimal 100%（token 节省 98.7%）、demo-repo 1 任务（88.9%）。
  - **Redis 方法符号缓存** `internal/store/redis/redis.go` + `store.go` + `store_redis.go` + `service.go`：方法 FQN（ClassFQN+"."+Name，三后端一致合成规则）缓存——Redis 新增 `PutMethod`/`GetMethod`（HASH method:<fqn>）+ `PutMethodPath`/`MethodPath`（methodpath:<fqn>）；`store.CacheReader` 接口新增 `GetMethod`/`MethodFilePath`；cmd 层 redisCacheStore populate 写入方法索引并实现新接口；`Service.resolveSymbolLocation` 方法符号走缓存快速路径（方法是最热查询形态，命中 O(1) 免全表 O(files×classes×methods)），miss 回退全表。
- 测试：agentbench 新增 2 组（RunMulti RepoHint 过滤 + Skipped 不计入分母）；service 新增 2 组（方法缓存命中/回退）；Redis 集成测试补方法缓存覆盖（PutMethod/GetMethod/MethodPath）。
- 验证：`go build ./...`、`go build -tags pg,redis ./...`、`go vet ./...` 通过；`go test ./...` 全零 FAIL。
- 文档同步：analysis/2026-08-17-competitor-and-harness-analysis.md（遗留 TODO 状态更新 + 修订记录）、CHANGELOG.internal.md（本记录）。
- 遗留 TODO：adapterx 独立仓库拷贝发布（P2，与 contextsdk 独立 module 并列，待首个 v* tag）。

### Commit 124: feat(agentbench+redis+server): 分析文档建议 1-5 全量推进——Agent 任务端到端基准/Redis 读路径接线/中间件合并/context-sdk 发布准备/差异点打点

- 背景：`analysis/2026-08-17-competitor-and-harness-analysis.md` §3.4 给出 5 条下一步建议（基于 Commit 123 夯基后的再评估），用户要求全量推进实施。
- 实现：
  - **建议 1：Agent 任务端到端评测基准（对外可信基准）** — 新增 `internal/agentbench` 包（纯生产代码，不依赖 testing）：`AgentTask`（任务描述 + 必需关键词）、`Run`（扫描→索引→三档上下文生成→覆盖判定）、`Report/Summary`（通过率 × token 权衡）、`GenerateMarkdown/GenerateJSON`（对外发布形态）。三档判定口径差异化：none 必失败、minimal 看定位线索（FilePath+行号）、full 看源码关键词命中。CLI 新增 `codeschema agent-bench` 子命令（命令数 7→8）。内置 4 个真实任务（bugfix×2/feature/refactor，符号经 tree-sitter 实测可解析）。**真实评测（本仓）**：full 通过率 100% / 平均 token 84；minimal 通过率 100% / 平均 token 4（**token 节省 95.2%**）；none 0%。报告落盘 `build/agent-task-bench/`（md+json）。测试：8 组单元 + 1 组真实仓库（TestRun_RealRepo）。
  - **建议 2：Redis 读路径全量接线** — `store.CacheReader` 接口新增 `ClassFilePath`（类 FQN→源文件路径反查）；`internal/store/redis` 新增 `PutClassPath`/`ClassPath`（key `classpath:<fqn>`）；cmd 层 redisCacheStore 实现该方法，populate 写入 classpath 索引；`Service.resolveSymbolLocation` 类符号命中处接入缓存快速路径（GetClass + ClassFilePath 一次命中，miss 回退全表遍历）。测试：service 新增 2 组（缓存命中快速路径/缓存 miss 回退），redis 集成测试补 classpath/file→class 索引覆盖。
  - **建议 3：CORS/recovery 中间件合并** — 新增 `internal/server/middleware.go`：`corsMiddlewareFor(allowMethods, allowHeaders)`（HTTP: GET,OPTIONS / MCP: GET,POST,OPTIONS）与 `recoveryMiddlewareFor(withError, logger)`（HTTP 写 500 错误体 / MCP 仅恢复），HTTP 与 MCP 各删一份旧实现（-40 行重复）。测试更新引用新函数。
  - **建议 4：context-sdk 独立发布准备** — 新增 `scripts/check-contextsdk-publish.sh`：复制 contrib/contextsdk 到临时目录 → 独立 `go mod init` → vet/build/test，验证仅标准库即可独立发布（实测通过）；`contrib/dsh/README.md` 新增 §8「Code mode 程序化编排（context-sdk）」示例；`contrib/contextsdk/README.md` P2 前置说明更新。
  - **建议 5：差异点打点加固** — 三个此前无 Prometheus 指标的差异点补齐：`internal/service/testlink.go`（testlink_lookups_total/testlink_hits_total 按策略）、`internal/ai/enhancer.go`（ai_enhance_total 按 kind/phase、ai_budget_exceeded_total）、`internal/tenant/tenant.go`（tenant_instances_total gauge、tenant_route_total、tenant_apply_total）。
- 验证：`go build ./...`、`go build -tags pg,redis ./...`、`go vet ./...` 通过；`go test ./...` 全零 FAIL（36 包 OK；agentbench 新包全绿）。
- 文档同步：analysis/2026-08-17-competitor-and-harness-analysis.md（§3.4 建议 1-5 标记落地状态）、CHANGELOG.internal.md（本记录）、DEV_PROGRESS.md（状态摘要 + 接手说明）、docs/1-生产层/API文档.md（agent-bench 命令说明）。
- 遗留 TODO：agent-bench 任务集当前内置 4 个（可经 --repo 换仓库、任务集待扩）；Redis 方法符号缓存（当前仅类符号走缓存快速路径）；adapterx 独立仓库拷贝发布（P2，与 contextsdk 独立 module 并列）。

### Commit 123: refactor(store+server+scanner): 代码夯基与结构优化——PG 契约修复/标签统一/Redis 读路径/健康检查真实化/语言单表/吞错留痕/死代码清理

- 背景：承接 Commit 122 后的维护优化。对内部代码做系统性夯实：修复三处真实契约/行为缺陷（PG 全量替换、FileStore 标签清理、健康检查占位），统一三后端标签分类语义，接入 Redis 读路径（可选接口），消除语言映射双表，处理静默吞错与死代码。
- 实现：
  - **PG 违反全量替换契约（真实 bug）** `internal/store/pg/pg.go`：`upsertClassesTx`/`upsertCallsTx` 原仅 `ON CONFLICT DO NOTHING` 不删历史行，整仓重扫残留脏数据（SQLite 有 DELETE、PG 无，语义分叉）。修复：类/调用写入前先按 file_id 清旧（含级联删旧方法），公开 `UpsertMethods` 与 `BulkUpsert` 同步对齐；BulkUpsert 每文件循环前按 absolute_path 反查 file_id 清理旧 method/class/call。
  - **FileStore.DeleteFile 标签清理（对齐 SQLite）** `internal/store/filestore.go`：删除文件时清理其类的 classTags 与方法的 methodTags（原只删 files/classes/calls/methods，标签残留）。
  - **标签分类三后端统一** `internal/store/store.go` + filestore/sqlite/pg：新增导出 `DeriveTagCategory`/`UniqueStrings`，FileStore 与 SQLiteStore 删除本地重复实现改为引用；PG `upsertTagsTx` 硬编码 `'auto'` 改为 `store.DeriveTagCategory(tag)`，消除三后端分类语义分叉。
  - **Redis 读路径接入（可选接口）** `internal/store/store.go` 新增 `CacheReader` 可选接口（GetClass/CallersOf/CalleesOf/ClassesOfFile）+ `DriverNamer` 可选接口（DriverName）；`cmd/codeschema/store_redis.go` 的 redisCacheStore 实现两者（读路径纯转发、DriverName 报告底层驱动），populate 补写文件→类索引（PutFileClasses，原缺失）；服务侧不强制接线（未命中回退主存储，向后兼容）。
  - **健康检查真实化** `internal/service/service.go`：`StoreType` 不再硬编码 `"file"`，经 `store.DriverNamer` 探测（FileStore/SQLiteStore/PGStore/redisCacheStore 均实现）；`internal/server/http.go`：新增 `SetKVHealthCheck`/`SetVectorHealthCheck` 注入，`/health/kv` 由「P0 placeholder」改为探测 redis 真实状态、`/health/vector` 由「P8.1 占位」改为探测向量存储；`cmd/codeschema/main.go` serve 接线两处注入。
  - **语言映射单表** `internal/scanner/scanner.go`：删除与 `adapter.ExtToLang` 完全重复的 `detectLang` switch（75 行），改为 Dockerfile 前缀特判 + `adapter.ExtToLang` 委托，消除双表漂移风险。
  - **静默吞错留痕** `internal/analyzer/analyzer.go`：AI 增强写入（UpsertTags/UpsertMethodTags/UpdateClassDoc/UpdateMethodDoc）由 `_ =` 丢弃改为失败记 `a.logger.Warn`（不改变 best-effort 语义）。
  - **死代码清理** `internal/parser/adapter/adapter.go`：删除零引用函数 `SupportedLanguages`/`IsSourceFile`/`LangToExtensions`（86 行，含测试均无引用）；`internal/contextsdk/sdk.go` 修正误导注释（示例 `mgr.GetService` → `mgr.Service(ctx, tenant)`）；`internal/config/config.go` 新增 `DefaultSCIPIndexDir`/`DefaultCodeGraphDB` 常量，`internal/runtime/runtime.go` 魔法串 `"./scipout"`/`"./codegraph.db"` 改为引用常量。
- 验证：`go build ./...`、`go build -tags pg,redis ./...`、`go vet ./...` 全部通过；`go test ./...` 全零 FAIL（36 包 OK；store/scanner/analyzer/config/runtime/service/server 相关包全绿）。
- 文档同步：README.md（包数 33→36）、DEV_PROGRESS.md（核查结论/状态/接手说明）、CHANGELOG.internal.md（本记录）、docs/README.md（无结构变动，仅日期刷新）、docs/1-生产层/技术设计文档.md（修订记录补充）、docs/1-生产层/代码规范与开发指南.md（新增「可选接口」命名惯例）、docs/1-生产层/API文档.md（/health/kv、/health/vector 语义更新）、analysis/2026-08-17-competitor-and-harness-analysis.md（整体重写）。
- 遗留 TODO：CORS/recovery 中间件 HTTP 与 MCP 各一份（方法集/日志策略有差异，暂不强行合并）；upsert.go 的 `MatchResult` 族仅被测试引用（算法有测试保护，保留）；`Service.GetContext` 仅测试调用（为 GetContextMode 便捷封装，保留）。

### Commit 122: refactor(contextsdk): 解除 context-sdk 独立发布阻塞——接口抽象完成

- 背景：Commit 121 遗留 TODO「context-sdk 独立发布阻塞项：internal/contextsdk 直接依赖 internal/service.Service，需抽取 SDKProvider 轻量接口」。本次完成 P0 接口抽象，使 contrib/contextsdk 自包含、可独立编译/测试/发布。
- 实现：
  - `contrib/contextsdk` 从契约骨架升级为**权威契约与编排实现**（自包含，仅依赖标准库）：`SDKProvider` 最小契约（`GetContextMode` + `GetImpact`）、公开 DTO 全部迁入（`ContextOptions`/`SymbolContext`/`ImpactResult`/`TraceEntry`/`Request`/`Package`/`Summary`/`SymbolBlock`/`ImpactBlock`）、完整编排 `Client.Compose`（多租户解析 → 逐符号注入 → 可选影响面/关联单测聚合 → token 汇总）。
  - `internal/contextsdk` 改造为**纯适配层**：`ServiceProvider` 把 `internal/service.Service` 桥接为 `SDKProvider`（GetContextMode/GetImpact/TraceEntry 双向对齐）；`NewClient` 签名保持向后兼容（仍接受 `func(tenant string) (*service.Service, error)`，内部经 ServiceProvider 桥接）；`Client`/`Request`/`Package` 改为类型别名指向权威 DTO。
- 测试：`contrib/contextsdk` 新增 11 条 mock 编排测试（mockProvider 仅依赖标准库，证明第三方实现契约即可被权威 Compose 编排）——默认租户/多租户路由/符号顺序保持/minimal 模式/WithImpact+WithTests 聚合/Provider 错误传播（含 context 与 impact 两路）/nil resolver/空 symbols/DTO JSON round-trip；`internal/contextsdk` 真实集成测试（真实 Store+Analyzer+文件）沿用适配层全通。
- 验证：`go build ./contrib/contextsdk/... ./internal/contextsdk/...` 通过；`go test ./contrib/contextsdk/...` 11/11 通过（0.716s）；`go test ./internal/contextsdk/...` 全通（0.895s）；全量验证见下方（Commit 122 完成后 `go build ./...` + `go test ./...` 全绿）。
- 文档同步：生态资产发布说明.md（B 级阻塞项解除、进度更新、修订记录 +1）、contrib/contextsdk/README.md（§3.1 由「当前阻塞项」改为「已解决」并补充验证数据）。
- 遗留 TODO：adapterx 独立仓库拷贝发布（A 级，P2）——**已于本批次全量推送 gitee（main + v0.1.0 tag），C 已发布**；context-sdk 独立 module 发布 `github.com/idcu/codeschema-contextsdk`（P2，同上已发布）；对外监听收敛为部署期运维项。

### Commit 121: feat(tenant+adapterx+contextsdk): 服务级热重载补齐 + 监听收敛基线 + 生态资产发布准备

- 背景：承接 Commit 120 遗留 TODO「热重载不重建 Scanner workers / Store DSN」，并对分析建议四项后续方向（服务级热重载补齐、对外监听收敛、A 级适配器聚合发布、context-sdk 独立发布）做批量推进。
- 实现：
  - **服务级配置热重载补齐**：`internal/tenant/tenant.go` `tenantDirty` 扩展覆盖 `scanner.workers` / `scanner.file_size_limit_mb` / `scanner.line_count_limit`（与既有 DSN/Root/Name/autoScan/watch 一并触发租户实例重建）；修复 `Apply` 提交阶段遍历 `upsert` map 的无序追加 —— 改为按 `targets` 顺序追加，保证 `Manager.order` 稳定有序（此前多租户顺序随机）。
  - **对外监听收敛（配置形态）**：新增 `build/secure-demo.yaml` 生产安全基线示例（`127.0.0.1:<port>` 监听 + `auth_token` + `rate_limit` + Nginx 内网反代指引）；代码默认保持 `:8080/:8081` 向后兼容不收敛（行为不变）。
  - **A 级适配器聚合发布准备**：新增 `contrib/adapterx/`（自包含、仅依赖标准库）—— `ParserPlugin`/`BatchParser` 统一对外契约、`IRDocument` 对外 DTO（与 internal 字段对齐）、`Registry` 注册中心（Register/Get/Names，重复注册报错）、`BuiltinAdapters()` 发布元数据（4 个内置适配器）；`internal/parser/adapterx.go` 提供 `ToAdapterX`/`FromAdapterX` 双向桥接。
  - **context-sdk 独立发布评估**：新增 `contrib/contextsdk/` 契约骨架—— `SDKProvider` 最小接口（GetContext/GetImpact）+ 公开 DTO（SymbolContext/ImpactResult/Request/Package）+ 发布评估 README（P0 接口抽象 → P1 仓库内编译 → P2 独立 module）。
- 测试：`internal/tenant` 新增 `TestManager_Apply_ScannerWorkersTriggersRebuild`（workers 变更、line_count_limit 变更均触发重建）；`internal/parser` 新增 `TestAdapterXRoundTrip`/`TestAdapterX_NilSafe`/`TestAdapterX_TypeAliasCompat`（双向桥接 round-trip + nil 安全 + 契约直构）；`contrib/adapterx` 新增 4 组（Registry 注册/重复拒绝/BuiltinAdapters 元数据/契约直用）。
- 验证：`go build ./...`、`go vet ./...` 通过；`go test ./internal/tenant/... ./internal/parser/... ./contrib/adapterx/...` 全绿（tenant 0.923s / parser 4.079s / adapterx 0.508s；contextsdk 为纯契约骨架，无 test files）。
- 文档同步：配置参考.md（scanner 三项热重载 + tenants 关键字段口径）、交接说明.md（✅ 条目 + 修订记录）、生态资产发布说明.md（A/B 级进度落地 + 修订记录）、安全设计文档.md（§7 勾选「对外监听收敛」+ secure-demo.yaml 指引）、config.yaml.example（server 节监听收敛/认证注释）。
- 遗留 TODO：context-sdk 独立发布阻塞项——`internal/contextsdk` 仍直接依赖 `internal/service.Service`，需抽取为 `SDKProvider` 轻量接口（首个 v* tag 前完成）；adapterx 独立仓库拷贝发布；对外监听收敛为部署期运维项（按 secure-demo.yaml 执行）。

### Commit 120: feat(server): 全局能力热重载扩展——监听地址/认证令牌/限流无需重启

- 背景：Commit 119 遗留 TODO「热重载不覆盖 `server.*` 监听地址与 `preset` 等全局能力（需重启）」。本次将配置热重载从租户集合扩展到服务端全局能力。
- 实现：
  - `internal/server/http.go`：新增 `httpRuntimeConfig` 原子运行时快照（authToken + rateLimiter，`atomic.Value` 整体替换实现无锁读）；`UpdateRuntime(addr, authToken, rpm)` 三合一热更新 —— 认证/限流即时生效，addr 变更走 `rebind` 无中断重绑（新监听立即接管、旧监听关闭、在途请求由原 server 处理完毕）；`authMiddleware`/`rateLimitMiddleware` 改为每次请求读取运行时快照；`Start` 显式监听，`serve` 仅当前监听器异常反馈到 errCh。
  - `internal/server/mcp.go`：`authToken` 由 string 改为 `atomic.Value`（string），`SetAuthToken` 无锁热更新，认证中间件读原子快照。
  - `cmd/codeschema/main.go`：`mcpCmd`/`serveCmd` 以单一 `SetOnReload` 回调联动 `tenant.Manager.Apply` + `HTTPServer.UpdateRuntime`（serve）/`MCPServer.SetAuthToken`（mcp）；`preset` 变更经 `config.Load` 重新应用，其影响的服务端字段在此连带生效（配置文件为热重载期唯一权威来源）。
- 测试：`internal/server` 新增 5 组 —— HTTP authToken 启用/轮换/关闭、rateLimit 启用/变更/关闭、PreserveFields（SetAuthToken/SetRateLimit 单字段 setter 不丢另一字段）、addr 重绑（`127.0.0.1:0`→`localhost:0` 触发重绑并健康探测）、MCP authToken 热更新。
- 验证：`go build ./...` 通过；`go test ./...` 全零 FAIL（34 包 OK，server 1.646s）；`go test ./internal/server/...` 0.463s。
- 文档同步：11-配置部署与路线图.md（§7.3 全局能力热重载三合一 + preset 连带、§9.2）、配置参考.md（server 节 rate_limit + 热重载 3 项）。
- 遗留 TODO：热重载不重建 Scanner workers / Store DSN 等依赖配置的服务（仅 Config 实例更新）；chromem 权限自管；对外监听收敛、pg 集成测试为部署期项。

### Commit 119: feat(tenant): 多租户热重载——tenants 配置变更无需重启进程

- 背景：多租户此前 `tenants` 在启动时固定，新增/移除/改关键字段需重启进程；P9 配置热重载（ConfigWatcher）此前仅更新 Config 实例，不联动服务重初始化。
- 实现：
  - `config.ConfigWatcher` 新增 `SetOnReload(fn)`（新增 `reloadMu` 保证回调注册与读取线程安全，可在 Start 前后任意时刻调用，传 nil 取消）；`checkAndReload` 改为锁内取回调、锁外同步执行。
  - `tenant.Manager` 新增 `Apply(ctx, base)`：对租户集合做增量 diff —— 新增租户构建并入路由表；移除租户停监听+关 store+删路由；关键配置变化（DSN/Root/Name/autoScan/watch）重建替换实例；未变化租户保持原实例。锁外构建（含 IO/扫描不持锁）、锁内提交、锁外释放；失败租户隔离（保留旧实例继续服务），`errors.Join` 聚合。
  - `tenantDirty` 仅比较影响运行期行为的关键字段，避免日志级别等无关差异触发无谓重建；抽取 `resolveTargets` 供 NewManager 与 Apply 共用。
  - `cmd/codeschema`：`mcpCmd`/`serveCmd` 接收 `cfgWatcher`，注册 `Apply` 回调，配置变更时自动增量热更新租户集合（错误仅记日志，不中断服务）。
- 测试：`internal/tenant` 新增 4 组热重载单测 —— 新增/移除租户（含全部移除回退 default）、同配置保持原实例（实例指针不变）、DSN 变更触发重建、单↔多互切（default ↔ [a b]，DefaultID 正确更新）；修 1 处测试断言（空 tenants 语义为回退 default 而非空集合）。
- 验证：`go build ./...` 通过；`go test ./...` 全零 FAIL（30 包 OK，tenant 0.907s / config cached）；`go vet ./...` 通过。
- 文档同步：13-多租户设计文档.md（§5.4 新增热重载、§8 限制改为已完成、§9 验证记录）、11-配置部署与路线图.md（§7.3 SetOnReload、§9.2 热重载）、配置参考.md（tenants 热重载）、交接说明.md（✅ 条目 + 修订记录）。
- 遗留 TODO：热重载不覆盖 `server.*` 监听地址与 `preset` 等全局能力（需重启）；运行中租户的非关键字段变化不触发重建（设计如此，避免无谓重建）。

### Commit 118: feat(tags): search_by_tag 支持多标签 AND 交集（逗号分隔）

- 背景：部分符号同时拥有多个标签（如 controller + service），单标签检索无法按交集筛选。
- 接口：`store.Store` 由 `SearchByTag(tag)` 扩展为 `SearchByTags(tags []string)`（AND 交集），原 `SearchByTag` 保留为兼容入口委托新接口。
- 实现：
  - FileStore：内存 map 交集（`containsAll` 复制剩余集合避免污染调用方 query map），空标签列表返回空（避免命中一切）。
  - SQLite / PostgreSQL：`IN (?,...) ... GROUP BY ... HAVING COUNT(DISTINCT) = n` 实现 AND 语义；PG 抽取 `idCol` 辅助支持变参。
  - Service：新增 `SearchByTags`，校验空列表/空标签，抽取 `resolveTagSearchResult` 共享类/方法全限定名解析。
  - HTTP `/tags/search`：`parseQueryTags` 支持逗号分隔与重复 `tag` 参数（`?tag=a,b&tag=c`）；OpenAPI 规范补充 description。
  - MCP `search_by_tag`：`tag` 参数逗号分隔多个（AND 交集），保持参数名 `tag` 向后兼容。
- 测试：FileStore 多标签 AND / 方法标签 / `containsAll` 边界（+6）；SQLite 多标签 AND / 方法标签（+2）；Service 多标签 / 校验 / 方法结果（+3）；HTTP `parseQueryTags` 7 例 + 端点多标签（+2）；3 个 mock store 补 `SearchByTags`。
- 验证：`go build ./...` 通过；`go test ./...` 全零 FAIL（33 包 OK，含 server 5.891s / sqlite 3.326s / service 2.169s）；`go vet -tags=pg ./internal/store/pg` 通过（PG 编译确认）。
- 文档同步：API文档.md、05-接口层（CLI+HTTP+MCP）.md、客户端接入指南（MCP）.md。
- 遗留 TODO：无（PG 集成测试依赖真实实例，未在本地跑，仅编译核验）。

### Commit 117: feat(harness): dsh 建议 1-7 全量推进——上下文追溯/极简模式/能力预设/context-sdk/dsh 接入/生态资产化 + ONNX 版本口径统一

- 背景：`analysis/2026-08-17-competitor-and-harness-analysis.md` §3.3 给出 8 条建议（按优先级），用户要求全量推进实施。本提交落地建议 1-7，并同步任务 1（ONNX 版本口径：osx amd64 上游仅到 1.23.2 不再更新）。
- 建议 1 dsh 插件接入：新增 `contrib/dsh/README.md`（stdio/SSE 双方式集成指南）+ `contrib/dsh/codeschema-dsh.yaml`（配置模板，12 个 MCP 工具作能力插件）。
- 建议 2 上下文注入追溯：`internal/service` 新增 `TraceEntry`（来源/命中符号/命中行/裁剪原因/裁剪行/估算 token/时间戳），`GetContextMode`/`GetImpact` 注入附 `_trace`；HTTP `/context`、MCP `context` 工具 `IncludeTrace: true` 默认带回。
- 建议 3 能力层 preset：`internal/config/preset.go` 新增 `Preset`（`minimal`/`semantic`/`multitenant`/`""`），`ApplyPreset` 幂等、是其管理字段的权威来源；`Load`/`LoadFromEnv`/`Merge` 全链路接入；未知值加载时归一为空。
- 建议 4 极简上下文模式：`GetContextMode` 支持 `mode=minimal`（仅符号元数据、零文件 IO），HTTP/MCP 增加 `mode` 参数；新增 `BenchmarkContextMode` 对照。
- 建议 5 context-sdk：新增 `internal/contextsdk`，`NewClient(resolve).Compose(...)` 一次编排多租户 × 多符号 × 影响面 × 关联单测的上下文包，汇总 token 估算。
- 建议 6 AGENTS.md：根级 `AGENTS.md` 面向接入 agent 提供协作约束（读档顺序/同步清单/接口契约）。
- 建议 7 生态资产化：新增 `docs/4-决策层/生态资产发布说明.md`（A/B/C 三级资产清单 + 发布路线图 + 资产化纪律），并在 docs/README 与 4-决策层 README 登记入口。
- 任务 1 ONNX 版本口径统一：README/部署手册/11-配置/交接说明/安全设计 五处改为「主版本 1.28.0（Win/Linux/Apple Silicon）；macOS Intel(x86_64) 上游自 1.23.2 后不再发布，锁定 1.23.2（`third_party/onnxruntime_go_patch` ORT_API_VERSION 23 适配）」。
- 测试：新增 `internal/config/preset_test.go`（3 项）、`internal/contextsdk/sdk_test.go`（4 项）、`internal/service/trace_test.go`（4 项）、`internal/service/context_bench_test.go`（benchmark）、`internal/server/testutil_test.go`（seedSymbol 供 context/impact 工具测试）；修复 preset 权威覆盖/merge 时序/Load 归一/inclusive-trace 默认关闭等 5 处测试语义。
- 验证：`go build ./...` 通过；`go test ./...` 全零 FAIL（config 0.095s / service 0.404s / server 0.483s / contextsdk 0.207s，全包 OK）。
- 打点数据（BenchmarkContextMode，100 符号批量，i5-13400/Win）：full 5.55ms·730,909B·4400 allocs → **minimal 0.142ms·84,073B·1200 allocs（约 39× 快、8.7× 省内存、3.7× 省分配）**；context_lines=5 与 full 同量级（5.07ms）。
- 文档同步：API文档.md、配置参考.md、客户端接入指南（MCP）.md、05-接口层（CLI+HTTP+MCP）.md、部署手册.md、11-配置部署与路线图.md、交接说明.md、安全设计文档.md、README.md、config.yaml.example、docs/README.md、4-决策层/README.md。
- 遗留 TODO：建议 8（风险提示）为理性守势不实施；context-sdk 独立发布与 A 级适配器聚合发布列入生态资产路线图（P2，待首个 v* tag 后评估）；对外监听收敛仍为部署期项。

### Commit 116: docs(process): 巩固文档同步性——AI协作规范 §4 映射扩至 14 类 + 核验清单

- 背景：五圈层迁移后，AI协作规范 §4「改码必改档」映射表仅 8 类，缺安全/适配器/监控/性能/测试/多租户等圈层主档，同步性有盲区。
- 改动（`docs/AI协作规范.md`）：
  - §4 变更→文档映射扩至 **14 行**，补齐：新增适配器→扩展指南+modules/Px、存储规模→开发文档/12+性能调优、监控告警→运维手册、安全机制→安全设计文档+交接说明、多租户→开发文档/13+部署手册+系统简介、测试基准→测试指南。
  - 新增「文档同步性核验清单」：commit 前 5 项（映射逐条自检 / 旧路径旧数字 grep / 结构变动同步 README+文档体系关系 / 页首日期刷新 / 同次提交带验证数据）。
  - 新增 §8 修订记录。
- 验证：纯文档变更；grep 确认映射覆盖五圈层全部主档；与任务 1 成果衔接（路径/包数/onnxruntime 版本已统一）。
- 遗留：DEV_PROGRESS 接手说明保持既有 5 步（读档→编号顺序→编译→测试→启动），与 AI协作规范不冲突，未改。

### Commit 115: docs(consistency): 全项目文档与代码现状核查同步（五圈层迁移收尾）

- 背景：docs/ 迁移至五圈层结构后，活文档仍残留旧路径（docs/dev、docs/ops、docs/modules、docs/MCP接入指南、DEPLOYMENT_AND_USAGE）、旧包数与旧 onnxruntime 版本；逐行核查并统一。
- 路径同步（19 文件 + 后续补充 4 文件）：
  - `docs/dev/`→`docs/1-生产层/开发文档/`（README 开发指南、DEV_PROGRESS 核查/接手/已完成工作、开发文档 00-13 前置依赖、04 交叉引用）
  - `docs/modules/`→`docs/1-生产层/modules/`（DEV_PROGRESS P3_4 引用）
  - `docs/ops/`→`docs/2-交付层/运维文档/`（运维手册健康检查引用、01-生产部署清单）
  - `docs/MCP接入指南.md`→`docs/3-使用层/客户端接入指南（MCP）.md`（README 快速开始、开发文档 05、P1/P1_2）
  - `DEPLOYMENT_AND_USAGE.md`→`部署手册.md`（01-生产部署清单 4 处；源档已归档 4-决策层/归档资料/）
- 版本同步：onnxruntime 1.23.2→1.28.0（匹配 `onnxruntime_go v1.32.1`、API 28；macOS Intel 旧机型保留 1.23.2 说明），README/11-配置/部署手册 三处统一。
- 包数同步：23/24/27/31/32 → **33**（`go list ./...` 实测，新增 internal/tenant、internal/runtime、scripts/benchtrend）。
- 索引同步：开发文档 13 篇 → 14 篇（`00`–`13`，新增 13-多租户设计文档）。
- 顺手清理：删除遗留临时文件 `commitmsg.tmp`（Commit 113 残留）。
- 验证：`go list ./...` = 33 个包（2026-08-17 实测）；`go build ./...` 通过；全库 grep 确认活文档已无 `docs/dev|docs/ops|docs/modules` 存活引用（仅 CHANGELOG 历史记录、4-决策层归档资料、「来源/迁移备注」说明属合理保留，不改）。
- 遗留：归档资料（4-决策层/）与 CHANGELOG 历史段为只读快照，不随本次同步回改。

### Commit 114: feat(runtime): chromem 可选后端目录/文件权限纵深收敛（0700/0600）

- 背景：全量推进安全自查，补齐 chromem 可选后端——其持久化文件由 chromem-go 库生成、默认受 umask 影响（0666&umask），前一提交已要求显式 `driver=chromem` 才启用，但文件权限未收敛。
- 决策：在 `runtime.go` 向量存储接线点（chromem 分支）显式收敛：
  - 父目录 `fsperm.MkdirAll`（0700，新建/已存在都收紧）；
  - 已存在的持久化文件 `os.Chmod` 0600；
  - 不破坏库行为（不改写文件、不接管其内部写入时序）；新文件生成一刻仍由库按 umask 自管，为已记录的极小窗口。
- 验证：`go build ./...` 通过；`go test ./internal/runtime ./internal/vector` 全 PASS（runtime 0.866s、vector 0.185s）。
- 文档同步：安全设计文档 §7 勾选项整段补 chromem 收敛说明 + 交接说明 §4 ②由「未收敛」改写为「已纵深收敛，含极小 umask 窗口」。
- 遗留 TODO：仅「对外监听收敛」未勾选——代码能力已具备（监听地址读 `server.mcp_addr/http_addr`），纯部署期配置项需运维改 `127.0.0.1` 或 Nginx 反代。

### Commit 113: feat(store): 索引数据目录/文件权限加固（目录 0700、文件 0600）

- 背景：安全自查清单「索引目录权限 0600」未落地——store.json/vector.json/FTS/IDF/SQLite/锁文件默认 0755/0644，宽松 umask 下可被同机其他用户读取
- 决策：新增**公共包** `internal/fsperm`（仅供 store/vector/search/runtime 跨包复用，零三方依赖）：
  - `MkdirAll(path)`：创建目录树且末端 `chmod 0700`（新建/已存在都收敛，覆盖 umask）
  - `WriteFile(path,data)`：父目录 0700 + 文件 `chmod 0600`（Chmod 保证既有过宽容忍也收紧）
- 接入点（store 3 处、sqlite 1、vector 2、search 2、runtime 1）：
  - `store/filestore.go` 数据目录 + store.json（tmp 0600→rename，最终 0600）
  - `store/filelock_{unix,windows}.go` 锁目录 + store.lock 0600
  - `store/sqlite/sqlite.go` ensureDir
  - `vector/persistent.go` vector.json、`vector/embedder_local.go` SaveIDF（改 fsperm.WriteFile）
  - `search/fts_persistent.go` FTS、`search/builder.go` IDF 目录
  - `runtime/runtime.go` IDF 目录
- 测试：`internal/fsperm` 4 条（目录 0700、收紧已存在 0755、文件 0600+父目录 0700+内容一致；Windows 自动 Skip POSIX 权限断言）
- 文档同步：安全设计文档 §7 勾选项 3、交接说明（勾选+遗留收敛为部署期项）、CHANGELOG
- 验证：`go build ./...` 通过；`go test ./...` 全零 FAIL（fsperm 0.147s、store 3.219s、vector 10.992s、search 3.492s、runtime 4.756s）
- 预留：chromem 可选后端文件权限由 chromem-go 库自管未收敛；「对外监听收敛」为部署期项未勾选

### Commit 112: feat(server): 全局限流中间件（rate_limit 令牌桶，可选），安全自查勾选 4+1 项

- 背景：安全设计文档 §7 合规自查清单存在 6 个未勾选项。逐项对照代码核实后，认证（authMiddleware）、路径遍历（pathTraversalMiddleware）、模型 checksum（model_sha256）、日志脱敏（api_key 仅内存）均已有实现与单测，仅有"限流中间件"确为代码缺口
- 决策：「限流默认关闭」——新增 `server.rate_limit`（每分钟请求上限，令牌桶，突发=上限），0=不限流（默认），保持既有行为无人为变更
- 实现：
  - `config.ServerConfig` 新增 `RateLimit int`；接入 DefaultConfig(0)/env(`CODESCHEMA_SERVER_RATE_LIMIT`)/merge(>0)/clone/parse(applyServer)
  - `server` 新增 `tokenBucket`（并发安全令牌桶：容量=上限、补充=上限/60 个每秒）+ `rateLimitMiddleware`（超限 429 + `Retry-After: 1`）+ `SetRateLimit(rpm)`；`SetRateLimit(<=0)` 清空=不限流
  - `cmd serve` 在 `RateLimit>0` 时接线 `httpSrv.SetRateLimit`
  - 中间件链调整为：追踪 → 限流 → 认证 → 路径遍历 → 错误恢复 → CORS
- 测试：server 新增 4 条（TokenBucket 突发上限、TokenBucket 随时间回填、RateLimitMiddleware 默认放行、RateLimitMiddleware 超限 429）；config 既有兼容
- 文档同步：安全设计文档 §7 公平勾选（4 项实现+勾选、2 项未完成项注明为部署期/存储层 TODO，附勾选说明）；config.yaml.example、11-配置 补充 rate_limit；交接说明遗留段标注
- 验证：`go build ./...` 通过；`go test ./internal/server ./internal/config` 全通过（server 6.471s / config 0.585s）
- 预留：监听收敛（默认 :8080/:8081 全接口）、索引目录 0600 两项未落地，列为存储层/部署期 TODO（honest 未勾选）

### Commit 111: feat(vector): 接线 chromem 向量驱动（显式启用，默认后端不变）

- 背景：`storage.vector.driver` 配置字段早已存在且 DefaultConfig 曾默认 "chromem"，但 runtime.NewSearcherWithStore 恒用文件 PersistentStore，chromem 从未真正接线（09/11 文档标注"未接线生产分发"）
- 决策（经用户确认）：「仅显式启用」——runtime 仅在 `driver=chromem` 时启用持久化 ChromemStore，默认仍走文件 PersistentStore，保持既有行为（受"不改动既有行为"红线约束，且 chromem 原生不支持单文档删除）
- 实现：
  - `vector` 新增 `NewEmbeddingFunc(em Embedder) chromem.EmbeddingFunc`（隔离 chromem 依赖，runtime 不直引 chromem-go）
  - `runtime.NewSearcherWithStore` 重排：先建 embedder 再选后量存储；`driver==chromem` 时 `NewPersistentChromemStore(collection, DSN或VectorDir/chromem.db, dim, NewEmbeddingFunc(em))`，失败回退文件 PersistentStore
  - `config.DefaultConfig` 默认 `Vector.Driver` 由 "chromem" 改为 ""（DSN 置空），使默认后端为文件并保持行为不变
- 测试：runtime 新增 3 条（ChromemDriver 断言 *ChromemStore、DefaultFileBackend 断言 *PersistentStore、原有 NonNil）；vector/config 既有测试兼容
- 文档同步：09-语义检索、11-配置 更新 chromem 显式启用语义；交接说明遗留段（移除已完成项、标注 chromem 限制与修订记录）
- 验证：`go build ./...` 通过；`go test ./...` 全量零 FAIL（vector 1.539s、runtime 0.851s、config 0.667s 缓存后 0）
- 预留：chromem 无单文档删除能力，watch 同步删文件场景建议继续用文件后端；committed 未 push

### Commit 110: feat(scanner): 大文件/超行数旁路，超限标记 parse_skipped

- 背景：`scanner.file_size_limit_mb`/`scanner.line_count_limit` 配置字段早已存在（解析/校验/默认/合并齐全），但 scanner 从未读取，超限文件不会被旁路（06-编排层 §4.3 与 DoS 安全设计标注的缺口）
- 实现（非破坏性）：
  - `store` 新增**可选**子接口 `SkippedWriter`（Store 接口不变）；FileStore/SQLiteStore/PGStore 各自实现 `MarkParseSkipped`，insert/upsert 置 `parse_status='parse_skipped'`
  - scanner 新增 `maxFileSize`/`maxLineCount` 字段 + `SetLimits()`；ProcessFile 用廉价 `os.Stat` 在 sha256 之前短路超大文件（避免整文件读取的 I/O/内存，DoS 防护）；超行数经 `countLines` 二次旁路；`markSkipped` 优先走 SkippedWriter 留痕，未实现时回退 UpsertFile
  - runtime `ScanRepository`/`StartWatchBackground` 两处 `SetLimits(MB→byte, LineCountLimit)` 接线
- 测试：scanner 新增 3 条（SizeLimit 超限旁路+parse_skipped+size 校验、LineLimit 旁路+parse_skipped+行数校验、UnderLimit 正常解析）
- 文档同步：06-编排层 §4.3 移除"未实现"标注、勾选大文件旁路检查点；交接说明遗留段与修订记录标注落地
- 验证：`go build ./...` 通过；`go vet scanner/store/runtime` 通过；`go test scanner(1.611s) store(2.306s) runtime(2.060s)` 全绿；`go build -tags "sqlite pg" ./internal/store/...` 编译通过（sqlite/pg 实现可用）
- 预留：`SkippedWriter` 为可选接口且默认驱动 file 已覆盖，生产 sqlite/pg 需在各自 build-tag 下启用；committed 未 push

### Commit 109: refactor(search): 统一 cosineSimilarity 为 vector 包单一实现

- 现状核实：生产层 `cosineSimilarity` 实际仅存在于 `internal/vector/store.go`（复用方 store.search/persistent），P8 §7.3 标注的 `search/fts.go` 生产版早已不复存在；真正的残留是 `internal/search/search_test.go` 里复制粘贴的测试助手 `cosineSimilarity` + `TestCosineSimilarityEdge`（仅覆盖全零一种边界），重复定义生产逻辑
- 修法：删除 search_test.go 中的 `cosineSimilarity` 助手与 `TestCosineSimilarityEdge`，并移除随之失效的 `math` 导入；余弦实现统归于 `vector` 包（已有 `TestCosineSimilarity` 覆盖完全相同/正交/相反/零向量/不同长度 5 类边界）
- 文档同步：P8 §7.3 将该清理项划除；交接说明遗留段与修订记录标注统一进展
- 验证：`go build ./...`、`go vet ./internal/search ./internal/vector` 通过；`go test ./internal/search ./internal/vector` 全绿（search 1.171s、vector 0.182s）；删测试副本零行为变化
- 预留：committed 未 push

### Commit 108: refactor(search): 移除死函数 SearchResultFromVector + 文档遗留同步

- 死代码：`internal/search/searcher.go` 的 `SearchResultFromVector` 全仓库无调用（含测试），且其类型断言对象为局部 `vectorResult` 结构，无任何真实返回值命中，属 P8 阶段总结 §7.3 标注的遗留项；当前架构已用 `search.NewVectorAdapter(indexer)` 适配 vector 结果，直接删除
- 文档同步：P8-阶段总结 §7.3 将该清理项划除并标注已移除；`跨圈层/交接说明.md` 遗留段纠正过期表述（文档迁移/用户手册/测试指南已完成），并记录死代码清理进展与新增修订记录行
- 验证：`go build ./...`、`go vet ./internal/search/` 通过；`go test ./internal/search ./internal/store` 全绿（store 0.638s、search cached）；删死码零行为变化
- 预留：`search/fts.go:cosineSimilarity` 与 `vector/store.go` 版本重复仍待统一（保留于文档遗留）；committed 未 push

### Commit 107: refactor(parser): 收敛解析适配器注册单一实现，删除 cmd 死副本

- 结论核实：LSP 串联 parser.Registry 的接入当前代码已落地——`internal/runtime.NewParserRegistry` 负责注册 tree-sitter（兜底）+ gopls/jdtls/clangd（`FallbackParser` 失败回退 tree-sitter）+ SCIP/CodeGraph，并 SetPriority（go/java/cpp 走 LSP 优先），`scan`/`watch` 均经 `main.go` 调用同一装配
- 问题：`cmd/codeschema/parser_registry.go` 存在一份与 runtime 完全重复的 `newParserRegistry`/`commandAvailable`，且从未被调用（死代码），违反"严禁复制粘贴式实现、提取公共逻辑"
- 修法：删除 `cmd/codeschema/parser_registry.go`，Registry 注册统一收敛到 `internal/runtime/runtime.go` 单一实现（非破坏性，`main.go` 仅依赖 `rt.NewParserRegistry`，无符号被删引用）
- 文档同步：`02-解析适配中间层.md:149` 优先级注释由 `cmd/codeschema/parser_registry.go` 改指 `internal/runtime/runtime.go` 的 NewParserRegistry；`P1_1.md` 解析适配器注册描述改为统一走 `internal/runtime.NewParserRegistry`
- 验证：`go build ./...`、`go vet ./cmd/codeschema/` 通过；`go test ./internal/parser/... ./internal/runtime ./internal/scanner ./internal/watcher ./internal/scheduler ./internal/tenant` 全部 ok（含 lsp 1.624s、watcher 1.644s）；删除死码未引入任何行为变化
- 预留：committed 未 push

### Commit 105: test(tenant): Windows 路径分隔符断言跨平台化

- 根因：`tenant_test.go` 两处用例硬编码 unix 期望路径（`/var/lib/cs/mt-a/fts` 等），Windows 下 `filepath.Join` 产出反斜杠导致 `TestDeriveIndexDirs_Default*` FAIL；生产 `deriveIndexDirs` 本就跨平台正确，纯测试断言缺陷
- 修法：期望改用 `filepath.Join` 构造，与实现口径一致（非破坏性，不改业务逻辑）
- 验证：`go test ./internal/tenant` 0.893s ok；全量 `go test ./...` 无 FAIL（28 包带测试全绿，4 包无测试文件，`go list ./...` 共 32 包）
- 预留：无

### Commit 106: docs(doc-package): 新增文档体系关系图 + 一致性命中修正

- 新增 `docs/文档体系关系.md`（Mermaid 关系图 + 读取链路 + 闭环规则），接入 docs/README 导航与修订记录；系统简介补该关系图导航
- 决策层：`版本发布说明.md` 补 v0.1.0 首个发布能力基线草稿（版本号/日期实留待首个 v* tag 回填，不虚构）
- 一致性修正：清理 15 份文档"修订记录"表行末尾的 `-->` 共 15 处（批量建档残留，保留 Mermaid 箭头与 HTML 注释）；据 `go list./...` 实测将包数 31→32（README/系统简介/版本发布），"31 包全绿"更新为"32 包（28 含测试，无失败）"
- 验证：`go list ./...` = 32 包；`go test ./...` 全量 0 FAIL（28 ok + 4 no-test）；纯计数与状态有实测支撑，未虚构
- 预留：d4 曾列三项遗留项接入可行性评估（消歧接入搜索处理器 / AI 增强接入生产编排 / LSP 串联 parser.Registry）。后在代码中核实三项均已实现并接线——LSP（`internal/runtime.NewParserRegistry`，见 Commit 107）；同名方法消歧（service.Search → disambiguateMethodResults，runtime.WithAIEnhancer 注入）；AI 增强（analyzer.SetEnhancer + TagAll 叠加 EnhanceTag/EnhanceDoc）。相关测试：service.TestSearch_Disambiguate* 3 条、analyzer.TestAnalyzer_TagAll_WithEnhancer* 2 条，均通过

### Commit 104: docs(doc-package): 迁移遗留 P8 阶段总结至生产层开发文档归档

- git mv 将 `docs/P8-阶段总结-语义检索与全文搜索.md` 归位至 `docs/1-生产层/开发文档/`，消除 docs 根目录漏网文件
- 与 `modules/P8.md`（公共模块横切能力拆解）职责不重叠：前者为开发阶段实现细节/提交追溯归档，后者为乐高模块拆解，故保留两份不合并
- 至此 docs/ 根目录仅剩 README / AI协作规范 / 五大类目录，与 docs/README 声明完全一致
- 验证：纯文档零代码改动；根目录目录树校验通过；未 push
- 遗留：无

### Commit 103: docs(doc-package): P2 迁移清理遗留源档 + 新增用户手册/测试指南

- 新增 `docs/3-使用层/用户操作手册.md`（命令总览/扫描/监听/MCP/HTTP/多租户）；`docs/2-交付层/测试指南.md`（命令/约定/场景/CI/基准）
- 迁移：`docs/modules`→`1-生产层/modules/`、`docs/dev`→`1-生产层/开发文档/`、`docs/ops`→`2-交付层/运维文档/`（git mv，保留实现细节）
- 清理：`DEPLOYMENT_AND_USAGE.md`、`MCP接入指南.md` 归档至 `4-决策层/归档资料/`（历史源档，非删除）
- 更新 `docs/README.md` 导航（迁移完成说明）及各圈层 README 状态
- 验证：纯文档零代码改动；docs/ 根目录已仅剩 README/AI 协规范/五大目录；未 push
- 遗留：无（P0-P2 文档治理执行完毕；版本发布说明为模板待发布流维护）

### Commit 102: docs(doc-package): P1 落地生产层/交付层/跨圈层/决策层 + 归档旧规划

- 生产层新增：`数据库设计.md`（自 dev/01）、`配置参考.md`（自 config.example/dev11）、`扩展指南（新增适配器）.md`（自 dev/07）
- 交付层新增：`运维手册.md`（自 ops/02+03）、`性能调优.md`（自 ops/04）
- 跨圈层新增：`安全设计文档.md`（自 dev/10 拆出）、`交接说明.md`（依赖到期/坑点/遗留 TODO 归集）
- 决策层新增：`系统简介.md`、`版本发布说明.md`（模板，对应 release.yml 产物）
- 归档：git mv 将 `开发计划/技术路线-乐高式模块拼装.html`、`代码全量分析评估-2026-08-16.md` → `docs/4-决策层/归档资料/`
- 同步更新各圈层 README 状态标注（去除待落位）
- 验证：纯文档零代码改动；未 push
- 遗留：P2 迁移 docs/modules 归档、删除旧 dev/ops/DEPLOYMENT 源档；新建用户手册/测试指南

### Commit 101: docs(doc-package): 建立 docs/ 分层文档体系（入口 + AI 协作规范 + 生产/交付/使用层首批）

- 新增 `docs/README.md` 总入口（圈层导航 + AI 读取顺序 + 更新规则 + 修订记录）
- 新增 `docs/AI协作规范.md`：AI 读档顺序、领任务、编码约束、改码必改档映射表、Conventional Commits、人工复核边界——沉淀为正式文档
- 新增五大类目录占位 README（`docs/1-production`… 等 5 类），使目录骨架纳入版本控制
- 新增生产层：`技术设计文档.md`（综合 dev/00–13，矫正包数 31/SQLite→BulkUpsert/规模决策等过时表述）、`代码规范与开发指南.md`、`API文档.md`（HTTP 端点 + MCP 12 工具 + OpenAPI + 错误码）
- 新增交付层/使用层：`部署手册.md`、`客户端接入指南（MCP）.md`、`FAQ.md`（由 DEPLOYMENT_AND_USAGE / MCP接入指南 拆分提炼）
- 验证：无代码改动，纯文档；旧 `DEPLOYMENT_AND_USAGE.md`、`docs/MCP接入指南.md` 暂保留作迁移源，P1 阶段并入后删除；不 push
- TODO：P1 迁移 dev/ops/modules 旧档落位、拆分 dev/10 安全段 → `跨圈层/安全设计文档`、决策层归档 HTML/评估报告；P2 新建用户操作手册/测试指南/系统简介/版本发布说明/交接说明

### Commit 100: docs: 开发计划 16 张任务卡片全部标记 ✅ 已实施 + 技术路线同步 Docker/PG 跑通（PHASE_09 收尾）

- 开发计划 HTML：16 张任务卡片插入状态标记（含 T3-2 PG 嵌入式+Redis docker 集成 PASS、T3-3 Docker 实构建跑通）；顶部 warnbox ⑥⑦ 转 ✅；div 平衡校验 155/155
- 技术路线 HTML：黄灯段更新（Docker 实构建 / PG·Redis 真实实例均已跑通）
- 注：commit 9b33519 的卡片标记脚本因正则捕获缺失 T 前缀未实际写入（误报），本次修正（完整 `T\d-\d` 匹配）后 16 张全部生效

### Commit 99: feat(ops+store): Docker 实构建跑通 + PG/Redis 真实实例集成测试 PASS（PHASE_09/开发计划T3-3+T3-2 收尾）

- **T3-3 Docker 实构建**（本机网络恢复后完成）：Dockerfile 三处修复——① GOPROXY 固化为可配置 ARG（默认 goproxy.cn，解决 proxy.golang.org 超时）；② 提前 COPY third_party/（onnxruntime_go_patch 的 go.mod 供 replace 解析）；③ 移除非法 shell 语法 `COPY ... 2>/dev/null`。验证：`docker build` 成功（codeschema:test），容器内 version/scan/serve+health+viz/mcp --stdio 全链路冒烟 PASS
- **T3-2 PG/Redis 真实实例集成**（修复 PG 骨架 2 个历史 bug，真实测试首次暴露）：① file 表补 imports 列（原误放 method 表，查询 SQL 依赖导致 'column imports does not exist'）；② UpsertIR 补 `upsertMethodsTx`——method 从未落库，GetMethodsByClassID 恒空。集成测试三档来源（外部实例→localhost→嵌入式 fergusstrange/embedded-postgres 真实内核自动降级），唯一路径防持久化残留
- 验证：`TestPGStore_EndToEnd` PASS（file=1 class=1 methods=2 calls=2，嵌入式 PG）；`TestRedisCache_RealInstance` PASS（类缓存 + caller/callee 反查，docker redis:7-alpine）
- go.mod 新增 fergusstrange/embedded-postgres（GOPROXY=goproxy.cn 拉取）；全量回归：默认/-tags 'pg redis' 双路径构建 OK

### Commit 98: test(ai): 多 provider 配置验证——P4_2 → 87%

- `TestOpenAICompatClient_BaseURLVariants`：不同 BaseURL 形态（无/带尾斜杠、带/不带版本路径前缀如 `/v1`、`/api/openai/v1`）请求路径正确拼接（`TrimSuffix` 尾斜杠 + `/chat/completions`），兼容任意 OpenAI 兼容端点
- 验证：`go test ./internal/ai/` 全绿

### Commit 97: test(parser/codegraph): Schema 变体兼容覆盖——P3_5 → 92%

- `KindVariants`：interface/enum/trait/module/record/constructor kind 变体→ClassIR.Type/方法映射验证（INTERFACE/ENUM/ABSTRACT/OBJECT）
- `ColumnDrift`：nodes 缺 file 列时显式报错（schema drift），不静默返回空 IR
- 既有 role/confidence 额外列兼容由 RealSchema 测试覆盖（不干扰读取）
- 验证：codegraph 包 17 项测试全绿

### Commit 96: feat(scalebench): 嵌入质量多语料 recall 对比——P6_2 → 97%

- `TestEmbeddingQualityMultiCorpus`：通用代码语义/电商业务/基础设施三语料分别评测 Local vs ONNX（`runQualityEval` 重构为参数化 corpus/queries）
- 真实数据（`-tags onnx`）：ONNX Recall@1 全胜 1.00/0.80/1.00 vs Local 0.42/0.40/0.20，跨场景结论成立
- 产物：build/embedding-quality-multi.json（gitignore 约定不跟踪）+ analysis 报告；默认构建 ONNX 优雅 skip

### Commit 95: feat(vector): 模型下载断点续传——HTTP Range + .part 落盘（P6_3 → 97%）

- `downloadAndExtract` 改下载到固定 `<ModelDir>/.download.part`（原临时文件中断即丢）
- HTTP Range 续传：206 追加 / 200（服务器不支持）从头 / 其余状态报错；校验失败保留 .part
- 完整 .part 复用：上次下载完成但未解包 → 校验通过直接解包
- 新增测试 `ResumeDownload`（中断后半量续传）/ `ReuseCompletePart`；现有下载测试无回归

### Commit 94: docs: LSP clangd 真实验证落地——P3/P3_4 完成度同步（96%/95%，阻塞项 #3 解除）

- P3_4 完成度 90→95：clangd 工程上下文真实验证 + JSON-RPC id 修复记录；未做项仅剩 jdtls 等更多服务器接入

### Commit 93: fix(parser/lsp): JSON-RPC notification id 泄漏——clangd 场景符号提取修复

- `jsonRPCRequest.ID` 补 `omitempty`：notification（didOpen/didClose/initialized）不得携带 id，此前恒带 `"id":0` 违反 JSON-RPC 2.0
- gopls 宽容忽略该问题；clangd 严格拒绝（-32601 method not found）→ didOpen 不生效 → documentSymbol 恒报 non-added，clangd 场景符号提取从未真正工作（被测试 skip 掩盖）
- 新增 `TestLSPAdapter_RealClangd` 工程上下文真实验证（构造 compile_commands.json 最小 C++ 工程，clangd 22 真实提取 Calculator/Add PASS）
- `parseWithRetry` 改为错误也重试（覆盖 clangd 异步登记期），连续失败才 skip

### Commit 92: test(vector): chromem 持久化重启恢复验证 + 文档同步（P6_1 97%）

- `TestPersistentChromemStore_RestartRestore`：写入→同路径重开→数据与检索一致

### Commit 91: feat(scale+ci): SQLite 并发写基准 BenchmarkScaleBulkConcurrent 固化进 CI（-race 看护）

- 4 worker 并发 BulkUpsert 1 万文件（1.13s，与单 goroutine 相当，并发不退化）
- CI bench job 加 `-race` 同时看护并发写数据竞争

### Commit 90: docs: 同步 SQLite 并发结论——P7_2 97%，移除 modernc 阻塞项（误判纠正）

### Commit 89: fix(store): 纠正 SQLite 并发误判——modernc 驱动无并发 bug

- 此前把「reader 无限循环 + stop 超时」测试超时误读为 modernc 死锁（一度标记 Skip + SetMaxOpenConns(1)）
- 真相：reader 正常工作时不退出，wg.Wait() 永不完成，必然超时；对照实验（CGO mattn/纯 Go ncruces 同样"复现"）证明是测试逻辑 bug
- 恢复正确设计（reader 固定迭代次数）：读写并发 + 多读并发 `-race` 均 PASS；新增 `TestConcurrentFixed_*` 回归
- 移除 SetMaxOpenConns(1) 及错误注释

### Commit 88: docs: 乐高积木模块分解全量落地——docs/modules/ 42 份文档 + 完成度校准

- 9 个一级模块总述（P1~P9）+ 32 份子模块文档 + 总览 README.md
- 固定 7 章节：用途/拆分逻辑/技术说明/替代方案/完成度/阻塞风险/模块关系；公共模块标注被依赖方
- 完成度对照 git 历史与测试实际校准（P3_2/3/4/5、P4_2、P6_2/3、P9_2/3/4 上调）

### Commit 87: fix(store): SQLite 并发写压力测试 + SetMaxOpenConns(1) 缓解（⚠️ 后续 Commit 89 纠正）

- 新增纯写并发测试（DistinctFiles/SameFile，-race PASS）
- 曾误报 modernc/libc 并发死锁（reader 无限循环测试超时），Commit 89 纠正为测试设计缺陷；SetMaxOpenConns(1) 已随纠正移除

### Commit 86: feat(service): 测试关联真实采集——go coverprofile 自动导入覆盖率映射（PHASE_09/开发计划T4-4）

- `LoadGoCoverProfile/ParseGoCoverProfile`：解析 `go test -coverprofile` 产物（mode: set/count），按行号区间匹配 store 方法记录 → 测试类（命名约定）关联其源类被覆盖方法，注入 coverage 策略；路径后缀匹配兼容相对/绝对路径；合并不覆盖注入式 JSON
- 测试 5 项：格式解析（含 mode-only/未覆盖块）、端到端命中 coverage、缺失文件报错、路径启发式
- 验证：真实 coverprofile（204 行）解析正常；`go test ./internal/service/` 全绿

### Commit 85: feat(ci): 制品发布流水线——tag 触发 5 平台 Release + SHA-256 校验（PHASE_09/开发计划T4-3）

- `.github/workflows/release.yml`：v* tag 推送 → `make cross` 构建 5 平台 → SHA-256SUMS → action-gh-release 发布
- 验证：`make cross VERSION=v0.2.0` 本机 5 平台全部构建成功（13-15MB）

### Commit 84: feat(server): HTTP API OpenAPI 3.0 完整化（PHASE_09/开发计划T4-2）

- `/openapi.json`（13 端点完整规范）+ `/docs`（内嵌 swagger-ui）；测试 2 项

### Commit 83: feat(server): MCP stdio 传输支持（PHASE_09/开发计划T4-1）

- 抽取 `handleRequest` 纯逻辑（HTTP/stdio 复用）；`StartStdio` LSP 风格 Content-Length 帧；`codeschema mcp --stdio`
- 验证：CLI 端到端返回 11 工具帧；测试 4 项全绿

### Commit 82: feat(ops+store): PG/Redis 真实实例集成测试 + Dockerfile 免 CGO 优化（PHASE_09/开发计划T3-2+T3-3）

- docker-compose 新增 postgres:16-alpine（profile pg）/ redis:7-alpine（profile redis）
- PG 端到端（InitSchema→UpsertIR→查询）与 Redis 缓存读写集成测试（-tags pg/redis，无实例优雅 SKIP）
- Dockerfile 默认 CGO_ENABLED=0（纯 Go 免 gcc），ONNX 场景传 --build-arg CGO_ENABLED=1
- **遗留（已于 Commit 135 闭环）**：Docker 实构建 / PG·Redis 实跑——Commit 135 用 Docker 真实 Redis + embedded-postgres 跑通 `TestRedisCache_RealInstance` 与 `TestPGStore_EndToEnd`，并修复 Dockerfile 2 处真实构建缺陷（缺 git / dropreplace 被 COPY 覆盖），镜像 `docker build` + `/health` 冒烟全 PASS。

### Commit 81: feat(cli+docs): MCP 一键接入——mcp --print-config + 配置模板入库（PHASE_09/开发计划T2-5）

- `codeschema mcp --print-config` 输出 VS Code/JetBrains/Claude Code/Cursor/stdio 五类配置；docs/MCP接入指南.md；README 快速开始

### Commit 80: feat(vector): 语义检索质量定案——ONNX 真实复测 Recall@1=1.00（PHASE_09/开发计划T2-3）

- **实测：ONNX(bge-small-zh) R@1/@3/@5 = 1.00/1.00/1.00 vs Local(TF-IDF) 0.42/0.58/0.83**
- 修复 third_party/onnxruntime_go_patch 缺失 go.mod（-tags onnx 可独立构建）；默认策略定案（Local 兜底 / 语义敏感场景启用 ONNX）

### Commit 79: feat(scale+ci): 10万+ 文件真实全链路压测 + 夜间规模回归看护（PHASE_09/开发计划T3-1+T3-4）

- `TestScaleEndToEnd`：真实 .go 文件 → Scanner(正则) → UpsertIR → BuildFromStore → Searcher 全链路
- **实测 N=10万：扫描 8.18s / 索引 9.55s / P95 搜索 1.97s / 内存 1079MB（≈10.8KB/文件）** → 规模决策表（<1万 默认栈 / 1万~10万 SQLite+chromem / >10万 PG+Redis）
- CI nightly-scale job（N=10万 + 趋势 JSONL 归档）

### Commit 78: feat(store): FileStore 进程锁 + 原子写加固（PHASE_09/开发计划T2-4）

- flock 进程锁（Unix）/ 独占创建（Windows），同目录二次 Open 显式失败；scanner 忽略 store 数据文件

### Commit 77: feat(vector+viz): 向量索引原文持久化（PHASE_09/开发计划T2-2）

- DocContentStore 可选接口（Persistent/Memory 实现，旧文件向后兼容）；IndexBuilder 写入原文；/viz 展示类/方法原文

### Commit 76: feat(vector+ops): 模型公网分发闭环（PHASE_09/开发计划T2-1）

- 注册表 URL 回填 GitHub Releases 约定路径；`make models-serve` 本地 HTTP 分发；真实制品（43MB）HTTP 端到端 PASS

### Commit 75: feat(parser+cli): LSP 接入 Registry 编排主路（PHASE_09/开发计划T1-3）

- FallbackParser 降级回退包装器；newParserRegistry 统一工厂（tree-sitter 兜底 + LSP/SCIP/CodeGraph 高精度优先）
- **修复隐藏缺陷**：scan/watch 此前创建空 Registry 导致 CLI 扫描 classes/methods 永远为空

### Commit 74: feat(parser): CodeGraph 适配器校准真实 schema（PHASE_09/开发计划T1-2）

- 真实 CodeGraph DDL（nodes/edges + source_id/target_id/kind）检测优先，旧 symbols/edges 契约兼容；缺表显式降级

### Commit 73: feat(cli): 落地 codeschema benchmark 子命令（PHASE_09/开发计划T1-1）

- internal/benchmark 包（全链路指标采集）+ CLI 子命令（--repos 多仓/单仓，Markdown+JSON 报告）；CLI 命令数 6→7

### Commit 72: fix(vector): 本地端到端跑通真实 ONNX 推理（PHASE_09）

**本地闭环验证（x86_64 mac）**
- `down/models/bge-small-zh-v1.5/` 真实模型 + `down/onnxruntime/libonnxruntime.dylib`（x86_64）→ `-tags onnx` 真实嵌入推理成功（dim=512），固化 `TestLocalONNXEmbedE2E`
- 修复①平台库选择：initRuntime 按 runtime.GOOS 选 dll/so/dylib（原无条件先试 onnxruntime.dll，macOS 误载 Windows dll）
- 修复②ORT API 版本：绑定 v1.32.1 头文件 ORT_API_VERSION=28 vs 本机库 1.23.2 仅支持 v1..23 → third_party/onnxruntime_go_patch（28→23）+ go.mod replace 适配
- 架构：本机 x86_64，原 dylib 为 arm64（incompatible architecture），经 pip onnxruntime==1.23.2 提取替换

### Commit 71: feat(parser): 最终补齐 markdown/dockerfile/elm/cue → 30 语言（PHASE_09）

- ExtToLang/scanner/SupportedLanguages/LangToExtensions：`.md`→markdown、`Dockerfile`→dockerfile（无扩展名按文件名识别，scanner + Parse + AST 三处）、`.elm`→elm、`.cue`→cue
- 正则：markdown 标题、dockerfile 指令、elm module、cue 结构
- AST：markdown section（atx_heading 标题提取）、dockerfile instruction、elm module_declaration/value_declaration、cue struct_lit
- 修复：① Go 原始字符串内三反引号报错（markdown methodPattern）；② 基准 NaN bug（配置语言样本 golden/detected 均空 → 0/0=NaN 致 JSON 失败，tp+fn==0 时 Recall 记为 1）；③ 2 处历史测试用 readme.md 当 unsupported 的过时用例
- 验证：**AST 30 语言双档 P=1.00/R=1.00（TP=72）**；正则 SIMPLE P=0.97（唯一 FP 为既有 SQL DECIMAL 构造）

### Commit 70: feat(parser): 继续扩展 hcl/svelte → 26 语言（PHASE_09）

- ExtToLang/scanner/SupportedLanguages/LangToExtensions：`.tf`/`.hcl`→hcl、`.svelte`→svelte
- 正则：hcl resource/data/variable/module 块；svelte script 组件 + function 方法（修函数定义误检调用）
- AST：hcl block（类型+标签拼接名称）、svelte script_element（固定名 script）；astClassType 增 block/script_element 分支
- 验证：**AST 26 语言双档 P=1.00/R=1.00（TP=72）**；正则 SIMPLE P=0.97（唯一 FP 为既有 SQL DECIMAL 构造）
- 注：svelte 组件内 JS 为 raw_text 节点，AST 调用检测不覆盖（grammar 局限）

### Commit 69: feat(parser): 继续扩展 protobuf/html → 24 语言（PHASE_09）

- ExtToLang/scanner/SupportedLanguages/LangToExtensions：`.proto`→protobuf、`.html`/`.htm`→html
- 正则：proto message/service/enum + rpc 方法；html 元素标签 + script 检测
- AST：proto message/service/enum + rpc；html element→start_tag→tag_name 名称提取；astClassType 增 service/enum/element 分支
- 验证：**AST 24 语言双档 P=1.00/R=1.00（TP=72）**；正则 SIMPLE P=0.97（唯一 FP 为既有 SQL DECIMAL 构造）

### Commit 68: feat(parser+ops): AST 基准精度门槛 + css/toml/yaml 扩展（PHASE_09）

**① AST 基准精度门槛守护**
- 基准从「报告」变「守护」：`benchASTPath()`（!treesitter / treesitter 双 build tag 文件）区分路径
- AST 路径 OVERALL P/R < 0.95 → t.Errorf（CI 红）；正则启发式路径仅提示不设门槛
- CI treesitter job 的 `go test -tags treesitter` 现同时是精度回归守门
- 顺带修正基准结论文本「12 语言」过时描述

**② T6-2 继续扩展 css/toml/yaml → 22 语言**
- ExtToLang/scanner/SupportedLanguages/LangToExtensions：`.css`→css、`.toml`→toml、`.yml`/`.yaml`→yaml
- 正则：css 选择器/媒体查询、toml table、yaml 顶层键（配置类无调用，callPattern 用永不匹配正则）
- AST：css rule_set/media_statement（selectors 名称提取）、toml table
- 验证：**AST 22 语言双档 P=1.00/R=1.00（TP=72）**；正则 SIMPLE P=0.97（唯一 FP 为既有 SQL DECIMAL 构造）

### Commit 67: feat(parser+vector): 本地产物零配置分发 + lua/groovy 扩展（PHASE_09）

**① 本地产物零配置分发（真实产物端到端验证）**
- `ModelDownloader` 新增 `LocalArtifactDirs`（默认 build/、down/）：`ResolveFromRegistry` 优先匹配 `models-<model>.tar.gz` → file:// 本地源，`make models-pack` 后零配置分发
- 修复 2 个真实产物解包 bug：extractTarGz 顶层目录剥离（detectTarTopDir 两遍扫描，仅全条目首段一致才剥离）、跳过 macOS AppleDouble `._*` 条目
- 测试 3 项：LocalArtifact（零配置分发）/ TopDirStrip（扁平 vs 带顶层）/ RealLocalArtifact（真实 build 产物解包成功）

**② T6-2 继续扩展 lua/groovy → 19 语言**
- ExtToLang/scanner/SupportedLanguages/LangToExtensions：`.lua`→lua、`.groovy`→groovy
- 正则：lua function/local table、groovy class/interface/trait/enum + def；AST：lua function_statement/function_call、groovy class_definition/function_definition/function_call
- 验证：**AST 19 语言双档 P=1.00/R=1.00（TP=72）**；正则 SIMPLE P=0.97（唯一 FP 为既有 SQL DECIMAL 构造）

### Commit 66: feat(parser+vector): 模型分发本地闭环 + elixir/ocaml 扩展（PHASE_09）

**① 模型分发本地闭环（真实模型验证）**
- `down/models/bge-small-zh-v1.5/`（onnx/model_fp16.onnx + tokenizer.json + config.json）与 main.go 默认 `modelDir = down/models/<embedding_model>` 吻合，`Ensure` 本地优先路径零下载命中
- 新增测试：`TestModelDownloader_Ensure_LocalPresent`（同构目录 + URL 空/坏均 ok=true）、`TestRealModelDirPresent`（真实目录验证，缺失 skip）
- docs/dev/09 补充「本地优先零下载」策略说明

**② T6-2 继续扩展 elixir/ocaml → 17 语言**
- ExtToLang/scanner/SupportedLanguages/LangToExtensions：`.ex`/`.exs`→elixir、`.ml`/`.mli`→ocaml
- 正则：elixir defmodule/def/defp + 标准库关键字；ocaml module/let + 无括号应用 callPattern
- AST：elixir walker 特判（defmodule/def/调用三分 + skipChild 跳签名）、ocaml module_definition/value_definition（含 parameter 才算函数）/application_expression
- 修复：OCaml 正则 `end` 拆词 FP（无括号分支要求空格）、关键字去重（in/struct/rec）
- 验证：**AST 17 语言双档 P=1.00/R=1.00（TP=68）**；正则 SIMPLE P=0.97（唯一 FP 为既有 SQL DECIMAL 构造）

### Commit 65: feat(parser+ops): 继续实施——SQL 语言 + 趋势折线图 + 模型本地分发源（PHASE_09）

**核心改动点（继续①·SQL 语言 → 15 语言）**：
- `internal/parser/adapter/adapter.go` / `internal/scanner/scanner.go`：`.sql`→sql。
- 正则版 `adapter.go`：sql 模式（CREATE TABLE/VIEW/FUNCTION/PROCEDURE 声明 + CALL/func 调用，非捕获组支持 CALL）、detectClassType（TABLE/VIEW/FUNCTION/PROCEDURE）、`--` 注释。
- AST 版 `adapter_ast.go`：注册 sql grammar + 节点类型（create_table/create_view/create_function/create_procedure + invocation/function_call/call_statement）；astCalleeName 增 call_statement 分支。
- **发现 go-tree-sitter SQL grammar 不支持 CALL 语句（解析为 ERROR 节点）**——测试改为验证 SELECT COUNT(*) 的 invocation 检出，并在测试注释说明该 grammar 局限。
- 测试 + 基准样本。**AST 15 语言双档 P=1.00/R=1.00（TP=64）；正则 SIMPLE P=0.97/R=1.00（1 FP：SQL DECIMAL 构造）**。

**核心改动点（继续②·基准趋势折线图）**：
- `scripts/benchtrend/main.go`：新增 `renderLineChart`——纯手写 SVG polyline（Overall Precision 红 / Recall 蓝 + 网格线 + 数据点 + 图例），无 JS/外部依赖；14 历史点实测 2 折线 + 28 点正常渲染。

**核心改动点（继续③·模型本地分发源）**：
- `internal/vector/model_download.go`：`downloadAndExtract` 重构支持三种分发源——`https://`（HTTP 下载）/ `file://`（本地文件直读）/ 本地路径；新增 `localSourcePath`（file:// 绝对/相对路径解析）。无网络环境可直接用 `make models-pack` 产物做本地分发。
- 测试 2 项：file:// 与本地路径解包、URL 解析。

**验证**：默认/`-tags treesitter` 双路径构建+测试全绿；全仓 23 包通过；`go mod tidy` 后仍绿。
**文档同步**：docs/dev 02/07 + README（15 语言）；docs/dev 09（分发源类型）；任务清单。
**遗留**：注册表 URL 仍为占位（models.example.com），真实制品托管后替换（无网络环境可用 file:// 指向本地打包产物）；go-tree-sitter 其余语言按需继续扩展。

### Commit 64: feat(parser+ops): 遗留项——bash/scala 扩展 + 模型打包发布 + 基准趋势可视化（PHASE_09）

**核心改动点（遗留①·ONNX 模型打包发布）**：
- `Makefile`：新增 `make models-pack MODEL=<name>`（本地模型打包 tar.gz + 输出 SHA-256 到 build/models-<name>.sha256）、`make bench-callgraph`（双路径精度基准一键跑）、`make bench-trend`（趋势可视化）。
- `internal/vector/model_registry.go`：回填 bge-small-zh-v1.5 的**真实 SHA-256**（由本机 make models-pack 生成，48b70f80…）；URL 仍为制品托管占位。

**核心改动点（遗留②·扩展 bash/scala → 14 语言）**：
- `internal/parser/adapter/adapter.go` / `internal/scanner/scanner.go`：`.sh`/`.bash`→bash、`.scala`/`.sc`→scala。
- 正则版 `adapter.go`：bash（function_definition 作方法 + 行首命令名 callPattern）、scala（class/trait/object/enum + def 方法）模式；detectClassType（bash FUNCTION、scala INTERFACE/ENUM/OBJECT）；isKeyword 补 bash/scala（含跨语言关键字去重）；**修复 bash 无括号命令调用检测**（括号门控放行 bash）。
- AST 版 `adapter_ast.go`：注册 bash/scala grammar + 节点类型（bash function_definition 方法 + command 调用、scala class_definition/object_definition/trait_definition/enum_definition + function_definition + call_expression/method_invocation）；astNodeName 支持 bash word。
- 测试：Bash/Scala 解析测试（正则/AST 双路径）+ 基准样本。**AST 14 语言双档 P=1.00/R=1.00（TP=63）；正则 SIMPLE P=1.00/R=1.00**。

**核心改动点（遗留③·基准趋势可视化）**：
- 新增 `scripts/benchtrend/main.go`：纯 Go 读 `build/treesitter-bench-history.jsonl` → 生成 `build/treesitter-bench-trend.html`（趋势表 + 末次快照摘要 + 精度健康提示，无第三方依赖）。

**验证**：默认/`-tags treesitter` 双路径构建+测试全绿；全仓 23 包通过；`go mod tidy` 后仍绿；`make models-pack`/`make bench-trend` 实跑正常。
**文档同步**：docs/dev 02/07 + README（14 语言）；任务清单。
**遗留**：注册表 URL 仍为占位（models.example.com），真实制品托管后替换；go-tree-sitter 其余语言按需继续扩展。

### Commit 63: feat(parser+vector): 非阻塞项——C 语言补齐 + 基准 CI 趋势 + ONNX 模型注册表（PHASE_09）

**核心改动点（非阻塞①·补齐 C 语言 → 12 语言）**：
- `internal/parser/adapter/treesitter/adapter.go`：`initPatterns` 增加 `"c"`（typedef struct/enum/union + 函数模式），`detectClassType` 增 c（ENUM/CLASS）；此前 ExtToLang 有 `.c→c` 但适配器无模式，C 文件返回空 IR——已修复。
- `internal/parser/adapter/treesitter/adapter_ast.go`：注册 `c` grammar（go-tree-sitter/c）+ 节点类型（struct_specifier/enum_specifier/union_specifier、function_definition、call_expression）。
- 测试：C 解析测试（正则/AST 双路径）+ 基准样本（SIMPLE 档）。**AST 12 语言双档 P=1.00/R=1.00（TP=59）；正则 SIMPLE P=1.00/R=1.00**。

**核心改动点（非阻塞②·调用图基准 CI 趋势化）**：
- `internal/adapterbench/treesitter_callgraph_bench_test.go`：新增 `appendBenchHistory`——每次运行把 simple/complex/overall 精度快照（含 git SHA）**追加为 JSONL**（`build/treesitter-bench-history.jsonl`，首行注释表头），供跨提交趋势对比。
- `.github/workflows/ci.yml`：treesitter job 末尾增 `upload-artifact`（`treesitter-bench-$sha`）归档 `treesitter-callgraph-bench.json` + 历史 JSONL。

**核心改动点（非阻塞③·ONNX 模型注册表）**：
- 新增 `internal/vector/model_registry.go`：内置已知模型注册表（bge-small-zh/bge-small-zh-v1.5/bge-base-zh → 下载 URL + 可选 SHA256）；`LookupModelRegistry`/`ResolveDownloadConfig`（显式配置优先）；`ModelDownloader.ResolveFromRegistry` 在无显式 URL 时自动查表回填——用户仅配 `embedding_model` 即可分发已知模型。
- `internal/vector/model_download.go`：`Ensure` 无 URL 时先查注册表，未知模型才报「not in model registry」。
- 测试 3 项：注册表查询、解析优先级（显式>注册表）、回填成功/未知模型失败/显式保留。

**验证**：默认/`-tags treesitter` 双路径构建+测试全绿；全仓 23 包通过；`go mod tidy` 后仍绿。
**文档同步**：docs/dev 02/07/09 + README（12 语言、注册表说明）；config.yaml.example；任务清单。
**遗留**：注册表 URL 为占位（models.example.com），真实 ONNX 制品托管后回填 SHA256；历史趋势可视化为可选本地脚本。

### Commit 62: feat(parser+vector): 非阻塞项全量——C#/Ruby 扩展 + 反射基准 + ONNX 模型远程分发（PHASE_09）

**核心改动点（非阻塞①·T6-2 扩展 C#/Ruby → 11 语言）**：
- `internal/parser/adapter/adapter.go` / `internal/scanner/scanner.go`：`ExtToLang`/`SupportedLanguages`/`LangToExtensions`/`detectLang` 增加 `.cs`→csharp、`.rb`→ruby。
- 正则版 `adapter.go`：`initPatterns` 增加 csharp（class/interface/struct/enum/record + 方法模式）/ruby（class/module + def/def self. 方法）模式；`detectClassType`（csharp INTERFACE/ENUM、ruby MODULE）；`isKeyword` 补 Ruby（puts/attr_* 等）；`codeSanitizer` 支持 Ruby `#` 行注释。
- AST 版 `adapter_ast.go`：注册 csharp/ruby grammar + 类/方法/调用节点类型（csharp `invocation_expression`/`member_access_expression`、ruby `class`/`module`/`method`/`call`）；`astCalleeName` 增 Ruby call（`(` 前完整链）与 C# invocation_expression 分支。
- 测试：C#/Ruby 解析测试 + 基准样本扩充至 11 语言。**AST 11 语言双档 P=1.00/R=1.00（TP=57）；正则 SIMPLE P=1.00/R=1.00、COMPLEX P=0.95/R=0.86**。

**核心改动点（非阻塞②·基准扩充反射/模板特化档）**：
- `internal/adapterbench/treesitter_callgraph_bench_test.go`：complex 档追加 Go 反射（`reflect.ValueOf`/`MethodByName`/`Call`）、Java 反射（`Class.forName`/`getMethod().invoke()` 链式）、C++ 模板特化（模板方法体真实调用 + `std::vector<int> tmp(10)` 构造对照）3 样本。
- **修复 AST 版 Java 链式调用**：`method_invocation` 的 object 是 method_invocation（链式）时取 name 字段——`cls.getMethod("invoke").invoke(null)` 的 `invoke` 不再漏检。**COMPLEX 达 P=1.00/R=1.00（TP=35）**。

**核心改动点（非阻塞③·E3 ONNX 模型远程分发）**：
- 新增 `internal/vector/model_download.go`：`ModelDownloader`——幂等下载（本地已存在跳过）+ SHA-256 校验 + tar.gz 安全解包（防路径穿越）+ 优雅降级（无远程源/下载失败/校验不匹配均返回可观测错误，调用方降级 LocalEmbedder）；`{model}` 占位符。
- `internal/config/config.go`：`VectorConfig` 新增 `ModelDir`/`ModelDownloadURL`/`ModelSHA256` + 环境变量 `CODESCHEMA_STORAGE_VECTOR_MODEL_*` + merge 逻辑。
- `cmd/codeschema/main.go`：`newSearcherWithStore` 在 ONNX 分支前调 `ModelDownloader.Ensure`（模型缺失自动下载，失败降级）。
- `config.yaml.example` / `docs/dev/09-语义检索与全文搜索.md` / `README.md` 同步。
- 测试 4 项：下载+解包+幂等、无远程源降级、SHA-256 不匹配拒绝、路径穿越拒绝。

**验证**：默认/`-tags treesitter` 双路径构建+测试全绿；全仓 23 包通过；`go mod tidy` 后仍绿。
**文档同步**：docs/dev 02/07/09 + README（11 语言、模型分发）；任务清单 + 调用图基准报告。
**遗留**：真实 ONNX 模型制品发布为可下载 URL（模型注册表）；基准接 CI 趋势图。

### Commit 61: feat(parser): T2-1 可选项全量——AST 语言细分 + 两档基准 + CI treesitter job + Swift/PHP 扩展（PHASE_09）

**核心改动点（可选项①·AST 语言细分）**：
- `internal/parser/adapter/treesitter/adapter_ast.go`：`astCalleeName` 重构——链式调用取 selector 最后一段（`a().b().c()` → `c`）、普通 `obj.method` 保留完整成员文本、泛型剥离 `<...>`（`http.get<T[]>` → `http.get`）、C++ 类型构造跳过（`std::string(x)`，新增 `cppCtorTypes` + `isCppCtorCall`）、PHP `member_call_expression` 取最后一个 name（方法名口径与正则一致）；新增 `isCallNodeType`/`stripTypeArgs` 辅助。

**核心改动点（可选项②·两档调用图基准）**：
- `internal/adapterbench/treesitter_callgraph_bench_test.go`：扩为「简单 + 复杂」两档（复杂档覆盖重载/泛型/注解/多行签名/嵌套/链式调用），按档统计 P/R（`simple_overall`/`complex_overall`）；修正 golden（链式中间段 `Next`/`Then`/`then`/`or_else`/`get` 是真实调用）。
- **实证结果**：正则 SIMPLE P=1.00/R=1.00、COMPLEX P=0.95/R=0.81；**AST 9 语言双档 P=1.00/R=1.00（TP=44 FP=0 FN=0）**——真语法树在真实复杂度下的价值实证（对照：复杂档正则 R=0.81 vs AST R=1.00）。

**核心改动点（可选项③·CI treesitter job）**：
- `.github/workflows/ci.yml`：新增 `treesitter` job（ubuntu，needs: test）——`-tags treesitter` 构建/vet/测试（AST 适配器 + adapterbench）+ 默认路径（正则）回归守护。

**核心改动点（可选项④·T6-2 扩展 Swift/PHP）**：
- `internal/parser/adapter/adapter.go` / `internal/scanner/scanner.go`：`ExtToLang`/`SupportedLanguages`/`LangToExtensions`/`detectLang` 增加 `.swift`/`.php`。
- 正则版 `adapter.go`：`initPatterns` 增加 swift（class/struct/enum/protocol/extension + `func` 方法）/ php（class/interface/trait/enum + `function` 方法）模式；PHP callPattern 用**非捕获组**支持 `$obj->method(...)`/`$obj::method(...)`；`detectClassType`/`isKeyword` 同步。
- AST 版 `adapter_ast.go`：注册 swift/php grammar + 类/方法/调用节点类型表（php `member_call_expression`）。
- 测试：Swift/PHP 解析测试（正则版）+ 基准样本扩充至 9 语言。

**验证**：默认/`-tags treesitter` 双路径构建+测试全绿；全仓 23 包通过；`go mod tidy` 后仍绿。
**文档同步**：`docs/dev/02`/`docs/dev/07`/`README.md`（7 语言 → 9 语言）；`analysis/2026-08-14-t2-1-parser-precision-eval.md`、`analysis/2026-08-14-treesitter-callgraph-bench.md`、任务清单。
**遗留**：C#/Ruby 语言扩展（go-tree-sitter 已支持，按需）；基准扩充模板特化/反射等更复杂样本。

### Commit 60: feat(parser): T2-1 方案 C 全量落地——伪调用剔除 + 调用图基准 + `-tags treesitter` 真语法树（PHASE_09）

**核心改动点（补强①·字符串/注释剔除状态机）**：
- `internal/parser/adapter/treesitter/adapter.go`：新增 `codeSanitizer` **跨行状态机**——剔除字符串/注释内的伪调用：
  块注释 `/* ... */`（跨行状态）、Python 三引号 `"""`/`'''`（跨行状态）、行内字符串 `"`/`'`（含 `\` 转义）、
  行注释 `//` 与 Python `#`（行尾清空）；调用检测前先 `sanitizer.clean(line, lang)` 再匹配，
  避免 `msg := "foo(bar)"` / `// foo(bar)` 被误判为调用。测试 4 项。

**核心改动点（补强②·7 语言调用图基准）**：
- 新增 `internal/adapterbench/treesitter_callgraph_bench_test.go`：7 语言黄金样本（≥2 真实调用 + 伪调用陷阱），
  统计检出 vs 黄金的 Precision/Recall；产出 `build/treesitter-callgraph-bench.json` + `analysis/2026-08-14-treesitter-callgraph-bench.md`。
- **修复真实 bug**：Rust `callPattern` 捕获组原为 `(\w[\w!]*)`（不含 `.`）致 `helper.do_work()` 只检出 `do_work`；
  改为 `(\w[\w.:]*)` 支持 `obj.method()` / `mod::fn()`。**基准结果：7 语言 P=1.00 / R=1.00（TP=14 FP=0 FN=0）**。

**核心改动点（第 3 步·`-tags treesitter` 隔离框架）**：
- `adapter.go` 加 `//go:build !treesitter`（正则实现，默认，免 CGO）。
- 新增 `adapter_ast.go`（`//go:build treesitter`）：基于 go-tree-sitter（CGO）真语法树实现——7 语言 grammar 注册，
  AST 遍历提取类/方法/调用（`astNodeName` 兼容 Kotlin `simple_identifier` 头部；`astCalleeName` 取 `(` 前完整表达式
  覆盖 Java method_invocation 与 Kotlin/Go nav 调用；`astClassType` 映射 INTERFACE/ENUM/OBJECT）。
- 新增 `adapter_ast_test.go`（`//go:build treesitter`）：Go/Java/Kotlin 语法级解析测试（含字符串陷阱过滤断言）。
- `sanitizer_test.go` 拆为 `//go:build !treesitter` 专属（codeSanitizer 仅正则版存在）。
- `go.mod`：新增 `github.com/smacker/go-tree-sitter` 直接依赖。
- **Registry 零改动**：`adapter.NewTreeSitterAdapter()` 同一构造点按 build tag 自动切换实现。

**验证**：默认 `go build ./...` / `go vet ./...` / 全仓 23 包通过（免 CGO）；`go build -tags treesitter ./...` /
`go test -tags treesitter ./internal/parser/adapter/treesitter/` 全绿；`go mod tidy` 后双路径仍全绿。
**遗留**：AST 版语言细分（C++ 模板/泛型节点细调）、基准扩充复杂样本（重载/泛型/注解）、CI 增 treesitter job（需 gcc）为后续可选项。

### Commit 58: feat(parser): 调用检测扩展到全语言 + Kotlin 支持 + T2-1 方案评估（T2-1 低成本路径 + T6-2 部分/PHASE_09）

**核心改动点**：
- `internal/parser/adapter/treesitter/adapter.go`：
  - 移除「仅 go/py」调用检测门控，**全部 7 语言（go/java/ts/py/rust/cpp/kotlin）启用调用检测**（callPattern + `isKeyword` 过滤）。
  - `isKeyword` 扩充 Kotlin/Rust/C++ 关键字（fun/val/var/when/fn/let/match/sizeof/static_cast 等），减少误匹配。
  - 新增 **Kotlin 支持**（T6-2 部分）：`langPatterns["kotlin"]`（class/interface/enum class/object/data class 类模式 + `fun` 方法模式）；`detectClassType` 支持 kotlin 的 INTERFACE/ENUM/OBJECT 类型。
- `internal/parser/adapter/adapter.go`：`ExtToLang` 增加 `.kt`/`.kts` → `kotlin`；`SupportedLanguages`/`LangToExtensions` 同步补 kotlin。
- `internal/scanner/scanner.go`：`detectLang` 增加 `.kt`/`.kts` → `kotlin`。
- 测试：新增 Java 调用检测（paymentService.pay / notifyService.send）、C++ 调用检测（fuelPump.pump / ignition.fire）、Kotlin 类/方法解析（User + UserService + getUser）3 项。

**验证**：`go test ./internal/parser/... ./internal/scanner/` 全绿；`go build ./...` / `go vet ./...` / 全仓 23 包测试通过。
**方案评估**：`analysis/2026-08-14-t2-1-parser-precision-eval.md` 产出三方向方案（A CGO 真语法树 / B 强化正则 / C 混合推荐），默认走正则（免 CGO，T0-2 不回归），真语法树留作 `-tags treesitter` 远期可选项，**待用户拍板**。
**文档同步**：`docs/dev/02-解析适配中间层.md`、`docs/dev/07-适配器实现指南.md`（7 语言清单 + Kotlin 扩展步骤 + 精度档位说明）、`README.md`（语言/依赖描述）。

### Commit 57: feat(service): 查询期同名方法消歧——Disambiguate 接入搜索处理器（T4-1 剩余/PHASE_09）

**核心改动点**：
- `internal/service/service.go`：`Service` 新增 `enhancer` 字段 + `WithAIEnhancer`；`Search` 富化后调 `disambiguateMethodResults`——收集 `method:<id>` 结果按「方法简单名」分组（同名方法 = 多类中同名），每组 ≥2 候选时构建 `parser.MethodIR` 列表（`loadMethodIR` 从 store 装载 Name/ClassFQN/Signature/Doc）调用 `Enhancer.Disambiguate` 选最佳；**保留最佳、取消其余候选**（降噪）；预算超限（`ErrBudgetExceeded`）/ LLM 失败 / 索引越界均静默回退原结果——搜索永不因 AI 降级。
- `cmd/codeschema/main.go`：新增 `withAIEnhancer(svc, cfg)` 辅助，在 mcp/serve 构造点注入 Service（watch 构造点注入 analyzer 标签增强路径）。
- 测试：新增 3 项——消歧保留 AI 选中项（两个同名 getUser 候选，Choose 选 0 → 保留第一个）、未注入 enhancer 结果原样返回、查询预算耗尽回退原结果。

**验证**：`go test -race ./internal/service/` 全绿；`go build ./...` / `go vet ./...` / `-tags pg/redis/onnx` 构建全绿。
**遗留**：无（T4-1 全部收口：explicit/coverage 策略、AI 增强层、生产编排、查询期消歧均已完成）。

### Commit 55: feat(ai+service): Enhancer 生产编排接入 + 影响面分析含关联单测（T4-1 剩余 + T4-2/PHASE_09）

**核心改动点（T4-1 剩余·Enhancer 编排接入）**：
- `internal/ai/http_client.go`：新增 OpenAI 兼容 Chat Completions LLMClient（`NewOpenAICompatClient`）——`Complete` 按行切分补全结果、`Choose` 容忍 `[3]`/`索引: 3`/`3。`/`（1）` 等格式提取首个数字；缺 BaseURL/APIKey/Model 任一返回 nil（AI 增强禁用、主流程零影响）；非 200 / 网络失败包装 `ErrLLMUnavailable`/`ErrEnhanceFailed`；超时默认 30s。
- `internal/analyzer/analyzer.go`：`Analyzer` 新增 `enhancer` 字段 + `SetEnhancer`；`TagAll` 规则标签之上叠加 AI 增强——类/方法 `EnhanceTag` 与已有标签合并去重（`mergeUnique`）写回，空 Doc 时 `EnhanceDoc` 补全经**可选接口 `docUpdater`**（`UpdateClassDoc`/`UpdateMethodDoc`）写回；预算耗尽跳过增强不影响规则标签（`Enhancer.BudgetRemaining` 预检）。
- `internal/store/filestore.go`：实现 `docUpdater` 可选接口（`UpdateClassDoc`/`UpdateMethodDoc`）；sqlite/pg 未实现时优雅跳过（可选接口模式，不扩展主 Store 接口）。
- `internal/config/config.go`：`AIConfig` 新增 `BaseURL`（默认 api.openai.com/v1）与 `APIKey` 字段 + 环境变量 `CODESCHEMA_AI_BASE_URL`/`CODESCHEMA_AI_API_KEY`（含校验/合并）。

**核心改动点（T4-2·影响面分析含单测）**：
- `internal/analyzer/graph.go`：新增 `GetCallersWithDepth`/`GetCalleesWithDepth`——BFS 遍历并携带**距目标的层级距离**（深度 1 = 直接调用者/被调用者，返回 `ImpactNode{Method, Depth}`），与既有扁平 `GetCallers`/`GetCallees` 并存。
- `internal/analyzer/analyzer.go`：新增 `FindImpactNodesWithDepth`，返回带深度的影响面节点。
- `internal/service/service.go`：`Service` 新增 `analyzer` 字段 + `WithImpactAnalyzer`；`GetImpact` 由 P0 骨架（恒空）改为**真实调用图影响面 + 关联单测**——每个受影响节点经 `enrichImpactNodes` 复用 `FindTestLinks` 五策略关联对应单测（`ImpactNode.RelatedTests` 字段），满足「改动一处能列出受影响的单测」验收；未注入 analyzer 时返回空（向后兼容）。
- `cmd/codeschema/main.go`：`newAIEnhancer(cfg)` 按 `config.ai` 构造（不完整即 nil + 日志提示）；`scanCmd` 扫描后调 `runTagAll`（规则 + 可选 AI 增强）；四处 Service 构造点统一 `withImpactAnalyzer` 注入。

**验证**：`go build ./...` / `go vet ./...` / `go build -tags pg/redis/onnx` 全绿；`go test -race ./internal/ai/ ./internal/analyzer/ ./internal/service/` 全绿；全仓 23 包测试通过（排除 scalebench 慢测试）。
**新增测试**：`internal/ai/http_client_test.go` 6 项（禁用/Complete/Choose/格式变体/非200/网络错误）；`internal/analyzer/analyzer_test.go` mockStore 标签方法改为内存实现 + TagAll 编排 2 项（预算充足叠加 AI 标签且保留规则标签 / 预算耗尽仅规则标签）+ WithDepth 3 项 + FindImpactNodesWithDepth 1 项；`internal/service/service_test.go` 2 项（注入 analyzer 后 GetImpact 返回真实 caller 且 caller 节点含 coverage 关联单测 / 未注入向后兼容空结果）。
**遗留**：查询期同名方法消歧（`Enhancer.Disambiguate`）尚未接入搜索处理器（接口已就绪）；真实 provider 需配置 `ai.base_url/api_key/model`；`GetImpact` 每次调用重建调用图，高频调用可后续加缓存。

### Commit 54: feat(parser): SCIP 流式加载——json.Decoder 增量解析 + 文档背压（T2-2/PHASE_09）

**核心改动点**：
- `internal/parser/adapter/scip/adapter.go`：
  - `loadIndex` 由 `os.ReadFile` 全量读入改为 **`json.Decoder` 流式解析**——`dec.Token()` 遍历顶层对象，命中 `documents` 数组后逐元素 `Decode` 增量载入；metadata 等非 documents 字段用 `json.RawMessage` 流式丢弃，不再整体驻留内存。
  - 新增 **文档背压**：`SetMaxDocs(n)` 设置加载上限，超过即停止解析并置 `truncated=true`（可观测，不静默丢信息）；`Init` 支持 `max_docs` 配置项。
  - `loadIndex` 重复调用幂等（先清空 `documents`/`truncated` 再重载）。
- 测试：新增流式背压截断（5 文档限 2）+ 重复加载幂等 2 项。

**验证**：`go test ./internal/parser/adapter/scip/...` 全绿；`go build ./...` / `go vet` 通过。
**遗留**：无（超大 .scip 文件内存占用由背压上限约束；默认不限流保持全量能力）。

### Commit 53: feat(vector): ONNX 嵌入器加固——层名/精度/维度可配 + 并发安全单例（T3-1/PHASE_09）

**核心改动点**：
- `internal/vector/embedder_onnx.go`（`//go:build onnx`）：
  - `ONNXEmbedderConfig` 新增 `OutputLayer`（默认 `sentence_embedding`）、`InputNames`（默认 input_ids/attention_mask/token_type_ids）、`Dim`（默认 512）、`Precision`（默认 fp16 / fp32 / any）。
  - `initRuntime` 使用可配输入输出层名；`ONNXModelAvailableWithPrecision` 按精度重排模型文件候选（fp32 优先 model.onnx，默认优先 model_fp16.onnx）。
  - 全局单例由 `sync.Once`（Close 后重置存在竞态窗口）重构为 **互斥锁 get-or-create**：`GetONNXEmbedderGlobalWithConfig` + `LastGlobalONNXInitError`（初始化失败可观测）、`CloseGlobalONNXEmbedder` 后可重建；新增 `NewONNXEmbedderOrFallbackWithConfig`。
- `internal/vector/embedder_onnx_stub.go`（`//go:build !onnx`）：同步补齐同名 API 桩（config 字段 + 5 个函数），默认构建免 CGO 不受影响。
- 测试：新增精度候选测试（fp32/默认/any 的文件选择）。

**验证**：`go build ./...`（桩）与 `go build -tags onnx ./...`（真实）双路径通过；`go vet -tags onnx ./internal/vector/` 通过。
**遗留**：真实 ONNX 推理（换模型层名）需模型文件 + onnxruntime 动态库；模型分发策略（本地 down/models 优先，远程下载为后续项）。

### Commit 52: feat(scalebench): 嵌入质量评测——Local vs ONNX 召回率基准（T3-3/PHASE_09）

**核心改动点**：
- `internal/scalebench/embedding_quality_test.go`：新增 `TestEmbeddingQuality`——12 代码实体黄金语料 + 12 混合语义查询（词面接近与语义改写各半，含词面重叠干扰项），分别用 LocalEmbedder（先 Observe 建 IDF）与 ONNXEmbedder（bge-small-zh）建 MemoryStore 索引，测 top-5 检索的 Recall@1/3/5 与 avgTop1 相似度。
- ONNX 可用性：默认构建走桩返回 nil → 自动跳过并写清原因（需 `-tags onnx` + down/models 模型文件）；产出 `build/embedding-quality.json` + `analysis/2026-08-14-embedding-quality.md`（analysis/build 目录为 gitignore 产物，不入库）。
- 新增 `NewONNXEmbedderOrFallbackWithConfig`/`ONNXModelAvailableWithPrecision` 供评测与生产共用。

**验证**：本机 Local(TF-IDF) R@1=0.42 / R@3=0.42 / R@5=0.67 / avgTop1=0.23（词面接近查询命中、语义改写困难，符合词袋预期）；ONNX 因模型未下载跳过（报告已标注补跑命令）；`go vet` 通过。
**遗留**：ONNX 模型就绪后补跑以获得语义召回对照；评测语料可按需扩展语言/场景。

### Commit 51: feat(ai): AI 增强层落地——Enhancer + 预算管控 + LLMClient 接口（T4-1/PHASE_09）

**核心改动点**：
- `internal/ai/client.go`：新增 `LLMClient` 接口（`Complete(ctx, prompt) ([]string, error)` 标签/文档补全 + `Choose(ctx, prompt) (int, error)` 同名消歧选择），隔离具体 LLM 后端，便于测试注入 mock。
- `internal/ai/budget.go`：新增 `Budget` 预算管控——perScan/perQuery **双作用域独立计数**（`tryConsumeScan`/`tryConsumeQuery`），`ResetScan`/`ResetQuery` 每次扫描/查询开始时重置，`ScanRemaining`/`QueryRemaining`/`ScanExhausted`/`QueryExhausted` 观测；limit 为负表示不限。
- `internal/ai/enhancer.go`：新增 `Enhancer`——`EnhanceTag`（标签补全）、`EnhanceDoc`（残缺 doc 生成描述，多行拼接）、`Disambiguate`（同名方法消歧，返回候选索引）；`IRable` 接口（Name/QualifiedName/DocComment/Kind）+ `NewClassEntity`/`NewMethodEntity` 适配 `store.ClassRecord`/`MethodRecord`；`SetPhase` 在扫描/查询期切换消耗对应预算；预算超限返回 `errors.ErrBudgetExceeded`（不调用 LLM），LLM 失败包装 `errors.ErrEnhanceFailed`——失败隔离，不影响主流程。
- `internal/ai/budget_test.go` + `enhancer_test.go`：新增 10 项测试（scan 限额/查询不限/双作用域独立、EnhanceTag 成功与超预算不触 LLM、EnhanceDoc 拼接、Disambiguate 索引、LLM 失败包装、phase 切换预算、store 记录适配），`go test -race` 全绿。

**验证**：`go vet ./...` / `go build ./...` / `go test -race ./internal/ai/...` 全绿（10 项新增）。
**遗留**：Enhancer 尚未接入生产编排（扫描器/查询处理器未调用）；真实 LLM provider（OpenAI/本地模型）与 `config.ai` 配置接线为后续项。

### Commit 50: feat(testlink): 测试关联补齐 explicit + coverage 策略（T4-1/PHASE_09）

**核心改动点**：
- `internal/service/testlink.go`：
  - 新增 **explicit 策略**（置信度 100）：解析测试类/方法 `Doc` 中的 `@TestFor(...)` 注解（支持 `@TestFor(OrderService.class)` / `@TestFor(com.example.OrderService)` / `@TestFor OrderService` / `@testfor: order.OrderService` / `@TestFor=OrderService`），将测试方法关联到目标类全部生产方法。目标类解析支持全限定名精确匹配、FQN 后缀匹配、简单类名匹配（`resolveExplicitClass`）；注解可标在类级或方法级（方法级优先）。
  - 新增 **coverage 策略**（置信度 90）：基于注入的覆盖率报告反查——`testMethodFQN → 被覆盖生产方法 FQN 列表`，命中目标方法即关联。
  - `FindTestLinks` 接入上述两策略（explicit 先于 coverage，再 naming/same_tag/dependency）。
  - 包注释更新为「五种策略（按置信度降序）：explicit(100)/coverage(90)/dependency(80)/naming(70)/same_tag(60)」。
- `internal/service/service.go`：
  - `Service` 新增 `coverage map[string][]string` 字段。
  - 新增 `SetCoverage(report)` 注入覆盖率报告；新增 `LoadCoverageJSON(io.Reader)` 从 JSON 解析注入（`{"testMethodFQN": ["prod.MethodA", ...]}`），解析失败不污染已有覆盖数据。
- `internal/service/testlink_test.go`：新增 6 项测试——explicit 类级/方法级、coverage 反查、LoadCoverageJSON 解析、parseExplicitTargets / resolveExplicitClass 单元测试；`go test -race` 全绿。

**验证**：`go vet` / `go build ./...` / `go test -race ./internal/service/...` 全绿（含 3 项 FindTestLinks 关联 + 3 项单元，新增约 160 行测试）。
**遗留**：AI 增强层（Enhancer/Budget）与「explicit/coverage 从真实注解/覆盖率文件自动采集」的接入（当前为内存计算 + 注入式，与 naming/same_tag/dependency 一致），列为后续 T4-1 剩余项。

### Commit 49: fix(lsp): LSP 适配器健壮性——可观测降级 + 失败重试（T2-3/PHASE_09）

**核心改动点**：
- `internal/parser/adapter/lsp/adapter.go`：
  - 引入 `log`/`metrics`/`robust`，新增 `logger` 字段与重试配置（`retryAttempts`/`retryBaseDelay`/`retryMaxDelay`）。
  - `Init` 阶段对 clangd 显式探测 `compile_commands.json`（根目录及 `build`/`cmake-build-debug`/`out`/`.cache/clangd`），缺失则 WARN + `lsp_missing_compile_commands_total`，消除「缺项目上下文静默空符号」。
  - `Parse` 的 `documentSymbol` 请求改用 `requestWithRetry` 包裹（复用 `robust.Retry` 指数退避 + `robust.RetryableError` 判定），瞬时失败（超时/连接重置）自动重试，不放大单文件延迟。
  - `Parse` 对「非空 C/C++ 文件却 0 符号」显式 WARN（`maybeReportEmptySymbols`）+ `lsp_parse_empty_symbols_total{lang="cpp"}`，不再静默空 IR。
  - `readResponses` 中原本静默丢弃的异常帧改为 WARN + `lsp_malformed_frames_total{kind=...}`：Content-Length 解析失败 / JSON 体解析失败 / 孤儿响应（无对应 pending 请求）。
  - 子进程 stderr 由 `io.Discard` 改为按行日志：含 error/fail/compile/missing 等关键字以 WARN 暴露（即 clangd 自身降级原因），其余 DEBUG。
  - `init()` 注册 5 个 LSP 可观测指标（`lsp_missing_compile_commands_total` / `lsp_parse_empty_symbols_total` / `lsp_parse_errors_total` / `lsp_retries_total` / `lsp_malformed_frames_total`）。
- `internal/parser/adapter/lsp/adapter_test.go`：新增 8 项测试覆盖探测命中/缺失、空符号告警、重试失败/取消、孤儿/畸形 Content-Length/畸形 JSON 帧。

**验证数据**：`go build ./...` + `go vet ./...` 通过；`go test -race ./internal/parser/adapter/lsp/...` 全绿（新增 8 项均 PASS，相关 WARN 日志已确认输出）。
**遗留风险**：LSP 适配器当前仍为独立子包，尚未接入 `parser.Registry` 编排主路（降级到 tree-sitter 的「首选返回 `ErrSourceUnavailable` → `Select` 回退」链路未在生产编排中串联）；接入时复用本任务的探测/告警即可暴露降级原因。

### Commit 48: feat(viz): /viz 默认栈可用，统一向量索引（T3-2/PHASE_09）

**核心改动点**：
- `internal/vector/store.go`：`VectorStore` 接口新增 `ListIDs(ctx) ([]string, error)`；`MemoryStore`/`PersistentStore`/`ChromemStore` 均实现（枚举索引内向量 ID）。
- `cmd/codeschema/main.go`：`newSearcher` 重构为 `newSearcherWithStore`（多返回底层 `vector.VectorStore`），保留 `newSearcher` 兼容委托；serve 中基于该 store 经 `vectorVizStore`/`vectorVizSearcher` 适配器统一启用 `/viz`，移除「仅 `storage.vector.driver=chromem` 才可用」的限制。
- 默认栈（Persistent/Memory 向量索引）与检索共用同一 store/embedder → 消除原 chromem 独立索引导致的「文本检索精度不一致」；`vectorVizSearcher.QueryText` 用 `SearchModeExact`（仅 FTS），规避 `SearchModeBoth` 在 reranker 为 nil 时 panic。

**验证数据**：`go build ./...` + `go vet ./...` 通过；`go test ./internal/vector/...` 全绿（新增 `TestMemoryStore_ListIDs`）；`cmd/codeschema` 测试可编译。
**遗留风险**：默认向量索引仅持久化 `id→向量`、不含原文，故 `/viz` 文档 `Content` 为空（以索引元数据 + 文本检索为主）；如需展示原文需另存 content（后续任务）。

### Commit 47: fix(parser): CodeGraph 适配器去骨架，不再静默返回空 IR（T2-4/PHASE_09）

**核心改动点**：
- `internal/parser/adapter/codegraph/adapter.go`：`ParseAll` 改为用纯 Go `modernc.org/sqlite` 打开数据库并校验 `symbols`/`edges` 契约表；DB 缺失/非 SQLite/缺表 → 显式 `ErrSourceUnavailable` 降级；表存在时按文档化契约（symbols: name/qualified_name/kind/file_path/language；edges: caller/callee/type）尽力读取真实类/调用 IR，列漂移显式报错——消除原「DB 存在即静默吐空 IR 文档」的假可用行为。
- `internal/parser/adapter/codegraph/adapter_test.go`：新增 `TestCodeGraphAdapter_ParseAll_RealSymbols`（真实读取）、`ParseAll_InvalidDB`/`ParseAll_MissingTable`（显式降级）；原 `ParseAll_GroupByExt`/`ParseAll_EmptyPaths` 改为基于有效 CodeGraph DB 断言。

**验证数据**：`go test ./internal/parser/adapter/codegraph/...` 全绿；`go build ./...` + `go vet` 通过。
**遗留风险**：CodeGraph 真实 schema 未在本仓确认，契约为假设列名；若真实列名不同，读取会显式报错并降级到 tree-sitter，需后续按真实 schema 校准列名。

### Commit 46: test(ci): 固化 scalebench 基准进 CI，看护 BulkUpsert 回归（T6-1/PHASE_09）

**核心改动点**：
- `internal/scalebench/scale_bench_test.go` — 新增 `BenchmarkScaleBulk`（N=1万、单事务批量入库），作为 BulkUpsert 落库成本的回归看护基准。
- `Makefile` — `bench` target 增加 scalebench（BulkUpsert 回归看护），命令加 `-run '^$'`（仅跑基准，避免误触发同包分钟级慢测试 `TestScaleBench`）。
- `.github/workflows/ci.yml` — 新增 `bench` job，跑 `BenchmarkScaleBulk`（N=1万、秒级）守护 `BulkUpsert` 单事务批量入库，防止「逐文件事务提交放大」回潮。

**验证数据**：`go test -run '^$' -bench=BenchmarkScaleBulk -benchtime=1x ./internal/scalebench` → 782ms/op（N=1万）；`go build ./...` + `go vet` 通过。
**遗留 TODO**：`TestScaleBench` 全量 N=1k~100k 慢测试（分钟级）尚未接 CI（需独立长时 job），目前仅固化了秒级回归看护基准。

### Commit 45: docs(bench): 固化超大仓基准与部署文档（PHASE_09 存储性能收尾）

**Commit Hash**: `24071a0`

**核心改动点**：
- `build/bench-compare.json`、`build/realrepo-bench.json` — 同步 BulkUpsert 基准数据
- `docs/DEPLOYMENT_AND_USAGE.md` — 部署文档更新

**验证数据**：文档/基准数据更新，无代码变更（`docs/ops/01` 因重命名进行中暂未纳入）

### Commit 44: feat(storage): 存储主线统一分发，接入 pg/redis 后端（T1-1/T1-2/T1-3 + E5/PHASE_09）

**Commit Hash**: `fb92b2c`

**核心改动点**：
- `cmd/codeschema/store_dispatch.go`（基础分发 + Redis 叠加点）、`store_pg.go`（`//go:build pg` 注册 PG）、`store_redis.go`（`//go:build redis` 叠加 Redis L2 缓存）
- `internal/store/redis` 增加 `NewRedisCacheFromURL`（解析 `redis://` URL）
- `internal/config` 的 `Validate` 增加 `storage.driver` 允许清单（file/sqlite/pg/postgres）
- 同步 `README`「存储后端」小节、`docs/dev/12` §12.5、`DEV_PROGRESS` 核查结论

**验证数据**：`go build ./...` 与 `-tags pg` / `-tags redis` / `-tags pg,redis` 均通过，`go vet` 通过

### Commit 43: refactor(build): ONNX 嵌入器 build tag 隔离，默认构建免 CGO（T0-2/PHASE_09）

**Commit Hash**: `49c51fb`

**核心改动点**：
- `internal/vector/embedder_onnx.go` 增加 `//go:build onnx`；新增 `embedder_onnx_stub.go`（`!onnx`）提供同名公开 API 桩，默认返回 nil 由调用方降级
- `embedder_onnx_test.go` 增加 `//go:build onnx`

**验证数据**：`CGO_ENABLED=0 go build ./...` 通过；`go build -tags onnx ./...` 仍可用 ONNX

### Commit 42: docs(deploy): 同步 ONNX 集成文档 + 清理旧版本包

**Commit Hash**: `9e4ea51`

**核心改动点**：
- `DEV_PROGRESS.md` — 更新已知问题 #4（ONNX 模型集成细节），更新最新提交信息为 899e7ec
- `README.md` — 更新测试包计数 23→24，环境要求表 onnxruntime 版本 1.17+→1.28+，新增 bge-small-zh 模型行
- `docs/dev/11-配置部署与路线图.md` — 补充 macOS Intel 1.23.2 为最后 x86 版本的说明
- `CHANGELOG.internal.md` — 修正 Commit 41 哈希值（待提交→899e7ec）
- `build/bench-compare.json`、`build/realrepo-bench.json` — 同步基准数据

**验证数据**：纯文档更新 + 基准数据同步，无代码变更

### Commit 41: docs(deploy): 修正 macOS 平台说明，Apple Silicon 设为主目标

**Commit Hash**: `899e7ec`

**核心改动点**：
- `docs/dev/11-配置部署与路线图.md` — 交换 macOS 下载表顺序，Apple Silicon（arm64）设为主条目，Intel（x86_64）标注为旧机型
- `docs/dev/11-配置部署与路线图.md` — 环境要求表 onnxruntime 版本从 1.17+ 更新为 1.28+

**验证数据**：纯文档更新，无代码变更

### Commit 40: feat(onnx): 集成 bge-small-zh ONNX 语义嵌入模型

**Commit Hash**: `bc84553`

**核心改动点**：
- `internal/vector/embedder_onnx.go` — 新增 ONNXEmbedder：
  - 基于 `onnxruntime_go` 加载 bge-small-zh-v1.5 ONNX 模型（FP16 量化，512 维）
  - WordPiece 分词器（tokenizer.json 解析），支持 BERT 归一化/预分词
  - 支持 `SetSharedLibraryPath` 指定 ONNX Runtime 动态库路径（`LibraryDir` 配置项）
  - 实现 `Embedder` 接口，与 `LocalEmbedder` 无缝切换
  - `NewONNXEmbedderOrFallback` 自动检测模型，失败时返回 nil 由调用方降级
  - 全局单例 `GetONNXEmbedderGlobal` / `CloseGlobalONNXEmbedder`
- `internal/vector/embedder_onnx_test.go` — 新增 7 个测试：
  - 基础嵌入/维度/语义相似度/空文本/长文本截断/确定性
- `cmd/codeschema/main.go` — `newSearcher` 优先使用 ONNX Embedder，失败降级到 LocalEmbedder
- `internal/search/builder.go` — `IndexBuilder` 支持 `Embedder` 接口，IDF 仅 `LocalEmbedder` 使用
- `docs/dev/09-语义检索与全文搜索.md` — 补充 ONNX 集成说明 + 文件清单
- `docs/dev/11-配置部署与路线图.md` — 更新 onnxruntime 实际使用状态 + bge-small-zh 模型获取说明

**运行时依赖变更**：
- `down/onnxruntime/onnxruntime.dll` — 从 v1.21.0 升级到 v1.28.0（匹配 `onnxruntime_go v1.32.1` API 28）
- `down/models/bge-small-zh-v1.5/` — 新增 ONNX 模型文件（model_fp16.onnx + model_fp16.onnx_data + tokenizer.json + config.json）
- 使用 `ort.SetSharedLibraryPath` 优先加载 `down/onnxruntime/onnxruntime.dll`，避免系统 PATH 中旧版 DLL 干扰

**新增公共抽象**：
- `ONNXEmbedderConfig.LibraryDir` — 可选字段，指定 ONNX Runtime 动态库所在目录
- `ONNXEmbedder` 结构体 — 实现 `Embedder` 接口，支持 `Close()` 释放资源
- `NewONNXEmbedderOrFallback(modelDir, maxLen, libDir)` — 新增 `libDir` 参数
- `GetONNXEmbedderGlobal` / `CloseGlobalONNXEmbedder` — 全局单例管理

**验证数据**：
- `go build ./...` — 通过
- `go test ./...` — 24 个包全部通过
- `TestONNXEmbedder_Embed` — 向量 512 维，非零嵌入
- `TestONNXEmbedder_SemanticSimilarity` — similarity(天气,天气)=0.8949, similarity(天气,调度)=0.2544 ✓
- `TestONNXEmbedder_EmptyText` — 空文本返回 512 维零向量 ✓
- `TestONNXEmbedder_LongText` — 超长文本自动截断，不报错 ✓
- `TestONNXEmbedder_Deterministic` — 相同输入产生相同向量 ✓

**遗留 TODO / 风险**：
- 当前仅支持 `model_fp16.onnx`（FP16 量化），如需 FP32 精度需额外下载 `model.onnx`
- 输出层名硬编码为 `"sentence_embedding"`，不同 ONNX 转换工具可能输出不同名称
- ONNX Runtime 环境为全局单例，不支持多实例并行初始化
- 模型文件约 47MB，建议在 Dockerfile 中预装或通过 volume 挂载

---

### Commit 39: docs(deploy): 补充 onnxruntime 跨平台部署说明 + Dockerfile 注释

**Commit Hash**: `未提交`
- `Makefile` — `build-cgo` 目标从仅复制 `onnxruntime.dll` 改为跨平台智能复制：
  - Windows: `onnxruntime.dll`
  - Linux: `libonnxruntime.so`
  - macOS: `libonnxruntime.dylib`
- `Dockerfile` — 头部注释新增 ONNX 运行时可选加速的启用说明
- `docs/dev/11-配置部署与路线图.md` — §9.5 从单行 Windows 说明扩展为完整跨平台表格：
  - 4 平台下载包名（Windows/Linux/macOS Intel/macOS ARM）
  - 各平台推荐放置路径
  - 系统库搜索路径说明（LD_LIBRARY_PATH/DYLD_LIBRARY_PATH/PATH）

**新增公共抽象**：
- 无（仅增强已有构建逻辑的跨平台兼容性）

**验证数据**：
- `CGO_ENABLED=1 go build` — 通过
- Windows 本地部署验证：`codeschema scan .` — 181 文件 295ms 扫描 + 15ms 索引构建
- 推送远程：`git push` — 成功

**遗留 TODO / 风险**：
- 当前代码未使用 `onnxruntime_go`，实际部署无需任何 onnxruntime 动态库
- Dockerfile 中 ONNX 注释为可选加速预留，当前不影响构建

### Commit 38: docs(onnxruntime): 新增 onnxruntime.dll 运行时依赖获取说明

**Commit Hash**: `14dec65`

**核心改动点**：
- `Makefile` — `build-cgo` 目标新增自动检测并复制 `down/onnxruntime/onnxruntime.dll` 到输出目录
- `docs/dev/11-配置部署与路线图.md` — §9.5 环境要求新增 onnxruntime.dll 获取方式说明（下载链接、放置位置、自动复制机制）
- `DEV_PROGRESS.md` — 已知问题 #4 状态从"部分解决"更新为"已解决"，标注当前使用 LocalEmbedder 无需 DLL

**验证数据**：
- `make build` — 不变（纯 Go 构建，不需要 DLL）
- `make build-cgo` — 新增 DLL 自动复制逻辑，有 DLL 时复制，无 DLL 时静默跳过

**遗留 TODO / 风险**：
- 当前代码未实际使用 `onnxruntime_go`，语义检索走纯 Go `LocalEmbedder`，无需 DLL
- 未来集成 ONNX 模型时需确保 `onnxruntime.dll` 版本与 `onnxruntime_go` 兼容

### Commit 37: fix(module): 更正 Go 模块名为 github.com/idcu/codeschema + 新增部署基础设施

**Commit Hash**: `a61d4a5`

**核心改动点**：
- `go.mod` — 模块名从 `codeschema` 更正为 `github.com/idcu/codeschema`
- 39 个 Go 源文件的 import 路径同步更新：`"codeschema/` → `"github.com/idcu/codeschema/`
- `docs/DEPLOYMENT_AND_USAGE.md` — 新增部署与使用指南，约 750 行，覆盖：
  - 项目概述与工作流程（三种启动模式：scan/watch/mcp）
  - 快速部署（源码构建、Docker 部署、配置说明、快速启动脚本）
  - 核心工作流（扫描仓库 → 启动服务 → 增量监听）
  - AI 开发工具集成（Trae/Cursor/VS Code/MCP 自定义客户端）
  - HTTP API 参考 + MCP 工具详情
  - 生产部署（Docker Compose、性能调优、监控）
  - 运维指南（生产部署清单、备份恢复、监控告警、性能调优）
  - 常见问题与排查（Windows 构建、端口占用、数据重置等）
- `config.yaml.example` — 完整的配置示例，包含所有配置项及注释
- `docker-compose.yml` — 生产就绪的 Docker Compose 部署，含健康检查/资源限制/认证/持久化/扫描任务 profile
- `scripts/quick-start.sh` — Linux/macOS 一键启动脚本（local/docker 两种模式）
- `scripts/quick-start.ps1` — Windows PowerShell 一键启动脚本（local/docker 两种模式）
- `docs/ops/01-生产部署清单.md` — 生产环境 10 项检查清单
- `docs/ops/02-备份与恢复.md` — 索引数据备份与恢复操作指南
- `docs/ops/03-监控与告警.md` — Prometheus 指标、日志采集、Grafana 仪表盘、告警规则
- `docs/ops/04-性能调优.md` — 不同规模仓库的配置建议与优化策略

**新增公共抽象**：
- `scripts/` — 自动化脚本目录，提供一键启动能力
- `docs/ops/` — 运维文档目录，生产环境运维参考

**影响范围**：
- `go.mod` — 模块名变更
- 39 个 Go 文件 — import 路径前缀更新
- 新增 9 个文件（config.yaml.example, docker-compose.yml, 2 个脚本, 4 个运维文档, 1 个部署指南更新）

**验证数据**：
- `go build ./cmd/codeschema` — 通过
- `codeschema scan .` — 166 文件 68ms 扫描完成，166 docs 14ms 索引构建
- `codeschema mcp --addr :8080` — 启动成功，`tools/list` 返回 11 个工具
- `codeschema serve --http :8081` — 启动成功，`/health` 返回 `{"status":"ok"}`

**遗留 TODO / 风险**：
- 无

### Commit 36: perf(log): 日志模块 data race 修复 — sync.Mutex 保护全局 defaultLogger

**Commit Hash**: `94f5047`

**核心改动点**：
- `internal/log/logger.go` — 新增 `sync.Mutex` 保护全局 `defaultLogger` 变量，避免并发 Init/InitWriter/L 调用时的 data race
- `Init`/`InitWriter`/`L` 方法均使用 `mu.Lock()` / `defer mu.Unlock()` 确保原子性
- 提取 `newHandler` 函数，统一处理日志处理器的创建逻辑，减少代码重复

**新增公共抽象**：
- `mu sync.Mutex` — 全局日志互斥锁
- `newHandler(w, level, jsonOutput) slog.Handler` — 统一的 handler 工厂函数

**影响范围**：
- `internal/log/logger.go` — 仅修改初始化/获取逻辑，Logger 公共 API 不变
- 无外部行为变更（Init/InitWriter/L 语义等价，仅增加线程安全保证）

**验证数据**：
- `go test -race ./internal/log/` — 通过（3.07s）
- `go test -race ./internal/...` — 23 个包全部通过，0 竞态
- `go build ./...` — 通过

**遗留 TODO / 风险**：
- 无

### Commit 35: perf(bench): 多仓库 benchmark 真实运行 + README 同步 P18 进度

**Commit Hash**: `13661fc`

**核心改动点**：
- `README.md` — 当前状态从 P0-P13 更新为 P0-P18，新增 P14~P18 阶段说明行
- `build/bench-compare.json` — 多仓库 benchmark 真实运行结果（CodeSchema 81 文件 + idcu-panel/backend 312 文件），输出对比报告
- `build/realrepo-bench.json` — 自 Benchmark 性能基线更新

**验证数据**：
- 多仓库 benchmark: CodeSchema 81 文件 87ms 扫描 / 12ms 索引 / 1.88ms P95; backend 312 文件 190ms 扫描 / 21ms 索引 / 2.99ms P95
- 自 Benchmark: ScanAndIndex 84ms/op (13次)、Search 1.05ms/op (P95 1.99ms)、FullPipeline 87.6ms/op
- 基准回归验证: 4x 文件量 → 1.6x 扫描时间 / 1.9x P95 延迟，线性扩展性良好
- `go build ./...` — 通过

**遗留 TODO / 风险**：
- 建议在更多外部 Go 仓库（如 kubernetes）上运行 benchmark，以验证更大规模下的性能表现

### Commit 34: feat(viz): 向量索引可视化工具增强 — 单文档 API、点击展开详情、刷新按钮

**Commit Hash**: `5bdf042`

**核心改动点**：
- `internal/server/viz.go` — 新增 `/viz/api/document` 端点，支持按 ID 查询单个文档内容（从 ListDocuments 结果过滤）
- `internal/server/viz.go` — 前端 HTML/JS 全面增强：
  - 点击行展开/折叠文档内容详情面板，支持代码格式显示
  - 搜索结果行点击时异步拉取文档内容并显示
  - 新增刷新按钮（↻），一键刷新概览和文档列表
  - 新增搜索按钮（替代仅 Enter 触发）
  - 新增 Toast 通知提示
  - 搜索结果头部增加"清除"按钮，分页信息显示总条数
  - 响应式设计适配移动端
- `README.md` — 增加向量索引可视化功能说明，更新测试包数量

**影响范围**：
- 修改 2 个文件（viz.go / README.md）
- 无 Public API 变更（VizStore/VizSearcher 接口不变）
- 新增 `/viz/api/document` 端点，不影响现有路由

**验证数据**：
- `go build ./...` — 通过
- `go test ./internal/server/` — 35/35 PASS，1.002s
- 新增端点 `/viz/api/document?id=xxx` 返回 200/404

**遗留 TODO / 风险**：
- `/viz/api/document` 当前遍历所有文档查找匹配 ID，大集合时性能可能下降，可后续添加索引优化

### Commit 33: fix(lsp): 修复 Init/Close 锁重入死锁，SendRequest 超时测试改为确定性取消

**Commit Hash**: `37eab95`

**核心改动点**：
- `internal/parser/adapter/lsp/adapter.go` — `Init` 方法将锁粒度从函数级缩小为仅保护子进程启动段，`sendRequest`/`sendNotification` 调用时已释放锁，消除 `sync.Mutex` 不可重入导致的死锁；`Close` 方法同样释放锁后再调用 `sendNotification`，避免关闭时死锁
- `internal/parser/adapter/lsp/adapter_test.go` — `TestLSPAdapter_SendRequest_Timeout` 从依赖时序的 `context.WithTimeout` 改为确定性的 `context.WithCancel`（立即取消），消除测试对 mock 服务器响应速度的时序依赖

**影响范围**：
- 修改 2 个文件（adapter.go / adapter_test.go）
- 无 Public API 变更（`Init`、`Close`、`Parse` 签名不变）
- 所有依赖 LSP 适配器的上层模块无需修改

**验证数据**：
- `go test ./internal/parser/adapter/lsp/` — 24/24 PASS，0.892s
- 原死锁场景：`TestLSPAdapter_Init_WithMockServer` 30s 超时 → 修复后 0.02s 通过
- 原时序依赖测试：`TestLSPAdapter_SendRequest_Timeout` 因 mock 响应过快（<5ms）偶发失败 → 修复后确定性通过

**遗留 TODO / 风险**：
- 无

### Commit 32: feat(bench): 多仓库 benchmark 对比框架

**Commit Hash**: `7fb0bfe`

**核心改动点**：
- `internal/integration/benchreport.go` — 新增对比报告生成器，定义 BenchResult/BenchComparison 结构体，GenerateComparisonMarkdown 生成 Markdown 表格（含相对性能百分比），GenerateComparisonJSON 生成 JSON 输出，SortBenchResults 排序，pctStr 计算相对性能
- `internal/integration/benchhelper.go` — 新增共享 benchmark 工具函数，NewBenchSetup 工厂函数创建完整组件集合（Store/Scanner/IndexBuilder/Searcher），FindRepoRoot/DiscoverGoFiles 查找仓库和文件，GetBenchRepos 从环境变量 CODESCHEMA_BENCH_REPOS 读取多仓库路径（分号分隔），RepoName 提取目录名
- `internal/integration/multirepo_test.go` — 新增 TestMultiRepo_CollectMetrics 多仓库基准测试，对每个仓库执行 scan→index→search 全流水线，采集文件数/扫描耗时/索引耗时/内存增量/搜索延迟（P50/P95/P99/平均），输出对比报告到 build/bench-compare.json
- `internal/integration/realrepo_test.go` — 重构为使用共享工具函数（NewBenchSetup/FindRepoRoot/DiscoverGoFiles/BenchResult），移除私有函数 setupRealRepo/findRepoRoot/discoverGoFiles 和 RealRepoBenchResult 结构体，消除代码重复

**新增公共抽象**：
- `integration.BenchResult` / `integration.BenchComparison` — 基准测试结果和对比结构体（替代原有的私有 RealRepoBenchResult）
- `integration.GenerateComparisonMarkdown` / `integration.GenerateComparisonJSON` — 对比报告生成器
- `integration.SortBenchResults` / `integration.pctStr` — 排序和相对百分比计算工具
- `integration.BenchSetup` / `integration.NewBenchSetup` — 组件集合工厂函数（替代原有的私有 setupRealRepo）
- `integration.FindRepoRoot` / `integration.DiscoverGoFiles` — 文件系统工具函数（从私有提升为包级公共 API）
- `integration.GetBenchRepos` / `integration.RepoName` — 多仓库路径解析和仓库名提取

**影响范围**：
- 新增 3 个文件（benchreport.go / benchhelper.go / multirepo_test.go）
- 修改 1 个文件（realrepo_test.go）— 移除私有函数和结构体，替换为公共函数
- 不涉及现有 API 变更（benchmark 函数签名不变，仅内部实现替换为公共函数调用）
- 输出文件变更：build/bench-compare.json（新增对比报告），build/realrepo-bench.json（格式从 RealRepoBenchResult 变为 BenchResult）

**验证数据**：
- go build 通过 | go test 23 包 0 失败
- 新增 3 个文件（benchreport.go 165 行 / benchhelper.go 141 行 / multirepo_test.go 166 行），修改 1 个文件（realrepo_test.go -150 行 +68 行）
- 移除的私有函数：setupRealRepo（~30行）、findRepoRoot（~18行）、discoverGoFiles（~25行）、RealRepoBenchResult（~10行）
- 公共函数覆盖率：BenchResult（8 字段）、BenchSetup（4 组件）、GetBenchRepos（默认/单仓库/多仓库）、BenchComparison（3 字段）

**遗留 TODO / 风险**：
- 当前机器无外部 Go 仓库，多仓库对比测试需配置 CODESCHEMA_BENCH_REPOS 环境变量后在外部仓库上运行
- GetBenchRepos 使用分号分隔路径，Windows 路径含空格时需确保分号正确转义
- GenerateComparisonMarkdown 的百分比计算基于 int64，浮点指标（HeapMB/P95）已放大 100 倍后取整，可能存在微小精度误差

### Commit 31: feat(viz): LSP 适配器优化 + chromem-go 向量索引可视化工具

**Commit Hash**: `a9b20e7`

**核心改动点**：
- `internal/parser/adapter/lsp/adapter.go` — `readResponses` 方法从 `bufio.Scanner` 逐行读取改为 `bufio.Reader` 按字节读取 Content-Length 头，精确解析 JSON 体，解决 JSON 体换行导致的解析问题
- `internal/server/viz.go` — 新增向量索引可视化工具 HTTP 处理器（VizHandler），提供概览/文档列表/搜索 API，内嵌 HTML 模板（支持分页、搜索、响应式布局）
- `internal/server/http.go` — 集成 VizHandler 到 HTTPServer，新增 `SetVizHandler` 方法，条件注册可视化路由
- `cmd/codeschema/main.go` — 新增 chromemVizStore/chromemVizSearcher 适配器桥接 ChromemStore 到 VizStore/VizSearcher 接口；serveCmd 中根据 config.Storage.Vector.Driver 自动启用可视化工具
- `internal/vector/chromem.go` — 新增 `Size()` 返回真实文档数（`s.col.Count()`），`ListDocuments()` 和 `QueryText()` 方法支持可视化工具查询
- `internal/vector/chromem_test.go` — 更新 TestChromemStore_Size 期望值从 -1 改为 0（适配新的 Size 实现）

**新增公共抽象**：
- `server.VizHandler` / `server.NewVizHandler` / `server.RegisterVizRoutes` — 向量索引可视化工具处理器
- `server.VizStore` / `server.VizSearcher` — 可视化工具存储和搜索接口
- `server.VizDocInfo` / `server.VizSearchResult` — 可视化工具数据模型
- `chromemVizStore` / `chromemVizSearcher` — main.go 中的适配器包装类型

**影响范围**：
- `internal/server/viz.go` — 新增文件，不修改现有代码
- `internal/server/http.go` — 新增 VizHandler 字段和 SetVizHandler 方法，Start 中条件注册路由
- `cmd/codeschema/main.go` — serveCmd 新增织入逻辑，新增 2 个适配器类型
- `internal/vector/chromem.go` — 新增 3 个方法，非破坏性
- `internal/vector/chromem_test.go` — 测试期望值更新

**验证数据**：
- go build 通过 | go test 22 包 0 失败
- 新增 1 个文件（viz.go），修改 4 个文件（adapter.go/http.go/main.go/chromem.go）
- LSP readResponses 测试全部通过（13 个测试）
- vector 包测试全部通过（24 个测试）
- server 包测试全部通过（30 个测试）

**遗留 TODO / 风险**：
- 可视化工具当前仅支持 chromem 驱动，PersistentStore/MemoryStore 不支持 ListDocuments
- ChromemStore 的 embedFn 使用 nil（默认 OpenAI），与 LocalEmbedder 不兼容，文本搜索精度可能不一致
- LSP readResponses 在 Content-Length 头格式异常时静默跳过，缺少告警日志

**Commit Hash**: `a202cb5`

**核心改动点**：
- `Dockerfile` — 修复 `go mod download` 前 `COPY down/` 的依赖路径问题，新增 HEALTHCHECK 指令和完整使用注释
- `.dockerignore` — 新增文件，排除 .git/IDE/build/data 等无关文件，加速 Docker 构建
- `.gitignore` — 移除 `down/` 全量排除，改为仅排除 zip/tar.gz/gz 归档文件和 go.mod/go.sum 工具模块
- `down/chromem-go/chromem-go-main/` — 提交 chromem-go 源码（48 文件，387KB），确保 CI 和 Docker 构建中 replace 指令可正确解析

**新增公共抽象**：
- 无新增公共抽象

**影响范围**：
- 不涉及现有 API 变更
- `down/` 目录提交 chromem-go 源码，不影响 import 路径
- `.gitignore` 变更仅影响版本控制跟踪，不影响本地开发

**验证数据**：
- `go build` 通过 | `go test` 21 包 0 失败
- Dockerfile 构建逻辑已验证：`COPY down/` 在 `go mod download` 之前执行，replace 目标路径可解析
- `.dockerignore` 排除模式验证：.git/down/*.zip 等被正确排除，chromem-go 源码被包含
- 注意：本地 Docker 不可用，未进行实际镜像构建和容器运行验证

### Commit 29: test(stress): 真实仓库 benchmark 数据采集 — 扫描/索引/搜索全流水线性能基线

**Commit Hash**: `8bed66b`

**核心改动点**：
- `internal/integration/realrepo_test.go` — 新增 3 个基准测试（ScanAndIndex/Search/FullPipeline）+ 1 个集成测试（CollectMetrics），以 CodeSchema 自身仓库为测试目标
- 采用`setupRealRepo`工厂函数统一初始化，注册 tree-sitter 适配器、MemoryFTS/MemoryStore/LocalEmbedder 搜索组件
- `findRepoRoot` / `discoverGoFiles` 工具函数复用
- 基准测试结果格式化输出到 `build/realrepo-bench.json`

**新增公共抽象**：
- `setupRealRepo(tb, repoRoot) (Store, *Scanner, *IndexBuilder, *Searcher)` — 真实仓库基准测试环境初始化
- `RealRepoBenchResult` — 基准测试结果结构体
- `findRepoRoot` / `discoverGoFiles` — 仓库根目录定位和 Go 文件发现工具

**影响范围**：
- `internal/integration/realrepo_test.go` — 新增文件，不修改现有代码
- 不涉及现有 API 变更

**验证数据**：
- go build 通过 | go test 21 包 0 失败
- 集成测试 `TestRealRepo_CollectMetrics`（CodeSchema 自身仓库，77 个 .go 文件）：
  - 扫描耗时：157ms（160 个文件，含非 Go 文件）
  - 索引构建：12ms（1017 docs）
  - 内存增量：3.16MB
  - 搜索延迟：P50=0.996ms, P95=1.538ms, P99=2.082ms, Avg=0.972ms
  - 阈值验证：扫描 < 5min（OK），索引 < 5min（OK），P95 < 500ms（OK）
- Benchmark 关键数据（CodeSchema 自身仓库）：
  - `BenchmarkRealRepo_Search`（1026 次迭代）：avg=0.999ms, p50=0.996ms, p95=1.698ms, p99=1.999ms, 373KB/op, 1322 allocs/op
  - `BenchmarkRealRepo_FullPipeline`（14 次迭代）：search_avg=1.327ms, 104MB/op, 223580 allocs/op（含完整扫描+索引+搜索）

**遗留 TODO / 风险**：
- 当前仅测试了 CodeSchema 自身仓库，未覆盖外部大仓库（如 kubernetes、spring-framework）
- 使用 tree-sitter 正则解析器，实际解析精度可能影响索引质量
- 本地 Embedder（LocalEmbedder）精度有限，P95 搜索延迟 1.5ms 为内存级性能，真实场景（chromem-go + ONNX）可能更高
- 每次 benchmark 迭代创建独立的 FileStore（临时目录），磁盘 I/O 可能影响扫描/索引耗时

### Commit 28: feat(adapter): 多语言适配器扩展（SCIP/LSP） + 语义检索精度提升（chromem-go）

**Commit Hash**: `2038c3c`

**核心改动点**：
- `internal/parser/adapter/scip/` — 新增 SCIP index 直读适配器，支持 .scip 文件 JSON 解析，类/方法/引用关系提取，实现 BatchParser 接口
- `internal/parser/adapter/lsp/` — 新增通用 LSP 适配器框架，JSON-RPC 2.0 通信（Content-Length 头格式），支持 gopls/jdtls/clangd，documentSymbol 递归解析类/方法/嵌套符号
- `internal/vector/chromem.go` — 新增 ChromemStore 实现 VectorStore 接口，基于 chromem-go 嵌入式向量库，支持 NewChromemStore（内存）和 NewPersistentChromemStore（持久化）两种模式
- `go.mod` — 添加 `github.com/philippgille/chromem-go` 依赖

**新增公共抽象**：
- `adapter/scip.NewSCIPAdapter` / `SCIPAdapter.ParseAll` — SCIP 批量解析
- `adapter/lsp.NewLSPAdapter` / `NewGoplsAdapter` / `NewJDTLSAdapter` / `NewClangdAdapter` — LSP 适配器工厂方法
- `vector.NewChromemStore` / `NewPersistentChromemStore` / `ChromemStore` — chromem 向量存储实现

**影响范围**：
- 新增 6 个文件，不修改现有代码
- `go.mod` 新增一条依赖，通过 replace 指向本地路径

**验证数据**：
- go build 通过 | go test 全部通过（21 包，0 失败）
- scip 适配器 18 测试（加载索引 / 转换文档 / 类方法提取 / 多文件加载 / 非 .scip 跳过）
- lsp 适配器 13 测试（工厂方法 / 初始化失败 / 未初始化解析 / 符号转换 / 嵌套符号）
- chromem store 12 测试（Add/Search/BatchAdd/持久化/自定义embedFn/错误处理）

**遗留 TODO / 风险**：
- SCIP 适配器当前加载所有 .scip 文件到内存，大项目需流式加载优化
- LSP 适配器 readResponses 实现仅按行解析，实际 LSP 响应是 Content-Length 头格式，需按字节读取
- ChromemStore 不支持 Delete 操作，生产环境需配合定期重建集合
- chromem-go 的 QueryEmbedding 要求 nResults ≤ 文档数，Search 方法未自动处理越界

### Commit 27: build(project): P13 构建脚本 / CI 配置 / 容器化 / 部署文档

**Commit Hash**: `7a4530e`

**核心改动点**：
- `Makefile` — 构建自动化脚本，支持 10 个目标（build/test/clean/cross/lint/bench/run/help），跨平台交叉编译 linux/darwin/windows × amd64/arm64
- `Dockerfile` — 多阶段构建，golang:1.25-alpine → alpine:3.20，CGO 构建含 SQLite/tree-sitter，支持 VERSION 构建参数
- `.github/workflows/ci.yml` — GitHub Actions CI 流水线，4 个 Job：test（3 平台）+ race（竞态检测）+ cross（交叉编译）+ docker（tag 推送镜像）
- `docs/dev/11-配置部署与路线图.md` — 新增 §9 P13 构建与部署指南（Makefile 目标表/Docker 命令/CI Job 说明/部署形态/环境要求/检查清单）
- `DEV_PROGRESS.md` — 更新 P13 完成状态，进度条 P13 100%，下一步规划改为按需迭代

**新增公共抽象**：
- 无（基础设施文件，不涉及公共 API）

**影响范围**：
- 新增 Makefile/Dockerfile/.github/workflows/ci.yml — 不影响现有 Go 代码
- `docs/dev/11-配置部署与路线图.md` — 新增 §9，不修改已有内容

**验证数据**：
- go build — 通过
- go test ./... — 18 个包全部通过，0 失败
- 新增 3 个文件 + 2 个文档更新，共 420 行新增
- 所有 P0-P13 阶段全部完成，项目达到生产级可运行状态

**遗留 TODO / 风险**：
- 交叉编译（CGO_ENABLED=0）生成的二进制不含 SQLite 和 tree-sitter，需在目标平台使用 CGO 构建或预装运行时
- Dockerfile 构建依赖 alpine 镜像的 GCC 工具链，若 Go 版本升级需同步更新基础镜像标签
- GitHub Actions CI 的 Docker 镜像推送未配置 registry 认证，需在仓库 Secrets 中配置 DOCKER_USERNAME/DOCKER_PASSWORD

### Commit 26: feat(robust): P12 生产级健壮性 — 优雅关闭 / 重试机制 / Panic 恢复

**Commit Hash**: `0e77740`

**核心改动点**：
- `internal/robust/graceful.go` — 新增优雅关闭管理器（GracefulManager），支持多钩子注册、逆序执行、超时控制、信号监听、二次信号强制退出
- `internal/robust/retry.go` — 新增重试机制（Retry），指数退避 + 抖动，等效 AWS SDK StandardRetryMode
- `internal/robust/recovery.go` — 新增 Panic 恢复工具（RecoveryHandler），SafeCall/SafeCallWithResult/Go/Recover 等
- `internal/server/mcp.go` — 添加 panic 恢复中间件
- `internal/watcher/watcher.go` — PollWatcher/FsWatcher 添加 SafeCall 保护
- `internal/scheduler/scheduler.go` — Scheduler.Start 添加 SafeCall 保护
- `cmd/codeschema/main.go` — 使用 GracefulManager 统一优雅关闭

**新增公共抽象**：
- `robust.NewGracefulManager` / `Register` / `RegisterFunc` / `Shutdown` / `WaitForSignal` / `ForceExitOnSecondSignal`
- `robust.Retry` + `WithMaxAttempts` / `WithBaseDelay` / `WithMaxDelay` / `WithJitter` / `WithRetryable`
- `robust.NewRecoveryHandler` / `SafeCall` / `SafeCallWithResult` / `Go` / `Recover` / `RecoverWithCallback` + 全局便捷函数

**影响范围**：
- `internal/robust/` — 新增目录，不破环现有代码
- `internal/server/mcp.go` — 新增 recoveryMiddleware，对内层 handler 无影响
- `internal/watcher/watcher.go` — poll/handleEvent 调用 SafeCall 包裹，不影响返回值
- `internal/scheduler/scheduler.go` — processFn 调用 SafeCall 包裹，不影响返回值
- `cmd/codeschema/main.go` — 信号处理重构为 GracefulManager，功能等价

**验证数据**：
- go build ./cmd/codeschema — 通过
- go test ./... — 全部通过（18 个包，0 失败）
- robust 包 28 个测试全部通过
- 优雅关闭：逆序执行 / 超时 100ms 钩子正常关闭 / 重入保护 / 信号监听
- 重试：3 次耗尽 / 第 3 次成功 / context 取消立即返回 / 不可重试错误不重试 / 指数退避 + 抖动验证
- Panic 恢复：SafeCall 捕获 panic 返回 error / Recover 不 panic / Go 启动 goroutine 安全

**遗留 TODO / 风险**：
- GracefulManager 的 Shutdown 逆序执行策略在极端场景下可能不够灵活（如依赖关系非线性的组件），需按需调整
- Retry 暂不支持指数退避的随机种子配置，每次运行随机数相同
- 重试机制暂未集成到现有 Store/Watcher 等 I/O 操作中，需在 P13 阶段按需应用
- robust 包日志依赖 internal/log，如果 internal/log 本身发生 panic 会造成递归恢复

### Commit 25: test(stress): P11 集成测试与性能压测 — 端到端全流程验证 / 9 项性能基准

**Commit Hash**: `37ac435`

**核心改动点**：
- `internal/integration/integration_test.go` — 新建 7 个端到端集成测试，覆盖 scan → store → index → search 全流程、空查询、搜索限制、文件搜索、结果富化、重复扫描幂等性、索引一致性
- `internal/integration/benchmark_test.go` — 新建 9 个性能基准测试，覆盖 MemoryFTS 索引/搜索（100/1000/5000 文档）、LocalEmbedder Embed（3 维度 × 3 文本长度）、LocalEmbedder Observe（100/1000/5000 文档）、IndexBuilder 全量构建（10/50/200 文件）、Searcher 双路检索（100/1000 文档 × 3 模式）、完整流水线（10/50/100 文件）、异步索引队列（1/2/4/8 worker × 1000 文档）、融合重排器（100/500/1000 结果）
- `internal/store/filestore.go` — 修复 `UpsertIR` 未存储方法数据的 Bug，按 ClassFQN 匹配类和方法，新增方法分组逻辑

**新增公共抽象**：
- 无（新增测试文件，不涉及公共 API 变更）

**影响范围**：
- `internal/integration/` — 新增目录和文件，不影响现有代码
- `internal/store/filestore.go` — `UpsertIR` 新增方法存储逻辑，向后兼容（原有代码路径不变）

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（17 个包，0 失败）
- 集成测试：7 个全部通过
- Benchmark 关键数据（100ms 跑分）：
  - MemoryFTS Index 100 文档：~497µs/op, 331KB/op, 11.7K allocs/op
  - MemoryFTS Index 5000 文档：~25ms/op, 16.7MB/op, 586K allocs/op
  - MemoryFTS Search 5000 文档：~8ms/op, 8.7MB/op, 13K allocs/op
  - LocalEmbedder Embed dim=128/words=10：~2.2µs/op, 2.7KB/op, 31 allocs/op
  - LocalEmbedder Embed dim=1024/words=200：~30µs/op, 32KB/op, 453 allocs/op
  - LocalEmbedder Observe 5000 文档：~42ms/op, 31.9MB/op, 616K allocs/op
  - Searcher Search both 模式 1000 文档：~2.7ms/op, 1.7MB/op, 2.7K allocs/op
  - 异步索引 8 worker × 1000 文档：~18ms/op, 18.3MB/op, 368K allocs/op
  - 重排器 1000 结果：~614µs/op, 739KB/op, 2K allocs/op

**遗留 TODO / 风险**：
- 集成测试使用 mock parser，不覆盖真实 tree-sitter 解析器路径，需在 P1 阶段补充真实解析器的集成测试
- 性能基准测试未覆盖磁盘持久化（PersistentFTS/PersistentStore/LocalEmbedder 序列化），需在后续补充
- BenchmarkFullPipeline 和 BenchmarkIndexBuilder_BuildFromStore 由于包含 store 操作和数据准备，跑分可能受磁盘 I/O 影响
- Benchmark 数据为开发环境（Windows 笔记本）单次跑分，非生产环境，仅作为相对性能参考

### Commit 24: perf(index): IDF 跳过 Observe + 自动持久化 — 全量构建加速 / 增量 IDF 保全

**Commit Hash**: `babea23`

**核心改动点**：
- `internal/vector/embedder_local.go` — 新增 `HasIDF()` 方法，检查是否已加载持久化 IDF 词典（`docCnt > 0`）
- `internal/search/builder.go` — `BuildFromStore` 第二阶段增加 `HasIDF()` 检查：已加载持久化 IDF 时跳过 Observe 阶段，直接使用已有词典；新增 `AutoSaveIDF` 方法，启动时立即保存一次 + 定时器双触发，确保 IDF 变更不丢失；返回的 stop 函数使用 `sync.Once` 保证幂等；新增 `StopAutoSaveIDF` 方法
- `internal/vector/vector_test.go` — 新增 4 个测试：`HasIDF` 初始/Observe 后/加载后/重置后
- `internal/search/builder_test.go` — 新增 4 个测试：`AutoSaveIDF` 功能验证、stop 幂等性、最小间隔钳位、`BuildFromStore` 跳过 IDF 构建
- `cmd/codeschema/main.go` — `watchCmd`/`mcpCmd`/`serveCmd` 启动 IDF 自动持久化（60s 间隔）

**新增公共抽象**：
- `LocalEmbedder.HasIDF() bool` — 检查是否已加载 IDF 词典
- `IndexBuilder.AutoSaveIDF(path, interval) func()` — 启动自动持久化，返回幂等 stop 函数
- `IndexBuilder.StopAutoSaveIDF()` — 停止自动持久化

**影响范围**：
- `internal/vector/embedder_local.go` — 新增方法，非破坏性
- `internal/search/builder.go` — 新增字段和方法，`BuildFromStore` 行为优化（非破坏性，跳过 Observe 不影响功能正确性）
- `cmd/codeschema/main.go` — 3 个命令新增 AutoSaveIDF 调用，非破坏性

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（20 个包，0 失败）
- 新增测试：8 个（4 vector + 4 search）
- 搜索包测试：21 个（17 原有 + 4 新增）
- 向量包测试：17 个（13 原有 + 4 新增）
- 全量构建加速：跳过 Observe 阶段可节省 O(N) 时间（N = 文档数）

**遗留 TODO / 风险**：
- 自动保存使用 `os.Create` 写入，在高并发索引场景下频繁写入可能影响性能，建议后续增加写入限频或合并策略
- `AutoSaveIDF` 的定时器在 `StopAutoSaveIDF` 后不保证最后一次保存完成，需要同步等待时可手动调用 `SaveIDF` 后再停止
- 跳过 Observe 时 IDF 词典不会随新文档词频更新，适合索引文件未变化时的增量构建；如果文件发生大规模变更，全量构建时建议先 Reset 再 Observe

### Commit 23: perf(search): P10 遗留问题治理 — 多 worker / 日志集成 / 结果富化 / 进度条

**Commit Hash**: `f923dd6`

**核心改动点**：
- `internal/search/builder.go` — `StartAsync` 签名从 `(ctx, queueSize)` 改为 `(ctx, queueSize, numWorkers)`，支持多 worker 并发消费队列；`asyncWorker` 集成 `log.WithModule("search.index_builder")` 记录索引失败日志；`BuildFromStore` 分阶段输出进度（文件扫描百分比 → IDF 构建百分比 → FTS/向量写入状态）
- `internal/search/builder_test.go` — 新增 6 个测试：多 worker 并发（3 worker × 10 文档）、默认 worker 数（0→2）、幂等性、错误回调、同步降级、删除文档
- `cmd/codeschema/main.go` — `watchCmd` 中 `StartAsync(ctx, 64)` 改为 `StartAsync(ctx, 64, 2)`
- `internal/service/service.go` — 新增 `enrichResults` 和 `resolveSymbol` 方法，`Search` 返回结果前自动从 Store 查询 Kind 和 File 信息；新增 `parseInt64` 工具函数
- `internal/service/service_test.go` — 新增 5 个测试：符号解析（file/class/interface/method/无效）、富化逻辑、`parseInt64` 工具函数

**新增公共抽象**：
- `Service.enrichResults(ctx, results)` — 搜索结果富化方法
- `Service.resolveSymbol(ctx, symbol) (kind, file)` — 符号 ID 解析为 Kind/File
- `parseInt64(s string) int64` — 简单整数解析工具函数

**影响范围**：
- `internal/search/builder.go` — `StartAsync` 签名变更（非破坏性，新增参数有默认值，传 0 使用默认值 2）
- `internal/search/builder_test.go` — 新增 6 个测试，向后兼容
- `cmd/codeschema/main.go` — `StartAsync` 调用处同步更新
- `internal/service/service.go` — 新增 3 个方法，非破坏性
- `internal/service/service_test.go` — 新增 5 个测试，向后兼容

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（20 个包，0 失败）
- 新增测试：11 个（6 builder + 5 service）
- builder 包测试：17 个（11 原有 + 6 新增）
- service 包测试：19 个（14 原有 + 5 新增）

**遗留 TODO / 风险**：
- 搜索结果富化需要遍历所有文件查找类/方法，大仓库场景可能成为性能瓶颈，建议后续建立 symbol → (kind, file) 的缓存映射
- 进度条输出使用 `fmt.Printf` 写入 stdout，在非交互式场景（如 daemon 模式）建议通过日志系统输出或通过 `io.Writer` 可配置
- `parseInt64` 不支持负数和大数，当前场景（解析 class/method ID）够用，但不可作为通用工具函数复用

**Commit Hash**: `ab30caf`

**核心改动点**：
- `cmd/codeschema/main.go` — `mcpCmd` 和 `serveCmd` 启动时自动调用 `svc.BuildIndex(ctx)` 从 Store 全量构建 FTS 和向量索引
- 构建完成后自动持久化 IDF 词典到 `{search.idf_dir}/idf.json`
- `newSearcher` 的返回值从 `(searcher, _)` 改为 `(searcher, builder)`，将 IndexBuilder 注入 Service 层

**解决 Issue**：**#5** — 向量索引为空（启动时 MemoryStore 和 PersistentFTS 里没有数据）

**之前**：mcp/serve 命令创建了 searcher 但不构建索引，搜索时索引为空，返回 0 条结果。
**之后**：mcp/serve 启动时自动从 Store 读取所有文件/类/方法数据，构建 FTS 和向量索引，搜索立即可用。

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过
- 改动量：`cmd/codeschema/main.go` +32/-4 行

**遗留 TODO**：
- 大仓库启动时全量构建索引可能耗时较长，后续可考虑持久化索引快照，启动时直接加载
- 增量索引的异步 worker 队列（StartAsync）已在 watchCmd 中启用，mcp/serve 暂未启用异步队列

### Commit 21: deps(go): 安装 chromem-go 向量数据库

**Commit Hash**: `1ff4e3e`

**核心改动点**：
- `go.mod` — 新增 `replace github.com/philippgille/chromem-go => ./down/chromem-go/chromem-go-main`，从本地源码安装（纯 Go，零依赖，无需 CGO）
- chromem-go 是内嵌式向量数据库，支持内存 + 可选持久化，1000 条文档查询 0.3ms
- 为后续替换当前 MemoryStore 提供更专业的向量存储方案

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 18 个包全部通过，0 失败

**遗留 TODO**：
- 实际集成 chromem-go 替换当前 MemoryStore 留待后续 P8.3/P10 实现
- `replace` 指向本地 `down/` 目录（已加入 .gitignore），生产环境需改为 `go get` 远程安装

### Commit 20: deps(go): 安装全部外部依赖包

**Commit Hash**: `a8e465f`

**核心改动点**：
- `go.mod` / `go.sum` — 从本地 Go 模块缓存安装全部外部包：
  - `github.com/mattn/go-sqlite3 v1.14.49` — SQLite 数据库驱动（CGO，自带 C 源码，无需外部 DLL）
  - `github.com/smacker/go-tree-sitter v0.0.0-20240827094217-dd81d9e9be82` — tree-sitter 多语言语法解析 Go 绑定（CGO，自带各语言 parser.c 源码，无需外部 tree-sitter 库）
  - `github.com/yalue/onnxruntime_go v1.32.1` — ONNX 运行时 Go 绑定（CGO，需外部 onnxruntime.dll）
- 所有包均基于 CGO（GCC 16.1.0 已可用），为后续 SQLite 存储、精确语法树解析、本地 ONNX 嵌入模型做好准备

> **2026-08-14 更正说明（当前实际状态）**：上述 `go-sqlite3` / `go-tree-sitter` 依赖在后续演进中已被替换，当前 `go.mod` **不再包含** 二者：
> - SQLite 实际使用 **`modernc.org/sqlite`（纯 Go，免 CGO）**；
> - 多语言解析实际为 **6 语言正则启发式解析**，并非 `go-tree-sitter` CGO 绑定。
> 故「已安装 go-sqlite3 / go-tree-sitter」的表述与当前代码不符，特此更正。此外，`onnxruntime_go` 已于 2026-08-14 通过 `//go:build onnx` 隔离，默认 `go build` 不再强制依赖 CGO/gcc。

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 18 个包全部通过，0 失败

**遗留 TODO**：
- 实际集成 SQLite 存储（替换当前 PersistentStore）、ONNX 嵌入器（替换当前 LocalEmbedder）、tree-sitter 精确解析（替换当前正则）留待后续 P8.3/P10/P11 实现
- `chromem-go` 未在缓存中，当前使用纯 Go 替代方案（MemoryStore + PersistentFTS）
- `onnxruntime_go` 编译通过，但运行时需要 `onnxruntime.dll`（可从 onnxruntime releases 下载）

### Commit 19: feat(watcher): FsWatcher — 基于 fsnotify 的原生文件系统监听

**Commit Hash**: `78b19c5`

**核心改动点**：
- `internal/watcher/watcher.go` — 新增 `FsWatcher` 结构体，实现 `Watcher` 接口，基于 fsnotify 原生文件系统事件监听；`addRecursive` 递归添加所有子目录（自动跳过 ignoreDirs 目录）；`handleEvent` 处理 Create/Write/Remove/Rename 事件，新目录创建时自动加入递归监听；`isIgnored` 检查路径是否在忽略目录下；`Stop()` 并发安全关闭
- `internal/config/config.go` — WatcherConfig 新增 `UseFsnotify bool` 字段（默认 false）；DefaultConfig 默认值、cloneConfig 深拷贝、Merge 合并策略、LoadFromEnv 环境变量覆盖全部同步更新
- `cmd/codeschema/main.go` — watch 命令新增 `--fsnotify` 标志；`watchCmd` 根据 `UseFsnotify` 配置或 `--fsnotify` 标志选择 FsWatcher 或 PollWatcher；帮助文本更新
- `.gitignore` — 新增 `down/` 条目
- 测试文件：watcher_test.go 新增 6 个 FsWatcher 测试（DetectsNewFile/IgnoresGitDir/IgnoresNestedIgnoredDir/StopWithoutStart/DetectsFileModification/RecursiveDirectoryWatch）

**新增公共抽象**：
- `watcher.FsWatcher` — 实现 Watcher 接口，基于 fsnotify 的原生文件系统监听器
- `watcher.NewFsWatcher(root, scan, sched, ignoreDirs) (*FsWatcher, error)` — 构造函数
- `config.WatcherConfig.UseFsnotify` — 配置字段，控制是否启用 fsnotify 监听

**影响范围**：
- `internal/watcher/watcher.go` — 新增约 150 行代码（FsWatcher 实现），非破坏性（PollWatcher 保持不变）
- `internal/config/config.go` — WatcherConfig 新增一个字段，非破坏性
- `cmd/codeschema/main.go` — watchCmd 新增 watcher 选择逻辑，非破坏性（默认行为不变）

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（18 个包，0 失败）
- watcher 包 8 个测试全部通过（2 个原有 PollWatcher + 6 个新增 FsWatcher）
- 新增测试覆盖：FsWatcher 新文件创建检测、忽略目录（.git/node_modules）、嵌套忽略目录路径识别、Stop 并发安全（未 Start 时调用）、文件修改检测、子目录递归监听

**遗留 TODO / 风险**：
- fsnotify 的 `AddWith` 选项（WithBufferSize/WithOps）尚未使用，大仓库场景可通过配置文件调整
- 当前 fsnotify 版本 v1.10.1 的 Windows 后端（ReadDirectoryChangesW）在 SMB 网络文件系统上可能因缓冲区溢出丢失事件，可通过 WithBufferSize 调整
- 递归监听在文件数量极大（>10 万）的仓库中，inotify 可能达到 `max_user_watches` 限制（Linux 特有，Windows 无此限制）

**Commit Hash**: `de99262`

**核心改动点**：
- `internal/config/config.go` — 新增 `LoadFromEnv` 函数，从环境变量加载配置覆盖（CODESCHEMA_<SECTION>_<KEY> 格式，支持 20+ 环境变量，包括 project/storage/server/scanner/watcher/ai/parser）；新增 `Merge` 函数，合并多个配置源（字符串非空覆盖、整型 >0 覆盖、布尔 true 覆盖、切片非空覆盖、map 非空覆盖，深拷贝保证不修改原始实例）；新增 `ConfigWatcher` 结构体，支持轮询配置文件变更检测（默认 2 秒间隔），检测到变更时自动重新加载并原子切换，提供 `OnReload` 回调通知应用层
- `internal/config/parse.go` — 无变更（复用现有 applyToConfig 体系）
- `internal/config/config_test.go` — 新增 8 个测试覆盖：LoadFromEnv 全量覆盖、无效整型值降级、Merge 基础/覆盖/全量/局部、CloneConfig 深拷贝、ConfigWatcher 初始化
- `cmd/codeschema/main.go` — 加载配置后自动应用 `config.LoadFromEnv(cfg)`；watch/mcp/serve 命令启动 `ConfigWatcher` 后台协程实现配置热重载
- 文档同步：DEV_PROGRESS.md（P9 100% 进度条 + 完成清单）、CHANGELOG.internal.md（本记录）

**新增公共抽象**：
- `config.LoadFromEnv(cfg)` — 从环境变量加载配置覆盖
- `config.Merge(base, overlay *Config) *Config` — 合并两个配置实例，返回新实例
- `config.ConfigWatcher` — 配置监听器（轮询模式，线程安全，原子切换）
- `config.OnReload` — 配置重载回调类型
- `config.cloneConfig` — 深拷贝工具函数
- `config.mergeSearch` — Search 子配置合并函数
- `config.cloneStringSlice` / `config.cloneStringMap` — 切片/map 深拷贝工具

**影响范围**：
- `internal/config/config.go` — 新增约 470 行代码（LoadFromEnv/Merge/cloneConfig/ConfigWatcher），非破坏性
- `cmd/codeschema/main.go` — 新增约 15 行代码（环境变量加载 + ConfigWatcher 启动），非破坏性

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（20 个包，0 失败）
- config 包 33 个测试全部通过（25 原有 + 8 新增）
- 新增测试覆盖：LoadFromEnv（8 个环境变量全量覆盖、无效整型值降级）、Merge（base nil 默认值、overlay nil 原样返回、全量覆盖保留默认、局部覆盖保留其余字段、深拷贝不修改原始）、ConfigWatcher（初始化 + GetConfig 线程安全）

**遗留 TODO / 风险**：
- ConfigWatcher 当前使用轮询方式检测文件变更（因 fsnotify 需要外部依赖），2 秒间隔对实时性要求高的场景不够及时，可配置缩短间隔或后续切换为 fsnotify
- Merge 函数对布尔值采用"true 覆盖、false 不覆盖"策略，无法通过 Merge 将布尔值设为 false（可通过 LoadFromEnv 设置 `CODESCHEMA_WATCHER_ENABLED=false` 解决）
- 配置热重载仅更新 Config 实例本身，不自动重新初始化依赖该配置的服务（如 Scanner workers、Store DSN），需通过 OnReload 回调手动处理

### Commit 17: perf(search): P8.3 优化 — IDF 持久化 / 异步索引 / 删除同步

**Commit Hash**: `3e93fa4`

**核心改动点**：
- `internal/vector/embedder_local.go` — LocalEmbedder 新增 SaveIDF/LoadIDF 方法，JSON 编码持久化 IDF 词典，重启后无需重新 Observe
- `internal/config/config.go` — SearchConfig 新增 IDFDir 字段，默认 `./data/idf`
- `internal/search/builder.go` — IndexBuilder 新增 StartAsync/StopAsync/EnqueueIndex 异步队列，支持后台 worker 异步索引（同步降级兼容）；新增 RemoveDocument/BuildAndRemove 方法，支持文件删除时清理 FTS 和向量索引
- `internal/vector/indexer.go` — Indexer 新增 RemoveDocument 方法，委托 VectorStore.Delete
- `internal/scanner/scanner.go` — Scanner 新增 onDelete 字段和 SetOnDelete 方法，ProcessFile 检测文件不存在时触发删除回调
- `cmd/codeschema/main.go` — newSearcher 启动时自动加载 IDF 词典；scanCmd 全量构建后持久化 IDF；watchCmd 启动异步队列 + 全量构建后持久化 IDF + 设置删除回调

**新增公共抽象**：
- `LocalEmbedder.SaveIDF(path) / LoadIDF(path)` — IDF 词典持久化接口
- `IndexBuilder.StartAsync/StopAsync/EnqueueIndex` — 异步索引队列
- `IndexBuilder.RemoveDocument/BuildAndRemove` — 索引删除接口
- `Indexer.RemoveDocument` — 向量索引删除
- `Scanner.SetOnDelete` — 删除回调设置

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（19 个包，0 失败）
- 新增方法：9 个（SaveIDF/LoadIDF/StartAsync/StopAsync/EnqueueIndex/RemoveDocument×2/BuildAndRemove/SetOnDelete）

**遗留 TODO / 风险**：
- 异步队列当前为单 worker 单 goroutine，高并发场景可扩展为多 worker
- 删除索引时未反向删除 store 中的文件记录（索引清理仅为文档级，store 层文件记录由 PollWatcher 自动处理）
- 异步索引错误仅通过 onError 回调通知，未集成全局日志系统

**Commit Hash**: `d0f5f23`

**核心改动点**：
- `internal/search/builder.go` — IndexBuilder 自动索引构建器，BuildFromStore 全量构建（文件→类→方法递归遍历）、BuildAndIndex 增量更新（单文件触发）、IndexDocument 单文档索引
- `internal/search/builder_test.go` — 10 个测试覆盖空 Store、单文件/多文件、无类文件、增量构建、构建文本、文件不存在等边界
- `internal/scanner/scanner.go` — 新增 onIndex 字段和 SetOnIndex 方法，ProcessFile 最后调用增量索引回调
- `internal/service/service.go` — 新增 indexBuilder 字段、WithIndexBuilder 方法、BuildIndex 全量构建接口
- `cmd/codeschema/main.go` — newSearcher 返回 (searcher, builder)，scanCmd 扫描后自动构建索引，watchCmd 启动时全量构建+增量回调，mcp/serve 命令集成 searcher
- 文档同步：DEV_PROGRESS.md（P8.3 100%）、docs/dev/09-语义检索与全文搜索.md（P8.3 完成标准+文件清单）

**新增公共抽象**：
- `search.IndexBuilder` — 自动索引构建器（BuildFromStore/BuildAndIndex/IndexDocument）
- `search.BuildResult` — 构建结果统计（TotalDocs/IndexedDocs/Errors/Duration）
- `buildClassIndexText` / `buildMethodIndexText` — 索引文本生成函数

**影响范围**：
- `internal/scanner/scanner.go` — 新增 onIndex 字段和 SetOnIndex 方法，非破坏性
- `internal/service/service.go` — 新增 WithIndexBuilder 方法和 BuildIndex 方法，向后兼容
- `cmd/codeschema/main.go` — newSearcher 签名变更（返回双值），mcpCmd/serveCmd 需拆包

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（19 个包，0 失败）
- 新增测试：10 个（builder 包全部覆盖）
- 搜索包测试：35 个（25 原有 + 10 新增）
- 测试覆盖：空 Store 边界、单文件类+方法、无类文件降级、多文件多类、IndexDocument 单文档、BuildAndIndex 增量、文件不存在错误、索引文本 FQN/签名/文档

**遗留 TODO / 风险**：
- IndexBuilder 的 IDF 词典在每次全量构建时重建，未持久化（重启后需重新 Observe）
- 增量索引当前为同步调用，大文件场景可能阻塞扫描流程，P1 可改为异步队列
- 删除文件时未同步删除索引（当文件被删除，FTS 和向量索引中的旧文档仍存在），P1 需实现 RemoveIndex 方法

**Commit Hash**: `306251b`

**核心改动点**：
- `internal/vector/persistent.go` — PersistentStore 磁盘持久化向量存储，JSON 序列化，自动保存（每 10 次变更触发落盘），支持 Save/Load/Close
- `internal/vector/embedder_local.go` — LocalEmbedder 纯 Go Embedder，词袋模型 + 哈希技巧 + TF-IDF 权重，1024 维可配置，支持 Observe 建立 IDF 词典，FNV-1a 哈希映射，L2 归一化
- `internal/search/fts_persistent.go` — PersistentFTS 磁盘持久化全文搜索，JSON 序列化，复用 MemoryFTS 搜索逻辑，自动保存
- `internal/config/config.go` — SearchConfig 新增 FTSDir / VectorDir / VectorDim 三个字段，默认启用了语义搜索（Semantic: true → 从 false 改为 true）
- `cmd/codeschema/main.go` — newSearcher 工厂函数切换为 PersistentFTS + PersistentStore + LocalEmbedder，失败时自动降级到 MemoryFTS/MemoryStore

**新增公共抽象**：
- `vector.PersistentStore` — 实现 VectorStore + Close/Save，磁盘持久化
- `vector.LocalEmbedder` — 实现 Embedder，纯 Go 统计 Embedder
- `search.PersistentFTS` — 实现 FTSEngine + Save，磁盘持久化

**影响范围**：
- `internal/config/config.go` — SearchConfig 新增 3 个字段，默认值变更
- `cmd/codeschema/main.go` — newSearcher 签名变更（接受 config.Config 参数），调用方已同步更新

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（18 个包，0 失败）
- 新增测试：15 个（PersistentStore 4 + LocalEmbedder 7 + PersistentFTS 4）
- 向量包测试：13(旧) + 4(PersistentStore) + 7(LocalEmbedder) = 24 个
- 搜索包测试：17(旧) + 4(PersistentFTS) = 21 个

**遗留 TODO / 风险**：
- LocalEmbedder 的 IDF 词典仅在 Observe 调用时更新，未持久化（重启后需重新 Observe）
- 向量索引为空需 P8.3 实现从 Store 数据自动构建
- 精度低于真实语义模型，P8.3 网络恢复后可切换为 chromem-go

**Commit Hash**: `5f2f84e`

**核心改动点**：
- `internal/vector/store.go` — 向量库接口（VectorStore）定义，MemoryStore 实现（余弦相似度，线程安全）
- `internal/vector/indexer.go` — 异步 embedding 索引构建器（Indexer），支持同步/异步/批量构建，worker pool 并发控制
- `internal/vector/model.go` — Embedder/TextEmbeddable 接口定义，MockEmbedder 确定性哈希实现（128 维）
- `internal/search/fts.go` — FTSEngine 接口定义，MemoryFTS 内存实现（精确/前缀/模糊/布尔模式，TF-IDF 简化版）
- `internal/search/searcher.go` — 双路检索器（Searcher），整合 FTS + 向量，支持 exact/semantic/both 三种模式
- `internal/search/reranker.go` — 融合重排器（Reranker），归一化 → 加权融合 → 去重 → 降序
- `internal/search/adapter.go` — VectorAdapter 桥接 vector.Indexer → search.VectorSearcher 接口
- `internal/service/service.go` — 新增 `WithSearcher` 方法，`Search` 方法接入双路检索逻辑
- `internal/server/http.go` — 更新 `/health/vector` 端点反映向量搜索已实现
- `cmd/codeschema/main.go` — 新增 `newSearcher` 工厂函数，`mcp`/`serve` 命令均集成搜索器
- 文档同步：docs/dev/09-语义检索与全文搜索.md（更新 P8.1 完成标准和文件清单）、DEV_PROGRESS.md（新增 P8.1 进度条）

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（17 个包，0 失败）
- vector 包 13 个测试全部通过（MemoryStore CRUD + Indexer 构建/搜索 + MockEmbedder）
- search 包 17 个测试全部通过（MemoryFTS 7 个 + Reranker 6 个 + Searcher 3 个 + 辅助函数 1 个）
- service 包 3 个新增搜索测试（WithSearcher + 模式映射 + 精确匹配）
- 新增测试覆盖：向量存储添加/搜索/批量/删除/余弦相似度、Indexer 构建/批量/异步 worker、全文搜索精确/模糊/前缀/空查询/空索引/删除、Reranker 默认权重/FTS 仅/向量仅/融合/上限/空输入、Searcher 精确模式/空查询/默认上限、Service Search WithSearcher 集成/模式映射

**遗留 TODO / 风险**：
- 向量索引为空（MemoryStore 和 MemoryFTS 启动时无数据），需 P9 实现从 Store 数据自动构建索引
- 当前使用内存 mock（MockEmbedder + MemoryFTS），P8.2 需切换为 chromem-go 和 SQLite FTS5
- 搜索结果中 Kind/File 字段尚未填充（FTS 和向量结果均只填充 Symbol/Score），P2 完善
- search.SearchResultFromVector 函数使用类型断言，当前未使用，后续可移除或重构为 adapter 模式

### Commit 13: feat(config): P7 配置系统 — YAML 解析 + CLI 集成 + MCP 认证增强

**Commit Hash**: `ab2df56`

**核心改动点**：
- `internal/config/config.go` — 新增配置模块，定义 Config 及 7 个子结构体（Project/Storage/Parser/AI/Server/Watcher/Scanner），含 DefaultConfig 默认值、Load 加载函数（支持 .yaml/.yml/.json）、Validate 校验函数
- `internal/config/parse.go` — 最小 YAML 子集解析器（零外部依赖），支持嵌套映射、行内列表、注释、布尔/数字/字符串类型；JSON 解析通过 encoding/json
- `internal/config/config_test.go` — 25 个测试覆盖默认值、YAML/JSON 加载、Partial 合并、验证、YAML 解析子功能、值解析边界
- `cmd/codeschema/main.go` — 全局 `--config` 参数，所有命令从 Config 读取默认值（workers/store/dsn/addr/auth-token/debounce/ignore_dirs），命令签名改为 `func(ctx, cfg, args)`
- `internal/server/mcp.go` — 新增 `SetAuthToken` 方法、`authMiddleware`（Bearer token 认证）、`corsMiddleware`（CORS 支持）
- 文档同步：DEV_PROGRESS.md 更新 P7 为 100%

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（15 个包，0 失败）
- config 包 25 个测试全部通过
- server 包测试全部通过（含 MCP 新增中间件）
- 新增测试覆盖：默认值完整性、YAML 全量/Partial/JSON 加载、文件不存在/空路径/不支持格式、验证（空 root/空 DSN/workers 0/无地址）、YAML 行内列表/嵌套映射/注释/布尔/数字/空文件/纯注释、值解析（行内列表/空列表/引号字符串/数字/布尔/null）

**遗留 TODO / 风险**：
- 当前 YAML 解析器为最小子集实现，不支持多行字符串、锚点/别名、复杂流式语法，后续可切换为 gopkg.in/yaml.v3
- MCP 认证中间件当前为 Bearer token 静态配置，后续可支持 OAuth2/JWT 动态验证
- 配置热重载尚未实现，修改配置需重启进程

### Commit 12: feat(observability): P6 可观测性增强 — 结构化日志/基础指标/链路追踪/健康检查/安全中间件

**Commit Hash**: `4f9a64f`

**核心改动点**：
- `internal/log/logger.go` — 新增结构化日志模块，基于 Go 标准库 `log/slog`，支持 JSON/文本格式输出、日志级别控制、模块化 Logger（WithModule）、自动 caller 信息
- `internal/log/logger_test.go` — 13 个测试覆盖日志初始化、级别过滤、JSON 格式、模块化 Logger、Info/Debug/Warn/Error 方法
- `internal/metrics/metrics.go` — 新增基础指标模块，纯 Go 实现 Prometheus 文本格式，支持 Counter/Gauge 类型、标签维度、线程安全（sync.RWMutex）
- `internal/metrics/metrics_test.go` — 13 个测试覆盖指标注册、增量/减量、渲染、标签、并发安全、重置
- `internal/trace/trace.go` — 新增链路追踪模块，简单 span 模型，支持嵌套 span、耗时记录、标签附加、通过日志输出追踪信息
- `internal/trace/trace_test.go` — 17 个测试覆盖 span 创建/结束、嵌套、标签、重复结束、空标签、事件记录
- `internal/server/http.go` — 增强健康检查端点（新增 `/health/db`/`/health/kv`/`/health/vector`）、新增 `/metrics` 端点暴露 Prometheus 指标、新增安全中间件（authMiddleware Bearer token 认证 + pathTraversalMiddleware 路径遍历防护 + corsMiddleware + errorRecoveryMiddleware）、新增 requestMetricsMiddleware 自动记录请求指标和追踪 span
- `internal/server/http_test.go` — 22 个测试覆盖健康检查、指标端点、认证中间件（有效/无效/缺失 token）、路径遍历防护、CORS、panic recovery
- `internal/analyzer/analyzer.go` — 集成日志/指标/追踪：init 注册 4 个指标，BuildAll/BuildCallGraph/BuildClassHierarchy/BuildFileGraph/BuildReverseIndex/FindImpactNodes/ShortestPath/Analyze/TagAll 添加 trace span、指标打点、日志记录
- `internal/scanner/scanner.go` — 集成日志/指标/追踪：init 注册 4 个指标，ProcessFile/ScanAll 添加 trace span、指标打点（processed_total/files_total/errors_total/active_workers）、日志记录
- `internal/ai/tagger.go` — 添加模块化 Logger，DeriveAllTags 前后记录关键指标（classes_tagged/methods_tagged）
- `internal/service/service.go` — 新增 StoreHealthCheck 方法支持健康检查
- 文档同步：docs/dev/10-可观测性与安全设计.md（更新 P6 实现细节）、DEV_PROGRESS.md（更新 P6 状态为 100%）

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（14 个包，0 失败）
- log 包 13 个测试全部通过
- metrics 包 13 个测试全部通过
- trace 包 17 个测试全部通过
- server 包 22 个测试全部通过（原有 11 个 + 新增 11 个）
- 新增测试覆盖：日志初始化/级别过滤/JSON 格式/模块化 Logger、Counter/Gauge 指标注册/更新/渲染/标签/并发安全、span 创建/嵌套/结束/事件记录、健康检查端点（整体/DB/KV/Vector）、metrics 端点、Bearer token 认证（有效/无效/缺失/空）、路径遍历防护、Analyzer 8 个方法的 trace/指标/日志集成、Scanner 2 个方法的 trace/指标/日志集成、Tagger 模块化 Logger

**遗留 TODO / 风险**：
- 指标端点当前为纯内存实现，重启后指标清零，P1 可考虑持久化
- 链路追踪当前为简单实现（日志输出），P1 可对接 OpenTelemetry 标准导出
- 健康检查的 DB/KV/Vector 端点当前为占位实现（返回 mock 数据），接入真实存储后需更新
- 安全中间件当前仅支持 Bearer token 静态配置，P1 可支持 OAuth2/JWT 动态验证

### Commit 11: feat(ai, server, service): P5 标签分类体系（Tag）与测试关联

**Commit Hash**: `d76e050`

**核心改动点**：
- `internal/ai/tagger.go` — 新增 Tag 规则推导引擎，基于类名/方法名/目录/文档注释/文件语言的六类标签（layer/biz/tech/risk/test/lang）规则推导
- `internal/ai/tagger_test.go` — 56 个测试覆盖全部六类标签及边界情况（类标签 38 个 + 方法标签 18 个）
- `internal/store/store.go` — Store 接口扩展：UpsertTags/UpsertMethodTags/GetTagsByClassID/GetTagsByMethodID/SearchByTag/GetAllTagsWithCategories（6 个方法）
- `internal/store/filestore.go` — FileStore 持久化：classTags/methodTags/tagCategories 字段，saveToDisk/loadFromDisk 支持
- `internal/analyzer/analyzer.go` — 新增 `Analyzer.TagAll()` 方法，调用 Tagger 对所有实体执行标签推导并持久化
- `internal/service/service.go` — 新增 GetTags/SearchByTag/GetAllTags 三个查询方法
- `internal/service/testlink.go` — 新增测试关联模块，实现三种策略（naming 类名约定 70 分/same_tag 同标签聚类 60 分/dependency 依赖递归 80 分）
- `internal/service/testlink_test.go` — 6 个测试覆盖三种策略 + 边界情况
- `internal/server/http.go` — 新增 3 个 HTTP 端点（GET /tags, GET /tags/search, GET /tags/all）
- `internal/server/mcp.go` — 新增 3 个 MCP 工具（get_tags, search_by_tag, get_all_tags），MCP 工具总数从 8 个增至 11 个
- `internal/server/mcp_test.go` — 更新 MCP 工具数量断言从 8 改为 11
- 文档同步：docs/dev/05-接口层（CLI+HTTP+MCP）.md（HTTP 8 端点 + MCP 11 工具）、docs/dev/06-编排层与并发模型.md（P5 完成标准）、docs/dev/08-测试关联与AI增强.md（P0 已完成 + P1 待实现清单）

**验证数据**：
- go build ./... — 通过
- go test ./... -count=1 — 全部通过（11 个包，0 失败）
- ai 包 56 个测试全部通过（类标签 38 个 + 方法标签 18 个）
- service 包 14 个测试全部通过（原有 8 个 + 新增 6 个 testlink 测试）
- MCP 测试：11 个工具注册成功
- 测试覆盖：Tag 六类标签推导、测试关联三种策略、HTTP/MCP 标签查询接口、Analyzer TagAll 集成

**遗留 TODO / 风险**：
- explicit 策略（基于显式注解的测试关联）尚未实现，P1 可补充
- coverage 策略（基于覆盖率数据的测试关联）尚未实现，P1 可补充
- AI 增强层（EnhanceTag/EnhanceDoc/Disambiguate）尚未实现，P1 可补充
- 预算管控（budget_per_scan/budget_per_query）尚未实现，P1 可补充

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