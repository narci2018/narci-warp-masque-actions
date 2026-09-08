@echo off
chcp 65001 >nul
title WARPSCOUT Pages 一键同步与部署

echo ==========================================================
echo 正在执行 WARPSCOUT Cloudflare Pages 同步与部署...
echo ==========================================================

powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0deploy-pages.ps1"

if %ERRORLEVEL% NEQ 0 (
    echo.
    echo 部署失败，请检查上方错误提示。
    pause
)
