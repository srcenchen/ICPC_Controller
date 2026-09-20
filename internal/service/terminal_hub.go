package service

import (
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// TerminalSession holds browser WebSocket connections for a terminal session.
type TerminalSession struct {
	Conns map[*websocket.Conn]bool
	Mu    sync.Mutex
}

// TerminalHubManager manages terminal sessions identified by session ID.
type TerminalHubManager struct {
	Mu       sync.RWMutex
	Sessions map[string]*TerminalSession
}

var TerminalHub = &TerminalHubManager{
	Sessions: make(map[string]*TerminalSession),
}

// Subscribe adds a browser WS connection to a terminal session.
func (h *TerminalHubManager) Subscribe(sessionID string, conn *websocket.Conn) {
	h.Mu.Lock()
	s, ok := h.Sessions[sessionID]
	if !ok {
		s = &TerminalSession{Conns: make(map[*websocket.Conn]bool)}
		h.Sessions[sessionID] = s
	}
	h.Mu.Unlock()

	s.Mu.Lock()
	s.Conns[conn] = true
	s.Mu.Unlock()
	log.Printf("[terminal] %s: browser subscribed (%d listeners)", sessionID, len(s.Conns))
}

// Unsubscribe removes a browser WS connection from a terminal session.
func (h *TerminalHubManager) Unsubscribe(sessionID string, conn *websocket.Conn) {
	h.Mu.RLock()
	s, ok := h.Sessions[sessionID]
	h.Mu.RUnlock()
	if !ok {
		return
	}
	s.Mu.Lock()
	delete(s.Conns, conn)
	s.Mu.Unlock()
}

// Broadcast sends data to all browser connections in a terminal session.
func (h *TerminalHubManager) Broadcast(sessionID string, data interface{}) {
	h.Mu.RLock()
	s, ok := h.Sessions[sessionID]
	h.Mu.RUnlock()
	if !ok {
		return
	}

	var msg []byte
	switch v := data.(type) {
	case string:
		msg = []byte(v)
	case []byte:
		msg = v
	default:
		return
	}

	s.Mu.Lock()
	defer s.Mu.Unlock()
	for conn := range s.Conns {
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
			log.Printf("[terminal] %s: write error: %v", sessionID, err)
			delete(s.Conns, conn)
		}
	}
}

// Close removes a terminal session and closes all browser connections.
func (h *TerminalHubManager) Close(sessionID string) {
	h.Mu.Lock()
	s, ok := h.Sessions[sessionID]
	if ok {
		delete(h.Sessions, sessionID)
	}
	h.Mu.Unlock()

	if s != nil {
		s.Mu.Lock()
		for conn := range s.Conns {
			conn.Close()
		}
		s.Mu.Unlock()
	}
}
