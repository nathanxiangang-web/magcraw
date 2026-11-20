#!/usr/bin/env bash
# 停止 ./start.sh 启动的进程；systemd 安装的请用 systemctl stop magcraw
set -euo pipefail

cd "$(dirname "$0")"
PIDFILE=magcraw.pid

if [[ ! -f "$PIDFILE" ]]; then
  echo "没有 $PIDFILE，可能不是 ./start.sh 启动的。"
  if command -v systemctl >/dev/null && systemctl is-active --quiet magcraw 2>/dev/null; then
    echo "检测到 systemd 服务在运行，请用：sudo systemctl stop magcraw"
  fi
  exit 0
fi

PID="$(cat "$PIDFILE")"
if kill -0 "$PID" 2>/dev/null; then
  kill "$PID"
  for _ in $(seq 1 10); do
    sleep 0.5
    kill -0 "$PID" 2>/dev/null || break
  done
  kill -0 "$PID" 2>/dev/null && kill -9 "$PID" || true
  echo "已停止 PID $PID"
else
  echo "PID $PID 已不在运行"
fi
rm -f "$PIDFILE"