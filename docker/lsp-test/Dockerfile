FROM debian:trixie
RUN apt-get update && apt-get install -y wget ca-certificates git curl gcc build-essential nodejs npm && rm -rf /var/lib/apt/lists/*
# Rust：rust-analyzer（LSP）+ cargo（rust-analyzer 符号解析无需 cargo 构建，保留供完整工程上下文）；
# 直接 apt 安装，免 rustup 工具链下载（原 rustup 路径在本环境下载极慢，曾卡 30min+）
RUN apt-get update && apt-get install -y rust-analyzer cargo && rm -rf /var/lib/apt/lists/*
RUN wget -q https://golang.google.cn/dl/go1.25.2.linux-amd64.tar.gz -O /tmp/go.tgz && tar -C /usr/local -xzf /tmp/go.tgz && rm /tmp/go.tgz
ENV PATH="/usr/local/go/bin:/root/go/bin:${PATH}"
ENV GOPROXY=https://goproxy.cn,direct
RUN go install golang.org/x/tools/gopls@latest
RUN npm install -g pyright typescript-language-server typescript@5

# C++：clangd（LSP 服务器）+ clang（供被测工程 compile_commands.json 引用的 clang++ 可解析）
RUN apt-get update && apt-get install -y clangd clang && rm -rf /var/lib/apt/lists/*

# Java：JDK 21（最新 jdtls 快照要求 class file 65.0=Java 21；JDK 17 会 UnsupportedClassVersionError）+ Eclipse JDT LS
# trixie 默认 JDK 即 21（/usr/lib/jvm/default-java 软链）
RUN apt-get update && apt-get install -y default-jdk && rm -rf /var/lib/apt/lists/*
ENV JAVA_HOME=/usr/lib/jvm/default-java
ENV PATH="$JAVA_HOME/bin:$PATH"
RUN wget -q https://download.eclipse.org/jdtls/snapshots/jdt-language-server-latest.tar.gz -O /tmp/jdtls.tar.gz && \
    mkdir -p /opt/jdtls && tar -C /opt/jdtls -xzf /tmp/jdtls.tar.gz && rm /tmp/jdtls.tar.gz
COPY docker/lsp-test/jdtls.sh /usr/local/bin/jdtls
RUN chmod +x /usr/local/bin/jdtls

# SCIP：scip CLI（本地 clone 构建——其 go.mod 自带 replace 指令，Go 1.21+ 拒绝 go install @latest）。
# github 直连不稳定（曾超时 exit 128），失败不阻断：SCIPAdapter 直接读 .scip 文件，CLI 仅调试辅助。
RUN (git clone --depth 1 https://github.com/sourcegraph/scip.git /tmp/scip-src && \
    cd /tmp/scip-src && go build -o /root/go/bin/scip ./cmd/scip && rm -rf /tmp/scip-src) || echo "scip CLI build skipped"
# scip-typescript（TS→SCIP index 生成器）+ @optave/codegraph（真实 CodeGraph 产物，tree-sitter→SQLite nodes/edges）
RUN npm install -g @sourcegraph/scip-typescript@0.4.0 @optave/codegraph

WORKDIR /src
