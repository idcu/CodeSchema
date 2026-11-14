# 公开基准（S-2）· 2026-11-14

> 最后更新：2026-11-14

首轮公开基准：在 3 个真实仓库上测「测试关联 P/R/F1」与「调用图结构指标」。
**只报告、不设 CI 阈值**（roadmap R5-4）；原始数据见同目录 `*.json`。

## 口径

- **真值独立于被测启发式**：以 IR 中「测试方法 → 生产方法」的**直接调用边**作为真值，
  不使用命名约定构造真值（否则等于用命名约定评估命名约定）。
- **确定性**：文件顺序固定排序、pair 排序固定、等距 stride 采样；同输入两次运行结果一致
  （`TestRun_MultiLanguageFixtures` 断言除时间戳外 DeepEqual）。
- **指标**：对真值中的每个生产方法调用 `FindTestLinks`，把返回的 `TestMethod` 与真值集合比对，
  微平均 TP/FP/FN 与 Precision/Recall/F1；`PredictedLinks=0` 表示策略链**完全无输出**
  （区别于「有输出但 FQN 口径不一致」）。
- **调用图**：`ResolutionRate = 能落到方法索引的 callee 边 / 全部调用边`。
- **负控**：`TestAffectedNegativeControl_DeleteTest`——删除测试文件前后 `GetAffected` 必须
  从「含该测试」变为「不含」（通过）。

## 环境

| 仓库 | 语言 | commit | 构建变体 |
|---|---|---|---|
| `code-schema`（本仓） | go | `3887a42` | 默认 `regex` / `-tags treesitter` |
| `spring-petclinic` | java | `500158f` | 默认 `regex` / `-tags treesitter` |
| `lytd` | ts | `f5e6604` | 默认 `regex` / `-tags treesitter` |

运行命令（两个变体各执行一次）：

```bash
go build -o /tmp/cs-regex ./cmd/codeschema
go build -tags treesitter -o /tmp/cs-ts ./cmd/codeschema

/tmp/cs-regex release-bench --repo <repo> --lang <lang> --out <dir>
/tmp/cs-ts    release-bench --repo <repo> --lang <lang> --out <dir>
```

## 结果

### 测试关联（`FindTestLinks`，min-confidence=60）

| 仓库 | 变体 | 测试方法 | 真值 pair（采样） | 评估生产方法 | 预测链接 | TP | FP | FN | P | R | F1 |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| code-schema | regex | 1120 | 1720（192） | 145 | 0 | 0 | 0 | 192 | 0 | 0 | 0 |
| code-schema | treesitter | 1085 | 1844（185） | 140 | 0 | 0 | 0 | 185 | 0 | 0 | 0 |
| petclinic | regex | 29 | 45（45） | 19 | 0 | 0 | 0 | 45 | 0 | 0 | 0 |
| petclinic | treesitter | 104 | 160（160） | 37 | 0 | 0 | 0 | 160 | 0 | 0 | 0 |
| lytd | regex | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| lytd | treesitter | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |

### 调用图

| 仓库 | 变体 | calls | callers | callees | resolved | resolution |
|---|---|---:|---:|---:|---:|---:|
| code-schema | regex | 16376 | 2049 | 2755 | 3481 | 0.2126 |
| code-schema | treesitter | 18593 | 2062 | 2756 | 3800 | 0.2044 |
| petclinic | regex | 1451 | 78 | 578 | 179 | 0.1234 |
| petclinic | treesitter | 1609 | 147 | 525 | 288 | 0.1790 |
| lytd | regex | 848 | 41 | 527 | 36 | 0.0425 |
| lytd | treesitter | 407 | 33 | 128 | 58 | 0.1425 |

## 结论与根因（首轮暴露的真实缺口，未在本次修复）

1. **`FindTestLinks` 在三个真实仓库上零输出（`PredictedLinks=0`）。** 策略链本身能工作
   （单测夹具 `CalculatorTest.TestAdd` 场景 TP≥1），但真实仓库的测试组织方式全部落在发现规则之外：
   - **Go：包级测试函数不可见。** `discoverTestClasses` 只按**类名**约定（`*Test` / `Test*`）
     识别测试容器；Go 测试函数挂在合成 `PACKAGE` 容器下（容器名 = 包名），名字不以 `Test` 结尾，
     因此本仓 100 个 `_test.go` 中的测试函数一个都不会进入候选集。`GetAffected` 复用同一策略，
     同样盲区（负控夹具用 `CalculatorTest` 结构体，所以能通过）。
   - **Java：`*Tests` 后缀未剥离。** petclinic 的测试类统一命名 `OwnerControllerTests`，
     `matchTestClass` 只处理 `Test` 前后缀（`isTestClass` 已支持 `Tests`，两处口径不一致）。
   - **TypeScript：Jest/Vitest 的 `it/test` 回调不是 IR 方法。** 两个变体都解析出 0 个测试方法，
     真值因此为 0；调用图分辨率也最低（0.04–0.14）。
2. **调用图分辨率整体偏低（0.04–0.21）。** 正则路径对非 Go 语言只做裸名/包名限定，跨类 receiver
   无法消歧；AST 路径提升 Java 分辨率（0.1234 → 0.1790）但 Go 略降（0.2126 → 0.2044），
   因为 AST 产出更多真实边，分母变大。
3. **正则 vs AST 的规模差异**：petclinic 测试方法 29 → 104（正则把部分测试方法体误判/漏判）、
   lytd 生产方法 301 → 66（正则虚增）。Java 侧 AST 明显更接近真实结构。

以上 1 的修复属于新的独立条目（测试关联发现规则），不在 S-2 范围内；本轮先固化基线，
后续修复后可直接用同一命令重跑对比，不需要改基准代码。
