package main

import (
	"context"
	"encoding/base64"
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

func localChallenge(purpose string) judgeChallenge {
	now := time.Now().Unix()
	return judgeChallenge{Audience: "cwxu-challenge", Nonce: strings.Repeat("a", 64), SessionHash: strings.Repeat("b", 64),
		UID: 9, CID: 3, StudentName: "联调选手", StudentNumber: "00123", Origin: "http://oj.example", Purpose: purpose,
		IssuedAt: now, ExpiresAt: now + 60}
}

func localChallengeBody(claims judgeChallenge) string {
	payload, _ := json.Marshal(claims)
	token := "v1." + base64.RawURLEncoding.EncodeToString(payload) + ".opaque-domserver-signature"
	body, _ := json.Marshal(map[string]string{"challenge": token})
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

// Simulate only the original checkin_config/checkin_response messages. No new
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
			case "query_checkin_config":
				if waiter := configWaiters[message.CorrelationID]; waiter != nil {
					waiter <- model.CheckinConfigMessage{Type: "checkin_config", CorrelationID: message.CorrelationID}
				}
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
	claims := localChallenge("login")
	for _, mutate := range []func(*judgeChallenge){
		func(c *judgeChallenge) { c.Origin = "http://wrong.example" },
		func(c *judgeChallenge) { c.Purpose = "checkout" },
		func(c *judgeChallenge) { c.Nonce = "bad-nonce" },
		func(c *judgeChallenge) { c.ExpiresAt = c.IssuedAt + 61 },
		func(c *judgeChallenge) { c.StudentName = "invalid|name" },
		func(c *judgeChallenge) { c.UID = 0 },
	} {
		invalid := claims
		mutate(&invalid)
		if w := localProofRequest("POST", "127.0.0.1:123", "127.0.0.1:8090", "http://oj.example", localChallengeBody(invalid)); w.Code != 400 {
			t.Fatalf("bad challenge accepted: %+v (%d)", invalid, w.Code)
		}
	}
}

func TestJudgeLoginUsesOriginalProtocolAndPreservesSameIdentity(t *testing.T) {
	sender := useJudgeSender(t)
	messages := originalControlReplies(t, sender, false, "", "")
	claims := localChallenge("login")
	for i := 0; i < 2; i++ {
		w := localProofRequest("POST", "127.0.0.1:123", "127.0.0.1:8090", "http://oj.example", localChallengeBody(claims))
		if w.Code != 200 {
			t.Fatalf("login %d: %d %s", i, w.Code, w.Body.String())
		}
		var response struct {
			Success bool   `json:"success"`
			Proof   string `json:"proof"`
		}
		json.Unmarshal(w.Body.Bytes(), &response)
		if !response.Success || !strings.HasPrefix(response.Proof, "local.v1.") {
			t.Fatal("local receipt missing")
		}
		payload, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(response.Proof, "local.v1."))
		var fields map[string]any
		json.Unmarshal(payload, &fields)
		if fields["connected"] != true || fields["device_id"] != float64(7) || fields["student_number"] != "00123" || fields["nonce"] != claims.Nonce || fields["challenge_hash"] == "" {
			t.Fatalf("receipt fields: %+v", fields)
		}
	}
	got := strings.Join(messages(), ",")
	want := "query_checkin_config,checkin_query,checkin,checkin_query,query_checkin_config,checkin_query"
	if got != want {
		t.Fatalf("original protocol sequence: %s", got)
	}
	state.mu.Lock()
	name, num, when := state.studentName, state.studentNum, state.checkinTime
	state.mu.Unlock()
	if name != claims.StudentName || num != claims.StudentNumber || when != "original-checkin-time" {
		t.Fatal("fresh identity was not recorded locally")
	}
}

func TestJudgeSubmitChecksFreshIdentityWithoutCheckin(t *testing.T) {
	cases := []struct {
		label   string
		checked bool
		name    string
		allowed bool
	}{
		{"same identity", true, "联调选手", true},
		{"wrong identity", true, "其他选手", false},
		{"not checked in", false, "联调选手", false},
	}
	for _, entry := range cases {
		t.Run(entry.label, func(t *testing.T) {
			sender := useJudgeSender(t)
			messages := originalControlReplies(t, sender, entry.checked, entry.name, "00123")
			w := localProofRequest("POST", "127.0.0.1:123", "127.0.0.1:8090", "http://oj.example", localChallengeBody(localChallenge("submit")))
			if (w.Code == 200) != entry.allowed {
				t.Fatalf("%s: %d %s", entry.label, w.Code, w.Body.String())
			}
			if got := strings.Join(messages(), ","); got != "query_checkin_config,checkin_query" {
				t.Fatalf("submit changed check-in: %s", got)
			}
		})
	}
}

func TestJudgeProbeFailsOnTimeoutAndDisconnectWithoutCachedFallback(t *testing.T) {
	sender := useJudgeSender(t)
	claims := localChallenge("submit")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := probeJudgeConnection(ctx, sender, 7, claims); err == nil {
		t.Fatal("cached state accepted when server did not respond")
	}
	checkinMu.Lock()
	for id := range configWaiters {
		if strings.HasPrefix(id, "ojcfg_") {
			t.Error("timed-out waiter leaked")
		}
	}
	checkinMu.Unlock()
	state.mu.Lock()
	state.sender = nil
	state.mu.Unlock()
	if w := localProofRequest("POST", "127.0.0.1:123", "127.0.0.1:8090", "http://oj.example", localChallengeBody(claims)); w.Code != 503 {
		t.Fatal("disconnected device accepted cached registration")
	}
}

func TestJudgeProbeRejectsResponseFromReplacedConnection(t *testing.T) {
	sender := useJudgeSender(t)
	replacement := newSafeSender(1)
	defer replacement.Close()
	go func() {
		raw := <-sender.Chan()
		var message model.CheckinConfigMessage
		json.Unmarshal(raw, &message)
		state.mu.Lock()
		state.sender = replacement
		state.mu.Unlock()
		checkinMu.Lock()
		if waiter := configWaiters[message.CorrelationID]; waiter != nil {
			waiter <- model.CheckinConfigMessage{Type: "checkin_config", CorrelationID: message.CorrelationID}
		}
		checkinMu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := probeJudgeConnection(ctx, sender, 7, localChallenge("login")); err == nil {
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
	login := localChallenge("login")
	request := func(claims judgeChallenge) *httptest.ResponseRecorder {
		return localProofRequest("POST", "127.0.0.1:123", "127.0.0.1:8090", "http://oj.example", localChallengeBody(claims))
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
	if device.CheckinTime != "existing-time" {
		t.Fatal("same identity login overwrote existing checkin time")
	}
	if response := request(localChallenge("submit")); response.Code != 200 {
		t.Fatalf("original server submit: %s", response.Body.String())
	}
	repo.Checkout(registered.AssignedID)
	if response := request(localChallenge("submit")); response.Code == 200 {
		t.Fatal("original server checked-out state was not observed")
	}
}
