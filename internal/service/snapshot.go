package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/model"
)

// errSnapshotDeferred means the operation is still covered by something that
// has not happened yet. The device must not be marked delivered.
var errSnapshotDeferred = errors.New("snapshot operation not applied yet")

// SnapshotPayload is the replayable description of a broadcast operation.
// It is deliberately transport agnostic so the same record can be replayed on
// the local machine (to one device) and on the cloud (to one room).
type SnapshotPayload struct {
	Kind          string          `json:"kind"`
	Command       string          `json:"command,omitempty"`
	Files         []RelayFile     `json:"files,omitempty"`
	SaveDir       string          `json:"save_dir,omitempty"`
	PostCmd       string          `json:"post_cmd,omitempty"`
	Action        string          `json:"action,omitempty"`
	RunAt         string          `json:"run_at,omitempty"`
	Note          string          `json:"note,omitempty"`
	Rooms         []string        `json:"rooms,omitempty"`
	BroadcastJSON json.RawMessage `json:"broadcast,omitempty"`
	BroadcastMode string          `json:"broadcast_mode,omitempty"`
}

// SnapshotManager records broadcast operations while an operation snapshot is
// active and replays them to devices/rooms that join after the fact.
type SnapshotManager struct {
	repo         *data.SnapshotRepo
	settings     *ServerSettings
	devices      *data.DeviceRepo
	hub          *biz.Hub
	dispatcher   *biz.CommandDispatcher
	distribution *DistributionManager
	power        *PowerHandler
	federation   *Federation
	started      bool
	mu           sync.Mutex
}

func NewSnapshotManager(repo *data.SnapshotRepo, settings *ServerSettings, devices *data.DeviceRepo, hub *biz.Hub, dispatcher *biz.CommandDispatcher) *SnapshotManager {
	return &SnapshotManager{repo: repo, settings: settings, devices: devices, hub: hub, dispatcher: dispatcher}
}

func snippet(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}

func (m *SnapshotManager) SetDistribution(distribution *DistributionManager) {
	m.distribution = distribution
}
func (m *SnapshotManager) SetPower(power *PowerHandler)         { m.power = power }
func (m *SnapshotManager) SetFederation(federation *Federation) { m.federation = federation }

// Start launches the periodic reconciliation loop. It is a no-op when the
// manager has not been configured (keeps tests and standalone usage safe).
func (m *SnapshotManager) Start(ctx context.Context) {
	if m.repo == nil || m.started {
		return
	}
	m.started = true
	go func() {
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.reconcile()
			}
		}
	}()
}

func (m *SnapshotManager) scope() (scope, scopeID string) {
	mode := m.settings.GetDeployment()
	if mode.Mode == "cloud" {
		return "cloud", "cloud"
	}
	return "local", mode.NodeID
}

// Active returns the currently active snapshot for this server, if any.
func (m *SnapshotManager) Active() *data.OperationSnapshot {
	if m.repo == nil {
		return nil
	}
	scope, _ := m.scope()
	snapshot, err := m.repo.ActiveSnapshot(scope)
	if err != nil {
		log.Printf("[snapshot] active: %v", err)
		return nil
	}
	return snapshot
}

// RecordLocal records a whole-fleet operation and marks only the devices that
// have already received it. Callers must not pass devices whose delivery failed.
func (m *SnapshotManager) RecordLocal(kind, summary string, payload SnapshotPayload, delivered []int) {
	if m == nil || m.repo == nil || m.settings.GetDeployment().Mode == "cloud" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	opID := m.recordLocked("local", kind, summary, payload)
	if opID == 0 {
		return
	}
	for _, deviceID := range delivered {
		_ = m.repo.MarkDelivered(opID, "device", strconv.Itoa(deviceID))
	}
}

// ObserveFanout records a whole-fleet command, runs the send, then marks only
// the devices whose send succeeded. Replay cannot run in between.
func (m *SnapshotManager) ObserveFanout(kind, summary string, payload SnapshotPayload, fanout func() []int) {
	if m == nil || m.repo == nil || m.settings.GetDeployment().Mode == "cloud" {
		fanout()
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	opID := m.recordLocked("local", kind, summary, payload)
	for _, deviceID := range fanout() {
		if opID != 0 {
			_ = m.repo.MarkDelivered(opID, "device", strconv.Itoa(deviceID))
		}
	}
}

func (m *SnapshotManager) recordLocked(scope, kind, summary string, payload SnapshotPayload) int64 {
	snapshot, err := m.repo.ActiveSnapshot(scope)
	if err != nil || snapshot == nil {
		return 0
	}
	payload.Kind = kind
	raw, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[snapshot] marshal %s op: %v", scope, err)
		return 0
	}
	opID, err := m.repo.AddOp(snapshot.ID, kind, string(raw), summary)
	if err != nil {
		log.Printf("[snapshot] record %s op: %v", scope, err)
		return 0
	}
	m.hub.BroadcastAdminEvent("snapshot_updated", map[string]interface{}{"snapshot_id": snapshot.ID})
	return opID
}

// RecordCloud records a broadcast operation issued by the cloud admin and marks
// the explicitly targeted rooms as already delivered.
func (m *SnapshotManager) RecordCloud(kind, summary string, payload SnapshotPayload) {
	if m == nil || m.repo == nil || m.settings.GetDeployment().Mode != "cloud" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	opID := m.recordLocked("cloud", kind, summary, payload)
	// These rooms already have a durable job from the publish that just
	// committed. Marking them keeps replay from enqueueing a second copy.
	// Rooms that did not exist yet are absent here and are caught up on connect.
	for _, room := range payload.Rooms {
		if opID != 0 {
			_ = m.repo.MarkDelivered(opID, "room", room)
		}
	}
}

// ReplayToDevice applies every undelivered local snapshot operation to a device
// that just connected (or is being reconciled).
func (m *SnapshotManager) ReplayToDevice(deviceID int) {
	if m.repo == nil || !m.hub.IsOnline(deviceID) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot, err := m.repo.ActiveSnapshot("local")
	if err != nil || snapshot == nil {
		return
	}
	ops, err := m.repo.ListOps(snapshot.ID)
	if err != nil {
		return
	}
	for _, op := range ops {
		delivered, _ := m.repo.IsDelivered(op.ID, "device", strconv.Itoa(deviceID))
		if delivered {
			continue
		}
		if err := m.executeLocal(op, deviceID); err != nil {
			if !errors.Is(err, errSnapshotDeferred) {
				log.Printf("[snapshot] replay op %d to device %d: %v", op.ID, deviceID, err)
			}
			continue
		}
		_ = m.repo.MarkDelivered(op.ID, "device", strconv.Itoa(deviceID))
	}
}

// ReplayToRoom applies every undelivered cloud snapshot operation to a room
// that just (re)connected. The room re-broadcasts it to all of its devices and
// records it in its own local snapshot, so late devices are covered as well.
func (m *SnapshotManager) ReplayToRoom(roomID string) {
	if m.repo == nil || m.federation == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot, err := m.repo.ActiveSnapshot("cloud")
	if err != nil || snapshot == nil {
		return
	}
	ops, err := m.repo.ListOps(snapshot.ID)
	if err != nil {
		return
	}
	for _, op := range ops {
		delivered, _ := m.repo.IsDelivered(op.ID, "room", roomID)
		if delivered {
			continue
		}
		if err := m.federation.enqueueSnapshotOp(roomID, op); err != nil {
			log.Printf("[snapshot] replay op %d to room %s: %v", op.ID, roomID, err)
			continue
		}
		_ = m.repo.MarkDelivered(op.ID, "room", roomID)
	}
}

func (m *SnapshotManager) reconcile() {
	if m.repo == nil {
		return
	}
	if m.settings.GetDeployment().Mode == "cloud" {
		if m.federation == nil {
			return
		}
		for _, roomID := range m.federation.onlineRooms() {
			m.ReplayToRoom(roomID)
		}
		return
	}
	for _, deviceID := range m.hub.OnlineIDs() {
		m.ReplayToDevice(deviceID)
	}
}

func (m *SnapshotManager) executeLocal(op data.SnapshotOp, deviceID int) error {
	if m.dispatcher == nil {
		return fmt.Errorf("dispatcher unavailable")
	}
	var payload SnapshotPayload
	if err := json.Unmarshal([]byte(op.Payload), &payload); err != nil {
		return err
	}
	executedBy := "snapshot#" + strconv.FormatInt(op.SnapshotID, 10)
	switch payload.Kind {
	case "command":
		target := deviceID
		return m.dispatcher.CreateAndDispatch(&model.CommandLog{
			TargetType: "single", TargetID: &target, Command: payload.Command,
			Status: model.CommandStatusPending, ExecutedBy: executedBy,
		})
	case "wol":
		device, err := m.devices.GetByAssignedID(deviceID)
		if err != nil || device == nil || device.MacAddress == "" {
			return fmt.Errorf("设备缺少 MAC 地址")
		}
		return sendMagicPacket(device.MacAddress)
	case "distribute":
		if m.distribution == nil {
			return fmt.Errorf("分发服务不可用")
		}
		files := make([]string, 0, len(payload.Files))
		for _, file := range payload.Files {
			files = append(files, file.Name)
		}
		_, err := m.distribution.StartTask(files, payload.SaveDir, []int{deviceID}, "", payload.PostCmd)
		return err
	case "schedule":
		return m.executeSchedule(payload, deviceID, executedBy)
	}
	return fmt.Errorf("未知操作类型 %q", payload.Kind)
}

func (m *SnapshotManager) executeSchedule(payload SnapshotPayload, deviceID int, executedBy string) error {
	runAt, err := parseScheduleTime(payload.RunAt)
	if err != nil {
		return err
	}
	if runAt.After(time.Now()) {
		if m.power == nil {
			return fmt.Errorf("电源服务不可用")
		}
		covered, err := m.power.scheduleRepo.HasPendingFleet(payload.Action, runAt)
		if err != nil {
			return err
		}
		if covered {
			return errSnapshotDeferred
		}
		return m.power.scheduleRepo.Create(&data.PowerSchedule{
			Action: payload.Action, RunAt: runAt.Format(time.RFC3339),
			TargetType: "list", TargetIDs: []int{deviceID}, Note: payload.Note, CreatedBy: executedBy,
		})
	}
	if payload.Action == PowerActionWOL {
		device, err := m.devices.GetByAssignedID(deviceID)
		if err != nil || device == nil || device.MacAddress == "" {
			return fmt.Errorf("设备缺少 MAC 地址")
		}
		return sendMagicPacket(device.MacAddress)
	}
	command := shutdownCommand
	if payload.Action == PowerActionReboot {
		command = rebootCommand
	}
	if payload.Action != PowerActionShutdown && payload.Action != PowerActionReboot {
		return fmt.Errorf("未知电源操作 %q", payload.Action)
	}
	target := deviceID
	return m.dispatcher.CreateAndDispatch(&model.CommandLog{
		TargetType: "single", TargetID: &target, Command: command,
		Status: model.CommandStatusPending, ExecutedBy: executedBy,
	})
}

// NoteScheduleApplied marks devices that just received a fired fleet schedule.
// Devices that were offline stay unmarked so a later connect can catch up once.
func (m *SnapshotManager) NoteScheduleApplied(action, runAt string, deviceIDs []int) {
	if m == nil || m.repo == nil || m.settings.GetDeployment().Mode == "cloud" || len(deviceIDs) == 0 {
		return
	}
	when, err := parseScheduleTime(runAt)
	if err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot, err := m.repo.ActiveSnapshot("local")
	if err != nil || snapshot == nil {
		return
	}
	ops, err := m.repo.ListOps(snapshot.ID)
	if err != nil {
		return
	}
	for _, op := range ops {
		if op.Kind != "schedule" {
			continue
		}
		var payload SnapshotPayload
		if json.Unmarshal([]byte(op.Payload), &payload) != nil || payload.Action != action {
			continue
		}
		opWhen, err := parseScheduleTime(payload.RunAt)
		if err != nil || !opWhen.Equal(when) {
			continue
		}
		for _, deviceID := range deviceIDs {
			_ = m.repo.MarkDelivered(op.ID, "device", strconv.Itoa(deviceID))
		}
	}
}

// ---- HTTP API ----

type snapshotStatusResponse struct {
	Active     *data.OperationSnapshot  `json:"active"`
	History    []data.OperationSnapshot `json:"history"`
	ServerMode string                   `json:"scope"`
}

func (m *SnapshotManager) List(w http.ResponseWriter, r *http.Request) {
	if m.repo == nil {
		writeJSON(w, http.StatusOK, snapshotStatusResponse{History: []data.OperationSnapshot{}})
		return
	}
	scope, _ := m.scope()
	active, _ := m.repo.ActiveSnapshot(scope)
	history, err := m.repo.ListSnapshots(scope, 20)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, snapshotStatusResponse{Active: active, History: history, ServerMode: scope})
}

func (m *SnapshotManager) StartSnapshot(w http.ResponseWriter, r *http.Request) {
	if m.repo == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "快照服务不可用"})
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body) != nil || strings.TrimSpace(body.Name) == "" || len(body.Name) > 100 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请填写快照名称（最多 100 字）"})
		return
	}
	scope, scopeID := m.scope()
	snapshot, err := m.repo.StartSnapshot(strings.TrimSpace(body.Name), scope, scopeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	m.hub.BroadcastAdminEvent("snapshot_updated", map[string]interface{}{"snapshot_id": snapshot.ID})
	writeJSON(w, http.StatusCreated, snapshot)
}

func (m *SnapshotManager) EndSnapshot(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	if err := m.repo.EndSnapshot(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	m.hub.BroadcastAdminEvent("snapshot_updated", map[string]interface{}{"snapshot_id": id})
	writeJSON(w, http.StatusOK, map[string]string{"message": "快照已结束"})
}

func (m *SnapshotManager) DeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	if err := m.repo.DeleteSnapshot(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	m.hub.BroadcastAdminEvent("snapshot_updated", map[string]interface{}{"snapshot_id": id})
	writeJSON(w, http.StatusOK, map[string]string{"message": "快照已删除"})
}

func (m *SnapshotManager) Ops(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	scope, _ := m.scope()
	snapshot, err := m.repo.GetSnapshot(id)
	if err != nil || snapshot == nil || snapshot.Scope != scope {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "快照不存在"})
		return
	}
	ops, err := m.repo.ListOps(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ops)
}

func (m *SnapshotManager) DeleteOp(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	opID, err := strconv.ParseInt(r.PathValue("opID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid op id"})
		return
	}
	if err := m.repo.DeleteOp(id, opID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	m.hub.BroadcastAdminEvent("snapshot_updated", map[string]interface{}{"snapshot_id": id})
	writeJSON(w, http.StatusOK, map[string]string{"message": "操作已从队列移除"})
}
