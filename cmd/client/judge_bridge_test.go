package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ICPCRemoteControl/internal/judgebridge"
	"ICPCRemoteControl/internal/model"
)

func localProofRequest(method, remote, host, origin, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8090/api/judge-proof", strings.NewReader(body))
	r.RemoteAddr = remote
	r.Host = host
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", "application/json")
	if method == "OPTIONS" {
		r.Header.Set("Access-Control-Request-Method", "POST")
	}
	w := httptest.NewRecorder()
	handleJudgeProof(w, r)
	return w
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
}

func TestJudgeLocalBridgeRelaysCurrentConnectionOnly(t *testing.T) {
	sender := newSafeSender(4)
	state.mu.Lock()
	state.sender = sender
	state.assignedID = 7
	state.mu.Unlock()
	defer func() { state.mu.Lock(); state.sender = nil; state.assignedID = 0; state.mu.Unlock(); sender.Close() }()
	token, _ := judgebridge.Sign(judgebridge.Claims{Audience: "cwxu-challenge", Origin: "http://oj.example", Purpose: "login", CID: 1, IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Unix() + 60}, strings.Repeat("x", 32))
	data, _ := json.Marshal(map[string]string{"challenge": token})
	go func() {
		raw := <-sender.Chan()
		var request model.JudgePresenceRequest
		json.Unmarshal(raw, &request)
		deliverJudgeProof(sender, model.JudgePresenceResponse{CorrelationID: request.CorrelationID, Success: true, Proof: "signed-server-proof", StudentName: "选手", StudentNumber: "001", CheckinTime: "time"})
	}()
	w := localProofRequest("POST", "127.0.0.1:123", "127.0.0.1:8090", "http://oj.example", string(data))
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("signed-server-proof")) {
		t.Fatalf("relay %d %s", w.Code, w.Body.String())
	}
	if w := localProofRequest("POST", "127.0.0.1:123", "127.0.0.1:8090", "http://wrong.example", string(data)); w.Code != 400 {
		t.Fatal("Origin mismatch accepted")
	}
	state.mu.Lock()
	state.sender = nil
	state.mu.Unlock()
	if w := localProofRequest("POST", "127.0.0.1:123", "127.0.0.1:8090", "http://oj.example", string(data)); w.Code != 503 {
		t.Fatal("disconnected cache accepted")
	}
}
