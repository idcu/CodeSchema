#!/bin/bash
# =============================================================================
# CodeSchema 快速启动脚本 (Linux/macOS)
# =============================================================================
# 用法：
#   ./scripts/quick-start.sh                   # 扫描当前目录并启动
#   ./scripts/quick-start.sh /path/to/repo     # 扫描指定仓库并启动
#   ./scripts/quick-start.sh --docker /path    # 使用 Docker 部署
# =============================================================================

set -euo pipefail

REPO_PATH="${1:-.}"
BINARY="./build/codeschema"
DATA_DIR="./data"
MCP_PORT="${CODESCHEMA_MCP_PORT:-8080}"
HTTP_PORT="${CODESCHEMA_HTTP_PORT:-8081}"
MODE="${2:-local}"  # local 或 docker

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; }

check_dependency() {
    if ! command -v "$1" &>/dev/null; then
        error "$1 未安装，请先安装 $1"
        exit 1
    fi
}

# =============================================================================
# Docker 模式
# =============================================================================
if [ "$MODE" = "docker" ] || [ "$2" = "--docker" ]; then
    check_dependency docker

    info "使用 Docker 部署 CodeSchema"
    info "仓库路径: $REPO_PATH"

    # 构建镜像
    docker build -t codeschema:latest .

    # 创建数据目录
    mkdir -p "$DATA_DIR"
    DATA_DIR=$(cd "$DATA_DIR" && pwd)
    REPO_PATH=$(cd "$REPO_PATH" && pwd)

    # 先扫描
    info "扫描仓库..."
    docker run --rm \
        -v "$DATA_DIR:/app/data" \
        -v "$REPO_PATH:/repo:ro" \
        codeschema:latest scan /repo

    # 启动服务
    info "启动 MCP Server (端口 $MCP_PORT) + HTTP API (端口 $HTTP_PORT)..."
    docker run -d \
        --name codeschema \
        --restart unless-stopped \
        -p "$MCP_PORT:8080" \
        -p "$HTTP_PORT:8081" \
        -v "$DATA_DIR:/app/data" \
        -v "$REPO_PATH:/repo:ro" \
        -e CODESCHEMA_PROJECT_ROOT=/repo \
        codeschema:latest

    info "CodeSchema 已启动！"
    info "  MCP Server: http://localhost:$MCP_PORT"
    info "  HTTP API:   http://localhost:$HTTP_PORT"
    info "查看日志: docker logs -f codeschema"
    exit 0
fi

# =============================================================================
# 本地模式
# =============================================================================
check_dependency go

# 构建
if [ ! -f "$BINARY" ]; then
    info "构建 CodeSchema..."
    make build
fi

# 检查 REPO_PATH 是否存在
if [ ! -d "$REPO_PATH" ]; then
    error "仓库路径不存在: $REPO_PATH"
    exit 1
fi

# 创建数据目录
mkdir -p "$DATA_DIR"

# 扫描仓库
info "扫描仓库: $REPO_PATH"
$BINARY scan --store "$DATA_DIR" "$REPO_PATH"

# 启动 MCP Server（后台运行）
info "启动 MCP Server (端口 $MCP_PORT)..."
$BINARY mcp --addr ":$MCP_PORT" --store "$DATA_DIR" &
MCP_PID=$!

# 启动 HTTP API（后台运行）
info "启动 HTTP API (端口 $HTTP_PORT)..."
$BINARY serve --http ":$HTTP_PORT" --store "$DATA_DIR" &
HTTP_PID=$!

# 注册退出信号
cleanup() {
    info "正在停止服务..."
    kill "$MCP_PID" 2>/dev/null || true
    kill "$HTTP_PID" 2>/dev/null || true
    info "服务已停止"
}
trap cleanup EXIT

info "CodeSchema 已启动！"
info "  MCP Server: http://localhost:$MCP_PORT"
info "  HTTP API:   http://localhost:$HTTP_PORT"
info "  数据目录:   $DATA_DIR"
info ""
info "按 Ctrl+C 停止服务"

# 等待子进程
wait