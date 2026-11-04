package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ICPCRemoteControl/internal/model"
	"github.com/google/uuid"
)

// Serialize OJ probes so simultaneous logins cannot switch the device identity
// in the middle of another probe's check-in / verification sequence.
var judgeProbeGate = make(chan struct{}, 1)

type judgeRequest struct {
	Purpose       string `json:"purpose"`
	StudentNumber string `json:"student_num"`
	StudentName   string `json:"student_name"`
}

func loopbackAddress(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func judgeConnectionCurrent(sender *safeSender, assigned int) bool {
	state.mu.Lock()
	current := sender != nil && state.sender == sender && state.assignedID == assigned && assigned > 0
	state.mu.Unlock()
	if !current {
		return false
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	return !sender.closed
}

// Use the original check-in protocol and wait for this request's server reply.
func queryJudgeCheckin(ctx context.Context, sender *safeSender, kind, name, number string) (model.CheckinResponseMessage, error) {
	id := "ojcheck_" + uuid.NewString()
	waiter := make(chan model.CheckinResponseMessage, 1)
	checkinMu.Lock()
	checkinWaiters[id] = waiter
	checkinMu.Unlock()
	defer func() { checkinMu.Lock(); delete(checkinWaiters, id); checkinMu.Unlock() }()
	if !sendJSONSafe(sender, model.CheckinMessage{Type: kind, CorrelationID: id, StudentName: name, StudentNum: number}) {
		return model.CheckinResponseMessage{}, errors.New("集控连接不可用，请重新连接")
	}
	select {
	case response := <-waiter:
		if !response.Success {
			if response.Message != "" {
				return response, errors.New(response.Message)
			}
			return response, errors.New("集控未确认成功，请重试")
		}
		return response, nil
	case <-ctx.Done():
		return model.CheckinResponseMessage{}, errors.New("集控未响应，请检查客户端连接")
	}
}

func probeJudgeConnection(ctx context.Context, sender *safeSender, assigned int, request judgeRequest) (model.CheckinResponseMessage, error) {
	var status model.CheckinResponseMessage
	if !judgeConnectionCurrent(sender, assigned) {
		return status, errors.New("客户端尚未连接集控服务器")
	}
	kind, name, number := "checkin_query", "", ""
	if request.Purpose == "login" {
		kind, name, number = "checkin", request.StudentName, request.StudentNumber
	}
	status, err := queryJudgeCheckin(ctx, sender, kind, name, number)
	if err != nil {
		return status, err
	}
	if !judgeConnectionCurrent(sender, assigned) {
		return status, errors.New("集控连接已改变，请重新验证")
	}
	state.mu.Lock()
	if state.sender == sender && state.assignedID == assigned {
		if request.Purpose == "login" {
			// The original check-in success reply does not include identity/status.
			state.checkinStatus = 1
			state.studentName, state.studentNum = name, number
			state.checkinTime = time.Now().Format(time.RFC3339)
		} else {
			state.checkinStatus = status.CheckinStatus
			state.studentName, state.studentNum = status.StudentName, status.StudentNum
			state.checkinTime, state.checkoutTime = status.CheckinTime, status.CheckoutTime
		}
	}
	state.mu.Unlock()
	return status, nil
}

// Only the contestant browser on this machine can call this endpoint. Online
// status and automatic check-in use the unchanged control-server TCP protocol.
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
	var body judgeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 12288)).Decode(&body); err != nil {
		writeClientJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "验证请求格式无效"})
		return
	}
	if body.Purpose != "login" && body.Purpose != "submit" {
		writeClientJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "验证请求用途无效"})
		return
	}
	if body.Purpose == "login" && (strings.TrimSpace(body.StudentName) == "" || strings.TrimSpace(body.StudentNumber) == "") {
		writeClientJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请填写姓名和学号"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	select {
	case judgeProbeGate <- struct{}{}:
		defer func() { <-judgeProbeGate }()
	case <-ctx.Done():
		writeClientJSON(w, http.StatusGatewayTimeout, map[string]any{"success": false, "message": "本机正在验证其他请求，请重试"})
		return
	}
	state.mu.Lock()
	sender, assigned := state.sender, state.assignedID
	state.mu.Unlock()
	if _, err := probeJudgeConnection(ctx, sender, assigned, body); err != nil {
		writeClientJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "message": err.Error()})
		return
	}
	writeClientJSON(w, http.StatusOK, map[string]any{"success": true, "device_id": assigned})
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
