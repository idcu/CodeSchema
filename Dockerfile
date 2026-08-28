# CodeSchema Dockerfile
# 多阶段构建：build → runtime
#
# 构建镜像：
#   docker build -t codeschema:latest .
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
# ONNX 语义检索（可选）：默认构建为纯 Go 免 CGO（与 make build 一致）；
# 需要 ONNX 时构建时传 --build-arg CGO_ENABLED=1 并确保 down/onnxruntime/libonnxruntime.so
# 存在（构建阶段会把 .so 复制进镜像 /app/down/onnxruntime/）。

# === 构建阶段 ===
FROM golang:1.25-alpine AS builder

# 默认纯 Go 免 CGO；ONNX 场景传 --build-arg CGO_ENABLED=1（需 gcc musl-dev）
ARG CGO_ENABLED=0
# Go 模块代理：国内网络默认走 goproxy.cn（避免 proxy.golang.org 超时）；
# 其他环境可 --build-arg GOPROXY=https://proxy.golang.org,direct 覆盖
ARG GOPROXY=https://goproxy.cn,direct
RUN if [ "$CGO_ENABLED" = "1" ]; then apk add --no-cache gcc musl-dev; fi

WORKDIR /src

# 先复制 go.mod/go.sum 和本地 replace 依赖（down/ 的 chromem-go、third_party/ 的
# onnxruntime_go_patch），确保 go mod download 能解析替换路径
COPY go.mod go.sum ./
COPY down/ ./down/
COPY third_party/ ./third_party/
ENV GOPROXY=${GOPROXY}
# idcu-go 公共模块（trim / ttlcache / pathsafe）：go.mod 的 replace 指向仓库外的 ../idcu-go/*，
# 而 Docker 构建上下文拿不到上级目录，因此在镜像内去掉 replace，改按已发布的 v0.1.0 tag 拉取
#（go.sum 已含三模块的校验和；gitee.com/idcu-go/* 未进公共 sumdb，必须设 GOPRIVATE 跳过校验）。
RUN go mod edit -dropreplace=gitee.com/idcu-go/trim \
    -dropreplace=gitee.com/idcu-go/ttlcache \
    -dropreplace=gitee.com/idcu-go/pathsafe
ENV GOPRIVATE=gitee.com/idcu-go/*
RUN go mod download

# 复制全部源码并构建
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=${CGO_ENABLED} go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /build/codeschema \
    ./cmd/codeschema

# === 运行阶段 ===
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /build/codeschema .

# 健康检查
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/app/codeschema", "version"]

EXPOSE 8080 8081

ENTRYPOINT ["/app/codeschema"]
CMD ["serve", "--http", ":8081"]