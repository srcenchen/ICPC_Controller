package service

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/judgebridge"
	"ICPCRemoteControl/internal/model"
)

const bridgeTestSecret = "test-secret-for-oj-bridge-32-characters"
const bridgeTestOrigin = "http://127.0.0.1:8080"

func testJudgeBridge(t *testing.T) (*JudgeBridge, *biz.ClientConn) {
	t.Helper()
	db, err := data.NewDB(filepath.Join(t.TempDir(), "judge.db"))
	if err != nil {
		t.Fatal(err)
	}
	repo := data.NewDeviceRepo(db)
	if err := repo.Create(&model.Device{AssignedID: 7, MacAddress: "test-device"}); err != nil {
		t.Fatal(err)
	}
	hub := biz.NewHub(repo)
	settings := NewServerSettings("test", data.NewSettingsRepo(db))
	bridge, err := NewJudgeBridge(db, repo, hub, settings, bridgeTestSecret, []string{bridgeTestOrigin})
	if err != nil {
		t.Fatal(err)
	}
	server, peer := net.Pipe()
	client := &biz.ClientConn{AssignedID: 7, Conn: server, Send: make(chan []byte, 16), Hub: hub}
	go io.Copy(io.Discard, peer)
	hub.Register(client)
	waitFor(t, func() bool {
		d, e := repo.GetByAssignedID(7)
		return hub.GetClient(7) == client && e == nil && d.Connected
	})
	t.Cleanup(func() {
		hub.Unregister(client)
		waitFor(t, func() bool { return hub.GetClient(7) == nil })
		peer.Close()
		db.Close()
	})
	return bridge, client
}

func signedChallenge(t *testing.T, purpose, nonce string) string {
	t.Helper()
	now := time.Now().Unix()
	token, err := judgebridge.Sign(judgebridge.Claims{Audience: "cwxu-challenge", Nonce: nonce, SessionHash: strings.Repeat("b", 64), UID: 22, CID: 8, StudentNumber: "00123", StudentName: "测试选手", Origin: bridgeTestOrigin, Purpose: purpose, IssuedAt: now, ExpiresAt: now + 60}, bridgeTestSecret)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestJudgeProofRequiresLiveConnectionAndIdentity(t *testing.T) {
	bridge, client := testJudgeBridge(t)
	login := signedChallenge(t, "login", strings.Repeat("a", 64))
	if _, err := bridge.Proof(&biz.ClientConn{AssignedID: 7}, login, bridgeTestOrigin); err == nil {
		t.Fatal("stale connection accepted")
	}
	if _, err := bridge.Proof(client, login, "http://other.invalid"); err == nil {
		t.Fatal("wrong origin accepted")
	}
	if _, err := bridge.Proof(client, login+"x", bridgeTestOrigin); err == nil {
		t.Fatal("bad signature accepted")
	}
	submit := signedChallenge(t, "submit", strings.Repeat("c", 64))
	if _, err := bridge.Proof(client, submit, bridgeTestOrigin); err == nil {
		t.Fatal("unsigned-in identity submitted")
	}
	proof, err := bridge.Proof(client, login, bridgeTestOrigin)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := judgebridge.Verify(proof, bridgeTestSecret, "cwxu-proof", time.Now())
	if err != nil || claims.DeviceID != 7 || claims.NodeID == "" {
		t.Fatalf("proof: %+v %v", claims, err)
	}
	duplicate, err := bridge.Proof(client, login, bridgeTestOrigin)
	if err != nil || duplicate != proof {
		t.Fatal("retry not idempotent")
	}
	first, _ := bridge.devices.GetByAssignedID(7)
	// Verify login retry retains the first check-in time, and submit never checks in again.
	bridge.db.Exec("UPDATE devices SET checkin_time='original-time' WHERE assigned_id=7")
	if _, err := bridge.Proof(client, signedChallenge(t, "login", strings.Repeat("d", 64)), bridgeTestOrigin); err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.Proof(client, submit, bridgeTestOrigin); err != nil {
		t.Fatal(err)
	}
	after, _ := bridge.devices.GetByAssignedID(7)
	if first.CheckinStatus != 1 || after.CheckinTime != "original-time" || after.StudentNum != "00123" {
		t.Fatalf("checkin changed: %+v", after)
	}
	bridge.devices.Checkout(7)
	if _, err := bridge.Proof(client, signedChallenge(t, "submit", strings.Repeat("e", 64)), bridgeTestOrigin); err == nil {
		t.Fatal("checked out device submitted")
	}
}

func TestJudgeAttendanceAuthenticationAndIdempotency(t *testing.T) {
	bridge, _ := testJudgeBridge(t)
	now := time.Now().Unix()
	claims := judgebridge.Claims{Audience: "cwxu-attendance", CID: 8, StudentNumber: "00123", StudentName: "测试选手", IssuedAt: now, ExpiresAt: now + 60, EventID: strings.Repeat("f", 32), AccessKind: "remote", LoggedInAt: now}
	post := func(token string) int {
		body, _ := json.Marshal(map[string]string{"token": token})
		r := httptest.NewRequest("POST", "/api/judge/attendance", bytes.NewReader(body))
		w := httptest.NewRecorder()
		bridge.Attendance(w, r)
		return w.Code
	}
	token, _ := judgebridge.Sign(claims, bridgeTestSecret)
	if post(token) != 200 || post(token) != 200 {
		t.Fatal("idempotent attendance failed")
	}
	claims.IssuedAt++
	claims.ExpiresAt++
	token, _ = judgebridge.Sign(claims, bridgeTestSecret)
	if post(token) != 200 {
		t.Fatal("retry with refreshed signature rejected")
	}
	claims.StudentName = "其他选手"
	token, _ = judgebridge.Sign(claims, bridgeTestSecret)
	if post(token) != 409 {
		t.Fatal("event collision accepted")
	}
	if post(token+"x") != 401 {
		t.Fatal("unsigned attendance accepted")
	}
	var count int
	bridge.db.QueryRow("SELECT COUNT(*) FROM judge_attendance").Scan(&count)
	if count != 1 {
		t.Fatalf("count %d", count)
	}
	device, _ := bridge.devices.GetByAssignedID(7)
	if device.CheckinStatus != 0 {
		t.Fatal("remote event altered local device")
	}
}

// Exercise the production TCP dispatcher without starting the system-control client.
func TestJudgeProofOverRegisteredTCP(t *testing.T) {
	root := t.TempDir()
	db, err := data.NewDB(filepath.Join(root, "tcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := data.NewDeviceRepo(db)
	hub := biz.NewHub(repo)
	settings := NewServerSettings("test", data.NewSettingsRepo(db))
	commands := data.NewCommandRepo(db)
	handler := NewTCPHandler(hub, repo, commands, biz.NewIDAssigner(repo), biz.NewCommandDispatcher(hub, commands), settings, data.NewBroadcastRepo(db), data.NewDeviceEventRepo(db))
	bridge, err := NewJudgeBridge(db, repo, hub, settings, bridgeTestSecret, []string{bridgeTestOrigin})
	if err != nil {
		t.Fatal(err)
	}
	handler.SetJudgeBridge(bridge)
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() { handler.Handle(server); close(done) }()
	defer func() {
		client.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("TCP handler failed to stop")
		}
	}()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	encoder := json.NewEncoder(client)
	decoder := json.NewDecoder(client)
	encoder.Encode(model.RegisterRequest{Type: "register_request", MacAddress: "02:00:00:00:00:99"})
	var register model.RegisterResponse
	if err := decoder.Decode(&register); err != nil {
		t.Fatal(err)
	}
	encoder.Encode(map[string]any{"type": "system_info", "info": []any{}})
	waitFor(t, func() bool { d, e := repo.GetByAssignedID(register.AssignedID); return e == nil && d.Connected })
	encoder.Encode(model.JudgePresenceRequest{Type: "judge_presence_request", CorrelationID: "local-test", Challenge: signedChallenge(t, "login", strings.Repeat("9", 64)), Origin: bridgeTestOrigin})
	var response model.JudgePresenceResponse
	for i := 0; i < 10; i++ {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatal(err)
		}
		if response.Type == "judge_presence_response" {
			break
		}
	}
	if !response.Success || response.StudentNumber != "00123" {
		t.Fatalf("TCP proof rejected: %+v", response)
	}
	if _, err := judgebridge.Verify(response.Proof, bridgeTestSecret, "cwxu-proof", time.Now()); err != nil {
		t.Fatal(err)
	}
}
