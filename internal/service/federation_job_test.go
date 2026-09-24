package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestShouldDeliverJobSkipsInFlightWhileRoomIsOnline(test *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	if !shouldDeliverJob("queued", "", false, now) {
		test.Fatal("queued job was not delivered")
	}
	if shouldDeliverJob("sent", now.Add(-time.Minute).Format(time.RFC3339), true, now) {
		test.Fatal("in-flight job was delivered again to a connected room")
	}
	if !shouldDeliverJob("sent", now.Add(-time.Minute).Format(time.RFC3339), false, now) {
		test.Fatal("sent job was not retried after the room disconnected")
	}
	if shouldDeliverJob("sent", now.Add(-5*time.Second).Format(time.RFC3339), false, now) {
		test.Fatal("recently sent job was retried before the room could answer")
	}
	if shouldDeliverJob("completed", "", true, now) {
		test.Fatal("completed job was delivered")
	}
}

func TestQueuedJobIsNotHiddenByInFlightRows(test *testing.T) {
	federation := testFederation(test)
	if err := federation.settings.SetDeployment(DeploymentConfig{Mode: "cloud", Token: strings.Repeat("q", 32), DeviceIDStart: 1}); err != nil {
		test.Fatal(err)
	}
	upgrader := websocket.Upgrader{}
	accepted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		accepted <- struct{}{}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	socket, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http://", "ws://", 1), nil)
	if err != nil {
		test.Fatal(err)
	}
	defer socket.Close()
	<-accepted
	room := "room-backlog"
	federation.mu.Lock()
	federation.connections[room] = &relayConnection{conn: socket}
	federation.mu.Unlock()
	for index := 0; index < 100; index++ {
		created := fmt.Sprintf("2020-01-01T00:%02d:00Z", index%60)
		if _, err := federation.db.Exec(`INSERT INTO cluster_jobs(id,room_id,request,status,sent_at,created_at) VALUES(?,?,?,?,?,?)`,
			fmt.Sprintf("sent-%d", index), room, `{"type":"request","id":"old","method":"POST","path":"/api/commands","body":"e30="}`, "sent", "2020-01-01T00:00:00Z", created); err != nil {
			test.Fatal(err)
		}
	}
	if _, err := federation.db.Exec(`INSERT INTO cluster_jobs(id,room_id,request,status,created_at) VALUES(?,?,?,?,?)`,
		"queued-new", room, `{"type":"request","id":"new","method":"POST","path":"/api/commands","body":"e30="}`, "queued", "2026-09-24T00:00:00Z"); err != nil {
		test.Fatal(err)
	}
	federation.deliverJobs()
	var status string
	if err := federation.db.QueryRow(`SELECT status FROM cluster_jobs WHERE id='queued-new'`).Scan(&status); err != nil {
		test.Fatal(err)
	}
	if status != "sent" {
		test.Fatalf("queued job stayed %s behind 100 in-flight rows", status)
	}
}

func TestDisconnectRequeuesSentJob(test *testing.T) {
	federation := testFederation(test)
	if _, err := federation.db.Exec(`INSERT INTO cluster_jobs(id,room_id,request,status,sent_at,created_at) VALUES('job','room','{}','sent','2026-09-24T00:00:00Z','2026-09-24T00:00:00Z')`); err != nil {
		test.Fatal(err)
	}
	federation.requeueInFlight("room")
	var status, sentAt string
	if err := federation.db.QueryRow(`SELECT status, sent_at FROM cluster_jobs WHERE id='job'`).Scan(&status, &sentAt); err != nil {
		test.Fatal(err)
	}
	if status != "queued" || sentAt != "" {
		test.Fatalf("sent job was not requeued: %s %q", status, sentAt)
	}
}
