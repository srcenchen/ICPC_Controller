package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"ICPCRemoteControl/internal/judgebridge"
	"ICPCRemoteControl/internal/model"
	"github.com/google/uuid"
)

var judgeMu sync.Mutex
var judgeWaiters = make(map[string]chan model.JudgePresenceResponse)

func loopbackAddress(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// This endpoint is reachable only from a browser on this machine. The control
// server checks the challenge signature and its allowed Origin independently.
func handleJudgeProof(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	host := r.Host
	if h, _, e := net.SplitHostPort(host); e == nil {
		host = h
	}
	if err != nil || !loopbackAddress(remote) || !loopbackAddress(host) {
		writeClientJSON(w, http.StatusForbidden, map[string]any{"success": false, "message": "仅允许本机访问客户端验证"})
		return
	}
	origin := r.Header.Get("Origin")
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		writeClientJSON(w, http.StatusForbidden, map[string]any{"success": false, "message": "OJ 来源无效"})
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Vary", "Origin")
	if r.Method == http.MethodOptions {
		if r.Header.Get("Access-Control-Request-Method") != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeClientJSON(w, http.StatusUnsupportedMediaType, map[string]any{"success": false, "message": "需要 JSON 验证请求"})
		return
	}
	var body struct {
		Challenge string `json:"challenge"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 12288)).Decode(&body); err != nil {
		writeClientJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "验证请求格式无效"})
		return
	}
	claims, err := judgebridge.Unverified(body.Challenge)
	if err != nil || claims.Origin != origin || claims.Audience != "cwxu-challenge" || (claims.Purpose != "login" && claims.Purpose != "submit") {
		writeClientJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "验证请求来源或用途不符"})
		return
	}
	state.mu.Lock()
	sender, assigned := state.sender, state.assignedID
	state.mu.Unlock()
	if sender == nil || assigned <= 0 {
		writeClientJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "message": "客户端尚未连接集控服务器"})
		return
	}
	id := uuid.NewString()
	waiter := make(chan model.JudgePresenceResponse, 1)
	judgeMu.Lock()
	judgeWaiters[id] = waiter
	judgeMu.Unlock()
	defer func() { judgeMu.Lock(); delete(judgeWaiters, id); judgeMu.Unlock() }()
	message, _ := json.Marshal(model.JudgePresenceRequest{Type: "judge_presence_request", CorrelationID: id, Challenge: body.Challenge, Origin: origin})
	if !sender.Send(message) {
		writeClientJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "message": "集控连接不可用，请重新连接"})
		return
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case result := <-waiter:
		state.mu.Lock()
		current := state.sender == sender
		state.mu.Unlock()
		if !current {
			writeClientJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "message": "集控连接已改变，请重新验证"})
			return
		}
		status := http.StatusOK
		if !result.Success {
			status = http.StatusForbidden
		}
		writeClientJSON(w, status, result)
	case <-timer.C:
		writeClientJSON(w, http.StatusGatewayTimeout, map[string]any{"success": false, "message": "集控未响应验证，请检查客户端连接"})
	case <-r.Context().Done():
		return
	}
}

func deliverJudgeProof(sender *safeSender, response model.JudgePresenceResponse) {
	state.mu.Lock()
	current := state.sender == sender
	if current && response.Success {
		state.checkinStatus = 1
		state.studentName = response.StudentName
		state.studentNum = response.StudentNumber
		state.checkinTime = response.CheckinTime
	}
	state.mu.Unlock()
	if !current {
		return
	}
	judgeMu.Lock()
	defer judgeMu.Unlock()
	if waiter := judgeWaiters[response.CorrelationID]; waiter != nil {
		select {
		case waiter <- response:
		default:
		}
		delete(judgeWaiters, response.CorrelationID)
	}
}

func serveJudgeStatus(w http.ResponseWriter, r *http.Request) {
	data, err := checkinPageFS.ReadFile("judge_status.html")
	if err != nil {
		http.Error(w, "page not found", 404)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}
