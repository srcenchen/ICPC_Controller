package service

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/model"
)

// Power actions.
const (
	PowerActionShutdown = "shutdown"
	PowerActionReboot   = "reboot"
	PowerActionWOL      = "wol"
)

// Commands used for scheduled power actions (mirror the built-in presets).
const (
	shutdownCommand = "shutdown now"
	rebootCommand   = "reboot"
)

// PowerHandler implements Wake-on-LAN and scheduled power actions.
// WoL bypasses the hub on purpose: its targets are machines that are off.
type PowerHandler struct {
	deviceRepo   *data.DeviceRepo
	dispatcher   *biz.CommandDispatcher
	scheduleRepo *data.PowerScheduleRepo
	hub          *biz.Hub
	settings     *ServerSettings
	snapshots    *SnapshotManager
}

func NewPowerHandler(deviceRepo *data.DeviceRepo, dispatcher *biz.CommandDispatcher,
	scheduleRepo *data.PowerScheduleRepo, hub *biz.Hub, settings *ServerSettings) *PowerHandler {
	return &PowerHandler{
		deviceRepo:   deviceRepo,
		dispatcher:   dispatcher,
		scheduleRepo: scheduleRepo,
		hub:          hub,
		settings:     settings,
	}
}

func (h *PowerHandler) SetSnapshotManager(snapshots *SnapshotManager) { h.snapshots = snapshots }

type wolRequest struct {
	TargetType string `json:"target_type"` // "all" | "list"
	DeviceIDs  []int  `json:"device_ids"`
}

type wolResult struct {
	DeviceID int    `json:"device_id"`
	MAC      string `json:"mac"`
	Sent     bool   `json:"sent"`
	Error    string `json:"error,omitempty"`
}

// Wake handles POST /api/power/wol.
func (h *PowerHandler) Wake(w http.ResponseWriter, r *http.Request) {
	var req wolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	devices, err := h.deviceRepo.GetAllFull()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	wanted := map[int]bool{}
	for _, id := range req.DeviceIDs {
		wanted[id] = true
	}

	results := []wolResult{}
	sent := 0
	for _, d := range devices {
		if req.TargetType != "all" && !wanted[d.AssignedID] {
			continue
		}
		res := wolResult{DeviceID: d.AssignedID, MAC: d.MacAddress}
		if d.MacAddress == "" {
			res.Error = "该设备没有记录 MAC 地址"
		} else if err := sendMagicPacket(d.MacAddress); err != nil {
			res.Error = err.Error()
		} else {
			res.Sent = true
			sent++
		}
		results = append(results, res)
	}

	log.Printf("[power] WoL: %d/%d magic packets sent (by %s)", sent, len(results), getClientIP(r))
	if req.TargetType == "all" && h.snapshots != nil {
		h.snapshots.RecordLocal("wol", "批量唤醒", SnapshotPayload{})
	}
	h.hub.BroadcastAdminEvent("power_wol", map[string]interface{}{
		"sent":  sent,
		"total": len(results),
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sent":    sent,
		"total":   len(results),
		"results": results,
	})
}

// sendMagicPacket broadcasts a Wake-on-LAN packet for one MAC address.
// Requires WoL to be enabled in the machine's BIOS/NIC, and the stored MAC to
// belong to the wired interface.
func sendMagicPacket(mac string) error {
	hw, err := net.ParseMAC(strings.TrimSpace(mac))
	if err != nil {
		return fmt.Errorf("invalid MAC %q: %w", mac, err)
	}
	if len(hw) != 6 {
		return fmt.Errorf("unsupported MAC length %d", len(hw))
	}
	packet := make([]byte, 0, 102)
	for i := 0; i < 6; i++ {
		packet = append(packet, 0xFF)
	}
	for i := 0; i < 16; i++ {
		packet = append(packet, hw...)
	}

	// Port 9 is the conventional WoL discard port; 7 is also common.
	var lastErr error
	ok := false
	for _, addr := range magicPacketTargets() {
		conn, err := net.DialTimeout("udp", addr, 2*time.Second)
		if err != nil {
			lastErr = err
			continue
		}
		_, err = conn.Write(packet)
		conn.Close()
		if err != nil {
			lastErr = err
			continue
		}
		ok = true
	}
	if !ok {
		return fmt.Errorf("send magic packet: %w", lastErr)
	}
	return nil
}

// magicPacketTargets returns the global broadcast plus every local subnet
// broadcast address, so WoL works on hosts with several NICs.
func magicPacketTargets() []string {
	targets := []string{"255.255.255.255:9"}
	ifaces, err := net.Interfaces()
	if err != nil {
		return targets
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			ip := ipnet.IP.To4()
			mask := ipnet.Mask
			bcast := net.IP{ip[0] | ^mask[0], ip[1] | ^mask[1], ip[2] | ^mask[2], ip[3] | ^mask[3]}
			targets = append(targets, bcast.String()+":9")
		}
	}
	return targets
}

type scheduleRequest struct {
	Action     string `json:"action"`      // shutdown | reboot | wol
	RunAt      string `json:"run_at"`      // RFC3339 or "2006-01-02T15:04"
	TargetType string `json:"target_type"` // all | list
	DeviceIDs  []int  `json:"device_ids"`
	Note       string `json:"note"`
}

// Schedules handles GET/POST /api/power/schedules.
func (h *PowerHandler) Schedules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := h.scheduleRepo.List(100)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, list)
	case http.MethodPost:
		h.createSchedule(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (h *PowerHandler) createSchedule(w http.ResponseWriter, r *http.Request) {
	var req scheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	switch req.Action {
	case PowerActionShutdown, PowerActionReboot, PowerActionWOL:
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "action 必须是 shutdown / reboot / wol"})
		return
	}
	runAt, err := parseScheduleTime(req.RunAt)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.TargetType != "all" && len(req.DeviceIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请选择目标设备，或使用 target_type=all"})
		return
	}
	if req.TargetType == "" {
		req.TargetType = "all"
	}

	s := &data.PowerSchedule{
		Action:     req.Action,
		RunAt:      runAt.Format(time.RFC3339),
		TargetType: req.TargetType,
		TargetIDs:  req.DeviceIDs,
		Note:       req.Note,
		CreatedBy:  getClientIP(r),
	}
	if err := h.scheduleRepo.Create(s); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("[power] scheduled %s at %s (%s) by %s", s.Action, s.RunAt, s.TargetType, s.CreatedBy)
	if req.TargetType == "all" && h.snapshots != nil {
		h.snapshots.RecordLocal("schedule", "电源计划："+s.Action+" @ "+s.RunAt, SnapshotPayload{Action: s.Action, RunAt: s.RunAt, Note: s.Note})
	}
	h.hub.BroadcastAdminEvent("power_schedule_created", map[string]interface{}{"id": s.ID})
	writeJSON(w, http.StatusOK, s)
}

// parseScheduleTime accepts RFC3339 or the HTML datetime-local format.
func parseScheduleTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("run_at 不能为空")
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间 %q（示例 2026-07-26T18:30）", raw)
}

// DeleteSchedule handles DELETE /api/power/schedules/{id}.
func (h *PowerHandler) DeleteSchedule(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	if err := h.scheduleRepo.Delete(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.hub.BroadcastAdminEvent("power_schedule_deleted", map[string]interface{}{"id": id})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// RunSchedule executes a due schedule. Called by the janitor.
func (h *PowerHandler) RunSchedule(s data.PowerSchedule) error {
	switch s.Action {
	case PowerActionWOL:
		devices, err := h.deviceRepo.GetAllFull()
		if err != nil {
			return err
		}
		wanted := map[int]bool{}
		for _, id := range s.TargetIDs {
			wanted[id] = true
		}
		sent := 0
		for _, d := range devices {
			if s.TargetType != "all" && !wanted[d.AssignedID] {
				continue
			}
			if d.MacAddress == "" {
				continue
			}
			if err := sendMagicPacket(d.MacAddress); err == nil {
				sent++
			}
		}
		log.Printf("[power] schedule #%d: %d magic packets sent", s.ID, sent)
		return nil

	case PowerActionShutdown, PowerActionReboot:
		command := shutdownCommand
		if s.Action == PowerActionReboot {
			command = rebootCommand
		}
		executedBy := fmt.Sprintf("schedule#%d", s.ID)
		if s.TargetType == "all" {
			parent := &model.CommandLog{
				TargetType: "broadcast",
				Command:    command,
				Status:     model.CommandStatusPending,
				ExecutedBy: executedBy,
			}
			if err := h.dispatcher.CreateAndDispatch(parent); err != nil {
				return err
			}
			return nil
		}
		var firstErr error
		for _, id := range s.TargetIDs {
			target := id
			cmd := &model.CommandLog{
				TargetType: "single",
				TargetID:   &target,
				Command:    command,
				Status:     model.CommandStatusPending,
				ExecutedBy: executedBy,
			}
			if err := h.dispatcher.CreateAndDispatch(cmd); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}
	return fmt.Errorf("unknown action %q", s.Action)
}
