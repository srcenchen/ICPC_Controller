package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/data"
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

func mustOnlyOp(test *testing.T, manager *SnapshotManager) int64 {
	test.Helper()
	ops, err := manager.repo.ListOps(manager.Active().ID)
	if err != nil || len(ops) != 1 {
		test.Fatalf("ops: %+v %v", ops, err)
	}
	return ops[0].ID
}
