package service

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/model"

	"github.com/gorilla/websocket"
)

func TestTerminalWSUniqueSessionsAndReconnect(t *testing.T) {
	db, err := data.NewDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := data.NewDeviceRepo(db)
	hub := biz.NewHub(repo)
	termH := NewTerminalWSHandler(hub)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/terminal/{id}", termH.Serve)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsBase := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/terminal/"

	// 1. Offline device should close with 4404 instead of hanging silently.
	offlineWS, _, err := websocket.DefaultDialer.Dial(wsBase+"99?cols=80&rows=24", nil)
	if err != nil {
		t.Fatalf("dial offline terminal: %v", err)
	}
	_ = offlineWS.SetReadDeadline(time.Now().Add(3 * time.Second))
	var gotOfflineNotice bool
	for {
		_, msg, rerr := offlineWS.ReadMessage()
		if rerr != nil {
			if ce, ok := rerr.(*websocket.CloseError); !ok || ce.Code != 4404 {
				t.Fatalf("expected close 4404, got %v", rerr)
			}
			break
		}
		if strings.Contains(string(msg), "不在线") {
			gotOfflineNotice = true
		}
	}
	if !gotOfflineNotice {
		t.Fatal("expected offline notice before close")
	}

	// 2. Register a simulated online client on AssignedID=1.
	serverEnd, clientEnd := net.Pipe()
	defer serverEnd.Close()
	defer clientEnd.Close()

	clientConn := &biz.ClientConn{
		AssignedID: 1,
		Conn:       serverEnd,
		Send:       make(chan []byte, 64),
		Hub:        hub,
	}
	hub.Register(clientConn)
	defer hub.Unregister(clientConn)

	// Drain client TCP messages and record received messages.
	clientMsgs := make(chan map[string]interface{}, 32)
	go func() {
		dec := json.NewDecoder(clientEnd)
		for {
			var m map[string]interface{}
			if err := dec.Decode(&m); err != nil {
				return
			}
			clientMsgs <- m
		}
	}()

	// Wait briefly for Hub.Run() to register client #1.
	deadline := time.Now().Add(2 * time.Second)
	for !hub.IsOnline(1) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !hub.IsOnline(1) {
		t.Fatal("client 1 not online in hub")
	}

	// Open session 1.
	ws1, _, err := websocket.DefaultDialer.Dial(wsBase+"1?cols=100&rows=30", nil)
	if err != nil {
		t.Fatalf("dial session 1: %v", err)
	}
	msg1 := <-clientMsgs
	if msg1["type"] != "terminal_open" {
		t.Fatalf("expected terminal_open, got %v", msg1)
	}
	sid1, _ := msg1["session_id"].(string)
	if sid1 == "" || sid1 == "term_1" {
		t.Fatalf("expected unique session id, got %q", sid1)
	}

	// Close session 1 and immediately open session 2; a delayed terminal_closed
	// from session 1 must NOT kill session 2.
	_ = ws1.Close()
	ws2, _, err := websocket.DefaultDialer.Dial(wsBase+"1?cols=120&rows=40", nil)
	if err != nil {
		t.Fatalf("dial session 2: %v", err)
	}
	defer ws2.Close()

	var sid2 string
	for sid2 == "" {
		m := <-clientMsgs
		if m["type"] == "terminal_open" {
			sid2, _ = m["session_id"].(string)
		}
	}
	if sid2 == sid1 {
		t.Fatalf("session 2 reused session 1 ID %q", sid2)
	}

	// Simulate delayed terminal_closed for session 1.
	TerminalHub.Close(sid1)

	// Verify session 2 still receives output and forwards both JSON paste (binary) and resize (text).
	TerminalHub.Broadcast(sid2, "prompt# ")
	_ = ws2.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, out, err := ws2.ReadMessage()
	if err != nil || string(out) != "prompt# " {
		t.Fatalf("session 2 read = %q, err = %v", string(out), err)
	}

	// Binary frame starting with '{' must be treated as terminal_input, NOT swallowed as control!
	jsonPaste := []byte(`{"type":"resize","cols":1,"rows":1}`)
	if err := ws2.WriteMessage(websocket.BinaryMessage, jsonPaste); err != nil {
		t.Fatal(err)
	}
	// Text frame with resize must be treated as terminal_resize.
	if err := ws2.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":132,"rows":43}`)); err != nil {
		t.Fatal(err)
	}

	var sawPaste, sawResize bool
	timeout := time.After(2 * time.Second)
	for !(sawPaste && sawResize) {
		select {
		case m := <-clientMsgs:
			switch m["type"] {
			case "terminal_input":
				if m["session_id"] == sid2 && m["data"] == string(jsonPaste) {
					sawPaste = true
				}
			case "terminal_resize":
				if m["session_id"] == sid2 && int(m["cols"].(float64)) == 132 {
					sawResize = true
				}
			}
		case <-timeout:
			t.Fatalf("timed out waiting for paste(%v) & resize(%v)", sawPaste, sawResize)
		}
	}
}

func TestScreenProxyManyDevicesAndCoalescing(t *testing.T) {
	db, err := data.NewDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := data.NewDeviceRepo(db)

	var upstreamHits atomic.Int32
	fakeJPEG := []byte{0xff, 0xd8, 0xff, 0xd9}
	// Start an upstream mock client on :8090? ScreenProxyHandler dials <ip>:8090,
	// so test fetchThumbnail coalescing via a custom RoundTripper on h.client.
	proxy := NewScreenProxyHandler(repo)
	proxy.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		upstreamHits.Add(1)
		time.Sleep(25 * time.Millisecond)
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Type", "image/jpeg")
		rec.WriteHeader(http.StatusOK)
		_, _ = rec.Write(fakeJPEG)
		return rec.Result(), nil
	})

	// Populate 15 devices (> 7!)
	for i := 1; i <= 15; i++ {
		if err := repo.Create(&model.Device{
			AssignedID: i,
			Hostname:   "cwxu-icpc-" + strconv.Itoa(i),
			LocalIP:    "10.0.0." + strconv.Itoa(i),
			Connected:  true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/devices/{id}/screen", proxy.Proxy)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Fire 30 concurrent thumbnail requests across all 15 devices (2 viewers each).
	var wg sync.WaitGroup
	errs := make(chan error, 30)
	for viewer := 0; viewer < 2; viewer++ {
		for id := 1; id <= 15; id++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				resp, err := http.Get(srv.URL + "/api/devices/" + strconv.Itoa(id) + "/screen?hd=0&single=1")
				if err != nil {
					errs <- err
					return
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					errs <- &websocket.CloseError{Code: resp.StatusCode, Text: "unexpected status"}
				}
			}(id)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("screen request failed: %v", err)
	}
	// With 2 concurrent viewers on 15 devices within 350ms TTL, coalescing should
	// make at most 15 upstream calls instead of 30.
	if got := upstreamHits.Load(); got > 15 {
		t.Fatalf("expected coalesced upstream hits <= 15, got %d", got)
	}

	// Also check allowedRelayRequest allows /api/settings/screen while blocking /api/settings.
	if !allowedRelayRequest("GET", "/api/settings/screen") || !allowedRelayRequest("PUT", "/api/settings/screen") {
		t.Fatal("/api/settings/screen should be allowed through room relay")
	}
	if allowedRelayRequest("GET", "/api/settings") {
		t.Fatal("/api/settings must remain blocked from room relay")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
