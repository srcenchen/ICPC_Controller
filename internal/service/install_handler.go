package service

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/model"
)

// mustJSONLine marshals v and appends a newline for the line-delimited TCP protocol.
func mustJSONLine(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return append(b, '\n')
}

// InstallHandler serves the one-line client bootstrap and the client binary.
// The binary is uploaded by an admin into uploadDir (reusing the distribution
// storage) so the server need not be cross-compiled against the client.
type InstallHandler struct {
	uploadDir string
	tcpPort   string
	hub       *biz.Hub
}

const clientBinaryName = "icpc-client"

func NewInstallHandler(uploadDir, tcpPort string, hub *biz.Hub) *InstallHandler {
	return &InstallHandler{uploadDir: uploadDir, tcpPort: tcpPort, hub: hub}
}

func (h *InstallHandler) clientPath() string {
	return filepath.Join(h.uploadDir, clientBinaryName)
}

// serverHost returns the host:port the client should dial back on. Prefers the
// request Host so the installer works regardless of how the server is reached.
func (h *InstallHandler) serverHost(r *http.Request) (host string) {
	host = r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// InstallScript serves GET /install.sh — a curl|bash bootstrap that installs
// the client as a systemd service pointed at this server.
func (h *InstallHandler) InstallScript(w http.ResponseWriter, r *http.Request) {
	serverIP := h.serverHost(r)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	base := fmt.Sprintf("%s://%s", scheme, r.Host)

	script := strings.ReplaceAll(installScriptTemplate, "@@BASE@@", base)
	script = strings.ReplaceAll(script, "@@SERVER@@", serverIP)

	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(script))
}

// DownloadClient serves GET /download/client — the uploaded client binary.
func (h *InstallHandler) DownloadClient(w http.ResponseWriter, r *http.Request) {
	path := h.clientPath()
	if _, err := os.Stat(path); err != nil {
		http.Error(w, "client binary not uploaded yet (upload it in the admin distribution page as "+clientBinaryName+")", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=icpc-client")
	http.ServeFile(w, r, path)
}

// TriggerUpdate pushes an update_client message to the given devices (or all).
// Clients download the new binary, verify it, swap it in and restart.
func (h *InstallHandler) TriggerUpdate(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat(h.clientPath()); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no client binary uploaded"})
		return
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	url := fmt.Sprintf("%s://%s/download/client", scheme, r.Host)

	msg := model.UpdateClientMessage{Type: "update_client", URL: url}
	data := mustJSONLine(msg)

	sent := 0
	for _, id := range h.hub.OnlineIDs() {
		if h.hub.TrySend(id, data) {
			sent++
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"sent": sent})
}

const installScriptTemplate = `#!/usr/bin/env bash
# ICPC 集控客户端一键安装脚本。用法（选手机上以 root 运行）：
#   curl -fsSL @@BASE@@/install.sh | sudo bash
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "请以 root 运行：curl -fsSL @@BASE@@/install.sh | sudo bash" >&2
  exit 1
fi

BASE="@@BASE@@"
SERVER="@@SERVER@@"
BIN=/usr/local/bin/icpc-client
STATEDIR=/var/lib/icpc-client

echo "[icpc] 下载客户端 ..."
mkdir -p "$STATEDIR"
TMP="$(mktemp)"
curl -fsSL "$BASE/download/client" -o "$TMP"
install -m 0755 "$TMP" "$BIN"
rm -f "$TMP"

# 记录服务器地址（防 mDNS 解析失败）。
echo "$SERVER" > "$STATEDIR/server"

echo "[icpc] 安装 systemd 服务 ..."
cat > /etc/systemd/system/icpc-client.service <<UNIT
[Unit]
Description=ICPC Remote Control Client
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$BIN --server $SERVER
Restart=always
RestartSec=5
# 客户端需要 root（管理输入设备、截屏、水印覆盖层）。
User=root

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now icpc-client.service
echo "[icpc] 完成。查看状态： systemctl status icpc-client"
`
