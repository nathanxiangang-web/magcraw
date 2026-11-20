#!/usr/bin/env bash
# 不使用 systemd 时的前台/后台启动（Linux/macOS）
#
#   ./start.sh            后台启动，日志 magcraw.log，PID 写 magcraw.pid
#   PORT=9000 ./start.sh
set -euo pipefail

cd "$(dirname "$0")"
BIN="${BIN:-./magcraw}"
PORT="${PORT:-8900}"

[[ -x "$BIN" ]] || { echo "[ERROR] 找不到可执行文件 $BIN，先运行 go build -o magcraw ." >&2; exit 1; }
[[ -f magcraw.pid ]] && kill -0 "$(cat magcraw.pid)" 2>/dev/null && {
  echo "[ERROR] 已在运行（PID $(cat magcraw.pid)），先执行 ./stop.sh" >&2; exit 1; }

nohup env PORT="$PORT" "$BIN" >> magcraw.log 2>&1 &
echo $! > magcraw.pid

for _ in $(seq 1 20); do
  sleep 0.5
  if command -v curl >/dev/null && curl -fsS -o /dev/null "http://127.0.0.1:$PORT/api/v1/health"; then
    echo "已启动：http://127.0.0.1:$PORT  （PID $(cat magcraw.pid)，日志 magcraw.log）"
    exit 0
  fi
done

echo "启动后 10 秒内未通过健康检查，请查看 magcraw.log" >&2
exit 1