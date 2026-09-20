#!/usr/bin/env bash
# Install the ICPC Remote Control server as a systemd service.
# Run from the repo root after `make build` (or with a prebuilt ./server binary):
#   sudo deploy/install-server.sh
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "请以 root 运行： sudo deploy/install-server.sh" >&2
  exit 1
fi

BIN_SRC="${1:-./server}"
if [ ! -x "$BIN_SRC" ]; then
  echo "找不到服务器二进制： $BIN_SRC （先执行 go build -o server ./cmd/server）" >&2
  exit 1
fi

install -d /opt/icpc
install -m 0755 "$BIN_SRC" /opt/icpc/icpc-server
install -m 0644 deploy/icpc-server.service /etc/systemd/system/icpc-server.service

systemctl daemon-reload
systemctl enable --now icpc-server.service
echo "已安装并启动 icpc-server。查看： systemctl status icpc-server"
