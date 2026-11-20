@echo off
rem 停止磁链抓取服务
taskkill /F /IM magnet-crawler.exe >nul 2>&1
echo stopped