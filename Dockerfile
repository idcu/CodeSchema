# CodeSchema Dockerfile（默认构建口径：纯 Go 免 CGO 轻量镜像）
# 多阶段构建：build → runtime
#
# 构建口径决策（2026-09-03，消除三份镜像职责重叠）：
#   - 本 root Dockerfile = 默认轻量镜像（纯 Go 免 CGO，语义检索降级 LocalEmbedder）：
#     `docker build -t codeschema:latest .`
#     与项目默认构建 `make build`/CI 同口径：零 CGO、镜像最小、构建最快，结构化 / FTS / 影响面分析不受影响。
#   - ONNX 语义检索变体 = 独立入口 `docker/onnx/Dockerfile`（产物 tags codeschema:onnx，需 CGO + onnxruntime）：
#     `docker build -f docker/onnx/Dockerfile -t codeschema:onnx .`（详见 docs/03-部署运维/codeschema-onnx镜像生成.md）
#   - 国内网络拉取 docker.io 过慢时，用 Dockerfile.mirror（已预置 daocloud 国内源）构建同一默认镜像：
#     `docker build -f Dockerfile.mirror -t codeschema:latest .`
#
# 运行容器（HTTP API）：
#   docker run --rm -p 8081:8081 -v codeschema-data:/app/data codeschema:latest
#
# 运行容器（MCP Server）：
#   docker run --rm -p 8080:8080 -v codeschema-data:/app/data codeschema:latest mcp --addr :8080
#
# 运行容器（扫描指定目录）：
#   docker run --rm -v /host/path:/repo codeschema:latest scan /repo
#
# 依赖解析策略：本镜像使用 `go mod vendor`（vendor/ 由仓库/构建上下文提供），而非从
# gitee.com/idcu-go/* 拉取发布版。原因：code-schema 的 go.mod 通过本地 replace 指向
# ../idcu-go/*（开发期直接编本地代码），其中 recovery / trace 等模块并未发布到 gitee，
# 去掉 replace 后无法解析；vendor 把全部本地 replace 固化进镜像，构建离线且代码与本地一致。
#
# ONNX 语义检索所需的 glibc 对齐（libonnxruntime.so 依赖 libstdc++.so.6 / libgomp）说明，
# 已归整到 docker/onnx/Dockerfile；本默认镜像不包含 down/onnxruntime 与 ONNX 模型。

# === 构建阶段 ===
FROM golang:1.25-bookworm AS builder

# 默认纯 Go 免 CGO（零依赖、镜像最小，与 `make build`/CI 同口径）。
# 若确需在默认镜像上启用 CGO（不引入 ONNX，仅 CGO 编译），可传 --build-arg CGO_ENABLED=1。
ARG CGO_ENABLED=0
# Go 模块代理：国内网络默认走 goproxy.cn；其他环境可覆盖。
# 注意：本镜像使用 go mod vendor（vendor/ 随构建上下文提供），构建全程离线，
# 不依赖 gitee.com/idcu-go/* 的发布状态。
ARG GOPROXY=https://goproxy.cn,direct
# 纯 Go 构建无需 gcc/glibc 头文件；仅保留 ca-certificates/tzdata（时区与证书，运行期可复用）。
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# 复制全部源码（含 vendor/）。vendor/ 由 `go mod vendor` 生成，已把 go.mod 中的
# 本地 replace（../idcu-go/*、down/chromem-go、third_party/onnxruntime_go_patch）全部固化，
# 因此构建无需访问 gitee 或上级目录，且与本地开发代码一致。
COPY . .

ARG VERSION=dev
# 默认构建：纯 Go 免 CGO、无 -tags onnx（ONNX 语义检索走 docker/onnx/Dockerfile 独立入口）。
ENV GOTOOLCHAIN=local
RUN CGO_ENABLED=${CGO_ENABLED} go build -mod=vendor \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /build/codeschema \
    ./cmd/codeschema

# === 运行阶段 ===
FROM debian:bookworm-slim

# 纯 Go 运行期零 CGO 依赖；仅安装 ca-certificates / tzdata（网络与时间）。
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /build/codeschema .

# 健康检查
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/app/codeschema", "version"]

EXPOSE 8080 8081

ENTRYPOINT ["/app/codeschema"]
CMD ["serve", "--http", ":8081"]
