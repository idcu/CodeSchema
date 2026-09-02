# 测试与 CI

> 写给：要跑测试、加测试、理解 CI 门禁的开发者

> 最后更新：2026-09-02
## 本地测试

```bash
make test          # 单元测试
make test-race     # 竞态检测（-race）
make test-cover    # 覆盖率
go test ./internal/... -run TestX -v   # 单测聚焦
```

> 慢测试用 `-short` 与 env gate 控制；CI bench / nightly 不受影响。

## CI 门禁（`.github/workflows/ci.yml`）

| job | 作用 |
|---|---|
| `test` | 单元测试 + 竞态 |
| `tag-guard` | `go list -tags 'onnx pg redis' ./...` 解析+类型检查（不链接运行时），防止 build-tag 变体编译断裂 |
| `counts-guard` | `make counts-check`：实时计数 vs `scripts/counts_baseline.json`，任一漂移即失败（防「包数/工具数」文档漂移） |
| `agent-bench` | Agent 任务端到端评测（快照归一化 repo_path） |

## 计数守护（防数字漂移）

- 权威来源：`scripts/project_counts.py`（go list + 源码正则重算：internal 包/总包/MCP 工具/HTTP 路由/非 vendor LoC）。
- 基线：`scripts/counts_baseline.json`。
- 工作流：改接口 → 跑 `make counts` 确认 → `make counts-update` 刷新基线 → 同次提交。
- **禁止手填计数到文档**：文档数字一律从 `make counts` 取。

## Lint / 规范

- `make lint` 静态检查；遵循 Go 惯例与项目命名/commit 规范（见 [04-贡献者](../04-贡献者/README.md)）。
