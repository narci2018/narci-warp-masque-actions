#!/usr/bin/env bash
# ==============================================================================
# WARPSCOUT WebUI - Docker 一键构建与启动脚本 (Linux / macOS)
# ==============================================================================

set -e

GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${CYAN}==========================================================${NC}"
echo -e "${CYAN}       WARPSCOUT WebUI - Docker 一键部署脚本              ${NC}"
echo -e "${CYAN}==========================================================${NC}"
echo ""

# 1. 检查 Docker 环境
echo -e "${YELLOW}[1/4] 检查 Docker 守护进程状态...${NC}"
if ! docker info >/dev/null 2>&1; then
    echo -e "${RED}[错误] 无法连接到 Docker，请确认 Docker 守护进程已启动！${NC}"
    exit 1
fi
echo -e "${GREEN}  -> Docker 环境正常运行中${NC}"

# 2. 初始化数据持久化目录
echo -e "${YELLOW}[2/4] 初始化持久化数据目录 (./data)...${NC}"
mkdir -p ./data

if [ -f "./warpscout-account.json" ] && [ ! -f "./data/warpscout-account.json" ]; then
    cp ./warpscout-account.json ./data/
    echo -e "${GREEN}  -> 已将现有 warpscout-account.json 同步到容器挂载目录${NC}"
fi

# 3. 构建并启动容器
echo -e "${YELLOW}[3/4] 正在构建镜像并启动 Docker 容器...${NC}"
COMPOSE_CMD="docker compose"
if ! docker compose version >/dev/null 2>&1; then
    COMPOSE_CMD="docker-compose"
fi

$COMPOSE_CMD up -d --build

sleep 2
if ! docker ps -q --filter "name=warpscout-web" | grep -q .; then
    echo -e "${RED}[错误] 容器启动后退出，最新日志如下：${NC}"
    docker logs warpscout-web
    exit 1
fi

# 4. 显示服务地址
WEB_PORT="${PORT_WEB:-29880}"
SOCKS_PORT="${PORT_SOCKS:-29881}"
WEB_URL="http://localhost:${WEB_PORT}"

echo ""
echo -e "${GREEN}==========================================================${NC}"
echo -e "${GREEN}  [部署成功] WARPSCOUT WebUI 容器已在后台运行！${NC}"
echo -e "${GREEN}==========================================================${NC}"
echo -e "${CYAN}  -> Web 控制台地址 : ${WEB_URL}${NC}"
echo -e "${CYAN}  -> SOCKS5 代理端口 : 127.0.0.1:${SOCKS_PORT}${NC}"
echo -e "${CYAN}  -> 数据持久化目录 : $(pwd)/data${NC}"
echo -e "${GREEN}==========================================================${NC}"
echo ""

# 尝试自动打开浏览器
if command -v xdg-open >/dev/null 2>&1; then
    xdg-open "$WEB_URL" >/dev/null 2>&1 || true
elif command -v open >/dev/null 2>&1; then
    open "$WEB_URL" >/dev/null 2>&1 || true
fi
