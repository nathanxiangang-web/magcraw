@echo off
rem 启动磁链抓取服务（默认 8900 端口）
cd /d "%~dp0"
if not exist magnet-crawler.exe (
  echo [ERROR] magnet-crawler.exe not found, build first: go build -o magnet-crawler.exe .
  pause
  exit /b 1
)
start "" /min magnet-crawler.exe
echo started on http://127.0.0.1:8900