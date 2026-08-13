# =============================================================================
# CodeSchema 快速启动脚本 (Windows PowerShell)
# =============================================================================
# 用法：
#   .\scripts\quick-start.ps1                   # 扫描当前目录并启动
#   .\scripts\quick-start.ps1 -RepoPath D:\repo # 扫描指定仓库并启动
#   .\scripts\quick-start.ps1 -UseDocker        # 使用 Docker 部署
# =============================================================================

param(
    [string]$RepoPath = ".",
    [switch]$UseDocker = $false,
    [int]$McpPort = 8080,
    [int]$HttpPort = 8081
)

$Binary = ".\build\codeschema.exe"
$DataDir = ".\data"

function Write-Info($msg) { Write-Host "[INFO] $msg" -ForegroundColor Green }
function Write-Warn($msg) { Write-Host "[WARN] $msg" -ForegroundColor Yellow }
function Write-Error($msg) { Write-Host "[ERROR] $msg" -ForegroundColor Red }

# =============================================================================
# Docker 模式
# =============================================================================
if ($UseDocker) {
    $docker = Get-Command "docker" -ErrorAction SilentlyContinue
    if (-not $docker) {
        Write-Error "Docker 未安装，请先安装 Docker Desktop"
        exit 1
    }

    $RepoPath = Resolve-Path $RepoPath
    Write-Info "使用 Docker 部署 CodeSchema"
    Write-Info "仓库路径: $RepoPath"

    # 构建镜像
    docker build -t codeschema:latest .

    # 创建数据目录
    $DataDir = Join-Path (Get-Location) "data"
    New-Item -ItemType Directory -Force -Path $DataDir | Out-Null

    # 先扫描
    Write-Info "扫描仓库..."
    docker run --rm `
        -v "$DataDir`:/app/data" `
        -v "$RepoPath`:/repo:ro" `
        codeschema:latest scan /repo

    # 启动服务
    Write-Info "启动 MCP Server (端口 $McpPort) + HTTP API (端口 $HttpPort)..."
    docker run -d `
        --name codeschema `
        --restart unless-stopped `
        -p "${McpPort}:8080" `
        -p "${HttpPort}:8081" `
        -v "$DataDir`:/app/data" `
        -v "$RepoPath`:/repo:ro" `
        -e CODESCHEMA_PROJECT_ROOT=/repo `
        -e CODESCHEMA_STORAGE_DSN=/app/data `
        codeschema:latest

    Write-Info "CodeSchema 已启动！"
    Write-Info "  MCP Server: http://localhost:$McpPort"
    Write-Info "  HTTP API:   http://localhost:$HttpPort"
    Write-Info "查看日志: docker logs -f codeschema"
    exit 0
}

# =============================================================================
# 本地模式
# =============================================================================
$go = Get-Command "go" -ErrorAction SilentlyContinue
if (-not $go) {
    Write-Error "Go 未安装，请先安装 Go 1.25+"
    exit 1
}

# 构建
if (-not (Test-Path $Binary)) {
    Write-Info "构建 CodeSchema..."
    go build -o $Binary .\cmd\codeschema
    if ($LASTEXITCODE -ne 0) {
        Write-Error "构建失败"
        exit 1
    }
}

# 检查仓库路径
if (-not (Test-Path $RepoPath)) {
    Write-Error "仓库路径不存在: $RepoPath"
    exit 1
}
$RepoPath = Resolve-Path $RepoPath

# 创建数据目录
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null

# 扫描仓库
Write-Info "扫描仓库: $RepoPath"
& $Binary scan --store $DataDir $RepoPath

# 启动 MCP Server（新窗口）
Write-Info "启动 MCP Server (端口 $McpPort)..."
$mcpJob = Start-Job -ScriptBlock {
    param($b, $p, $d)
    & $b mcp --addr ":$p" --store $d
} -ArgumentList $Binary, $McpPort, $DataDir

# 启动 HTTP API（新窗口）
Write-Info "启动 HTTP API (端口 $HttpPort)..."
$httpJob = Start-Job -ScriptBlock {
    param($b, $p, $d)
    & $b serve --http ":$p" --store $d
} -ArgumentList $Binary, $HttpPort, $DataDir

Write-Info "CodeSchema 已启动！"
Write-Info "  MCP Server: http://localhost:$McpPort"
Write-Info "  HTTP API:   http://localhost:$HttpPort"
Write-Info ""
Write-Info "按任意键停止服务..."
$null = $Host.UI.RawUI.ReadKey("NoEcho,IncludeKeyDown")

# 清理
Write-Info "正在停止服务..."
Stop-Job $mcpJob -ErrorAction SilentlyContinue
Stop-Job $httpJob -ErrorAction SilentlyContinue
Remove-Job $mcpJob -ErrorAction SilentlyContinue
Remove-Job $httpJob -ErrorAction SilentlyContinue
Write-Info "服务已停止"