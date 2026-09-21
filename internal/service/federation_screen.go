package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// ScreenStream relays a contestant's MJPEG screen stream from a room through
// the cloud so the admin browser can render it with a normal <img src="...">,
// exactly like a local server. The room opens the upstream stream locally and
// forwards the raw multipart bytes over the control channel.
func (federation *Federation) ScreenStream(writer http.ResponseWriter, httpRequest *http.Request) {
	room := httpRequest.PathValue("room")
	deviceID, err := strconv.Atoi(httpRequest.PathValue("id"))
	federation.mu.Lock()
	connection := federation.connections[room]
	federation.mu.Unlock()
	if err != nil || deviceID < 1 || connection == nil || federation.settings.GetDeployment().Mode != "cloud" {
		http.Error(writer, "room or device unavailable", http.StatusNotFound)
		return
	}
	hd := httpRequest.URL.Query().Get("hd") == "1"
	sessionID := uuid.NewString()
	key := room + ":" + sessionID
	stream := make(chan FederationMessage, 64)
	federation.mu.Lock()
	federation.screens[key] = stream
	federation.mu.Unlock()
	defer func() {
		federation.mu.Lock()
		delete(federation.screens, key)
		federation.mu.Unlock()
		_ = connection.send(FederationMessage{Type: "screen_close", ID: sessionID, DeviceID: deviceID})
	}()
	if err := connection.send(FederationMessage{Type: "screen_open", ID: sessionID, DeviceID: deviceID, HD: hd}); err != nil {
		http.Error(writer, "room offline", http.StatusBadGateway)
		return
	}
	flusher, _ := writer.(http.Flusher)
	started := false
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-httpRequest.Context().Done():
			return
		case message, ok := <-stream:
			if !ok {
				return
			}
			switch message.Type {
			case "screen_opened":
				contentType := message.ContentType
				if contentType == "" {
					contentType = "multipart/x-mixed-replace; boundary=frame"
				}
				writer.Header().Set("Content-Type", contentType)
				writer.Header().Set("Cache-Control", "no-store")
				writer.WriteHeader(http.StatusOK)
				started = true
				if flusher != nil {
					flusher.Flush()
				}
			case "screen_data":
				if !started {
					continue
				}
				if _, err := writer.Write(message.Body); err != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			case "screen_closed":
				if !started {
					http.Error(writer, "屏幕流不可用", http.StatusBadGateway)
				}
				return
			}
		case <-ticker.C:
			federation.mu.Lock()
			same := federation.connections[room] == connection
			federation.mu.Unlock()
			if !same {
				return
			}
		}
	}
}

func (federation *Federation) handleRelayScreen(ctx context.Context, connection *relayConnection, message FederationMessage, sessions map[string]context.CancelFunc) {
	if _, err := uuid.Parse(message.ID); err != nil {
		return
	}
	switch message.Type {
	case "screen_open":
		if stop := sessions[message.ID]; stop != nil {
			stop()
		}
		sessionCtx, cancel := context.WithCancel(ctx)
		sessions[message.ID] = cancel
		go federation.streamLocalScreen(sessionCtx, connection, message)
	case "screen_close":
		if stop := sessions[message.ID]; stop != nil {
			stop()
			delete(sessions, message.ID)
		}
	}
}

func (federation *Federation) streamLocalScreen(ctx context.Context, connection *relayConnection, message FederationMessage) {
	closed := func() {
		_ = connection.send(FederationMessage{Type: "screen_closed", ID: message.ID})
	}
	device, err := federation.devices.GetByAssignedID(message.DeviceID)
	if err != nil || device == nil {
		closed()
		return
	}
	ip := extractDeviceIP(device.LocalIP)
	if ip == "" {
		closed()
		return
	}
	hd := "0"
	if message.HD {
		hd = "1"
	}
	upstream := fmt.Sprintf("http://%s:8090/screen?hd=%s", ip, hd)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream, nil)
	if err != nil {
		closed()
		return
	}
	client := &http.Client{Timeout: 0}
	response, err := client.Do(request)
	if err != nil {
		closed()
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		closed()
		return
	}
	contentType := response.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "multipart/x-mixed-replace; boundary=frame"
	}
	if connection.send(FederationMessage{Type: "screen_opened", ID: message.ID, ContentType: contentType}) != nil {
		return
	}
	buffer := make([]byte, 64*1024)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buffer[:n])
			if connection.send(FederationMessage{Type: "screen_data", ID: message.ID, Body: chunk}) != nil {
				return
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				// The upstream stream ended (device offline / capture disabled).
			}
			break
		}
		if ctx.Err() != nil {
			return
		}
	}
	closed()
}
