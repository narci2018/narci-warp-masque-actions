# ==========================================================
# WARPSCOUT Pages 一键同步部署与自动打开脚本
# ==========================================================

$ErrorActionPreference = "Stop"

Write-Host ">>> [1/4] 从 GitHub Actions 获取最新的探测结果 (gh-pages 分支)..." -ForegroundColor Cyan
git fetch origin gh-pages

Write-Host ">>> [2/4] 同步最新扫描文件到本地发布目录..." -ForegroundColor Cyan
if (Test-Path "temp-pages") {
    Remove-Item -Recurse -Force "temp-pages" -ErrorAction SilentlyContinue
}
git worktree add -f temp-pages origin/gh-pages
Copy-Item -Recurse -Force temp-pages/* warpscout/public/
git worktree remove -f temp-pages

Write-Host ">>> [3/4] 部署静态资源至 Cloudflare Pages (项目: warpscout)..." -ForegroundColor Cyan
npx wrangler pages deploy warpscout/public --project-name warpscout --branch main --commit-dirty=true

$PagesUrl = "https://warpscout-46m.pages.dev"
$ClashSubUrl = "$PagesUrl/data/clash-sub.yaml"

Write-Host ""
Write-Host "==========================================================" -ForegroundColor Green
Write-Host "🎉 部署成功！Cloudflare Pages 全球 CDN 已刷新上线！" -ForegroundColor Green
Write-Host "==========================================================" -ForegroundColor Green
Write-Host "🌐 面板访问地址: $PagesUrl" -ForegroundColor Yellow
Write-Host "📡 Clash 订阅地址: $ClashSubUrl" -ForegroundColor Yellow
Write-Host "==========================================================" -ForegroundColor Green
Write-Host ""

Write-Host ">>> [4/4] 正在为您自动在浏览器中打开面板..." -ForegroundColor Cyan
Start-Process $PagesUrl
