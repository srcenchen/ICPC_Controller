package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/model"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func testFederation(test *testing.T) *Federation {
	test.Helper()
	root := test.TempDir()
	db, err := data.NewDB(filepath.Join(root, "test.db"))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { db.Close() })
	repo := data.NewDeviceRepo(db)
	hub := biz.NewHub(repo)
	settings := NewServerSettings("test", data.NewSettingsRepo(db))
	return NewFederation(db, settings, repo, NewDistributionManager(hub, filepath.Join(root, "uploads")), hub)
}

func TestDuplicateRelayIdentityRejected(test *testing.T) {
	cloud := testFederation(test)
	token := strings.Repeat("s", 32)
	if err := cloud.settings.SetDeployment(DeploymentConfig{Mode: "cloud", Token: token, DeviceIDStart: 1}); err != nil {
		test.Fatal(err)
	}
	server := httptest.NewServer(cloud.NodeAuth(http.NotFoundHandler()))
	defer server.Close()
	endpoint := strings.Replace(server.URL, "http://", "ws://", 1) + "/api/cluster/connect"
	header := http.Header{"Authorization": []string{"Bearer " + token}}
	first, _, err := websocket.DefaultDialer.Dial(endpoint, header)
	if err != nil {
		test.Fatal(err)
	}
	defer first.Close()
	roomID := uuid.NewString()
	hello := FederationMessage{Type: "hello", Snapshot: &RoomSnapshot{ID: roomID, Name: "original"}}
	if err := first.WriteJSON(hello); err != nil {
		test.Fatal(err)
	}
	waitFor(test, func() bool { cloud.mu.Lock(); defer cloud.mu.Unlock(); return cloud.connections[roomID] != nil })
	second, _, err := websocket.DefaultDialer.Dial(endpoint, header)
	if err != nil {
		test.Fatal(err)
	}
	defer second.Close()
	if err := second.WriteJSON(hello); err != nil {
		test.Fatal(err)
	}
	_ = second.SetReadDeadline(time.Now().Add(3 * time.Second))
	var response FederationMessage
	if err := second.ReadJSON(&response); err != nil || response.Type != "rejected" {
		test.Fatalf("cloned relay replaced live room: %s %v", response.Type, err)
	}
}

func waitFor(test *testing.T, condition func() bool) {
	test.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	test.Fatal("timed out")
}

func TestFederationTwoRoomsOverlappingDeviceIDs(test *testing.T) {
	cloud := testFederation(test)
	token := strings.Repeat("k", 32)
	if err := cloud.settings.SetDeployment(DeploymentConfig{Mode: "cloud", Token: token, DeviceIDStart: 1}); err != nil {
		test.Fatal(err)
	}
	server := httptest.NewServer(cloud.NodeAuth(http.NotFoundHandler()))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	for _, name := range []string{"机房 A", "机房 B"} {
		relay := testFederation(test)
		if err := relay.settings.SetDeployment(DeploymentConfig{Mode: "relay", Token: token, DeviceIDStart: 1, RoomName: name, CloudURL: server.URL, AllowInsecure: true}); err != nil {
			test.Fatal(err)
		}
		if err := relay.devices.Exec(`INSERT INTO devices(assigned_id,hostname) VALUES(1,?)`, name); err != nil {
			test.Fatal(err)
		}
		relay.Start(ctx, http.HandlerFunc(func(writer http.ResponseWriter, httpRequest *http.Request) {
			calls.Add(1)
			writeJSON(writer, 201, map[string]string{"room": name})
		}))
	}
	waitFor(test, func() bool {
		rooms, _ := cloud.roomList()
		return len(rooms) == 2 && rooms[0].Online && rooms[1].Online
	})
	rooms, _ := cloud.roomList()
	if rooms[0].ID == rooms[1].ID || rooms[0].Devices[0].AssignedID != 1 || rooms[1].Devices[0].AssignedID != 1 {
		test.Fatal("room-local identity lost")
	}
	for _, room := range rooms {
		request := FederationMessage{Type: "request", ID: uuid.NewString(), Method: "POST", Path: "/api/commands", Body: []byte(`{"command":"hostname","target_type":"single","target_id":1}`)}
		for iteration := 0; iteration < 2; iteration++ {
			response, err := cloud.call(ctx, room.ID, request)
			if err != nil || response.Status != 201 || !bytes.Contains(response.Body, []byte(room.Name)) {
				test.Fatalf("wrong room response: %s %v", response.Body, err)
			}
		}
	}
	if calls.Load() != 2 {
		test.Fatalf("duplicate delivery executed %d times", calls.Load())
	}
	cancel()
	waitFor(test, func() bool { cloud.mu.Lock(); defer cloud.mu.Unlock(); return len(cloud.connections) == 0 })
}

func TestRelayRejectsScreensAndCredentials(test *testing.T) {
	for _, path := range []string{"/api/devices/1/screen", "/api/settings", "/api/data/backup", "/api/auth/password", "/api/devices/../settings"} {
		if allowedRelayRequest("GET", path) {
			test.Fatalf("allowed forbidden path %s", path)
		}
	}
	cloud := testFederation(test)
	if err := cloud.settings.SetDeployment(DeploymentConfig{Mode: "cloud", Token: strings.Repeat("s", 32), DeviceIDStart: 1}); err != nil {
		test.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	cloud.NodeAuth(http.NotFoundHandler()).ServeHTTP(recorder, httptest.NewRequest("GET", "/api/cluster/connect", nil))
	if recorder.Code != 401 {
		test.Fatalf("unauthenticated node: %d", recorder.Code)
	}
}

func TestRelayPullResumesVerifiesAndCaches(test *testing.T) {
	federation := testFederation(test)
	content := []byte(strings.Repeat("verified payload", 1000))
	source := filepath.Join(test.TempDir(), "data.bin")
	if err := os.WriteFile(source, content, 0600); err != nil {
		test.Fatal(err)
	}
	digest, _ := fileSHA256(source)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, httpRequest *http.Request) {
		requests.Add(1)
		if httpRequest.Header.Get("Authorization") != "Bearer token" {
			test.Error("missing node token")
		}
		http.ServeFile(writer, httpRequest, source)
	}))
	defer server.Close()
	if err := os.WriteFile(filepath.Join(federation.distribution.uploadDir, "."+digest+".part"), content[:100], 0600); err != nil {
		test.Fatal(err)
	}
	file := RelayFile{Name: "data.bin", SHA256: digest, Size: int64(len(content))}
	cfg := DeploymentConfig{CloudURL: server.URL, Token: "token"}
	for attempt := 0; attempt < 2; attempt++ {
		if err := federation.pullFile(context.Background(), cfg, file); err != nil {
			test.Fatal(err)
		}
	}
	if requests.Load() != 1 {
		test.Fatalf("cached file re-downloaded: %d", requests.Load())
	}
	actual, _ := os.ReadFile(filepath.Join(federation.distribution.uploadDir, "data.bin"))
	if !bytes.Equal(content, actual) {
		test.Fatal("file mismatch")
	}
}

func TestCloudQueuePersistsExplicitTargets(test *testing.T) {
	cloud := testFederation(test)
	if err := cloud.settings.SetDeployment(DeploymentConfig{Mode: "cloud", Token: strings.Repeat("s", 32), DeviceIDStart: 1}); err != nil {
		test.Fatal(err)
	}
	room := uuid.NewString()
	cloud.storeSnapshot(&RoomSnapshot{ID: room, Name: "room"})
	body, _ := json.Marshal(map[string]interface{}{"room_ids": []string{room}, "operation": "command", "command": "hostname", "targets": map[string][]int{room: {1, 2}}})
	recorder := httptest.NewRecorder()
	cloud.Queue(recorder, httptest.NewRequest("POST", "/api/cluster/jobs", bytes.NewReader(body)))
	if recorder.Code != 202 {
		test.Fatalf("queue failed: %s", recorder.Body.String())
	}
	var count int
	if err := cloud.db.QueryRow(`SELECT COUNT(*) FROM cluster_jobs WHERE status='queued'`).Scan(&count); err != nil || count != 1 {
		test.Fatalf("expected one batched job per room: %d %v", count, err)
	}
	var raw string
	if err := cloud.db.QueryRow(`SELECT request FROM cluster_jobs`).Scan(&raw); err != nil {
		test.Fatal(err)
	}
	var message FederationMessage
	if err := json.Unmarshal([]byte(raw), &message); err != nil {
		test.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(message.Body, &payload); err != nil {
		test.Fatal(err)
	}
	ids, _ := payload["target_ids"].([]any)
	if payload["target_type"] != "list" || len(ids) != 2 {
		test.Fatalf("devices were not batched: %+v", payload)
	}
}

func TestCloudIndependentPageOperationsKeepRoomTargets(test *testing.T) {
	for _, operation := range []string{"network_apply", "network_remove", "wol", "schedule"} {
		test.Run(operation, func(test *testing.T) {
			cloud := testFederation(test)
			if err := cloud.settings.SetDeployment(DeploymentConfig{Mode: "cloud", Token: strings.Repeat("o", 32), DeviceIDStart: 1}); err != nil {
				test.Fatal(err)
			}
			room := uuid.NewString()
			cloud.storeSnapshot(&RoomSnapshot{ID: room, Name: "room"})
			body, _ := json.Marshal(map[string]any{"room_ids": []string{room}, "targets": map[string][]int{room: {1}}, "operation": operation, "rules": []NetworkRule{{Type: "DOMAIN", Value: "example.org"}}, "action": "wol", "run_at": time.Now().Add(time.Hour).Format(time.RFC3339)})
			response := httptest.NewRecorder()
			cloud.Queue(response, httptest.NewRequest("POST", "/api/cluster/jobs", bytes.NewReader(body)))
			if response.Code != 202 {
				test.Fatalf("%s not queued: %s", operation, response.Body.String())
			}
			var raw, actualRoom string
			if err := cloud.db.QueryRow(`SELECT room_id,request FROM cluster_jobs`).Scan(&actualRoom, &raw); err != nil || actualRoom != room {
				test.Fatalf("room lost: %s %v", actualRoom, err)
			}
			var message FederationMessage
			if err := json.Unmarshal([]byte(raw), &message); err != nil {
				test.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(message.Body, &payload); err != nil {
				test.Fatal(err)
			}
			if strings.HasPrefix(operation, "network_") {
				ids, _ := payload["target_ids"].([]any)
				if message.Path != "/api/commands" || payload["target_type"] != "list" || len(ids) != 1 || payload["command"] == "" {
					test.Fatalf("network target lost: %+v", payload)
				}
			} else if payload["target_type"] != "list" || len(payload["device_ids"].([]any)) != 1 {
				test.Fatalf("power target broadened: %+v", payload)
			}
		})
	}
}

func TestExpiredRelayPowerPlanDoesNotExecute(test *testing.T) {
	relay := testFederation(test)
	if err := relay.settings.SetDeployment(DeploymentConfig{Mode: "relay", Token: strings.Repeat("p", 32), DeviceIDStart: 1, RoomName: "room", CloudURL: "http://localhost", AllowInsecure: true}); err != nil {
		test.Fatal(err)
	}
	relay.local = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { test.Error("executed expired plan") })
	body, _ := json.Marshal(scheduleRequest{Action: "shutdown", TargetType: "list", DeviceIDs: []int{1}, RunAt: time.Now().Add(-time.Minute).Format(time.RFC3339)})
	response := relay.executeRelay(context.Background(), relay.settings.GetDeployment(), FederationMessage{ID: uuid.NewString(), Method: "POST", Path: "/api/power/schedules", Body: body})
	if response.Status != 400 {
		test.Fatalf("accepted expired plan: %d", response.Status)
	}
}

func TestRoomSnapshotIncludesDashboardCommandSummary(test *testing.T) {
	relay := testFederation(test)
	command := &model.CommandLog{TargetType: "broadcast", Command: strings.Repeat("hostname;", 200), Status: "completed", Output: "private long output"}
	if err := data.NewCommandRepo(relay.db).Create(command); err != nil {
		test.Fatal(err)
	}
	snapshot := relay.snapshot(relay.settings.GetDeployment())
	if snapshot.TotalCommands != 1 || len(snapshot.RecentCommands) != 1 || snapshot.RecentCommands[0].Output != "" || len(snapshot.RecentCommands[0].Command) > 512 {
		test.Fatalf("wrong dashboard summary: %+v", snapshot)
	}
}

func TestDeletedRoomIsNotifiedOnReconnect(test *testing.T) {
	cloud := testFederation(test)
	token := strings.Repeat("d", 32)
	if err := cloud.settings.SetDeployment(DeploymentConfig{Mode: "cloud", Token: token, DeviceIDStart: 1}); err != nil {
		test.Fatal(err)
	}
	roomID := uuid.NewString()
	cloud.storeSnapshot(&RoomSnapshot{ID: roomID, Name: "离线机房"})
	request := httptest.NewRequest("DELETE", "/api/cluster/rooms/"+roomID, nil)
	request.SetPathValue("room", roomID)
	recorder := httptest.NewRecorder()
	cloud.DeleteRoom(recorder, request)
	if recorder.Code != http.StatusOK {
		test.Fatalf("delete room: %d %s", recorder.Code, recorder.Body.String())
	}
	if !cloud.roomDeleted(roomID) {
		test.Fatal("deletion marker missing")
	}
	server := httptest.NewServer(cloud.NodeAuth(http.NotFoundHandler()))
	defer server.Close()
	endpoint := strings.Replace(server.URL, "http://", "ws://", 1) + "/api/cluster/connect"
	conn, _, err := websocket.DefaultDialer.Dial(endpoint, http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		test.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(FederationMessage{Type: "hello", Snapshot: &RoomSnapshot{ID: roomID, Name: "离线机房"}}); err != nil {
		test.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var notice FederationMessage
	if err := conn.ReadJSON(&notice); err != nil || notice.Type != "room_deleted" {
		test.Fatalf("deleted room was not notified: %+v %v", notice, err)
	}
	if err := conn.WriteJSON(FederationMessage{Type: "room_deleted_ack", ID: notice.ID}); err != nil {
		test.Fatal(err)
	}
	waitFor(test, func() bool { return !cloud.roomDeleted(roomID) })
	rooms, _ := cloud.roomList()
	if len(rooms) != 0 {
		test.Fatalf("deleted room was re-added: %+v", rooms)
	}
}

func TestRelaySwitchesToStandaloneAfterRoomDeletion(test *testing.T) {
	cloud, relay := testFederation(test), testFederation(test)
	token := strings.Repeat("e", 32)
	if err := cloud.settings.SetDeployment(DeploymentConfig{Mode: "cloud", Token: token, DeviceIDStart: 1}); err != nil {
		test.Fatal(err)
	}
	server := httptest.NewServer(cloud.NodeAuth(http.NotFoundHandler()))
	defer server.Close()
	if err := relay.settings.SetDeployment(DeploymentConfig{Mode: "relay", Token: token, DeviceIDStart: 1, RoomName: "在线机房", CloudURL: server.URL, AllowInsecure: true}); err != nil {
		test.Fatal(err)
	}
	roomID := relay.settings.GetDeployment().NodeID
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	relay.Start(ctx, http.NotFoundHandler())
	waitFor(test, func() bool {
		rooms, _ := cloud.roomList()
		return len(rooms) == 1 && rooms[0].Online
	})
	request := httptest.NewRequest("DELETE", "/api/cluster/rooms/"+roomID, nil)
	request.SetPathValue("room", roomID)
	cloud.DeleteRoom(httptest.NewRecorder(), request)
	waitFor(test, func() bool {
		cfg := relay.settings.GetDeployment()
		return cfg.Mode == "standalone" && cfg.CloudURL == "" && cfg.Token == ""
	})
	waitFor(test, func() bool { return !cloud.roomDeleted(roomID) })
	rooms, _ := cloud.roomList()
	if len(rooms) != 0 {
		test.Fatalf("deleted room still listed: %+v", rooms)
	}
}

func TestContestResetQueuesEveryRoomAndClearsCache(test *testing.T) {
	cloud := testFederation(test)
	if err := cloud.settings.SetDeployment(DeploymentConfig{Mode: "cloud", Token: strings.Repeat("r", 32), DeviceIDStart: 1}); err != nil {
		test.Fatal(err)
	}
	for _, name := range []string{"A", "B"} {
		cloud.storeSnapshot(&RoomSnapshot{ID: uuid.NewString(), Name: name, Devices: []model.DeviceSummary{{AssignedID: 1, Hostname: name}}})
	}
	cloud.ResetContest(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/contest/reset", bytes.NewReader([]byte(`{}`))))
	var jobs int
	if err := cloud.db.QueryRow(`SELECT COUNT(*) FROM cluster_jobs WHERE status='queued'`).Scan(&jobs); err != nil || jobs != 2 {
		test.Fatalf("expected reset job per room: %d %v", jobs, err)
	}
	rows, err := cloud.db.Query(`SELECT request, room_id FROM cluster_jobs`)
	if err != nil {
		test.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw, roomID string
		if err := rows.Scan(&raw, &roomID); err != nil {
			test.Fatal(err)
		}
		var message FederationMessage
		if err := json.Unmarshal([]byte(raw), &message); err != nil || message.Path != "/api/devices/reset" || message.Method != "POST" {
			test.Fatalf("wrong reset job: %+v %v", message, err)
		}
	}
	rooms, _ := cloud.roomList()
	if len(rooms) != 2 {
		test.Fatalf("rooms disappeared: %+v", rooms)
	}
	for _, room := range rooms {
		if len(room.Devices) != 0 {
			test.Fatalf("cached devices were not cleared: %+v", room.Devices)
		}
	}
}

func TestDeploymentTokenRemainsVisible(test *testing.T) {
	settings := testFederation(test).settings
	token := strings.Repeat("v", 32)
	if err := settings.SetDeployment(DeploymentConfig{Mode: "cloud", Token: token, DeviceIDStart: 1}); err != nil {
		test.Fatal(err)
	}
	if snapshot := settings.Snapshot(); snapshot.Deployment == nil || snapshot.Deployment.Token != token || !snapshot.Deployment.TokenSet {
		test.Fatalf("token is no longer readable from settings: %+v", snapshot.Deployment)
	}
}
