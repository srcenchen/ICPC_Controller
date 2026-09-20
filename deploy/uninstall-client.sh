#!/usr/bin/env bash
# Remove the ICPC Remote Control client from a contestant machine.
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "请以 root 运行： sudo deploy/uninstall-client.sh" >&2
  exit 1
fi

systemctl disable --now icpc-client.service 2>/dev/null || true
rm -f /etc/systemd/system/icpc-client.service
systemctl daemon-reload
rm -f /usr/local/bin/icpc-client
echo "已卸载 icpc-client（保留 /var/lib/icpc-client 状态目录；如需清除请手动删除）。"
