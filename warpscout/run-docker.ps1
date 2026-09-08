# ==============================================================================
# WARPSCOUT WebUI - Docker 一键构建与启动脚本 (Windows PowerShell)
# ==============================================================================

$ErrorActionPreference = "Stop"

Write-Host ""
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "       WARPSCOUT WebUI - Docker 一键部署脚本              " -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host ""

# 1. 检查 Docker 运行环境
Write-Host "[1/4] 检查 Docker 守护进程状态..." -ForegroundColor Yellow
try {
    $dockerInfo = docker info 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Docker 未启动，请先打开 Docker Desktop！"
    }
    Write-Host "  -> Docker 环境正常运行中" -ForegroundColor Green
} catch {
    Write-Host "  [错误] 无法连接到 Docker 引擎，请确认 Docker Desktop 是否已经启动！" -ForegroundColor Red
    Write-Host "  详情: $_" -ForegroundColor Red
    exit 1
}

# 2. 准备持久化数据目录并同步已有账号
Write-Host "[2/4] 初始化持久化数据目录 (./data)..." -ForegroundColor Yellow
$dataDir = Join-Path $PSScriptRoot "data"
if (-not (Test-Path $dataDir)) {
    New-Item -ItemType Directory -Path $dataDir -Force | Out-Null
}

# 如果已有账号文件，自动复制到 data 目录持久化
$dataAccount = Join-Path $dataDir "warpscout-account.json"
if (-not (Test-Path $dataAccount)) {
    $searchPaths = @(
        (Join-Path $PSScriptRoot "warpscout-account.json"),
        (Join-Path $PSScriptRoot "..\tools\warpscout\warpscout-account.json"),
        "C:\tools2\warp\tools\warpscout\warpscout-account.json"
    )
    foreach ($sp in $searchPaths) {
        if (Test-Path $sp) {
            Copy-Item $sp $dataAccount -Force
            Write-Host "  -> 发现已有账号并自动同步到容器挂载目录: $sp" -ForegroundColor Green
            break
        }
    }
}

# 3. 构建并启动容器
Write-Host "[3/4] 正在构建镜像并启动 Docker 容器..." -ForegroundColor Yellow
$composeCmd = "docker compose"
try {
    & docker compose version | Out-Null
} catch {
    $composeCmd = "docker-compose"
}

Invoke-Expression "$composeCmd up -d --build"
if ($LASTEXITCODE -ne 0) {
    Write-Host "  [错误] 容器构建或启动失败，请检查 Docker 日志！" -ForegroundColor Red
    exit 1
}

Start-Sleep -Seconds 2
$runningId = docker ps -q --filter "name=warpscout-web"
if (-not $runningId) {
    Write-Host "  [错误] 容器未能成功保持运行，查看崩溃日志如下：" -ForegroundColor Red
    docker logs warpscout-web
    exit 1
}

# 4. 显示服务地址并自动打开浏览器
$webPort = if ($env:PORT_WEB) { $env:PORT_WEB } else { "29880" }
$socksPort = if ($env:PORT_SOCKS) { $env:PORT_SOCKS } else { "29881" }
$webUrl = "http://localhost:$webPort"

Write-Host ""
Write-Host "==========================================================" -ForegroundColor Green
Write-Host "  [部署成功] WARPSCOUT WebUI 容器已在后台运行！" -ForegroundColor Green
Write-Host "==========================================================" -ForegroundColor Green
Write-Host "  -> Web 控制台地址 : $webUrl" -ForegroundColor Cyan
Write-Host "  -> SOCKS5 代理端口 : 127.0.0.1:$socksPort" -ForegroundColor Cyan
Write-Host "  -> 数据持久化目录 : $dataDir" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Green
Write-Host ""
Write-Host "正在为你打开浏览器..." -ForegroundColor Yellow
Start-Process $webUrl
