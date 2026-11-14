package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/model"
)

// errSnapshotDeferred means the operation is still covered by something that
// has not happened yet. The device must not be marked delivered.
var errSnapshotDeferred = errors.New("snapshot operation not applied yet")

type cloudSnapshotCtxKey struct{}

type cloudSnapshotMeta struct {
	SnapshotID   int64
	SnapshotName string
	OpID         int64
	Suppress     bool
}

func withCloudSnapshotOp(ctx context.Context, snapshotID int64, snapshotName string, opID int64) context.Context {
	return context.WithValue(ctx, cloudSnapshotCtxKey{}, &cloudSnapshotMeta{
		SnapshotID:   snapshotID,
		SnapshotName: snapshotName,
		OpID:         opID,
	})
}

func withSuppressSnapshot(ctx context.Context) context.Context {
	return context.WithValue(ctx, cloudSnapshotCtxKey{}, &cloudSnapshotMeta{Suppress: true})
}

func cloudSnapshotFromContext(ctx context.Context) *cloudSnapshotMeta {
	if ctx == nil {
		return nil
	}
	meta, _ := ctx.Value(cloudSnapshotCtxKey{}).(*cloudSnapshotMeta)
	return meta
}

// CloudSnapshotSync is sent over the federation WebSocket so Relay rooms mirror
// the Cloud's active snapshot as their remote base sequence.
type CloudSnapshotSync struct {
	Active *data.RemoteSnapshotState `json:"active"`
}

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
	repo          *data.SnapshotRepo
	settings      *ServerSettings
	devices       *data.DeviceRepo
	hub           *biz.Hub
	dispatcher    *biz.CommandDispatcher
	distribution  *DistributionManager
	power         *PowerHandler
	federation    *Federation
	broadcastRepo *data.BroadcastRepo
	started       atomic.Bool
	mu            sync.Mutex
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
func (m *SnapshotManager) SetPower(power *PowerHandler)                   { m.power = power }
func (m *SnapshotManager) SetFederation(federation *Federation)           { m.federation = federation }
func (m *SnapshotManager) SetBroadcastRepo(broadcast *data.BroadcastRepo) { m.broadcastRepo = broadcast }

// Start launches the periodic reconciliation loop. It is a no-op when the
// manager has not been configured (keeps tests and standalone usage safe).
func (m *SnapshotManager) Start(ctx context.Context) {
	if m.repo == nil || !m.started.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer m.started.Store(false)
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

func (m *SnapshotManager) nudgeReconcile() {
	if m != nil && m.started.Load() {
		go m.reconcile()
	}
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
	m.RecordLocalContext(context.Background(), kind, summary, payload, delivered)
}

// RecordLocalContext is Context-aware so Cloud-relayed snapshot jobs mark the
// corresponding synced remote op rather than appending a duplicate local op.
func (m *SnapshotManager) RecordLocalContext(ctx context.Context, kind, summary string, payload SnapshotPayload, delivered []int) {
	if m == nil || m.repo == nil || m.settings.GetDeployment().Mode == "cloud" {
		return
	}
	meta := cloudSnapshotFromContext(ctx)
	if meta != nil && meta.Suppress {
		return
	}
	if meta != nil && meta.OpID > 0 {
		m.recordRemoteDelivery(meta, kind, summary, payload, delivered)
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
	m.ObserveFanoutContext(context.Background(), kind, summary, payload, fanout)
}

// ObserveFanoutContext is the Context-aware variant of ObserveFanout.
func (m *SnapshotManager) ObserveFanoutContext(ctx context.Context, kind, summary string, payload SnapshotPayload, fanout func() []int) {
	if m == nil || m.repo == nil || m.settings.GetDeployment().Mode == "cloud" {
		fanout()
		return
	}
	meta := cloudSnapshotFromContext(ctx)
	if meta != nil && meta.Suppress {
		fanout()
		return
	}
	if meta != nil && meta.OpID > 0 {
		m.mu.Lock()
		delivered := fanout()
		m.recordRemoteDeliveryLocked(meta, kind, summary, payload, delivered)
		m.mu.Unlock()
		m.nudgeReconcile()
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

func (m *SnapshotManager) recordRemoteDelivery(meta *cloudSnapshotMeta, kind, summary string, payload SnapshotPayload, delivered []int) {
	m.mu.Lock()
	m.recordRemoteDeliveryLocked(meta, kind, summary, payload, delivered)
	m.mu.Unlock()
	m.nudgeReconcile()
}

func (m *SnapshotManager) recordRemoteDeliveryLocked(meta *cloudSnapshotMeta, kind, summary string, payload SnapshotPayload, delivered []int) {
	payload.Kind = kind
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, scopeID := m.scope()
	localOpID, err := m.repo.RecordRemoteDeliveryOnRoom(scopeID, meta.SnapshotID, meta.SnapshotName, meta.OpID, kind, string(raw), summary, delivered)
	if err != nil {
		log.Printf("[snapshot] record remote delivery op=%d: %v", meta.OpID, err)
		return
	}
	if localOpID != 0 {
		m.hub.BroadcastAdminEvent("snapshot_updated", map[string]interface{}{"op_id": localOpID})
	}
}

// MarkRemoteOpSkipOnline marks a Cloud snapshot op as ready on this room while
// marking all currently online devices as already delivered.
func (m *SnapshotManager) MarkRemoteOpSkipOnline(snapshotID int64, snapshotName string, remoteOpID int64, kind, summary string, payload SnapshotPayload) {
	if m == nil || m.repo == nil {
		return
	}
	online := m.hub.OnlineIDs()
	m.recordRemoteDelivery(&cloudSnapshotMeta{
		SnapshotID:   snapshotID,
		SnapshotName: snapshotName,
		OpID:         remoteOpID,
	}, kind, summary, payload, online)
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

// RecordCloud records a broadcast operation issued by the cloud admin, marks
// the explicitly targeted rooms as already delivered, and returns the snapshot
// metadata to attach to the outgoing FederationMessages.
func (m *SnapshotManager) RecordCloud(kind, summary string, payload SnapshotPayload) (snapshotID int64, snapshotName string, opID int64) {
	if m == nil || m.repo == nil || m.settings.GetDeployment().Mode != "cloud" {
		return 0, "", 0
	}
	m.mu.Lock()
	snapshot, err := m.repo.ActiveSnapshot("cloud")
	if err != nil || snapshot == nil {
		m.mu.Unlock()
		return 0, "", 0
	}
	payload.Kind = kind
	raw, err := json.Marshal(payload)
	if err != nil {
		m.mu.Unlock()
		log.Printf("[snapshot] marshal cloud op: %v", err)
		return 0, "", 0
	}
	createdID, err := m.repo.AddOp(snapshot.ID, kind, string(raw), summary)
	if err != nil {
		m.mu.Unlock()
		log.Printf("[snapshot] record cloud op: %v", err)
		return 0, "", 0
	}
	// These rooms already have a durable job from the publish that is committing.
	// Marking them keeps replay from enqueueing a second copy.
	for _, room := range payload.Rooms {
		_ = m.repo.MarkDelivered(createdID, "room", room)
	}
	m.hub.BroadcastAdminEvent("snapshot_updated", map[string]interface{}{"snapshot_id": snapshot.ID})
	m.mu.Unlock()
	return snapshot.ID, snapshot.Name, createdID
}

// BuildCloudSync serializes the Cloud's active snapshot and ordered operations.
func (m *SnapshotManager) BuildCloudSync() ([]byte, error) {
	if m == nil || m.repo == nil {
		return json.Marshal(CloudSnapshotSync{})
	}
	active, err := m.repo.ActiveSnapshot("cloud")
	if err != nil {
		return nil, err
	}
	if active == nil {
		return json.Marshal(CloudSnapshotSync{Active: nil})
	}
	ops, err := m.repo.ListOps(active.ID)
	if err != nil {
		return nil, err
	}
	remoteOps := make([]data.RemoteSnapshotOp, 0, len(ops))
	for _, op := range ops {
		remoteOps = append(remoteOps, data.RemoteSnapshotOp{
			ID:        op.ID,
			Kind:      op.Kind,
			Payload:   op.Payload,
			Summary:   op.Summary,
			CreatedAt: op.CreatedAt,
		})
	}
	return json.Marshal(CloudSnapshotSync{
		Active: &data.RemoteSnapshotState{
			ID:        active.ID,
			Name:      active.Name,
			CreatedAt: active.CreatedAt,
			Ops:       remoteOps,
		},
	})
}

// PushCloudSync broadcasts the Cloud's current snapshot state to all connected rooms.
func (m *SnapshotManager) PushCloudSync() {
	if m == nil || m.federation == nil || m.settings.GetDeployment().Mode != "cloud" {
		return
	}
	raw, err := m.BuildCloudSync()
	if err != nil {
		log.Printf("[snapshot] build cloud sync: %v", err)
		return
	}
	m.federation.BroadcastSnapshotSync(raw)
}

// PushCloudSyncToRoom sends the Cloud's current snapshot state to a single room.
func (m *SnapshotManager) PushCloudSyncToRoom(roomID string) {
	if m == nil || m.federation == nil || m.settings.GetDeployment().Mode != "cloud" {
		return
	}
	raw, err := m.BuildCloudSync()
	if err != nil {
		log.Printf("[snapshot] build cloud sync for room %s: %v", roomID, err)
		return
	}
	m.federation.SendSnapshotSync(roomID, raw)
}

// ApplyCloudSync merges an incoming Cloud snapshot state on a Relay room while
// keeping room-local inserted operations anchored in place.
func (m *SnapshotManager) ApplyCloudSync(raw []byte) {
	if m == nil || m.repo == nil || m.settings.GetDeployment().Mode != "relay" {
		return
	}
	var msg CloudSnapshotSync
	if err := json.Unmarshal(raw, &msg); err != nil {
		log.Printf("[snapshot] unmarshal cloud sync: %v", err)
		return
	}
	m.mu.Lock()
	_, scopeID := m.scope()
	snapID, err := m.repo.SyncRemoteSnapshot(scopeID, msg.Active)
	m.mu.Unlock()
	if err != nil {
		log.Printf("[snapshot] sync remote snapshot: %v", err)
		return
	}
	m.hub.BroadcastAdminEvent("snapshot_updated", map[string]interface{}{"snapshot_id": snapID})
	if snapID > 0 {
		m.nudgeReconcile()
	}
}

// ReplayToDevice applies every undelivered local snapshot operation to a device
// that just connected (or is being reconciled), strictly in sort_order.
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
		if !op.RoomReady {
			// Wait for the preceding Cloud op's room-level delivery/files so we
			// never run later steps out of order.
			break
		}
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
	snapshot, err := m.repo.ActiveSnapshot("cloud")
	if err != nil || snapshot == nil {
		m.mu.Unlock()
		return
	}
	ops, err := m.repo.ListOps(snapshot.ID)
	if err != nil {
		m.mu.Unlock()
		return
	}
	enqueued := false
	for _, op := range ops {
		delivered, _ := m.repo.IsDelivered(op.ID, "room", roomID)
		if delivered {
			continue
		}
		if err := m.federation.enqueueSnapshotOp(roomID, snapshot, op, false); err != nil {
			log.Printf("[snapshot] replay op %d to room %s: %v", op.ID, roomID, err)
			continue
		}
		_ = m.repo.MarkDelivered(op.ID, "room", roomID)
		enqueued = true
	}
	m.mu.Unlock()
	if enqueued {
		m.federation.nudgeJobs()
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
	case "broadcast":
		return m.executeBroadcast(payload, deviceID, executedBy)
	}
	return fmt.Errorf("未知操作类型 %q", payload.Kind)
}

func (m *SnapshotManager) executeBroadcast(payload SnapshotPayload, deviceID int, executedBy string) error {
	action := payload.Action
	mode := payload.BroadcastMode
	if action == "" && len(payload.BroadcastJSON) > 0 {
		var pub struct {
			Action string `json:"action"`
			Mode   string `json:"mode"`
		}
		if json.Unmarshal(payload.BroadcastJSON, &pub) == nil {
			action = pub.Action
			if mode == "" {
				mode = pub.Mode
			}
		}
	}
	command := ""
	switch action {
	case "stop":
		command = "full-firefox kill"
	case "start":
		baseURL := ""
		if m.broadcastRepo != nil {
			baseURL, _ = m.broadcastRepo.GetConfig("base_url")
		}
		baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
		if baseURL == "" {
			baseURL = "http://icpc-server.local:8080"
		}
		if !validBroadcastMode(mode) {
			mode = "before"
		}
		address := baseURL + "/broadcast/" + mode
		command = "full-firefox kill; full-firefox '" + strings.ReplaceAll(address, "'", "'\"'\"'") + "'"
	default:
		return nil
	}
	target := deviceID
	return m.dispatcher.CreateAndDispatch(&model.CommandLog{
		TargetType: "single", TargetID: &target, Command: command,
		Status: model.CommandStatusPending, ExecutedBy: executedBy,
	})
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
	Relay      bool                     `json:"relay"`
}

func (m *SnapshotManager) List(w http.ResponseWriter, r *http.Request) {
	if m.repo == nil {
		writeJSON(w, http.StatusOK, snapshotStatusResponse{History: []data.OperationSnapshot{}})
		return
	}
	scope, _ := m.scope()
	mode := m.settings.GetDeployment().Mode
	active, _ := m.repo.ActiveSnapshot(scope)
	history, err := m.repo.ListSnapshots(scope, 20)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, snapshotStatusResponse{
		Active:     active,
		History:    history,
		ServerMode: scope,
		Relay:      mode == "relay",
	})
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
	m.mu.Lock()
	scope, scopeID := m.scope()
	if m.settings.GetDeployment().Mode == "relay" {
		if active, _ := m.repo.ActiveSnapshot("local"); active != nil && active.RemoteID > 0 {
			m.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]string{"error": "当前机房已自动跟随云端快照，可直接在此快照中插入本机房操作"})
			return
		}
	}
	snapshot, err := m.repo.StartSnapshot(strings.TrimSpace(body.Name), scope, scopeID)
	m.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	m.hub.BroadcastAdminEvent("snapshot_updated", map[string]interface{}{"snapshot_id": snapshot.ID})
	if scope == "cloud" {
		m.PushCloudSync()
	}
	writeJSON(w, http.StatusCreated, snapshot)
}

func (m *SnapshotManager) EndSnapshot(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	m.mu.Lock()
	scope, _ := m.scope()
	snapshot, err := m.repo.GetSnapshot(id)
	if err != nil || snapshot == nil || snapshot.Scope != scope {
		m.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "快照不存在"})
		return
	}
	if m.settings.GetDeployment().Mode == "relay" && snapshot.RemoteID > 0 {
		m.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "云端同步快照由云端统一结束；如需跳过单项操作可在列表中移除"})
		return
	}
	err = m.repo.EndSnapshot(id)
	m.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	m.hub.BroadcastAdminEvent("snapshot_updated", map[string]interface{}{"snapshot_id": id})
	if scope == "cloud" {
		m.PushCloudSync()
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "快照已结束"})
}

func (m *SnapshotManager) DeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	m.mu.Lock()
	scope, _ := m.scope()
	snapshot, err := m.repo.GetSnapshot(id)
	if err != nil || snapshot == nil || snapshot.Scope != scope {
		m.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "快照不存在"})
		return
	}
	if snapshot.Active && m.settings.GetDeployment().Mode == "relay" && snapshot.RemoteID > 0 {
		m.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "当前正在跟随云端快照，请先在云端结束或删除"})
		return
	}
	ops, _ := m.repo.ListOps(id)
	err = m.repo.DeleteSnapshot(id)
	m.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if scope == "cloud" {
		if m.federation != nil {
			for _, op := range ops {
				m.federation.cancelQueuedSnapshotJobs(op.ID)
			}
		}
		if snapshot.Active {
			m.PushCloudSync()
		}
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

// AddCustomOp handles POST /api/snapshots/{id}/ops — manually adding or inserting
// an operation at a chosen position in the snapshot queue.
func (m *SnapshotManager) AddCustomOp(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	var body struct {
		Kind       string   `json:"kind"`
		Command    string   `json:"command"`
		Files      []string `json:"files"`
		SaveDir    string   `json:"save_dir"`
		PostCmd    string   `json:"post_cmd"`
		Action     string   `json:"action"`
		RunAt      string   `json:"run_at"`
		Note       string   `json:"note"`
		Summary    string   `json:"summary"`
		AfterOpID  *int64   `json:"after_op_id"`
		SkipOnline bool     `json:"skip_online"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式无效"})
		return
	}

	storeKind, payload, summary, errMsg := m.buildCustomOp(body.Kind, body.Command, body.Files, body.SaveDir, body.PostCmd, body.Action, body.RunAt, body.Note, body.Summary)
	if errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	m.mu.Lock()
	scope, _ := m.scope()
	snapshot, err := m.repo.GetSnapshot(id)
	if err != nil || snapshot == nil || snapshot.Scope != scope {
		m.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "快照不存在"})
		return
	}
	if !snapshot.Active {
		m.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "已结束的快照不能继续添加操作"})
		return
	}

	opID, err := m.repo.InsertOp(snapshot.ID, storeKind, string(rawPayload), summary, body.AfterOpID)
	if err != nil {
		m.mu.Unlock()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	created, _ := m.repo.GetOp(snapshot.ID, opID)

	if scope == "local" && body.SkipOnline {
		for _, devID := range m.hub.OnlineIDs() {
			_ = m.repo.MarkDelivered(opID, "device", strconv.Itoa(devID))
		}
		if created != nil {
			created, _ = m.repo.GetOp(snapshot.ID, opID)
		}
	}
	m.mu.Unlock()

	if scope == "cloud" {
		m.PushCloudSync()
		if m.federation != nil && created != nil {
			if body.SkipOnline {
				m.mu.Lock()
				for _, roomID := range m.federation.onlineRooms() {
					if err := m.federation.enqueueSnapshotOp(roomID, snapshot, *created, true); err == nil {
						_ = m.repo.MarkDelivered(opID, "room", roomID)
					}
				}
				m.mu.Unlock()
				m.federation.nudgeJobs()
			} else {
				rooms, _ := m.federation.allRoomIDs()
				for _, roomID := range rooms {
					m.ReplayToRoom(roomID)
				}
			}
		}
	} else if !body.SkipOnline {
		m.reconcile()
	}

	m.hub.BroadcastAdminEvent("snapshot_updated", map[string]interface{}{"snapshot_id": snapshot.ID})
	writeJSON(w, http.StatusCreated, created)
}

func (m *SnapshotManager) buildCustomOp(kind, command string, files []string, saveDir, postCmd, action, runAtStr, note, customSummary string) (string, SnapshotPayload, string, string) {
	customSummary = strings.TrimSpace(customSummary)
	switch kind {
	case "command":
		cmd := strings.TrimSpace(command)
		if cmd == "" || len(cmd) > 64<<10 {
			return "", SnapshotPayload{}, "", "请填写要执行的命令（最多 64 KB）"
		}
		sum := customSummary
		if sum == "" {
			sum = "执行命令：" + snippet(cmd, 80)
		}
		return "command", SnapshotPayload{Kind: "command", Command: cmd}, sum, ""

	case "network_apply":
		cmd := buildApplyCommand(m.settings.GetNetworkRules())
		sum := customSummary
		if sum == "" {
			sum = "启用网络白名单"
		}
		return "command", SnapshotPayload{Kind: "command", Command: cmd}, sum, ""

	case "network_remove":
		cmd := buildRemoveCommand()
		sum := customSummary
		if sum == "" {
			sum = "解除网络限制"
		}
		return "command", SnapshotPayload{Kind: "command", Command: cmd}, sum, ""

	case "wol":
		sum := customSummary
		if sum == "" {
			sum = "批量唤醒"
		}
		return "wol", SnapshotPayload{Kind: "wol"}, sum, ""

	case "schedule":
		if action != PowerActionWOL && action != PowerActionReboot && action != PowerActionShutdown {
			return "", SnapshotPayload{}, "", "请选择有效的电源动作（唤醒 / 重启 / 关机）"
		}
		runAt, err := parseScheduleTime(runAtStr)
		if err != nil || !runAt.After(time.Now()) {
			return "", SnapshotPayload{}, "", "请选择未来的执行时间"
		}
		formatted := runAt.Format(time.RFC3339)
		sum := customSummary
		if sum == "" {
			sum = "电源计划：" + action + " @ " + formatted
		}
		return "schedule", SnapshotPayload{Kind: "schedule", Action: action, RunAt: formatted, Note: strings.TrimSpace(note)}, sum, ""

	case "distribute":
		if m.distribution == nil {
			return "", SnapshotPayload{}, "", "分发服务不可用"
		}
		if len(files) == 0 {
			return "", SnapshotPayload{}, "", "请至少选择一个要分发的文件"
		}
		dir := strings.TrimSpace(saveDir)
		if dir == "" {
			dir = "/home/cwxu/桌面"
		}
		if !strings.HasPrefix(dir, "/") {
			return "", SnapshotPayload{}, "", "保存目录必须是绝对路径"
		}
		manifest := make([]RelayFile, 0, len(files))
		seen := make(map[string]bool, len(files))
		names := make([]string, 0, len(files))
		for _, name := range files {
			if name != filepath.Base(name) || strings.HasPrefix(name, ".") {
				return "", SnapshotPayload{}, "", "非法文件名：" + name
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			fullPath := filepath.Join(m.distribution.uploadDir, name)
			info, err := os.Lstat(fullPath)
			if err != nil || !info.Mode().IsRegular() {
				return "", SnapshotPayload{}, "", "文件库中不存在文件：" + name
			}
			digest, err := fileSHA256(fullPath)
			if err != nil {
				return "", SnapshotPayload{}, "", "计算文件校验失败：" + err.Error()
			}
			manifest = append(manifest, RelayFile{Name: name, SHA256: digest, Size: info.Size()})
			names = append(names, name)
		}
		sum := customSummary
		if sum == "" {
			sum = "分发文件：" + snippet(strings.Join(names, ", "), 80)
		}
		return "distribute", SnapshotPayload{Kind: "distribute", Files: manifest, SaveDir: dir, PostCmd: strings.TrimSpace(postCmd)}, sum, ""
	}
	return "", SnapshotPayload{}, "", "不支持的操作类型"
}

// ReorderOps handles PUT /api/snapshots/{id}/ops/reorder.
func (m *SnapshotManager) ReorderOps(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	var body struct {
		OpIDs []int64 `json:"op_ids"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body) != nil || len(body.OpIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请提供有效的操作顺序"})
		return
	}

	m.mu.Lock()
	scope, _ := m.scope()
	snapshot, err := m.repo.GetSnapshot(id)
	if err != nil || snapshot == nil || snapshot.Scope != scope {
		m.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "快照不存在"})
		return
	}
	lockRemote := scope == "local" && snapshot.RemoteID > 0
	err = m.repo.ReorderOps(id, body.OpIDs, lockRemote)
	m.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if scope == "cloud" && snapshot.Active {
		m.PushCloudSync()
	}
	m.hub.BroadcastAdminEvent("snapshot_updated", map[string]interface{}{"snapshot_id": id})
	writeJSON(w, http.StatusOK, map[string]string{"message": "顺序已更新"})
}

func (m *SnapshotManager) DeleteOp(w http.ResponseWriter, r *http.Request) {
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
	m.mu.Lock()
	scope, _ := m.scope()
	snapshot, err := m.repo.GetSnapshot(id)
	if err != nil || snapshot == nil || snapshot.Scope != scope {
		m.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "快照不存在"})
		return
	}
	err = m.repo.DeleteOp(id, opID)
	m.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if scope == "cloud" {
		if m.federation != nil {
			m.federation.cancelQueuedSnapshotJobs(opID)
		}
		if snapshot.Active {
			m.PushCloudSync()
		}
	}
	m.hub.BroadcastAdminEvent("snapshot_updated", map[string]interface{}{"snapshot_id": id})
	writeJSON(w, http.StatusOK, map[string]string{"message": "操作已从队列移除"})
}

// RestoreRemoteOps handles POST /api/snapshots/{id}/ops/restore-remote.
func (m *SnapshotManager) RestoreRemoteOps(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	m.mu.Lock()
	scope, _ := m.scope()
	snapshot, err := m.repo.GetSnapshot(id)
	if err != nil || snapshot == nil || snapshot.Scope != scope {
		m.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "快照不存在"})
		return
	}
	err = m.repo.RestoreSkippedRemoteOps(id)
	m.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	m.hub.BroadcastAdminEvent("snapshot_updated", map[string]interface{}{"snapshot_id": id})
	if snapshot.Active {
		m.reconcile()
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "已恢复跳过的远端操作"})
}
