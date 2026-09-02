# 贡献者指南

> 写给：要提 PR / 修 bug / 加特性的贡献者（含 AI 编程工具）

## 0. 先读

- `AGENTS.md`（项目接入指南，AI 工具必读）
- `docs/AI协作规范.md`（读档顺序 / 任务领取 / 编码约束 / 提交规范 全流程）
- `README.md` 的「开发前必读」6 条红线

## 1. 提交规范（Conventional Commits）

- 类型：`feat / fix / docs / style / refactor / perf / test / chore`
- 示例：`feat(user): 新增批量导出`；`fix(parser): 回填 CallerFQN`
- Body 携带关键验证数据（如 `heapUsed +2.20MB / P95 <50ms`、`go build exit 0`）。
- **禁止水 commit**（`fix bug` / `update`）。

## 2. 改码必改档（零超前于代码）

代码变更**必须**同步受影响文档，同次提交；映射见 `docs/AI协作规范.md §4`。提交前逐项过清单：

1. 列出本次改动影响的文档（含 `docs/README.md` 地图若有新增）。
2. grep 核查旧路径/旧数字/旧版本号残留。
3. 新增/删除文档或目录 → 同步 `docs/README.md` 导航。
4. 被改文档页首「最后更新」刷新。
5. 与代码**同次提交**，Body 带验证数据。

## 3. 计数类字段禁手填

所有「包数 / MCP 工具数 / HTTP 路由数 / LoC」以 `scripts/project_counts.py` 为准：

```bash
make counts          # 打印
make counts-update  # 刷新 scripts/counts_baseline.json
make counts-check   # CI 断言漂移
```

改了接口（新增工具/路由/包）后必须 `make counts-update` 并同次提交，否则 `counts-guard` 失败。

## 4. CI 门禁

`test`（单元+竞态）、`tag-guard`（`-tags 'onnx pg redis'` 解析+类型检查）、`counts-guard`（数字漂移）、`agent-bench`（端到端）。本地先跑 `make test && make verify-tags && make counts-check`。

## 5. 破坏性变更

改接口签名 / 表结构 / 配置 / CLI 行为 → 先标注影响范围，等待评审，再动手。

## 6. 现实核查（reality-check）

文档不得超前于代码：所有数字、路径、tag 以 `go list` / `git tag` / 实际源码为准。本项目历史出现过「包数 27/31/32/36 四处不一致」「docs outrun code」等问题，已用脚本化计数杜绝。
