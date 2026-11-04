package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/model"
	"ICPCRemoteControl/internal/service"
)

func localProofRequest(method, remote, host, origin, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8090/api/judge-proof", strings.NewReader(body))
	r.RemoteAddr, r.Host = remote, host
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", "application/json")
	if method == "OPTIONS" {
		r.Header.Set("Access-Control-Request-Method", "POST")
	}
	w := httptest.NewRecorder()
	handleJudgeProof(w, r)
	return w
}

func localJudgeRequest(purpose string) judgeRequest {
	return judgeRequest{StudentName: "联调选手", StudentNumber: "00123", Purpose: purpose}
}

func localJudgeBody(request judgeRequest) string {
	body, _ := json.Marshal(request)
	return string(body)
}

func useJudgeSender(t *testing.T) *safeSender {
	t.Helper()
	sender := newSafeSender(16)
	state.mu.Lock()
	state.sender, state.assignedID = sender, 7
	state.checkinStatus, state.studentName, state.studentNum = 1, "缓存身份", "cached"
	state.mu.Unlock()
	t.Cleanup(func() {
		state.mu.Lock()
		state.sender, state.assignedID = nil, 0
		state.mu.Unlock()
		sender.Close()
	})
	return sender
}

// Simulate only the original checkin/checkin_query operations. No new
// control-server operation or shared secret is used by this test.
func originalControlReplies(t *testing.T, sender *safeSender, checked bool, name, number string) func() []string {
	t.Helper()
	var mu sync.Mutex
	var messages []string
	go func() {
		for raw := range sender.Chan() {
			var message model.CheckinMessage
			if err := json.Unmarshal(raw, &message); err != nil {
				t.Error(err)
				continue
			}
			mu.Lock()
			messages = append(messages, message.Type)
			mu.Unlock()
			checkinMu.Lock()
			switch message.Type {
			case "checkin", "checkin_query":
				if message.Type == "checkin" {
					checked, name, number = true, message.StudentName, message.StudentNum
				}
				status := 0
				if checked {
					status = 1
				}
				if waiter := checkinWaiters[message.CorrelationID]; waiter != nil {
					waiter <- model.CheckinResponseMessage{Type: "checkin_response", CorrelationID: message.CorrelationID,
						Success: true, CheckinStatus: status, StudentName: name, StudentNum: number, CheckinTime: "original-checkin-time"}
				}
			default:
				t.Errorf("client sent a new or destructive control operation: %s", message.Type)
			}
			checkinMu.Unlock()
		}
	}()
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), messages...) }
}

func TestJudgeLocalBridgeRestrictsNetworkAndOrigin(t *testing.T) {
	for _, args := range [][3]string{{"192.168.1.9:123", "127.0.0.1:8090", "http://oj.example"}, {"127.0.0.1:123", "attacker.example:8090", "http://oj.example"}, {"127.0.0.1:123", "127.0.0.1:8090", "null"}} {
		if w := localProofRequest("POST", args[0], args[1], args[2], "{}"); w.Code != 403 {
			t.Fatalf("unsafe request accepted: %+v %d", args, w.Code)
		}
	}
	w := localProofRequest("OPTIONS", "127.0.0.1:123", "localhost:8090", "https://oj.example", "")
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != "https://oj.example" || w.Header().Get("Access-Control-Allow-Private-Network") != "true" {
		t.Fatal("preflight failed")
	}
	request := localJudgeRequest("login")
	for _, mutate := range []func(*judgeRequest){
		func(c *judgeRequest) { c.Purpose = "checkout" },
		func(c *judgeRequest) { c.StudentNumber = "" },
		func(c *judgeRequest) { c.StudentName = "" },
	} {
		invalid := request
		mutate(&invalid)
		if w := localProofRequest("POST", "127.0.0.1:123", "127.0.0.1:8090", "http://oj.example", localJudgeBody(invalid)); w.Code != 400 {
			t.Fatalf("invalid request accepted: %+v (%d)", invalid, w.Code)
		}
	}
}

func TestJudgeLoginSendsOnlyOriginalCheckin(t *testing.T) {
	sender := useJudgeSender(t)
	messages := originalControlReplies(t, sender, false, "", "")
	request := localJudgeRequest("login")
	for i := 0; i < 2; i++ {
		w := localProofRequest("POST", "127.0.0.1:123", "127.0.0.1:8090", "http://oj.example", localJudgeBody(request))
		if w.Code != 200 {
			t.Fatalf("login %d: %d %s", i, w.Code, w.Body.String())
		}
		var response struct {
			Success  bool `json:"success"`
			DeviceID int  `json:"device_id"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if !response.Success || response.DeviceID != 7 {
			t.Fatalf("success result missing: %s", w.Body.String())
		}
	}
	got := strings.Join(messages(), ",")
	want := "checkin,checkin"
	if got != want {
		t.Fatalf("original protocol sequence: %s", got)
	}
	state.mu.Lock()
	name, num, when := state.studentName, state.studentNum, state.checkinTime
	state.mu.Unlock()
	if name != request.StudentName || num != request.StudentNumber || when == "" {
		t.Fatal("fresh identity was not recorded locally")
	}
}

func TestJudgeSubmitRequiresOnlyServerSuccessWithoutCheckin(t *testing.T) {
	cases := []struct {
		label   string
		checked bool
		name    string
	}{
		{"same identity", true, "联调选手"},
		{"different identity", true, "其他选手"},
		{"not checked in", false, "联调选手"},
	}
	for _, entry := range cases {
		t.Run(entry.label, func(t *testing.T) {
			sender := useJudgeSender(t)
			messages := originalControlReplies(t, sender, entry.checked, entry.name, "00123")
			w := localProofRequest("POST", "127.0.0.1:123", "127.0.0.1:8090", "http://oj.example", localJudgeBody(localJudgeRequest("submit")))
			if w.Code != 200 {
				t.Fatalf("%s: %d %s", entry.label, w.Code, w.Body.String())
			}
			if got := strings.Join(messages(), ","); got != "checkin_query" {
				t.Fatalf("submit changed check-in: %s", got)
			}
		})
	}
}

func TestJudgeProbeFailsOnTimeoutAndDisconnectWithoutCachedFallback(t *testing.T) {
	sender := useJudgeSender(t)
	request := localJudgeRequest("submit")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := probeJudgeConnection(ctx, sender, 7, request); err == nil {
		t.Fatal("cached state accepted when server did not respond")
	}
	checkinMu.Lock()
	for id := range checkinWaiters {
		if strings.HasPrefix(id, "ojcheck_") {
			t.Error("timed-out waiter leaked")
		}
	}
	checkinMu.Unlock()
	state.mu.Lock()
	state.sender = nil
	state.mu.Unlock()
	if w := localProofRequest("POST", "127.0.0.1:123", "127.0.0.1:8090", "http://oj.example", localJudgeBody(request)); w.Code != 503 {
		t.Fatal("disconnected device accepted cached registration")
	}
}

func TestJudgeProbeRejectsResponseFromReplacedConnection(t *testing.T) {
	sender := useJudgeSender(t)
	replacement := newSafeSender(1)
	defer replacement.Close()
	go func() {
		raw := <-sender.Chan()
		var message model.CheckinMessage
		json.Unmarshal(raw, &message)
		state.mu.Lock()
		state.sender = replacement
		state.mu.Unlock()
		checkinMu.Lock()
		if waiter := checkinWaiters[message.CorrelationID]; waiter != nil {
			waiter <- model.CheckinResponseMessage{Type: "checkin_response", CorrelationID: message.CorrelationID, Success: true}
		}
		checkinMu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := probeJudgeConnection(ctx, sender, 7, localJudgeRequest("login")); err == nil {
		t.Fatal("old connection response accepted after reconnect")
	}
}

// Use the unchanged production TCP dispatcher to verify that an old control
// server can serve the new local endpoint without any new server operation.
func TestJudgeProbeWorksWithUnchangedControlServer(t *testing.T) {
	db, err := data.NewDB(filepath.Join(t.TempDir(), "original-control.db"))
	if err != nil {
		t.Fatal(err)
	}
	repo := data.NewDeviceRepo(db)
	hub := biz.NewHub(repo)
	settings := service.NewServerSettings("test", data.NewSettingsRepo(db))
	commands := data.NewCommandRepo(db)
	handler := service.NewTCPHandler(hub, repo, commands, biz.NewIDAssigner(repo), biz.NewCommandDispatcher(hub, commands), settings, data.NewBroadcastRepo(db), data.NewDeviceEventRepo(db))
	serverConn, clientConn := net.Pipe()
	serverDone := make(chan struct{})
	go func() { handler.Handle(serverConn); close(serverDone) }()
	encoder := json.NewEncoder(clientConn)
	decoder := json.NewDecoder(clientConn)
	clientConn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := encoder.Encode(model.RegisterRequest{Type: "register_request", MacAddress: "02:00:00:00:00:99"}); err != nil {
		t.Fatal(err)
	}
	var registered model.RegisterResponse
	if err := decoder.Decode(&registered); err != nil {
		t.Fatal(err)
	}
	sender := newSafeSender(16)
	state.mu.Lock()
	state.sender = sender
	state.assignedID = registered.AssignedID
	state.mu.Unlock()
	defer func() {
		state.mu.Lock()
		state.sender = nil
		state.assignedID = 0
		state.mu.Unlock()
		sender.Close()
		clientConn.Close()
		select {
		case <-serverDone:
		case <-time.After(2 * time.Second):
			t.Error("original TCP handler did not stop")
		}
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			d, e := repo.GetByAssignedID(registered.AssignedID)
			if e == nil && !d.Connected {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		db.Close()
	}()
	if err := encoder.Encode(map[string]any{"type": "system_info", "info": []any{}}); err != nil {
		t.Fatal(err)
	}
	go func() {
		for raw := range sender.Chan() {
			if _, err := clientConn.Write(raw); err != nil {
				return
			}
		}
	}()
	go func() {
		for {
			var raw json.RawMessage
			if decoder.Decode(&raw) != nil {
				return
			}
			var kind struct {
				Type string `json:"type"`
			}
			json.Unmarshal(raw, &kind)
			checkinMu.Lock()
			switch kind.Type {
			case "checkin_config":
				var response model.CheckinConfigMessage
				json.Unmarshal(raw, &response)
				if waiter := configWaiters[response.CorrelationID]; waiter != nil {
					waiter <- response
				}
			case "checkin_response":
				var response model.CheckinResponseMessage
				json.Unmarshal(raw, &response)
				if waiter := checkinWaiters[response.CorrelationID]; waiter != nil {
					waiter <- response
				}
			}
			checkinMu.Unlock()
		}
	}()
	login := localJudgeRequest("login")
	request := func(request judgeRequest) *httptest.ResponseRecorder {
		return localProofRequest("POST", "127.0.0.1:123", "127.0.0.1:8090", "http://oj.example", localJudgeBody(request))
	}
	if response := request(login); response.Code != 200 {
		t.Fatalf("original server login: %d %s", response.Code, response.Body.String())
	}
	device, err := repo.GetByAssignedID(registered.AssignedID)
	if err != nil || device.CheckinStatus != 1 || device.StudentNum != "00123" {
		t.Fatalf("original server did not check in: %+v %v", device, err)
	}
	repo.Exec("UPDATE devices SET checkin_time='existing-time' WHERE assigned_id=?", registered.AssignedID)
	if response := request(login); response.Code != 200 {
		t.Fatalf("repeat login: %s", response.Body.String())
	}
	device, _ = repo.GetByAssignedID(registered.AssignedID)
	if device.CheckinTime == "existing-time" {
		t.Fatal("repeat login did not use the original check-in operation")
	}
	if response := request(localJudgeRequest("submit")); response.Code != 200 {
		t.Fatalf("original server submit: %s", response.Body.String())
	}
	repo.Checkout(registered.AssignedID)
	if response := request(localJudgeRequest("submit")); response.Code != 200 {
		t.Fatal("successful original status response was rejected")
	}
}

func TestJudgeControlFailureIsReturned(t *testing.T) {
	for _, purpose := range []string{"login", "submit"} {
		t.Run(purpose, func(t *testing.T) {
			sender := useJudgeSender(t)
			go func() {
				raw := <-sender.Chan()
				var message model.CheckinMessage
				json.Unmarshal(raw, &message)
				checkinMu.Lock()
				if waiter := checkinWaiters[message.CorrelationID]; waiter != nil {
					waiter <- model.CheckinResponseMessage{Success: false, Message: "签到未开放"}
				}
				checkinMu.Unlock()
			}()
			w := localProofRequest("POST", "127.0.0.1:123", "127.0.0.1:8090", "https://oj.example", localJudgeBody(localJudgeRequest(purpose)))
			if w.Code != 503 || !strings.Contains(w.Body.String(), "签到未开放") {
				t.Fatalf("control failure not returned: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
