package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ICPCRemoteControl/internal/data"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/model"
	"github.com/google/uuid"
)

type scriptedClient struct {
	id     int
	client *biz.ClientConn
	reader *bufio.Reader
	lines  chan string
}

func (client *scriptedClient) collect() {
	for {
		line, err := client.reader.ReadString('\n')
		if err != nil {
			return
		}
		client.lines <- line
	}
}

func registerScripted(test *testing.T, hub *biz.Hub, id int, read bool) *scriptedClient {
	test.Helper()
	left, right := net.Pipe()
	test.Cleanup(func() { left.Close(); right.Close() })
	client := &scriptedClient{
		id: id,
		client: &biz.ClientConn{
			AssignedID: id,
			Conn:       right,
			Send:       make(chan []byte, 64),
			Hub:        hub,
		},
		reader: bufio.NewReader(left),
		lines:  make(chan string, 8),
	}
	if read {
		go client.collect()
	}
	hub.Register(client.client)
	waitFor(test, func() bool { return hub.IsOnline(id) })
	return client
}

func (client *scriptedClient) waitCommand(test *testing.T, command string) int {
	test.Helper()
	deadline := time.After(3 * time.Second)
	count := 0
	for {
		select {
		case line := <-client.lines:
			if bytes.Contains([]byte(line), []byte(`"command":"`+command+`"`)) {
				count++
			}
		case <-deadline:
			return count
		}
	}
}

func snapshotStack(test *testing.T) (*Federation, *SnapshotManager, *CommandHandler) {
	test.Helper()
	federation := testFederation(test)
	repository := data.NewCommandRepo(federation.db)
	dispatcher := biz.NewCommandDispatcher(federation.hub, repository)
	manager := NewSnapshotManager(data.NewSnapshotRepo(federation.db), federation.settings, federation.devices, federation.hub, dispatcher)
	handler := NewCommandHandler(repository, dispatcher, federation.hub, federation.settings)
	handler.SetSnapshotManager(manager)
	federation.hub.SetConnectHook(manager.ReplayToDevice)
	if _, err := manager.repo.StartSnapshot("校赛", "local", "local"); err != nil {
		test.Fatal(err)
	}
	return federation, manager, handler
}

func postCommand(test *testing.T, handler *CommandHandler, body string) *model.CommandLog {
	test.Helper()
	response := httptest.NewRecorder()
	handler.Execute(response, httptest.NewRequest("POST", "/api/commands", bytes.NewReader([]byte(body))))
	if response.Code != http.StatusCreated {
		test.Fatalf("execute %d: %s", response.Code, response.Body.String())
	}
	var command model.CommandLog
	if err := json.Unmarshal(response.Body.Bytes(), &command); err != nil {
		test.Fatal(err)
	}
	return &command
}

func TestSnapshotReplaysWholeFleetCommandOnceToLateDevice(test *testing.T) {
	_, manager, handler := snapshotStack(test)
	first := registerScripted(test, manager.hub, 1, true)
	postCommand(test, handler, `{"target_type":"broadcast","command":"hostname"}`)
	if got := first.waitCommand(test, "hostname"); got != 1 {
		test.Fatalf("online device received hostname %d times", got)
	}
	second := registerScripted(test, manager.hub, 2, true)
	if got := second.waitCommand(test, "hostname"); got != 1 {
		test.Fatalf("late device received hostname %d times", got)
	}
	opID := mustOnlyOp(test, manager)
	waitFor(test, func() bool {
		delivered, err := manager.repo.IsDelivered(opID, "device", "2")
		return err == nil && delivered
	})
	manager.ReplayToDevice(2)
	if got := second.waitCommand(test, "hostname"); got != 0 {
		test.Fatalf("successful replay ran again: %d", got)
	}
}

func TestSnapshotDoesNotReplayPartialCommand(test *testing.T) {
	_, manager, handler := snapshotStack(test)
	registerScripted(test, manager.hub, 1, true)
	postCommand(test, handler, `{"target_type":"list","target_ids":[1],"command":"only-one"}`)
	late := registerScripted(test, manager.hub, 2, true)
	manager.ReplayToDevice(2)
	if got := late.waitCommand(test, "only-one"); got != 0 {
		test.Fatalf("partial command replayed to a late device %d times", got)
	}
}

func TestSnapshotSkipsRemovedOpAndEndedSnapshot(test *testing.T) {
	_, manager, handler := snapshotStack(test)
	registerScripted(test, manager.hub, 1, true)
	postCommand(test, handler, `{"target_type":"broadcast","command":"kept"}`)
	ops, err := manager.repo.ListOps(manager.Active().ID)
	if err != nil || len(ops) != 1 {
		test.Fatalf("recorded ops: %+v %v", ops, err)
	}
	if err := manager.repo.DeleteOp(manager.Active().ID, ops[0].ID); err != nil {
		test.Fatal(err)
	}
	removed := registerScripted(test, manager.hub, 3, true)
	manager.ReplayToDevice(3)
	if got := removed.waitCommand(test, "kept"); got != 0 {
		test.Fatalf("removed op replayed: %d", got)
	}
	if err := manager.repo.EndSnapshot(manager.Active().ID); err != nil {
		test.Fatal(err)
	}
	postCommand(test, handler, `{"target_type":"broadcast","command":"after-end"}`)
	ended := registerScripted(test, manager.hub, 4, true)
	manager.ReplayToDevice(4)
	if got := ended.waitCommand(test, "after-end"); got != 0 {
		test.Fatalf("ended snapshot replayed: %d", got)
	}
}

func TestFailedCommandReplayStaysUndelivered(test *testing.T) {
	_, manager, handler := snapshotStack(test)
	manager.hub.SetConnectHook(nil)
	blocked := registerScripted(test, manager.hub, 7, false)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for len(blocked.client.Send) < cap(blocked.client.Send) {
			select {
			case blocked.client.Send <- []byte("pad\n"):
			default:
			}
		}
		time.Sleep(20 * time.Millisecond)
		if len(blocked.client.Send) == cap(blocked.client.Send) {
			break
		}
	}
	if len(blocked.client.Send) != cap(blocked.client.Send) {
		test.Fatal("send buffer did not stay full")
	}
	postCommand(test, handler, `{"target_type":"broadcast","command":"needs-retry"}`)
	delivered, err := manager.repo.IsDelivered(mustOnlyOp(test, manager), "device", "7")
	if err != nil || delivered {
		test.Fatalf("failed send marked delivered: %v %v", delivered, err)
	}
	manager.hub.Unregister(blocked.client)
	waitFor(test, func() bool { return !manager.hub.IsOnline(7) })
	retried := registerScripted(test, manager.hub, 7, true)
	manager.ReplayToDevice(7)
	if got := retried.waitCommand(test, "needs-retry"); got != 1 {
		test.Fatalf("retry delivered %d times", got)
	}
	manager.ReplayToDevice(7)
	if got := retried.waitCommand(test, "needs-retry"); got != 0 {
		test.Fatalf("retry duplicated: %d", got)
	}
}

func TestFutureWakeScheduleIsNotRunDuringReplay(test *testing.T) {
	federation, manager, _ := snapshotStack(test)
	manager.hub.SetConnectHook(nil)
	manager.SetPower(NewPowerHandler(federation.devices, manager.dispatcher, data.NewPowerScheduleRepo(federation.db), federation.hub, federation.settings))
	if err := federation.devices.Exec(`INSERT INTO devices(assigned_id,hostname,mac_address) VALUES(5,'late','aa:bb:cc:dd:ee:ff')`); err != nil {
		test.Fatal(err)
	}
	runAt := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	manager.RecordLocal("schedule", "电源计划", SnapshotPayload{Action: PowerActionWOL, RunAt: runAt, Note: "later"}, nil)
	registerScripted(test, manager.hub, 5, true)
	manager.ReplayToDevice(5)
	var schedules int
	if err := federation.db.QueryRow(`SELECT COUNT(*) FROM power_schedules`).Scan(&schedules); err != nil {
		test.Fatal(err)
	}
	if schedules != 1 {
		test.Fatalf("future wake was not kept as a schedule: %d", schedules)
	}
	var commands int
	if err := federation.db.QueryRow(`SELECT COUNT(*) FROM command_log WHERE command LIKE '%shutdown%'`).Scan(&commands); err != nil {
		test.Fatal(err)
	}
	if commands != 0 {
		test.Fatalf("wake replay issued a power command")
	}
}

func TestPartialBroadcastIsNotReplayedToOtherRooms(test *testing.T) {
	cloud := testFederation(test)
	if err := cloud.settings.SetDeployment(DeploymentConfig{Mode: "cloud", Token: "abcdefghijklmnopqrstuvwxyz012345", DeviceIDStart: 1}); err != nil {
		test.Fatal(err)
	}
	repository := data.NewCommandRepo(cloud.db)
	dispatcher := biz.NewCommandDispatcher(cloud.hub, repository)
	manager := NewSnapshotManager(data.NewSnapshotRepo(cloud.db), cloud.settings, cloud.devices, cloud.hub, dispatcher)
	manager.SetFederation(cloud)
	cloud.snapshots = manager
	setupBroadcastFederation(test, cloud)
	if _, err := manager.repo.StartSnapshot("云端", "cloud", "cloud"); err != nil {
		test.Fatal(err)
	}
	first := uuid.NewString()
	second := uuid.NewString()
	cloud.storeSnapshot(&RoomSnapshot{ID: first, Name: "A", Devices: []model.DeviceSummary{{AssignedID: 1}}})
	cloud.storeSnapshot(&RoomSnapshot{ID: second, Name: "B", Devices: []model.DeviceSummary{{AssignedID: 1}}})
	body, _ := json.Marshal(map[string]any{"room_ids": []string{first}, "action": "start", "mode": "before"})
	response := httptest.NewRecorder()
	cloud.PublishBroadcast(response, httptest.NewRequest("POST", "/api/cluster/broadcast", bytes.NewReader(body)))
	if response.Code != http.StatusAccepted {
		test.Fatalf("publish: %d %s", response.Code, response.Body.String())
	}
	manager.ReplayToRoom(second)
	var jobs int
	if err := cloud.db.QueryRow(`SELECT COUNT(*) FROM cluster_jobs WHERE room_id=?`, second).Scan(&jobs); err != nil {
		test.Fatal(err)
	}
	if jobs != 0 {
		test.Fatalf("partial broadcast expanded onto another room: %d jobs", jobs)
	}
	manager.ReplayToRoom(second)
	if err := cloud.db.QueryRow(`SELECT COUNT(*) FROM cluster_jobs WHERE room_id=?`, second).Scan(&jobs); err != nil {
		test.Fatal(err)
	}
	if jobs != 0 {
		test.Fatalf("second replay created jobs: %d", jobs)
	}
}

func TestFleetBroadcastReplaysOnceToANewRoom(test *testing.T) {
	cloud := testFederation(test)
	if err := cloud.settings.SetDeployment(DeploymentConfig{Mode: "cloud", Token: "abcdefghijklmnopqrstuvwxyz012345", DeviceIDStart: 1}); err != nil {
		test.Fatal(err)
	}
	repository := data.NewCommandRepo(cloud.db)
	dispatcher := biz.NewCommandDispatcher(cloud.hub, repository)
	manager := NewSnapshotManager(data.NewSnapshotRepo(cloud.db), cloud.settings, cloud.devices, cloud.hub, dispatcher)
	manager.SetFederation(cloud)
	cloud.snapshots = manager
	setupBroadcastFederation(test, cloud)
	if _, err := manager.repo.StartSnapshot("云端", "cloud", "cloud"); err != nil {
		test.Fatal(err)
	}
	first := uuid.NewString()
	cloud.storeSnapshot(&RoomSnapshot{ID: first, Name: "A", Devices: []model.DeviceSummary{{AssignedID: 1}}})
	body, _ := json.Marshal(map[string]any{"room_ids": []string{first}, "action": "stop", "mode": "before"})
	response := httptest.NewRecorder()
	cloud.PublishBroadcast(response, httptest.NewRequest("POST", "/api/cluster/broadcast", bytes.NewReader(body)))
	if response.Code != http.StatusAccepted {
		test.Fatalf("publish: %d %s", response.Code, response.Body.String())
	}
	fresh := uuid.NewString()
	cloud.storeSnapshot(&RoomSnapshot{ID: fresh, Name: "C"})
	manager.ReplayToRoom(fresh)
	manager.ReplayToRoom(fresh)
	var jobs int
	if err := cloud.db.QueryRow(`SELECT COUNT(*) FROM cluster_jobs WHERE room_id=?`, fresh).Scan(&jobs); err != nil {
		test.Fatal(err)
	}
	if jobs != 1 {
		test.Fatalf("new room jobs = %d, want 1", jobs)
	}
	var original int
	if err := cloud.db.QueryRow(`SELECT COUNT(*) FROM cluster_jobs WHERE room_id=?`, first).Scan(&original); err != nil {
		test.Fatal(err)
	}
	if original != 1 {
		test.Fatalf("original room was replayed again: %d", original)
	}
}

func TestFleetScheduleJoinerIsExecutedOnce(test *testing.T) {
	federation, manager, _ := snapshotStack(test)
	power := NewPowerHandler(federation.devices, manager.dispatcher, data.NewPowerScheduleRepo(federation.db), federation.hub, federation.settings)
	power.SetSnapshotManager(manager)
	manager.SetPower(power)
	first := registerScripted(test, manager.hub, 1, true)
	runAt := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	response := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/power/schedules", bytes.NewReader([]byte(fmt.Sprintf(`{"action":"shutdown","run_at":%q,"target_type":"all","note":"end"}`, runAt))))
	power.Schedules(response, request)
	if response.Code != http.StatusOK {
		test.Fatalf("schedule: %d %s", response.Code, response.Body.String())
	}
	second := registerScripted(test, manager.hub, 2, true)
	manager.ReplayToDevice(1)
	manager.ReplayToDevice(2)
	var schedules int
	var targetType string
	if err := federation.db.QueryRow(`SELECT COUNT(*), MIN(target_type) FROM power_schedules WHERE status='pending'`).Scan(&schedules, &targetType); err != nil {
		test.Fatal(err)
	}
	if schedules != 1 || targetType != "all" {
		test.Fatalf("joiner was given a second schedule: %d %s", schedules, targetType)
	}
	delivered, err := manager.repo.IsDelivered(mustOnlyOp(test, manager), "device", "2")
	if err != nil || delivered {
		test.Fatalf("joiner marked delivered before the schedule fired: %v %v", delivered, err)
	}
	list, err := data.NewPowerScheduleRepo(federation.db).List(10)
	if err != nil || len(list) != 1 {
		test.Fatalf("schedule row: %+v %v", list, err)
	}
	if err := power.RunSchedule(list[0]); err != nil {
		test.Fatal(err)
	}
	if got := first.collectCommands(test, "shutdown now", time.Second); got != 1 {
		test.Fatalf("device online at creation received shutdown %d times", got)
	}
	if got := second.collectCommands(test, "shutdown now", time.Second); got != 1 {
		test.Fatalf("joiner received shutdown %d times", got)
	}
	manager.ReplayToDevice(1)
	manager.ReplayToDevice(2)
	if got := first.collectCommands(test, "shutdown now", 200*time.Millisecond) + second.collectCommands(test, "shutdown now", 200*time.Millisecond); got != 0 {
		test.Fatalf("schedule ran again after it was applied: %d", got)
	}
	if err := federation.db.QueryRow(`SELECT COUNT(*) FROM power_schedules`).Scan(&schedules); err != nil || schedules != 1 {
		test.Fatalf("replay inserted another schedule row: %d %v", schedules, err)
	}
}

func TestFleetWakeScheduleMarksOnlyWokenDevices(test *testing.T) {
	federation, manager, _ := snapshotStack(test)
	power := NewPowerHandler(federation.devices, manager.dispatcher, data.NewPowerScheduleRepo(federation.db), federation.hub, federation.settings)
	power.SetSnapshotManager(manager)
	manager.SetPower(power)
	mac := map[int]string{1: "aa:bb:cc:dd:ee:11", 2: "aa:bb:cc:dd:ee:22", 9: "aa:bb:cc:dd:ee:99"}
	if err := federation.devices.Exec(`INSERT INTO devices(assigned_id,hostname,mac_address) VALUES(1,'awake',?)`, mac[1]); err != nil {
		test.Fatal(err)
	}
	registerScripted(test, manager.hub, 1, true)
	runAt := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	response := httptest.NewRecorder()
	power.Schedules(response, httptest.NewRequest("POST", "/api/power/schedules", bytes.NewReader([]byte(fmt.Sprintf(`{"action":"wol","run_at":%q,"target_type":"all","note":"wake"}`, runAt)))))
	if response.Code != http.StatusOK {
		test.Fatalf("schedule: %d %s", response.Code, response.Body.String())
	}
	if err := federation.devices.Exec(`INSERT INTO devices(assigned_id,hostname,mac_address) VALUES(2,'joiner',?)`, mac[2]); err != nil {
		test.Fatal(err)
	}
	registerScripted(test, manager.hub, 2, true)
	manager.ReplayToDevice(1)
	manager.ReplayToDevice(2)
	var schedules int
	var targetType string
	if err := federation.db.QueryRow(`SELECT COUNT(*), MIN(target_type) FROM power_schedules WHERE status='pending'`).Scan(&schedules, &targetType); err != nil {
		test.Fatal(err)
	}
	if schedules != 1 || targetType != "all" {
		test.Fatalf("joiner was given a second wake schedule: %d %s", schedules, targetType)
	}
	opID := mustOnlyOp(test, manager)
	if delivered, err := manager.repo.IsDelivered(opID, "device", "2"); err != nil || delivered {
		test.Fatalf("joiner marked delivered before the wake fired: %v %v", delivered, err)
	}

	sent := map[string]int{}
	magicPacketObserver = func(address string, err error) {
		if err == nil {
			sent[address]++
		}
	}
	test.Cleanup(func() { magicPacketObserver = nil })
	list, err := data.NewPowerScheduleRepo(federation.db).List(10)
	if err != nil || len(list) != 1 {
		test.Fatalf("schedule row: %+v %v", list, err)
	}
	if err := power.RunSchedule(list[0]); err != nil {
		test.Fatal(err)
	}
	if err := data.NewPowerScheduleRepo(federation.db).SetStatus(list[0].ID, data.ScheduleStatusFired, ""); err != nil {
		test.Fatal(err)
	}
	if sent[mac[1]] != 1 || sent[mac[2]] != 1 {
		test.Fatalf("fleet wake packets = %v", sent)
	}
	for _, id := range []string{"1", "2"} {
		delivered, err := manager.repo.IsDelivered(opID, "device", id)
		if err != nil || !delivered {
			test.Fatalf("woken device %s was not marked delivered: %v %v", id, delivered, err)
		}
	}
	if delivered, err := manager.repo.IsDelivered(opID, "device", "9"); err != nil || delivered {
		test.Fatalf("device absent at fire was marked delivered: %v %v", delivered, err)
	}
	manager.ReplayToDevice(1)
	manager.ReplayToDevice(2)
	if sent[mac[1]] != 1 || sent[mac[2]] != 1 {
		test.Fatalf("woken devices received another packet: %v", sent)
	}
	// The janitor only fires a schedule once its time has passed. Move the
	// recorded instant into the past so the next connect takes that branch.
	ops, err := manager.repo.ListOps(manager.Active().ID)
	if err != nil || len(ops) != 1 {
		test.Fatalf("ops: %+v %v", ops, err)
	}
	var payload SnapshotPayload
	if err := json.Unmarshal([]byte(ops[0].Payload), &payload); err != nil {
		test.Fatal(err)
	}
	payload.RunAt = time.Now().Add(-time.Minute).Format(time.RFC3339)
	raw, err := json.Marshal(payload)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := federation.db.Exec(`UPDATE snapshot_ops SET payload=? WHERE id=?`, string(raw), ops[0].ID); err != nil {
		test.Fatal(err)
	}
	manager.ReplayToDevice(1)
	manager.ReplayToDevice(2)
	if sent[mac[1]] != 1 || sent[mac[2]] != 1 {
		test.Fatalf("past replay woke an already woken device again: %v", sent)
	}

	manager.hub.SetConnectHook(nil)
	if err := federation.devices.Exec(`INSERT INTO devices(assigned_id,hostname,mac_address) VALUES(9,'late',?)`, mac[9]); err != nil {
		test.Fatal(err)
	}
	registerScripted(test, manager.hub, 9, true)
	manager.ReplayToDevice(9)
	if sent[mac[9]] != 1 {
		test.Fatalf("absent device catch-up packets = %d", sent[mac[9]])
	}
	if delivered, err := manager.repo.IsDelivered(opID, "device", "9"); err != nil || !delivered {
		test.Fatalf("catch-up wake was not recorded: %v %v", delivered, err)
	}
	manager.ReplayToDevice(9)
	if sent[mac[9]] != 1 {
		test.Fatalf("absent device was woken twice: %d", sent[mac[9]])
	}
}

func (client *scriptedClient) collectCommands(test *testing.T, command string, wait time.Duration) int {
	test.Helper()
	deadline := time.After(wait)
	count := 0
	for {
		select {
		case line := <-client.lines:
			if bytes.Contains([]byte(line), []byte(`"command":"`+command+`"`)) {
				count++
			}
		case <-deadline:
			return count
		}
	}
}

func mustOnlyOp(test *testing.T, manager *SnapshotManager) int64 {
	test.Helper()
	ops, err := manager.repo.ListOps(manager.Active().ID)
	if err != nil || len(ops) != 1 {
		test.Fatalf("ops: %+v %v", ops, err)
	}
	return ops[0].ID
}

func (client *scriptedClient) waitCommandOrder(test *testing.T, want []string) {
	test.Helper()
	got := make([]string, 0, len(want))
	deadline := time.After(3 * time.Second)
	for len(got) < len(want) {
		select {
		case line := <-client.lines:
			var msg struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			}
			if json.Unmarshal([]byte(line), &msg) == nil && msg.Command != "" {
				got = append(got, msg.Command)
			}
		case <-deadline:
			test.Fatalf("timed out waiting for commands: got %v, want %v", got, want)
		}
	}
	for i := range want {
		if got[i] != want[i] {
			test.Fatalf("command[%d] = %q, want %q (full got=%v)", i, got[i], want[i], got)
		}
	}
}

func TestCustomAddReorderAndCloudToRoomInsertionReplayOrder(test *testing.T) {
	// Set up Relay Room A with local command handler + snapshot manager.
	room := testFederation(test)
	roomNodeID := uuid.NewString()
	if err := room.settings.SetDeployment(DeploymentConfig{
		Mode:          "relay",
		NodeID:        roomNodeID,
		RoomName:      "机房A",
		CloudURL:      "https://cloud.example.com",
		Token:         "abcdefghijklmnopqrstuvwxyz012345",
		DeviceIDStart: 1,
	}); err != nil {
		test.Fatal(err)
	}
	roomCmdRepo := data.NewCommandRepo(room.db)
	roomDispatcher := biz.NewCommandDispatcher(room.hub, roomCmdRepo)
	roomSnap := NewSnapshotManager(data.NewSnapshotRepo(room.db), room.settings, room.devices, room.hub, roomDispatcher)
	roomCmdH := NewCommandHandler(roomCmdRepo, roomDispatcher, room.hub, room.settings)
	roomCmdH.SetSnapshotManager(roomSnap)
	room.SetSnapshotManager(roomSnap)
	roomSnap.SetFederation(room)
	room.hub.SetConnectHook(roomSnap.ReplayToDevice)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/commands", roomCmdH.Execute)
	mux.HandleFunc("GET /api/snapshots", roomSnap.List)
	mux.HandleFunc("GET /api/snapshots/{id}/ops", roomSnap.Ops)
	mux.HandleFunc("POST /api/snapshots/{id}/ops", roomSnap.AddCustomOp)
	mux.HandleFunc("PUT /api/snapshots/{id}/ops/reorder", roomSnap.ReorderOps)
	mux.HandleFunc("DELETE /api/snapshots/{id}/ops/{opID}", roomSnap.DeleteOp)
	room.local = mux

	// Device 1 is already online in Room A before Cloud starts its snapshot.
	dev1 := registerScripted(test, room.hub, 1, true)

	// Cloud creates active snapshot with R1 ("cloud-1") and R2 ("cloud-2") via HTTP AddCustomOp.
	cloud := testFederation(test)
	if err := cloud.settings.SetDeployment(DeploymentConfig{Mode: "cloud", Token: "abcdefghijklmnopqrstuvwxyz012345", DeviceIDStart: 1}); err != nil {
		test.Fatal(err)
	}
	cloudCmdRepo := data.NewCommandRepo(cloud.db)
	cloudDispatcher := biz.NewCommandDispatcher(cloud.hub, cloudCmdRepo)
	cloudSnap := NewSnapshotManager(data.NewSnapshotRepo(cloud.db), cloud.settings, cloud.devices, cloud.hub, cloudDispatcher)
	cloud.SetSnapshotManager(cloudSnap)
	cloudSnap.SetFederation(cloud)
	cloud.storeSnapshot(&RoomSnapshot{ID: roomNodeID, Name: "机房A", Devices: []model.DeviceSummary{{AssignedID: 1, Connected: true}}})

	startRec := httptest.NewRecorder()
	cloudSnap.StartSnapshot(startRec, httptest.NewRequest("POST", "/api/snapshots", bytes.NewReader([]byte(`{"name":"2026 省赛"}`))))
	if startRec.Code != http.StatusCreated {
		test.Fatalf("start cloud snapshot: %d %s", startRec.Code, startRec.Body.String())
	}
	cloudActive := cloudSnap.Active()

	addCloudOp := func(cmd string) data.SnapshotOp {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", fmt.Sprintf("/api/snapshots/%d/ops", cloudActive.ID), bytes.NewReader([]byte(fmt.Sprintf(`{"kind":"command","command":%q}`, cmd))))
		req.SetPathValue("id", fmt.Sprintf("%d", cloudActive.ID))
		cloudSnap.AddCustomOp(rec, req)
		if rec.Code != http.StatusCreated {
			test.Fatalf("add cloud op: %d %s", rec.Code, rec.Body.String())
		}
		var op data.SnapshotOp
		_ = json.Unmarshal(rec.Body.Bytes(), &op)
		return op
	}
	op1 := addCloudOp("cloud-1")
	op2 := addCloudOp("cloud-2")

	// Sync Cloud snapshot to Room A and deliver the two queued cluster_jobs to Room A.
	syncBytes, err := cloudSnap.BuildCloudSync()
	if err != nil {
		test.Fatal(err)
	}
	roomSnap.ApplyCloudSync(syncBytes)

	rows, err := cloud.db.Query(`SELECT request FROM cluster_jobs WHERE room_id=? ORDER BY created_at ASC`, roomNodeID)
	if err != nil {
		test.Fatal(err)
	}
	var jobFrames []FederationMessage
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			test.Fatal(err)
		}
		var msg FederationMessage
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			test.Fatal(err)
		}
		jobFrames = append(jobFrames, msg)
	}
	rows.Close()
	if len(jobFrames) != 2 {
		test.Fatalf("expected 2 queued jobs for room, got %d", len(jobFrames))
	}
	for _, frame := range jobFrames {
		resp := room.executeRelay(test.Context(), room.settings.GetDeployment(), frame)
		if resp.Status >= 400 {
			test.Fatalf("relay execution failed: %d %s", resp.Status, string(resp.Body))
		}
	}

	// Online Device 1 must have executed cloud-1 then cloud-2 ONCE, and a subsequent
	// ReplayToDevice(1) must NOT duplicate either command!
	dev1.waitCommandOrder(test, []string{"cloud-1", "cloud-2"})
	roomSnap.ReplayToDevice(1)
	if extra := dev1.collectCommands(test, "cloud-", 150*time.Millisecond); extra != 0 {
		test.Fatalf("online device received duplicate replay: %d", extra)
	}

	// Room A now inserts a custom room-local operation RIGHT AFTER cloud-1!
	roomActive := roomSnap.Active()
	if roomActive == nil || roomActive.RemoteID != cloudActive.ID {
		test.Fatalf("room did not mirror cloud snapshot: %+v", roomActive)
	}
	roomOps, err := roomSnap.repo.ListOps(roomActive.ID)
	if err != nil || len(roomOps) != 2 {
		test.Fatalf("room ops: %+v %v", roomOps, err)
	}
	insertRec := httptest.NewRecorder()
	insertReq := httptest.NewRequest("POST", fmt.Sprintf("/api/snapshots/%d/ops", roomActive.ID),
		bytes.NewReader([]byte(fmt.Sprintf(`{"kind":"command","command":"room-insert","after_op_id":%d,"skip_online":true}`, roomOps[0].ID))))
	insertReq.SetPathValue("id", fmt.Sprintf("%d", roomActive.ID))
	roomSnap.AddCustomOp(insertRec, insertReq)
	if insertRec.Code != http.StatusCreated {
		test.Fatalf("room insert op: %d %s", insertRec.Code, insertRec.Body.String())
	}

	// Late Device 2 connects to Room A -> must receive cloud-1, room-insert, cloud-2 in exact order!
	dev2 := registerScripted(test, room.hub, 2, true)
	dev2.waitCommandOrder(test, []string{"cloud-1", "room-insert", "cloud-2"})

	// Cloud now reorders [cloud-2, cloud-1] and pushes sync -> Room A becomes [cloud-2, cloud-1, room-insert]!
	reorderRec := httptest.NewRecorder()
	reorderReq := httptest.NewRequest("PUT", fmt.Sprintf("/api/snapshots/%d/ops/reorder", cloudActive.ID),
		bytes.NewReader([]byte(fmt.Sprintf(`{"op_ids":[%d,%d]}`, op2.ID, op1.ID))))
	reorderReq.SetPathValue("id", fmt.Sprintf("%d", cloudActive.ID))
	cloudSnap.ReorderOps(reorderRec, reorderReq)
	if reorderRec.Code != http.StatusOK {
		test.Fatalf("cloud reorder: %d %s", reorderRec.Code, reorderRec.Body.String())
	}
	syncBytes2, _ := cloudSnap.BuildCloudSync()
	roomSnap.ApplyCloudSync(syncBytes2)

	// Late Device 3 connects to Room A -> must receive cloud-2, cloud-1, room-insert!
	dev3 := registerScripted(test, room.hub, 3, true)
	dev3.waitCommandOrder(test, []string{"cloud-2", "cloud-1", "room-insert"})
}
