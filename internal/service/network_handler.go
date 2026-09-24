package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/model"
)

// NetworkHandler handles network blocking configuration and dispatch.
type NetworkHandler struct {
	settings   *ServerSettings
	hub        *biz.Hub
	repo       *data.CommandRepo
	dispatcher *biz.CommandDispatcher
	snapshots  *SnapshotManager
}

func NewNetworkHandler(settings *ServerSettings, hub *biz.Hub, repo *data.CommandRepo, dispatcher *biz.CommandDispatcher) *NetworkHandler {
	return &NetworkHandler{settings: settings, hub: hub, repo: repo, dispatcher: dispatcher}
}

func (h *NetworkHandler) SetSnapshotManager(snapshots *SnapshotManager) { h.snapshots = snapshots }

// GetRules returns current network rules (GET /api/network/rules).
func (h *NetworkHandler) GetRules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.settings.GetNetworkRules())
}

// UpdateRules replaces the network rules list (PUT /api/network/rules).
func (h *NetworkHandler) UpdateRules(w http.ResponseWriter, r *http.Request) {
	var rules []NetworkRule
	if err := json.NewDecoder(r.Body).Decode(&rules); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if rules == nil {
		rules = make([]NetworkRule, 0)
	}
	for i, r := range rules {
		rules[i].Type = strings.TrimSpace(r.Type)
		rules[i].Value = strings.TrimSpace(r.Value)
		if rules[i].Value == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule value cannot be empty"})
			return
		}
	}
	h.settings.SetNetworkRules(rules)
	writeJSON(w, http.StatusOK, h.settings.GetNetworkRules())
}

// ApplyRequest is the JSON body for apply/remove actions.
type ApplyRequest struct {
	TargetType string `json:"target_type"`         // "single", "list", or "broadcast"
	TargetID   *int   `json:"target_id,omitempty"` // required for single
	TargetIDs  []int  `json:"target_ids,omitempty"`
}

// Apply constructs the mihomo config and dispatches apply commands (POST /api/network/apply).
func (h *NetworkHandler) Apply(w http.ResponseWriter, r *http.Request) {
	var req ApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	command := buildApplyCommand(h.settings.GetNetworkRules())
	cmd, status, errText := h.dispatch(req, command, "admin@"+getClientIP(r))
	if status != 0 {
		writeJSON(w, status, map[string]string{"error": errText})
		return
	}
	writeJSON(w, http.StatusCreated, cmd)
}

// Remove dispatches systemctl stop/disable commands (POST /api/network/remove).
func (h *NetworkHandler) Remove(w http.ResponseWriter, r *http.Request) {
	var req ApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	command := buildRemoveCommand()
	cmd, status, errText := h.dispatch(req, command, "admin@"+getClientIP(r))
	if status != 0 {
		writeJSON(w, status, map[string]string{"error": errText})
		return
	}
	writeJSON(w, http.StatusCreated, cmd)
}

func (h *NetworkHandler) dispatch(req ApplyRequest, command, executedBy string) (*model.CommandLog, int, string) {
	if req.TargetType != "single" && req.TargetType != "broadcast" && req.TargetType != "list" {
		return nil, http.StatusBadRequest, "target_type must be 'single', 'list', or 'broadcast'"
	}
	if req.TargetType == "single" && req.TargetID == nil {
		return nil, http.StatusBadRequest, "target_id is required for single target"
	}
	ids := uniqueDeviceIDs(req.TargetIDs)
	if req.TargetType == "list" && len(ids) == 0 {
		return nil, http.StatusBadRequest, "target_ids is required for list target"
	}
	cmd := &model.CommandLog{
		TargetType: req.TargetType,
		TargetID:   req.TargetID,
		Command:    command,
		Status:     model.CommandStatusDispatched,
		ExecutedBy: executedBy,
	}
	if err := h.repo.Create(cmd); err != nil {
		return nil, http.StatusInternalServerError, err.Error()
	}
	if req.TargetType == "single" {
		go h.dispatcher.DispatchSingle(cmd)
		return cmd, 0, ""
	}
	if req.TargetType == "broadcast" {
		ids = h.hub.ConnectedIDs()
	}
	if len(ids) == 0 {
		cmd.Status = model.CommandStatusFailed
		cmd.ErrorOutput = "no devices connected"
		_ = h.repo.UpdateStatus(cmd)
		if req.TargetType == "broadcast" && h.snapshots != nil {
			h.snapshots.RecordLocal("command", "网络规则变更", SnapshotPayload{Command: command}, nil)
		}
		return cmd, 0, ""
	}
	children, err := h.dispatcher.Materialize(cmd, ids)
	if err != nil {
		return nil, http.StatusInternalServerError, err.Error()
	}
	cmd.Children = children
	send := func() []int {
		h.dispatcher.SendPrepared(cmd.ID, children)
		delivered := make([]int, 0, len(children))
		for _, child := range children {
			if child.Status == model.CommandStatusDispatched && child.TargetID != nil {
				delivered = append(delivered, *child.TargetID)
			}
		}
		return delivered
	}
	if req.TargetType == "broadcast" && h.snapshots != nil {
		h.snapshots.ObserveFanout("command", "网络规则变更", SnapshotPayload{Command: command}, send)
	} else {
		send()
	}
	return cmd, 0, ""
}

// buildRemoveCommand constructs the shell command that reconfigures mihomo to allow
// all traffic (no user rules, MATCH → DIRECT). This "removes" restrictions without
// stopping the mihomo service, so the TUN stack stays intact.
func buildRemoveCommand() string {
	return `cat << 'MCFG' > /etc/mihomo/config.yaml
# 核心：自建 DNS 解析引擎
dns:
  enable: true
  listen: 0.0.0.0:53
  enhanced-mode: fake-ip
  nameserver:
    - 223.5.5.5
    - 114.114.114.114

port: 7890
socks-port: 7891
allow-lan: false
mode: rule
log-level: info

tun:
  enable: true
  stack: system
  auto-route: true
  auto-redirect: true
  auto-detect-interface: true

proxy-groups:
  - name: 白名单放行
    type: select
    proxies:
      - DIRECT
  - name: 黑名单拦截
    type: select
    proxies:
      - REJECT

rules:
  - GEOIP,LAN,DIRECT
  - IP-CIDR,10.0.0.0/8,DIRECT
  - IP-CIDR,172.16.0.0/12,DIRECT
  - IP-CIDR,192.168.0.0/16,DIRECT
  - MATCH,DIRECT
MCFG
systemctl daemon-reload
systemctl enable mihomo
systemctl restart mihomo
echo '网络限制已解除'`
}

// buildApplyCommand constructs the shell command to write mihomo config and restart.
func buildApplyCommand(rules []NetworkRule) string {
	var ruleLines string
	for _, r := range rules {
		ruleLines += fmt.Sprintf("  - %s,%s,白名单放行\n", r.Type, r.Value)
	}

	config := fmt.Sprintf(`cat << 'MCFG' > /etc/mihomo/config.yaml
# 核心：自建 DNS 解析引擎
dns:
  enable: true
  listen: 0.0.0.0:53
  enhanced-mode: fake-ip
  nameserver:
    - 223.5.5.5
    - 114.114.114.114

port: 7890
socks-port: 7891
allow-lan: false
mode: rule
log-level: info

tun:
  enable: true
  stack: system
  auto-route: true
  auto-redirect: true
  auto-detect-interface: true

proxy-groups:
  - name: 白名单放行
    type: select
    proxies:
      - DIRECT
  - name: 黑名单拦截
    type: select
    proxies:
      - REJECT

rules:
  - GEOIP,LAN,DIRECT
  - IP-CIDR,10.0.0.0/8,DIRECT
  - IP-CIDR,172.16.0.0/12,DIRECT
  - IP-CIDR,192.168.0.0/16,DIRECT
%s  - MATCH,黑名单拦截
MCFG
systemctl daemon-reload
systemctl enable mihomo
systemctl restart mihomo
echo '网络限制已生效'`, ruleLines)

	return config
}
