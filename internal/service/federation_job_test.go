package service

import (
	"testing"
	"time"
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
