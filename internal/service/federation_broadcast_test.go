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

	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/model"
	"github.com/google/uuid"
)

func setupBroadcastFederation(test *testing.T, federation *Federation) {
	test.Helper()
	federation.broadcastDir = test.TempDir()
	federation.broadcast = &BroadcastHandler{repo: data.NewBroadcastRepo(federation.db)}
	for _, kind := range []string{"fonts", "images"} {
		if err := os.MkdirAll(filepath.Join(federation.broadcastDir, kind), 0755); err != nil {
			test.Fatal(err)
		}
	}
}

func queueTestBroadcast(test *testing.T, cloud *Federation, room, action string) FederationMessage {
	test.Helper()
	body, _ := json.Marshal(map[string]any{"room_ids": []string{room}, "action": action, "mode": "before"})
	response := httptest.NewRecorder()
	cloud.PublishBroadcast(response, httptest.NewRequest("POST", "/api/cluster/broadcast", bytes.NewReader(body)))
	if response.Code != 202 {
		test.Fatalf("publish: %d %s", response.Code, response.Body.String())
	}
	var result struct {
		JobIDs []string `json:"job_ids"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result.JobIDs) != 1 {
		test.Fatalf("invalid job response: %v", err)
	}
	var raw string
	if err := cloud.db.QueryRow(`SELECT request FROM cluster_jobs WHERE id=? AND status='queued'`, result.JobIDs[0]).Scan(&raw); err != nil {
		test.Fatal(err)
	}
	var message FederationMessage
	if err := json.Unmarshal([]byte(raw), &message); err != nil {
		test.Fatal(err)
	}
	return message
}

func TestBroadcastPublicationReconnectAssetsAndIdempotency(test *testing.T) {
	cloud, relay := testFederation(test), testFederation(test)
	setupBroadcastFederation(test, cloud)
	setupBroadcastFederation(test, relay)
	token := strings.Repeat("b", 32)
	if err := cloud.settings.SetDeployment(DeploymentConfig{Mode: "cloud", Token: token, DeviceIDStart: 1}); err != nil {
		test.Fatal(err)
	}
	server := httptest.NewServer(cloud.NodeAuth(http.NotFoundHandler()))
	defer server.Close()
	if err := relay.settings.SetDeployment(DeploymentConfig{Mode: "relay", Token: token, DeviceIDStart: 1, RoomName: "实验室", CloudURL: server.URL, AllowInsecure: true}); err != nil {
		test.Fatal(err)
	}
	config := relay.settings.GetDeployment()
	if err := relay.broadcast.repo.SetConfig("base_url", "http://room.lan:18082"); err != nil {
		test.Fatal(err)
	}
	cloud.storeSnapshot(&RoomSnapshot{ID: config.NodeID, Name: config.RoomName})
	for _, path := range []string{"images/test.png", "fonts/test.woff2", "fonts/copy.woff2"} {
		if err := os.WriteFile(filepath.Join(cloud.broadcastDir, path), []byte("immutable asset contents"), 0600); err != nil {
			test.Fatal(err)
		}
	}
	page := model.BroadcastPage{Mode: "before", Title: "云端标题", DurationMs: 10000}
	if err := cloud.broadcast.repo.CreatePage(&page); err != nil {
		test.Fatal(err)
	}
	if err := cloud.broadcast.repo.CreateItem(&model.BroadcastItem{PageID: page.ID, ItemType: "image", Content: "/broadcast/images/test.png"}); err != nil {
		test.Fatal(err)
	}
	for _, name := range []string{"test.woff2", "copy.woff2"} {
		if err := cloud.broadcast.repo.CreateFont(&model.BroadcastFont{Name: name, Filename: name, Format: "woff2"}); err != nil {
			test.Fatal(err)
		}
	}
	if err := cloud.broadcast.repo.SetConfig("active_font", "copy.woff2"); err != nil {
		test.Fatal(err)
	}
	message := queueTestBroadcast(test, cloud, config.NodeID, "start")
	if err := os.RemoveAll(filepath.Join(cloud.broadcastDir, "images")); err != nil {
		test.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(cloud.broadcastDir, "fonts")); err != nil {
		test.Fatal(err)
	}
	var executions atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	relay.Start(ctx, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var command ExecuteRequest
		if request.URL.Path != "/api/commands" || json.NewDecoder(request.Body).Decode(&command) != nil || !strings.Contains(command.Command, "http://room.lan:18082/broadcast/before") || strings.Contains(command.Command, server.URL) {
			test.Errorf("wrong local broadcast command: %+v", command)
		}
		executions.Add(1)
		writeJSON(writer, 201, map[string]string{"message": "accepted"})
	}))
	waitFor(test, func() bool { rooms, _ := cloud.roomList(); return len(rooms) == 1 && rooms[0].Online })
	cloud.deliverJobs()
	waitFor(test, func() bool {
		var status string
		_ = cloud.db.QueryRow(`SELECT status FROM cluster_jobs WHERE id=?`, message.ID).Scan(&status)
		return status == "completed"
	})
	pages, err := relay.broadcast.repo.GetPagesWithItems("before")
	if err != nil || len(pages) != 1 || pages[0].Title != "云端标题" || len(pages[0].Items) != 1 {
		test.Fatalf("missing synced pages: %+v %v", pages, err)
	}
	assetPath := filepath.Join(relay.broadcastDir, strings.TrimPrefix(pages[0].Items[0].Content, "/broadcast/"))
	content, err := os.ReadFile(assetPath)
	if err != nil || string(content) != "immutable asset contents" {
		test.Fatalf("wrong cached asset: %q %v", content, err)
	}
	fonts, _ := relay.broadcast.repo.ListFonts()
	activeFont, _ := relay.broadcast.repo.GetConfig("active_font")
	if len(fonts) != 1 || fonts[0].Filename != activeFont {
		test.Fatalf("duplicate fonts or missing active font: %+v %s", fonts, activeFont)
	}
	response := relay.executeRelay(ctx, config, message)
	if response.Status != 200 || executions.Load() != 1 {
		test.Fatalf("duplicate broadcast executed: %+v %d", response, executions.Load())
	}
	message.ID = uuid.NewString()
	if err := os.RemoveAll(filepath.Join(cloud.broadcastDir, "releases")); err != nil {
		test.Fatal(err)
	}
	response = relay.executeRelay(ctx, config, message)
	if response.Status != 200 || executions.Load() != 1 {
		test.Fatal("old revision retried assets or executed commands")
	}
	unauthenticated := httptest.NewRecorder()
	cloud.NodeAuth(http.NotFoundHandler()).ServeHTTP(unauthenticated, httptest.NewRequest("GET", "/api/cluster/broadcast-assets/"+strings.Repeat("a", 64), nil))
	if unauthenticated.Code != 401 {
		test.Fatal("broadcast release asset is not node authenticated")
	}
	cancel()
	waitFor(test, func() bool { rooms, _ := cloud.roomList(); return len(rooms) == 1 && !rooms[0].Online })
}

func TestBroadcastCorruptAssetNeverReplacesLiveContent(test *testing.T) {
	relay := testFederation(test)
	setupBroadcastFederation(test, relay)
	content := []byte("original asset")
	source := filepath.Join(test.TempDir(), "original.png")
	if err := os.WriteFile(source, content, 0600); err != nil {
		test.Fatal(err)
	}
	digest, _ := fileSHA256(source)
	var broken atomic.Bool
	broken.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if broken.Load() {
			_, _ = writer.Write([]byte("tampered asset"))
			return
		}
		_, _ = writer.Write(content)
	}))
	defer server.Close()
	if err := relay.settings.SetDeployment(DeploymentConfig{Mode: "relay", Token: strings.Repeat("r", 32), DeviceIDStart: 1, RoomName: "relay", CloudURL: server.URL, AllowInsecure: true}); err != nil {
		test.Fatal(err)
	}
	if err := relay.broadcast.repo.CreatePage(&model.BroadcastPage{Mode: "before", Title: "original"}); err != nil {
		test.Fatal(err)
	}
	publication := broadcastPublication{Action: "sync", Mode: "before", Snapshot: &data.BroadcastSnapshot{Source: uuid.NewString(), Revision: 1, Pages: []model.BroadcastPage{{ID: 1, Mode: "before", Title: "updated", Items: []model.BroadcastItem{{ID: 1, ItemType: "image", Content: "/broadcast/images/" + digest + ".png"}}}}}, Assets: []RelayFile{{Name: "images/" + digest + ".png", SHA256: digest, Size: int64(len(content))}}}
	body, _ := json.Marshal(publication)
	message := FederationMessage{ID: uuid.NewString(), Type: "request", Method: "POST", Path: "/api/broadcast/sync", Body: body}
	response := relay.executeRelay(context.Background(), relay.settings.GetDeployment(), message)
	if response.Status != 102 {
		test.Fatalf("corruption not retryable: %d %s", response.Status, response.Body)
	}
	pages, _ := relay.broadcast.repo.GetPagesWithItems("before")
	if len(pages) != 1 || pages[0].Title != "original" {
		test.Fatal("failed asset replaced live content")
	}
	broken.Store(false)
	response = relay.executeRelay(context.Background(), relay.settings.GetDeployment(), message)
	if response.Status != 200 {
		test.Fatalf("retry did not converge: %d %s", response.Status, response.Body)
	}
	pages, _ = relay.broadcast.repo.GetPagesWithItems("before")
	if len(pages) != 1 || pages[0].Title != "updated" {
		test.Fatal("retry did not apply snapshot")
	}
	publication.Snapshot.Revision = 2
	publication.Assets[0].Name = "images/../../escape.png"
	message.ID = uuid.NewString()
	message.Body, _ = json.Marshal(publication)
	response = relay.executeRelay(context.Background(), relay.settings.GetDeployment(), message)
	if response.Status != 400 {
		test.Fatal("accepted asset traversal")
	}
	time.Sleep(250 * time.Millisecond)
	for _, mode := range []string{"before", "contesting", "after"} {
		BroadcastWS.StopCarousel(mode)
	}
}
