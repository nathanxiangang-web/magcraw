#!/usr/bin/env bash
# magcraw 一键部署（Linux + systemd）
#
#   sudo ./install.sh              # 用当前目录的 ./magcraw，没有就用 go build
#   sudo ./install.sh ./magcraw-linux-amd64
#   sudo PORT=9000 ./install.sh
#
# 可用环境变量：PREFIX(默认 /opt/magcraw) PORT(默认 8900) CI_API_KEY CI_PROXY
set -euo pipefail

PREFIX="${PREFIX:-/opt/magcraw}"
PORT="${PORT:-8900}"
SERVICE="/etc/systemd/system/magcraw.service"
BIN_SRC="${1:-}"

say() { printf '\033[36m==>\033[0m %s\n' "$*"; }
die() { printf '\033[31m[ERROR]\033[0m %s\n' "$*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "请用 root 运行：sudo ./install.sh"
command -v systemctl >/dev/null || die "本机没有 systemd，请改用 ./start.sh 手动运行"

# 1. 确定二进制
if [[ -n "$BIN_SRC" ]]; then
  [[ -f "$BIN_SRC" ]] || die "找不到二进制：$BIN_SRC"
  BIN="$BIN_SRC"
elif [[ -f ./magcraw ]]; then
  BIN=./magcraw
else
  say "未找到 ./magcraw，尝试用 Go 现场构建"
  command -v go >/dev/null || die "缺少 Go，也不会自动下载；请先 go build -o magcraw . 或传入二进制路径"
  ( cd "$(dirname "$0")" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o magcraw . )
  BIN=./magcraw
fi

# 2. 安装目录（data 归 nobody 可写）
say "安装到 $PREFIX"
install -d -m 0755 "$PREFIX"
install -m 0755 "$BIN" "$PREFIX/magcraw"
install -d -o nobody -m 0750 "$PREFIX/data"

# 3. systemd unit
say "写入 $SERVICE"
cat > "$SERVICE" <<EOF
[Unit]
Description=magcraw magnet crawler panel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=nobody
WorkingDirectory=$PREFIX
ExecStart=$PREFIX/magcraw
Restart=on-failure
RestartSec=3
Environment=PORT=$PORT
Environment=CI_BASE=https://cilisousuo.co
Environment=CI_RATE_LIMIT=60
# 公网暴露时必须打开（随机串用 openssl rand -hex 24 生成）
# Environment=CI_API_KEY=change-me
# 只有域名放行白名单时：
# Environment=CI_CORS_ORIGINS=https://your-site.example
# 需要代理出口时：
# Environment=CI_PROXY=socks5://127.0.0.1:1080
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=$PREFIX/data

[Install]
WantedBy=multi-user.target
EOF

[[ -n "${CI_API_KEY:-}" ]] && say "注意：CI_API_KEY 仅在本次安装生效，请手工写入 $SERVICE 的 Environment= 行"

systemctl daemon-reload
systemctl enable --now magcraw.service
sleep 1
systemctl --no-pager --lines=5 status magcraw.service || true

say "部署完成：http://<本机IP>:$PORT"
say "查看日志：journalctl -u magcraw -f"