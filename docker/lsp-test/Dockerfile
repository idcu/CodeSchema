FROM debian:bookworm
RUN apt-get update && apt-get install -y wget ca-certificates git curl gcc build-essential nodejs npm && rm -rf /var/lib/apt/lists/*
ENV RUSTUP_HOME=/usr/local/rustup CARGO_HOME=/usr/local/cargo
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain stable --profile minimal
ENV PATH="/usr/local/cargo/bin:${PATH}"
RUN rustup component add rust-analyzer
RUN wget -q https://go.dev/dl/go1.25.2.linux-amd64.tar.gz -O /tmp/go.tgz && tar -C /usr/local -xzf /tmp/go.tgz && rm /tmp/go.tgz
ENV PATH="/usr/local/go/bin:/root/go/bin:${PATH}"
ENV GOPROXY=https://goproxy.cn,direct
RUN go install golang.org/x/tools/gopls@latest
RUN npm install -g pyright typescript-language-server typescript@5
WORKDIR /src
