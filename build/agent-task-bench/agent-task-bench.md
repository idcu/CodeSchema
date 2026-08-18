## Agent 任务端到端评测报告

- 仓库：`code-schema`（/Volumes/Data/code-schema，文件数 353）
- 任务数：5
- 时间：2026-08-18T10:35:20Z

### 汇总

| 指标 | none（无上下文） | full（完整源码） | minimal（符号元数据） |
|---|---|---|---|
| 通过率 | 0.0% | 100.0% | 100.0% |
| 平均 token | 0 | 301 | 4 |
| 相对 none 增益 | — | +100.0pp | +100.0pp |
| **minimal vs full token 节省** | — | — | **98.7%** |

### 分任务明细

| 任务 | 类型 | 状态 | none | full (token) | minimal (token) |
|---|---|---|---|---|---|
| code-schema-bug-001 | bugfix | active | false | true (84) | true (4) |
| code-schema-bug-002 | bugfix | active | false | true (84) | true (4) |
| code-schema-feat-001 | feature | active | false | true (84) | true (4) |
| code-schema-refactor-001 | refactor | active | false | true (84) | true (4) |
| generic-bug-001 | bugfix | **skipped**（符号不适用） | — | — | — |
| generic-bug-002 | bugfix | **skipped**（符号不适用） | — | — | — |
| generic-feat-001 | feature | active | false | true (1168) | true (4) |
| generic-refactor-001 | refactor | **skipped**（符号不适用） | — | — | — |

> 判定口径：上下文中命中全部「必需符号」与「关键信息」即视为该档位任务可完成。
> full 注入完整源码原文（context_lines 裁剪）；minimal 仅符号元数据、零文件 IO。
> skipped：任务符号不适用于当前仓库（按 RepoHint 或符号预检），不计入通过率分母。
