package service

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var broadcastWSUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// BroadcastWSHub manages WebSocket connections from broadcast display pages.
type BroadcastWSHub struct {
	mu         sync.RWMutex
	conns      map[string]map[*wsConn]bool
	carousel   map[string]chan struct{}
	carouselMu sync.Mutex
	// Carousel page provider (set by handler).
	PageProvider func(mode string) []pageInfo
	// SnapshotProvider returns full pages_updated payload for a mode (set by handler).
	SnapshotProvider func(mode string) []byte
}

// wsConn wraps websocket.Conn with a write mutex (gorilla requires single writer).
type wsConn struct {
	c  *websocket.Conn
	mu sync.Mutex
}

type pageInfo struct {
	DurationMs int
}

var BroadcastWS = &BroadcastWSHub{
	conns:    make(map[string]map[*wsConn]bool),
	carousel: make(map[string]chan struct{}),
}

func (h *BroadcastWSHub) StartCarousel(mode string) {
	h.carouselMu.Lock()
	if stop, ok := h.carousel[mode]; ok {
		close(stop)
	}
	stop := make(chan struct{})
	h.carousel[mode] = stop
	h.carouselMu.Unlock()

	go h.runCarousel(mode, stop)
}

func (h *BroadcastWSHub) StopCarousel(mode string) {
	h.carouselMu.Lock()
	if stop, ok := h.carousel[mode]; ok {
		close(stop)
		delete(h.carousel, mode)
	}
	h.carouselMu.Unlock()
}

func (h *BroadcastWSHub) runCarousel(mode string, stop chan struct{}) {
	var pageIdx int

	// Initial delay to let pages load.
	select {
	case <-stop:
		return
	case <-time.After(500 * time.Millisecond):
	}

	for {
		if h.PageProvider == nil {
			select {
			case <-stop:
				return
			case <-time.After(time.Second):
			}
			continue
		}
		pages := h.PageProvider(mode)
		if len(pages) == 0 {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		if pageIdx >= len(pages) {
			pageIdx = 0
		}

		// Send current page.
		msg, _ := json.Marshal(map[string]interface{}{
			"type":       "page_switch",
			"mode":       mode,
			"page_index": pageIdx,
			"total":      len(pages),
		})
		h.Broadcast(mode, msg)

		// Wait for page duration.
		dur := pages[pageIdx].DurationMs
		if dur <= 0 {
			dur = 10000
		}
		select {
		case <-stop:
			return
		case <-time.After(time.Duration(dur) * time.Millisecond):
		}

		pageIdx = (pageIdx + 1) % len(pages)
	}
}

func (h *BroadcastWSHub) Serve(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "before"
	}
	raw, err := broadcastWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[broadcast-ws] upgrade: %v", err)
		return
	}
	conn := &wsConn{c: raw}

	h.mu.Lock()
	if h.conns[mode] == nil {
		h.conns[mode] = make(map[*wsConn]bool)
	}
	h.conns[mode][conn] = true
	total := len(h.conns[mode])
	h.mu.Unlock()

	log.Printf("[broadcast-ws] mode=%s connected (%d total)", mode, total)

	// Push current pages immediately so display is not blank.
	if h.SnapshotProvider != nil {
		if snap := h.SnapshotProvider(mode); len(snap) > 0 {
			conn.mu.Lock()
			_ = conn.c.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_ = conn.c.WriteMessage(websocket.TextMessage, snap)
			conn.mu.Unlock()
		}
	}

	defer func() {
		h.mu.Lock()
		if h.conns[mode] != nil {
			delete(h.conns[mode], conn)
			if len(h.conns[mode]) == 0 {
				delete(h.conns, mode)
			}
		}
		h.mu.Unlock()
		conn.c.Close()
		log.Printf("[broadcast-ws] mode=%s disconnected", mode)
	}()

	// Keep connection alive with ping/pong.
	conn.c.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.c.SetPongHandler(func(string) error {
		conn.c.SetReadDeadline(time.Now().Add(wsPongWait))
		return nil
	})

	// Send periodic pings to keep the connection alive through proxies/NATs.
	pingTicker := time.NewTicker(wsPingPeriod)
	defer pingTicker.Stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-pingTicker.C:
				conn.mu.Lock()
				conn.c.SetWriteDeadline(time.Now().Add(wsWriteWait))
				err := conn.c.WriteMessage(websocket.PingMessage, nil)
				conn.mu.Unlock()
				if err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()

	for {
		_, _, err := conn.c.ReadMessage()
		if err != nil {
			break
		}
	}
	pingTicker.Stop()
}

// Broadcast sends a message to all display connections for a given mode.
func (h *BroadcastWSHub) Broadcast(mode string, msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for conn := range h.conns[mode] {
		conn.mu.Lock()
		conn.c.SetWriteDeadline(time.Now().Add(5 * time.Second))
		err := conn.c.WriteMessage(websocket.TextMessage, msg)
		conn.mu.Unlock()
		if err != nil {
			log.Printf("[broadcast-ws] write error: %v", err)
		}
	}
}

// BroadcastAll sends a message to all display connections of all modes.
func (h *BroadcastWSHub) BroadcastAll(msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, conns := range h.conns {
		for conn := range conns {
			conn.mu.Lock()
			conn.c.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_ = conn.c.WriteMessage(websocket.TextMessage, msg)
			conn.mu.Unlock()
		}
	}
}
