package service

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/model"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// TerminalWSHandler handles browser WebSocket connections for interactive terminal.
type TerminalWSHandler struct {
	hub *biz.Hub
}

func NewTerminalWSHandler(hub *biz.Hub) *TerminalWSHandler {
	return &TerminalWSHandler{hub: hub}
}

const maxTermCols, maxTermRows = 500, 200

// Serve handles browser terminal WebSocket (GET /ws/terminal/{device_id}).
//
// Every browser connection gets its own session ID. A shared per-device ID let
// a late "terminal_closed" from a previous session tear down the next one,
// which made reconnecting fail. This goroutine is also the only writer on the
// WebSocket (gorilla forbids concurrent writers); output arrives through a
// channel from TerminalHub.
func (h *TerminalWSHandler) Serve(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	deviceID, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "invalid device id", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[terminal-ws] upgrade: %v", err)
		return
	}
	defer conn.Close()
	conn.SetReadLimit(64 << 10)

	client := h.hub.GetClient(deviceID)
	if client == nil {
		writeTermNotice(conn, "\x1b[31m设备 #"+idStr+" 不在线，无法打开终端\x1b[0m\r\n")
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4404, "device offline"), time.Now().Add(time.Second))
		return
	}

	sessionID := "term_" + idStr + "_" + uuid.NewString()[:8]
	stream, unsubscribe := TerminalHub.SubscribeStream(sessionID)
	defer func() {
		unsubscribe()
		TerminalHub.Close(sessionID)
	}()

	send := func(v interface{}) bool {
		data, _ := json.Marshal(v)
		data = append(data, '\n')
		select {
		case client.Send <- data:
			return true
		case <-time.After(2 * time.Second):
			return false
		}
	}

	cols, rows := 80, 24
	if v, err := strconv.Atoi(r.URL.Query().Get("cols")); err == nil && v > 0 && v <= maxTermCols {
		cols = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("rows")); err == nil && v > 0 && v <= maxTermRows {
		rows = v
	}
	if !send(model.TerminalOpenMessage{Type: "terminal_open", SessionID: sessionID, Cols: cols, Rows: rows}) {
		writeTermNotice(conn, "\x1b[31m设备连接繁忙，请稍后重试\x1b[0m\r\n")
		return
	}
	defer send(model.TerminalCloseMessage{Type: "terminal_close", SessionID: sessionID})

	conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(wsPongWait))
		return nil
	})

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			kind, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			conn.SetReadDeadline(time.Now().Add(wsPongWait))
			// Keystrokes arrive as binary frames; text frames that parse as
			// {"type":"resize"} are control messages. Anything else is input,
			// so pasting JSON into the shell still works.
			if kind == websocket.TextMessage && len(msg) > 0 && msg[0] == '{' {
				var ctrl struct {
					Type string `json:"type"`
					Cols int    `json:"cols"`
					Rows int    `json:"rows"`
				}
				if json.Unmarshal(msg, &ctrl) == nil && ctrl.Type == "resize" {
					if ctrl.Cols > 0 && ctrl.Rows > 0 {
						send(model.TerminalResizeMessage{
							Type:      "terminal_resize",
							SessionID: sessionID,
							Cols:      min(ctrl.Cols, maxTermCols),
							Rows:      min(ctrl.Rows, maxTermRows),
						})
					}
					continue
				}
			}
			if !send(model.TerminalInputMessage{Type: "terminal_input", SessionID: sessionID, Data: string(msg)}) {
				return
			}
		}
	}()

	ping := time.NewTicker(wsPingPeriod)
	defer ping.Stop()
	for {
		select {
		case <-readDone:
			return
		case data, ok := <-stream:
			if !ok {
				return
			}
			conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
				return
			}
		case <-ping.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait)); err != nil {
				return
			}
		}
	}
}

func writeTermNotice(conn *websocket.Conn, text string) {
	conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
	_ = conn.WriteMessage(websocket.BinaryMessage, []byte(text))
}
