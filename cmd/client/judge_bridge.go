package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"ICPCRemoteControl/internal/model"
	"github.com/google/uuid"
)

// Serialize OJ probes so simultaneous logins cannot switch the device identity
// in the middle of another probe's check-in / verification sequence.
var judgeProbeGate = make(chan struct{}, 1)
var judgeStudentNumber = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,59}$`)
var judgeNonce = regexp.MustCompile(`^[a-f0-9]{64}$`)

type judgeChallenge struct {
	Audience      string `json:"aud"`
	Nonce         string `json:"nonce"`
	SessionHash   string `json:"session_hash"`
	UID           int    `json:"uid"`
	CID           int    `json:"cid"`
	StudentNumber string `json:"student_number"`
	StudentName   string `json:"student_name"`
	Origin        string `json:"origin"`
	Purpose       string `json:"purpose"`
	IssuedAt      int64  `json:"iat"`
	ExpiresAt     int64  `json:"exp"`
}

func loopbackAddress(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func readJudgeChallenge(token, origin string) (judgeChallenge, map[string]json.RawMessage, error) {
	var claims judgeChallenge
	parts := strings.Split(token, ".")
	if len(token) > 8192 || len(parts) != 3 || parts[0] != "v1" || parts[2] == "" {
		return claims, nil, errors.New("验证请求格式无效")
	}
	// Only DOMserver knows its session-signing key. The client reads the
	// challenge, performs the local probe and returns its fields unchanged.
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(payload, &claims) != nil {
		return claims, nil, errors.New("验证请求内容无效")
	}
	now := time.Now().Unix()
	if claims.Audience != "cwxu-challenge" || claims.Origin != origin ||
		(claims.Purpose != "login" && claims.Purpose != "submit") ||
		claims.UID <= 0 || claims.CID <= 0 || !judgeNonce.MatchString(claims.Nonce) ||
		!judgeNonce.MatchString(claims.SessionHash) || !judgeStudentNumber.MatchString(claims.StudentNumber) ||
		!utf8.ValidString(claims.StudentName) || utf8.RuneCountInString(claims.StudentName) < 1 ||
		utf8.RuneCountInString(claims.StudentName) > 128 || strings.ContainsRune(claims.StudentName, '|') ||
		strings.IndexFunc(claims.StudentName, unicode.IsControl) >= 0 ||
		claims.IssuedAt > now+5 || claims.ExpiresAt <= now || claims.ExpiresAt <= claims.IssuedAt ||
		claims.ExpiresAt-claims.IssuedAt > 60 {
		return claims, nil, errors.New("验证请求身份、来源、用途或有效期不符")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return claims, nil, errors.New("验证请求内容无效")
	}
	return claims, fields, nil
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

// These queries use the existing control-server protocol. Unlike the manual
// status page, an OJ probe must fail on timeout and never fall back to a cache.
func queryJudgeConfig(ctx context.Context, sender *safeSender) error {
	id := "ojcfg_" + uuid.NewString()
	waiter := make(chan model.CheckinConfigMessage, 1)
	checkinMu.Lock()
	configWaiters[id] = waiter
	checkinMu.Unlock()
	defer func() { checkinMu.Lock(); delete(configWaiters, id); checkinMu.Unlock() }()
	if !sendJSONSafe(sender, model.CheckinConfigMessage{Type: "query_checkin_config", CorrelationID: id}) {
		return errors.New("集控连接不可用，请重新连接")
	}
	select {
	case <-waiter:
		return nil
	case <-ctx.Done():
		return errors.New("集控未响应，请检查客户端连接")
	}
}

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
			return response, errors.New("集控未确认签到状态，请重试")
		}
		return response, nil
	case <-ctx.Done():
		return model.CheckinResponseMessage{}, errors.New("集控未响应，请检查客户端连接")
	}
}

func probeJudgeConnection(ctx context.Context, sender *safeSender, assigned int, claims judgeChallenge) (model.CheckinResponseMessage, error) {
	var status model.CheckinResponseMessage
	if !judgeConnectionCurrent(sender, assigned) {
		return status, errors.New("客户端尚未连接集控服务器")
	}
	if err := queryJudgeConfig(ctx, sender); err != nil {
		return status, err
	}
	if !judgeConnectionCurrent(sender, assigned) {
		return status, errors.New("集控连接已改变，请重新验证")
	}
	status, err := queryJudgeCheckin(ctx, sender, "checkin_query", "", "")
	if err != nil {
		return status, err
	}
	matches := func() bool {
		return status.CheckinStatus == 1 && status.StudentName == claims.StudentName && status.StudentNum == claims.StudentNumber
	}
	if !judgeConnectionCurrent(sender, assigned) {
		return status, errors.New("集控连接已改变，请重新验证")
	}
	if claims.Purpose == "login" && !matches() {
		if _, err := queryJudgeCheckin(ctx, sender, "checkin", claims.StudentName, claims.StudentNumber); err != nil {
			return status, err
		}
		if !judgeConnectionCurrent(sender, assigned) {
			return status, errors.New("集控连接已改变，请重新验证")
		}
		status, err = queryJudgeCheckin(ctx, sender, "checkin_query", "", "")
		if err != nil {
			return status, err
		}
	}
	if !judgeConnectionCurrent(sender, assigned) {
		return status, errors.New("集控连接已改变，请重新验证")
	}
	if !matches() {
		return status, errors.New("本机签到身份与比赛账号不一致，请重新登录比赛")
	}
	state.mu.Lock()
	if state.sender == sender && state.assignedID == assigned {
		state.checkinStatus = status.CheckinStatus
		state.studentName = status.StudentName
		state.studentNum = status.StudentNum
		state.checkinTime = status.CheckinTime
		state.checkoutTime = status.CheckoutTime
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
	var body struct {
		Challenge string `json:"challenge"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 12288)).Decode(&body); err != nil {
		writeClientJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "验证请求格式无效"})
		return
	}
	claims, fields, err := readJudgeChallenge(body.Challenge, origin)
	if err != nil {
		writeClientJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
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
	if _, err := probeJudgeConnection(ctx, sender, assigned, claims); err != nil {
		writeClientJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "message": err.Error()})
		return
	}
	hash := sha256.Sum256([]byte(body.Challenge))
	for key, value := range map[string]any{"challenge_hash": hex.EncodeToString(hash[:]), "device_id": assigned, "connected": true, "checked_at": time.Now().Unix()} {
		encoded, _ := json.Marshal(value)
		fields[key] = encoded
	}
	payload, _ := json.Marshal(fields)
	proof := "local.v1." + base64.RawURLEncoding.EncodeToString(payload)
	writeClientJSON(w, http.StatusOK, map[string]any{"success": true, "proof": proof})
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
