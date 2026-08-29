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

WORKDIR /src
