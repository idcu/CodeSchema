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

# === 构建阶段 ===
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /src

# 先复制 go.mod/go.sum 和本地 replace 依赖，确保 go mod download 能解析替换路径
COPY go.mod go.sum ./
COPY down/ ./down/
RUN go mod download

# 复制全部源码并构建
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=1 go build \
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