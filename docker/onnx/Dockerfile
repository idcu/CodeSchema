# CodeSchema —— codeschema:onnx 专用镜像
#
# 本 Dockerfile 是 ONNX 语义检索变体的独立构建入口，产物镜像 tag 为 codeschema:onnx。
# 与根目录 Dockerfile 相同定位，但作为 onnx 变体的唯一入口（不含免 CGO 分支）。
#
# 构建（构建上下文必须是仓库根目录，因为需要 vendor/、cmd/、third_party/、down/）：
#   docker build -f docker/onnx/Dockerfile -t codeschema:onnx .
#
# 构建链路与根目录 Dockerfile 完全同源（仅精简免 CGO 分支），已在 codeschema:onnx
# 镜像上实测运行期日志 `semantic: ONNX embedder active (bge-small-zh-v1.5, dim=512)`。
#
# ⚠️ 前置依赖（不在 git 内，构建前必须准备，详见 docs/03-部署运维/codeschema-onnx镜像生成.md）：
#   - down/models/bge-small-zh-v1.5/     bge 嵌入模型（onnx/model_fp16.onnx + tokenizer.json）
#   - down/onnxruntime/libonnxruntime.so ONNX Runtime 动态库（Linux x64）
#     这两个目录被 .gitignore 排除，仅存在于本机工作区；缺任一则 ONNX 会在运行时
#     静默降级为 LocalEmbedder（日志可见 `ONNX embedder not loaded`）。
#
# 基础镜像必须基于 glibc（bookworm），不能用 alpine/musl：onnxruntime 预编译的
# libonnxruntime.so 依赖 libstdc++.so.6 / libgomp 等 glibc 运行时，musl 无法加载该 .so。
# 构建期（golang:1.25-bookworm）与运行期（debian:bookworm-slim）均用 glibc，CGO 链接的
# 二进制与 onnxruntime .so 同为 glibc ABI，ONNX 原生生效。

# === 构建阶段 ===
FROM golang:1.25-bookworm AS builder

ARG CGO_ENABLED=1
ARG GOPROXY=https://goproxy.cn,direct
# CGO 编译需要 gcc + glibc 头文件，并与 onnxruntime .so 的 ABI 对齐。
RUN apt-get update && apt-get install -y --no-install-recommends \
      gcc libc6-dev ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# 复制全部源码：vendor/ 已把 go.mod 中本地 replace（../idcu-go/*、down/chromem-go、
# third_party/onnxruntime_go_patch）固化，构建全程离线；down/ 携带 onnx 模型与运行时。
COPY . .

# onnxruntime 动态库 SONAME 带版本号（Linux 为 libonnxruntime.so.1），实际文件名无版本号，
# 链接与运行时按 SONAME 解析，故建版本号软链（构建期与运行时共用）。
RUN ln -sf libonnxruntime.so /src/down/onnxruntime/libonnxruntime.so.1

ARG VERSION=dev
# -L 指向构建期库目录，-rpath 指向运行时库目录（/app/down/onnxruntime，由运行阶段 COPY down/
# 提供），运行时无需系统 PATH/LD_LIBRARY_PATH。
ENV CGO_LDFLAGS="-L/src/down/onnxruntime -Wl,-rpath,/app/down/onnxruntime -lonnxruntime"
ENV GOTOOLCHAIN=local
RUN CGO_ENABLED=${CGO_ENABLED} go build -mod=vendor \
    -tags onnx \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /build/codeschema \
    ./cmd/codeschema

# === 运行阶段 ===
FROM debian:bookworm-slim

# libstdc++.so.6（C++ 运行时）、libgomp（OpenMP，ONNX CPU 推理依赖）；
# ca-certificates / tzdata 供网络与时间。
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates tzdata libstdc++6 libgomp1 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /build/codeschema .
# ONNX 模型与 onnxruntime 动态库（构建期已建好版本号软链），供语义检索运行时加载。
COPY --from=builder /src/down /app/down

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/app/codeschema", "version"]

EXPOSE 8080 8081

ENTRYPOINT ["/app/codeschema"]
CMD ["serve", "--http", ":8081"]