@echo off
rem 由计划任务调用：保持服务进程独立于任何终端会话
cd /d "%~dp0"
"%~dp0magnet-crawler.exe" >> "%~dp0magnet-crawler.log" 2>&1