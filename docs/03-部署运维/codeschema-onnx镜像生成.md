# codeschema:onnx 镜像生成

> 写给：需要构建带 ONNX 语义检索能力镜像的部署/SRE
> 入口 Dockerfile：[docker/onnx/Dockerfile](../../docker/onnx/Dockerfile)

这是一个**独立 ONNX 变体**镜像，启用 `-tags onnx`（CGO + onnxruntime 动态库），内置
bge 嵌入模型，语义检索开箱即用。相比根目录 [Dockerfile](../../Dockerfile)（免 CGO 纯 Go、
语义检索默认降级 LocalEmbedder），本镜像只生成 `codeschema:onnx` 一个 tag。

## 1. 前置依赖（⚠️ 不在 git 内，必须先准备）

以下两个目录被 [.gitignore](../../.gitignore) 排除、**未进入版本库**，只存在于本机工作区：

| 路径 | 内容 | 缺失后果 |
|---|---|---|
| `down/models/bge-small-zh-v1.5/` | 嵌入模型：`onnx/model_fp16.onnx(+_data)` + `tokenizer.json` | 启动无模型，降级 LocalEmbedder |
| `down/onnxruntime/libonnxruntime.so` | ONNX Runtime 动态库（Linux x64，glibc ABI） | 运行时加载不到 .so，降级 LocalEmbedder |

> 模型若配置了 `storage.vector.model_download_url` 可在启动时自动回填下载，但
> **onnxruntime 动态库必须手动放置**，镜像构建期与运行期都依赖它。

缺失任一都不会构建失败（编译期 -tags onnx 只依赖 .so 链接），但**运行时会静默降级**，
日志出现 `ONNX embedder not loaded ... → using LocalEmbedder`。

## 2. 构建

构建上下文必须是仓库根目录（需要 `vendor/`、`cmd/`、`third_party/`、`down/`）：

```bash
# 在仓库根目录执行
docker build -f docker/onnx/Dockerfile -t codeschema:onnx .
```

- 基础镜像 must 是 glibc（bookworm）：

| 阶段 | 镜像 | 说明 |
|---|---|---|
| build | `golang:1.25-bookworm` | 自带 gcc，CGO 链接 onnxruntime .so |
| runtime | `debian:bookworm-slim` | 已装 `libstdc++6 libgomp1`（onnx CPU 推理依赖） |

- 不用 alpine/musl：musl 无法加载 glibc ABI 的 `libonnxruntime.so`，会强制降级。
- `CGO_LDFLAGS` 里 `-rpath,/app/down/onnxruntime` 指向运行期库目录，容器内无需设
  `LD_LIBRARY_PATH`。
- 版本号写入：`docker build --build-arg VERSION=1.2.0 -f docker/onnx/Dockerfile -t codeschema:onnx:1.2.0 .`

国内拉取基础镜像慢时，可用根目录 [Dockerfile.mirror](../../Dockerfile.mirror)（预置 daocloud
源，同款 onnx 构建）。

## 3. 运行

```bash
# HTTP API（数据卷持久化；注意生产应绑定 127.0.0.1 或经反代）
docker run --rm -p 8081:8081 -v ./data:/app/data codeschema:onnx

# MCP Server
docker run --rm -p 8080:8080 -v ./data:/app/data codeschema:onnx mcp --addr :8080

# 扫描指定目录
docker run --rm -v /host/path:/repo codeschema:onnx scan /repo
```

## 4. 验证 ONNX 是否真正生效

启动后看**第一条 semantic 日志**：

- ✅ `semantic: ONNX embedder active (bge-small-zh-v1.5, dim=512)` → ONNX 原生加载
- ❌ `semantic: ONNX embedder not loaded ... → using LocalEmbedder` → 已降级

降级排查优先级：① `-tags onnx` 是否编译（本镜像已含）→ ② `down/models/` 模型是否齐全
→ ③ `down/onnxruntime/libonnxruntime.so` 是否存在 → ④ 基础镜像是否 glibc。

## 5. 常见排障

| 现象 | 原因 | 处理 |
|---|---|---|
| `docker build` 报 `403 Forbidden` / `DeadlineExceeded`（镜像源限流） | docker.io 或免费加速器限流 | 换可用镜像源（如 `docker.m.daocloud.io`），或使用 [Dockerfile.mirror](../../Dockerfile.mirror) |
| 容器启动后日志 `ONNX embedder not loaded ... using LocalEmbedder` | 模型或 .so 缺失 / 非 glibc | 补足 [§1](#1-前置依赖警告不在-git-内必须先准备) 的两个目录 |
| `docker build` 报找不到 `../idcu-go/*` | 未在**仓库根目录**构建 | 改在根目录执行，或确认 vendor/ 存在 |
| 加载 .so 报 `undefined symbol` / ABI 错 | 基础镜像为 musl 或 libonnxruntime 版本不匹配 | 换成 bookworm(glibc)，重建 softlink SONAME |

## 6. 关联文件

- 构建入口：[docker/onnx/Dockerfile](../../docker/onnx/Dockerfile)
- 兜底国内源：[Dockerfile.mirror](../../Dockerfile.mirror)
- ONNX Embedder：[internal/vector/embedder_onnx.go](../../internal/vector/embedder_onnx.go)
- 忽略规则：[.gitignore](../../.gitignore)（`down/models/`、`*.so`）