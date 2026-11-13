package service

import (
	"sync"
)

// TerminalSession fans terminal output out to subscriber channels. Subscribers
// (browser handlers, cloud relays) own their sockets and are the only writers
// on them, so the TCP read loop never blocks on, or races with, a WebSocket.
type TerminalSession struct {
	Streams map[chan []byte]bool
	Mu      sync.Mutex
}

// TerminalHubManager manages terminal sessions identified by session ID.
type TerminalHubManager struct {
	Mu       sync.RWMutex
	Sessions map[string]*TerminalSession
}

var TerminalHub = &TerminalHubManager{
	Sessions: make(map[string]*TerminalSession),
}

// terminalStreamBuffer is generous so bursts (e.g. `cat` of a large file) do
// not drop a viewer; a subscriber that still falls behind is disconnected.
const terminalStreamBuffer = 1024

// SubscribeStream registers a channel that receives every output chunk of the
// session. The returned func unsubscribes; the channel is closed when the
// session ends or the subscriber is too slow.
func (h *TerminalHubManager) SubscribeStream(sessionID string) (<-chan []byte, func()) {
	h.Mu.Lock()
	session := h.Sessions[sessionID]
	if session == nil {
		session = &TerminalSession{Streams: make(map[chan []byte]bool)}
		h.Sessions[sessionID] = session
	}
	h.Mu.Unlock()
	stream := make(chan []byte, terminalStreamBuffer)
	session.Mu.Lock()
	session.Streams[stream] = true
	session.Mu.Unlock()
	return stream, func() {
		session.Mu.Lock()
		if session.Streams[stream] {
			delete(session.Streams, stream)
			close(stream)
		}
		session.Mu.Unlock()
	}
}

// Broadcast sends data to all subscribers of a terminal session.
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
	for stream := range s.Streams {
		select {
		case stream <- append([]byte(nil), msg...):
		default:
			delete(s.Streams, stream)
			close(stream)
		}
	}
}

// Close removes a terminal session and closes all subscriber channels.
func (h *TerminalHubManager) Close(sessionID string) {
	h.Mu.Lock()
	s, ok := h.Sessions[sessionID]
	if ok {
		delete(h.Sessions, sessionID)
	}
	h.Mu.Unlock()

	if s != nil {
		s.Mu.Lock()
		for stream := range s.Streams {
			delete(s.Streams, stream)
			close(stream)
		}
		s.Mu.Unlock()
	}
}
