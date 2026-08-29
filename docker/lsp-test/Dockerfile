# LSP 端到端测试镜像（D 扩展验证用）
#
# 用途：在 Docker 内运行 code-schema 的 LSP 真实解析 e2e（TestLSPAdapter_Real*），
#       尤其验证本机/常规 CI 未安装的语言服务器（如 rust-analyzer）。
#
# 运行方式（卷挂载，免拷贝大目录、免 gitee 网络；所有 go.mod replace 解析到真实路径）：
#   docker build -t cs-lsp-test:latest -f docker/lsp-test/Dockerfile .
#   docker run --rm \
#     -v "$(pwd)":/src/code-schema \
#     -v /path/to/idcu-go:/src/idcu-go \
#     -v "$HOME/go/pkg/mod":/root/go/pkg/mod \
#     cs-lsp-test:latest \
#     bash -c 'cd /src/code-schema && export GOWORK=off GOPATH=/root/go \
#       GOMODCACHE=/root/go/pkg/mod GOCACHE=/tmp/gocache GOROOT=/usr/local/go && \
#       go test ./internal/parser/adapter/lsp/ -run "RealRustAnalyzer|RealGopls|RealClangd" -v -timeout 300s'
#
# 网络注意（本环境实测）：
#   - 默认 Go proxy (proxy.golang.org) 在本沙箱不可达（i/o timeout），故用 goproxy.cn 镜像；
#     `goproxy.cn,direct` 在其它环境同样安全（不可达时回退 direct）。
#   - 若 docker.io 镜像加速器繁忙拉不动 rust:latest，可改用本 Dockerfile 的 debian:bookworm
#     + rustup 基线（已验证可构建；rustup 工具链下载约 10 分钟）。
FROM debian:bookworm
RUN apt-get update && apt-get install -y wget ca-certificates git curl gcc build-essential && rm -rf /var/lib/apt/lists/*
ENV RUSTUP_HOME=/usr/local/rustup CARGO_HOME=/usr/local/cargo
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain stable --profile minimal
ENV PATH="/usr/local/cargo/bin:${PATH}"
RUN rustup component add rust-analyzer
RUN wget -q https://go.dev/dl/go1.25.2.linux-amd64.tar.gz -O /tmp/go.tgz && tar -C /usr/local -xzf /tmp/go.tgz && rm /tmp/go.tgz
ENV PATH="/usr/local/go/bin:/root/go/bin:${PATH}"
ENV GOPROXY=https://goproxy.cn,direct
RUN go install golang.org/x/tools/gopls@latest
WORKDIR /src
