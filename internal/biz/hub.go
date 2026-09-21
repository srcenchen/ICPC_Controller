package biz

import (
	"encoding/json"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/model"

	"github.com/gorilla/websocket"
)

// ClientConn wraps a TCP connection from a contestant machine.
type ClientConn struct {
	AssignedID int
	Conn       net.Conn
	Send       chan []byte // serialized write channel
	Hub        *Hub
	done       chan struct{}
	// lastSeenUpdated is unix nanos of the last last_seen DB write. The struct
	// is shared with the hub, so keep the access atomic.
	lastSeenUpdated atomic.Int64
}

// ShouldUpdateLastSeen reports whether enough time has passed to write
// last_seen again, and claims the slot when it returns true.
func (c *ClientConn) ShouldUpdateLastSeen(minInterval time.Duration) bool {
	now := time.Now()
	prev := c.lastSeenUpdated.Load()
	if prev != 0 && now.Sub(time.Unix(0, prev)) <= minInterval {
		return false
	}
	return c.lastSeenUpdated.CompareAndSwap(prev, now.UnixNano())
}

// AdminConn wraps an admin browser WebSocket connection.
type AdminConn struct {
	Conn *websocket.Conn
	Send chan []byte
	Hub  *Hub
}

// Hub maintains the set of active client and admin connections.
type Hub struct {
	mu          sync.RWMutex
	clients     map[int]*ClientConn
	admins      map[*AdminConn]bool
	register    chan *ClientConn
	unregister  chan *ClientConn
	adminReg    chan *AdminConn
	adminUnreg  chan *AdminConn
	deviceRepo  *data.DeviceRepo
	eventRepo   *data.DeviceEventRepo
	connectHook atomic.Value // func(int)
}

// SetConnectHook registers a callback invoked (asynchronously) after a client
// connection is fully registered. Used to replay operation snapshots to devices
// that were offline when the operation was issued.
func (h *Hub) SetConnectHook(fn func(int)) {
	h.connectHook.Store(fn)
}

func (h *Hub) runConnectHook(assignedID int) {
	fn, _ := h.connectHook.Load().(func(int))
	if fn != nil {
		go fn(assignedID)
	}
}

// SetEventRepo enables online/offline history recording.
func (h *Hub) SetEventRepo(r *data.DeviceEventRepo) {
	h.mu.Lock()
	h.eventRepo = r
	h.mu.Unlock()
}

func (h *Hub) recordEvent(assignedID int, event, detail string) {
	h.mu.RLock()
	repo := h.eventRepo
	h.mu.RUnlock()
	if repo == nil {
		return
	}
	if err := repo.Add(assignedID, event, detail); err != nil {
		log.Printf("[hub] record %s event for device %d: %v", event, assignedID, err)
	}
}

// NewHub creates a new Hub and starts its run loop.
func NewHub(deviceRepo *data.DeviceRepo) *Hub {
	h := &Hub{
		clients:    make(map[int]*ClientConn),
		admins:     make(map[*AdminConn]bool),
		register:   make(chan *ClientConn),
		unregister: make(chan *ClientConn),
		adminReg:   make(chan *AdminConn),
		adminUnreg: make(chan *AdminConn),
		deviceRepo: deviceRepo,
	}
	go h.Run()
	return h
}

// Run is the main hub event loop.
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			if old, ok := h.clients[client.AssignedID]; ok && old != client {
				// Replace stale connection: stop its write pump and close TCP.
				close(old.done)
				old.Conn.Close()
			}
			h.clients[client.AssignedID] = client
			h.mu.Unlock()
			if err := h.deviceRepo.UpdateConnected(client.AssignedID, true); err != nil {
				log.Printf("[hub] failed to mark device %d online: %v", client.AssignedID, err)
			}
			log.Printf("[hub] device %d connected", client.AssignedID)
			h.recordEvent(client.AssignedID, "online", client.Conn.RemoteAddr().String())
			h.broadcastAdminEvent("device_connected", map[string]interface{}{
				"assigned_id": client.AssignedID,
			})
			h.runConnectHook(client.AssignedID)

		case client := <-h.unregister:
			h.mu.Lock()
			current, ok := h.clients[client.AssignedID]
			// Only remove if this is still the active connection (reconnect-safe).
			removed := false
			if ok && current == client {
				delete(h.clients, client.AssignedID)
				close(client.done)
				removed = true
			}
			h.mu.Unlock()
			if !removed {
				// Stale unregister after reconnect — do not mark offline.
				continue
			}
			if err := h.deviceRepo.UpdateConnected(client.AssignedID, false); err != nil {
				log.Printf("[hub] failed to mark device %d offline: %v", client.AssignedID, err)
			}
			log.Printf("[hub] device %d disconnected", client.AssignedID)
			h.recordEvent(client.AssignedID, "offline", "")
			h.broadcastAdminEvent("device_disconnected", map[string]interface{}{
				"assigned_id": client.AssignedID,
			})

		case admin := <-h.adminReg:
			h.mu.Lock()
			h.admins[admin] = true
			h.mu.Unlock()
			log.Printf("[hub] admin connected (%d total)", len(h.admins))

		case admin := <-h.adminUnreg:
			h.mu.Lock()
			if _, ok := h.admins[admin]; ok {
				delete(h.admins, admin)
				close(admin.Send)
			}
			h.mu.Unlock()
			log.Printf("[hub] admin disconnected (%d remaining)", len(h.admins))
		}
	}
}

func (h *Hub) Register(client *ClientConn) {
	client.done = make(chan struct{})
	// Start write pump before registering.
	go func() {
		for {
			var msg []byte
			select {
			case <-client.done:
				return
			case msg = <-client.Send:
			}
			client.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := client.Conn.Write(msg); err != nil {
				client.Conn.Close()
				return
			}
		}
	}()
	h.register <- client
}
func (h *Hub) Unregister(client *ClientConn) { h.unregister <- client }

func (h *Hub) RegisterAdmin(admin *AdminConn)   { h.adminReg <- admin }
func (h *Hub) UnregisterAdmin(admin *AdminConn) { h.adminUnreg <- admin }

func (h *Hub) GetClient(assignedID int) *ClientConn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.clients[assignedID]
}

func (h *Hub) IsOnline(assignedID int) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.clients[assignedID]
	return ok
}

func (h *Hub) OnlineCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// OnlineIDs returns assigned IDs of all currently connected clients.
func (h *Hub) OnlineIDs() []int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ids := make([]int, 0, len(h.clients))
	for id := range h.clients {
		ids = append(ids, id)
	}
	return ids
}

// TrySend attempts a non-blocking send to a client. Returns false if offline or buffer full.
func (h *Hub) TrySend(assignedID int, data []byte) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	client := h.clients[assignedID]
	if client == nil {
		return false
	}
	select {
	case client.Send <- data:
		return true
	default:
		return false
	}
}

// BroadcastToClients sends a message to all connected TCP clients.
func (h *Hub) BroadcastToClients(data []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, client := range h.clients {
		select {
		case client.Send <- data:
		default:
		}
	}
}

// Kick closes a single client's TCP connection, forcing it to reconnect.
func (h *Hub) Kick(assignedID int) {
	h.mu.RLock()
	client, ok := h.clients[assignedID]
	h.mu.RUnlock()
	if ok {
		client.Conn.Close()
		log.Printf("[hub] kicked device %d", assignedID)
	}
}

// KickAll closes all client TCP connections, forcing them to reconnect.
func (h *Hub) KickAll() {
	h.mu.RLock()
	clients := make([]*ClientConn, 0, len(h.clients))
	for _, c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.RUnlock()

	for _, c := range clients {
		c.Conn.Close()
	}
	log.Printf("[hub] kicked %d clients", len(clients))
}

func (h *Hub) BroadcastAdminEvent(event string, data interface{}) {
	h.broadcastAdminEvent(event, data)
}

func (h *Hub) broadcastAdminEvent(event string, data interface{}) {
	msg, err := json.Marshal(model.AdminEvent{Event: event, Data: data})
	if err != nil {
		log.Printf("[hub] failed to marshal admin event: %v", err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	for admin := range h.admins {
		select {
		case admin.Send <- msg:
		default:
		}
	}
}
