package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"ICPCRemoteControl/internal/model"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func (federation *Federation) Terminal(writer http.ResponseWriter, httpRequest *http.Request) {
	room := httpRequest.PathValue("room")
	deviceID, err := strconv.Atoi(httpRequest.PathValue("id"))
	federation.mu.Lock()
	connection := federation.connections[room]
	federation.mu.Unlock()
	if err != nil || deviceID < 1 || connection == nil || federation.settings.GetDeployment().Mode != "cloud" {
		http.Error(writer, "room or device unavailable", 404)
		return
	}
	conn, err := upgrader.Upgrade(writer, httpRequest, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(64 << 10)
	sessionID := uuid.NewString()
	stream := make(chan FederationMessage, 128)
	federation.mu.Lock()
	federation.terminals[room+":"+sessionID] = stream
	federation.mu.Unlock()
	defer func() {
		federation.mu.Lock()
		delete(federation.terminals, room+":"+sessionID)
		federation.mu.Unlock()
		_ = connection.send(FederationMessage{Type: "terminal_close", ID: sessionID, DeviceID: deviceID})
	}()
	cols, _ := strconv.Atoi(httpRequest.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(httpRequest.URL.Query().Get("rows"))
	if connection.send(FederationMessage{Type: "terminal_open", ID: sessionID, DeviceID: deviceID, Cols: cols, Rows: rows}) != nil {
		return
	}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			_, body, err := conn.ReadMessage()
			if err != nil {
				return
			}
			message := FederationMessage{Type: "terminal_input", ID: sessionID, DeviceID: deviceID, Body: body}
			var control struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(body, &control) == nil && control.Type == "resize" {
				message.Type = "terminal_resize"
				message.Cols = control.Cols
				message.Rows = control.Rows
			}
			if connection.send(message) != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-readDone:
			return
		case message, ok := <-stream:
			if !ok {
				return
			}
			if message.Type == "terminal_closed" {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if conn.WriteMessage(websocket.BinaryMessage, message.Body) != nil {
				return
			}
		case <-ticker.C:
			federation.mu.Lock()
			same := federation.connections[room] == connection
			federation.mu.Unlock()
			if !same {
				return
			}
			if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
				return
			}
		}
	}
}

func (federation *Federation) handleRelayTerminal(ctx context.Context, connection *relayConnection, message FederationMessage, sessions map[string]context.CancelFunc) {
	if _, err := uuid.Parse(message.ID); err != nil {
		return
	}
	sessionID := "cloud_" + message.ID
	send := func(value interface{}) bool {
		raw, _ := json.Marshal(value)
		return federation.hub.TrySend(message.DeviceID, append(raw, '\n'))
	}
	cols, rows := message.Cols, message.Rows
	if cols < 1 || cols > 500 {
		cols = 80
	}
	if rows < 1 || rows > 200 {
		rows = 24
	}
	switch message.Type {
	case "terminal_open":
		if len(sessions) >= 32 || !federation.hub.IsOnline(message.DeviceID) {
			_ = connection.send(FederationMessage{Type: "terminal_closed", ID: message.ID})
			return
		}
		if stop := sessions[message.ID]; stop != nil {
			stop()
		}
		sessionCtx, cancel := context.WithCancel(ctx)
		sessions[message.ID] = cancel
		stream, unsubscribe := TerminalHub.SubscribeStream(sessionID)
		if !send(model.TerminalOpenMessage{Type: "terminal_open", SessionID: sessionID, Cols: cols, Rows: rows}) {
			cancel()
		}
		go func() {
			defer unsubscribe()
			defer TerminalHub.Close(sessionID)
			defer send(model.TerminalCloseMessage{Type: "terminal_close", SessionID: sessionID})
			defer connection.send(FederationMessage{Type: "terminal_closed", ID: message.ID})
			for {
				select {
				case <-sessionCtx.Done():
					return
				case body, ok := <-stream:
					if !ok {
						return
					}
					if connection.send(FederationMessage{Type: "terminal_output", ID: message.ID, Body: body}) != nil {
						return
					}
				}
			}
		}()
	case "terminal_input":
		if sessions[message.ID] != nil {
			send(model.TerminalInputMessage{Type: "terminal_input", SessionID: sessionID, Data: string(message.Body)})
		}
	case "terminal_resize":
		if sessions[message.ID] != nil {
			send(model.TerminalResizeMessage{Type: "terminal_resize", SessionID: sessionID, Cols: cols, Rows: rows})
		}
	case "terminal_close":
		if stop := sessions[message.ID]; stop != nil {
			stop()
			delete(sessions, message.ID)
		}
	}
}
