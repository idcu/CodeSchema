# 测试与 CI

> 写给：要跑测试、加测试、理解 CI 门禁的开发者

> 最后更新：2026-09-03
## 本地测试

```bash
make test          # 单元测试
make test-race     # 竞态检测（-race）
make test-cover    # 覆盖率
go test ./internal/... -run TestX -v   # 单测聚焦
```

> 慢测试用 `-short` 与 env gate 控制；CI bench / nightly 不受影响。

> PG/Redis 真实实例集成测试：`make test-pg-redis`（先 `docker compose --profile pg --profile redis up -d` 起真实服务）。

> **agent-bench 在容器内运行注意（遗留① 已定论）**：`TestRunMulti_RepoHintFilter` 等按 `RepoHint` 过滤任务的多仓库评测，取 `filepath.Base(仓库路径)` 与任务 `RepoHint`（如 `code-schema`）对齐；在 `docker/lsp-test` 等镜像内跑 Go 测试时，**仓库必须挂载到目录名与 RepoHint 一致的位置**（本仓应挂到 `/code-schema`，而非 `/src`），否则 `filepath.Base` 得 `src` 导致全部 `RepoHint` 任务被 Skipped、活跃任务数不符而失败。

## CI 门禁（`.github/workflows/ci.yml`）

| job | 作用 |
|---|---|
| `test` | 单元测试（OS 矩阵 ubuntu / macos / windows），核心合并门禁 |
| `race` | Race Detector 专项（`-race`） |
| `treesitter` | TreeSitter AST 变体（`-tags treesitter` CGO 真语法树） |
| `tag-guard` | `go list -tags 'onnx pg redis' ./...` 解析+类型检查（不链接运行时），防止 build-tag 变体编译断裂 |
| `pg-redis` | 真实 PG/Redis 实例集成（services 容器，`-tags 'pg redis'`），验证 PG 存储 + Redis 缓存真实验证 |
| `counts-guard` | `make counts-check`：实时计数 vs `scripts/counts_baseline.json`，任一漂移即失败（防「包数/工具数」文档漂移） |
| `agent-bench` | Agent 任务端到端评测（快照归一化 repo_path） |
| `bench` | Scale Benchmark 规模基准 |
| `nightly-scale` | Nightly Scale E2E（100k）夜间规模端到端 |
| `cross` | Cross-Compile 交叉编译 |
| `docker` | Docker Image 镜像构建 |

> 核心合并门禁：`test` / `tag-guard` / `counts-guard` / `agent-bench`；`race` / `treesitter` / `pg-redis`（需服务容器）为质量增强；`bench` / `nightly-scale` / `cross` / `docker` 为定期 / 辅助作业。

## 计数守护（防数字漂移）

- 权威来源：`scripts/project_counts.py`（go list + 源码正则重算：internal 包/总包/MCP 工具/HTTP 路由/非 vendor LoC）。
- 基线：`scripts/counts_baseline.json`。
- 工作流：改接口 → 跑 `make counts` 确认 → `make counts-update` 刷新基线 → 同次提交。
- **禁止手填计数到文档**：文档数字一律从 `make counts` 取。

## Lint / 规范

- `make lint` 静态检查；遵循 Go 惯例与项目命名/commit 规范（见 [04-贡献者](../04-贡献者/README.md)）。
