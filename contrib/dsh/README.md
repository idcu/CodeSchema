# CodeSchema × DeepSeek Harness 集成指南

## 1. 概述

- **CodeSchema** = 代码上下文供给层（代码理解 / 裁剪 / 检索）
- **DeepSeek Harness** = Agent 执行层（运行时 / 工具编排）
- 两者互补：CodeSchema 补 dsh 的「代码理解」盲区，dsh 扩 CodeSchema 的触达
- 对接后效果：dsh 在改代码前自动调 `context` / `impact` 获取精准代码上下文

## 2. 前置条件

- CodeSchema 已编译（`make build` 或 `go build -o codeschema ./cmd/codeschema`）
- Node.js >= 18（dsh 运行环境）
- dsh 已安装（`npm i -g @deepseek-ai/dsh`）

## 3. 方式一：stdio 直连（推荐，最稳定）

1. 启动 CodeSchema MCP Server（stdio 模式）：

   ```
   codeschema mcp --stdio --store /path/to/data
   ```

   > 注意：stdout 用于 MCP 协议帧，索引构建日志输出到 stderr。

2. 在 dsh 的 MCP 配置中添加 stdio 服务器。
   dsh 目前通过设置页面的 MCP 配置或直接编辑配置文件添加 MCP 服务器。

   配置示例（JSON 格式）：

   ```json
   {
     "codeschema": {
       "command": "codeschema",
       "args": ["mcp", "--stdio", "--store", "/path/to/data"]
     }
   }
   ```

3. 重启 dsh 会话，CodeSchema 的 12 个工具自动可用。
4. 验证：在 dsh 中提问"查找 OrderService 的上下文"或"分析 GetUser 的影响面"。

## 4. 方式二：SSE 远程模式（适合生产部署）

1. 启动 CodeSchema MCP Server（SSE 模式）：

   ```
   codeschema mcp --addr :8080
   ```

   生产加 `--auth-token`：

   ```
   codeschema mcp --addr :8080 --auth-token <token>
   ```

2. 确认服务就绪：`curl -s http://localhost:8080/sse`
3. 在 dsh 的 MCP 配置中添加 SSE 服务器：

   ```json
   {
     "codeschema": {
       "url": "http://localhost:8080/sse"
     }
   }
   ```

   启用认证时额外配置 Bearer token。

## 5. 可用工具清单

| 工具 | 作用 | 典型调用场景 |
|---|---|---|
| context | 获取符号上下文（源码+裁剪） | 改代码前理解目标符号 |
| impact | 影响面分析（callers/callees） | 修改前评估爆炸半径 |
| tests | 查找关联单测 | 改完代码快速定位测试 |
| affected | 变更影响文件 | 提交前检查影响范围 |
| get_call_graph | 调用图查询 | 理解调用链路 |
| search_config | 配置项检索 | 排查配置问题 |
| find_dependencies | 依赖分析 | 重构前评估依赖 |
| search_symbols | 符号搜索 | 快速定位代码位置 |
| get_tags | 获取符号标签 | 理解代码分类 |
| search_by_tag | 按标签搜索 | 按领域/层级筛选 |
| get_all_tags | 全量标签列表 | 了解标签体系 |
| list_projects | 多租户项目列表 | 多仓库场景路由 |

> 多租户配置下，前 11 个检索类工具额外接受 `project` 参数指定目标仓库；省略时路由默认租户（`"default"`）。`list_projects` 返回当前实例所有租户元信息。

## 6. 推荐工作流

- 在 dsh 中编辑代码前，先调 `context` 获取目标符号上下文
- 修改可能影响其他模块时，调 `impact` 评估爆炸半径
- 修改完成后，调 `tests` 获取关联单测
- 提交流前，调 `affected` 确认变更影响范围

## 7. 能力层预设（可选）

CodeSchema 支持三种 preset（`minimal` / `semantic` / `multitenant`），通过 config.yaml 的 `preset` 字段配置。示例如下（YAML 格式）：

```yaml
# 最小资源档（仅 FTS 检索，关语义/向量/ONNX，关 AI 增强）
preset: minimal
```

- `minimal`：最小资源档——仅 FTS 检索，关语义/向量/ONNX，关 AI 增强
- `semantic`：语义档——开启 FTS + 语义检索（向量/ONNX），保持 AI 增强
- `multitenant`：多租户档——保持全能力，监听默认开启，租户在 `tenants` 中声明

## 8. 方式三：Code mode 程序化编排（context-sdk）

dsh **Code mode** 让模型写 TypeScript 程序组合多轮工具调用。配合 CodeSchema 的
**context-sdk**（`contrib/contextsdk`，独立发布 `github.com/idcu/codeschema-contextsdk`），
可在单次程序化调用里组合「多租户 × 多符号 × 影响面 × 关联单测」的上下文包，
避免多轮往返：

```typescript
// dsh Code mode 脚本示例：一次编排拿到上下文包
import { Client, Request } from "codeschema-contextsdk";

const client = new Client(async (tenant) => {
  // 返回实现 SDKProvider 的后端（codeschema 服务端 / 任何第三方实现）
  return getCodeschemaProvider(tenant); // 桥接内部 Service → SDKProvider
});

const pkg = await client.Compose({
  tenant: "repo-a",
  symbols: ["com.example.OrderService.getUser"],
  withImpact: true,
  withTests: true,
  mode: "minimal", // 或 "full"：注入源码原文
});
console.log(`token 估算: ${pkg.summary.totalTokens}`);
// → 打印每个符号的源码/元数据 + 影响面 + 关联单测
```

- `mode: "minimal"` 仅符号元数据（零文件 IO，token 约为 full 的 1/20）；
- `withImpact` / `withTests` 聚合影响面与关联单测，一次注入；
- 发布验证：`bash scripts/check-contextsdk-publish.sh`（独立 module 编译+测试通过）。

## 9. 故障排查

- 连接失败：检查 CodeSchema 进程是否正常运行
- 工具调用超时：大仓库首次索引需时间，等待索引完成后再调用
- 认证拒绝：确认 auth-token 已正确配置
- 日志查看：CodeSchema 进程的 stderr 输出包含索引构建和请求日志

## 10. 参考链接

- [CodeSchema README](../../README.md)
- [CodeSchema MCP 接入指南](../../docs/00-新人上手/README.md)
- [DeepSeek Harness 官方](https://deepseek.com/harness)
- [DSH 社区插件](https://github.com/topics/dsh-plugin)
