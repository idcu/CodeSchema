## Agent 任务端到端评测报告

- 仓库：`.`（.，文件数 353）
- 任务数：5
- 时间：2026-08-17T12:59:22Z

### 汇总

| 指标 | none（无上下文） | full（完整源码） | minimal（符号元数据） |
|---|---|---|---|
| 通过率 | 0.0% | 100.0% | 100.0% |
| 平均 token | 0 | 84 | 4 |
| 相对 none 增益 | — | +100.0pp | +100.0pp |
| **minimal vs full token 节省** | — | — | **95.2%** |

### 分任务明细

| 任务 | 类型 | none | full (token) | minimal (token) |
|---|---|---|---|---|
| code-schema-bug-001 | bugfix | false | true (84) | true (4) |
| code-schema-bug-002 | bugfix | false | true (84) | true (4) |
| code-schema-feat-001 | feature | false | true (84) | true (4) |
| code-schema-refactor-001 | refactor | false | true (84) | true (4) |
| generic-feat-001 | feature | false | true (84) | true (4) |

> 判定口径：上下文中命中全部「必需符号」与「关键信息」即视为该档位任务可完成。
> full 注入完整源码原文（context_lines 裁剪）；minimal 仅符号元数据、零文件 IO。
