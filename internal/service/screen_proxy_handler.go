package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"ICPCRemoteControl/internal/data"
)

const screenThumbTTL = 350 * time.Millisecond

type cachedFrame struct {
	data      []byte
	fetchedAt time.Time
}

type inflightFrame struct {
	done chan struct{}
	data []byte
	err  error
	code int
}

// ScreenProxyHandler proxies contestant screen streams through the server
// so the admin browser does not need direct access to each machine :8090.
//
// Thumbnail snapshots (?single=1&hd=0) are coalesced across concurrent viewers
// and cached briefly so large screen walls (dozens or hundreds of machines)
// never stampede a contestant machine or exhaust upstream sockets.
type ScreenProxyHandler struct {
	deviceRepo *data.DeviceRepo
	client     *http.Client
	mu         sync.Mutex
	thumbs     map[int]cachedFrame
	inflight   map[int]*inflightFrame
}

// NewScreenProxyHandler creates a ScreenProxyHandler.
func NewScreenProxyHandler(deviceRepo *data.DeviceRepo) *ScreenProxyHandler {
	return &ScreenProxyHandler{
		deviceRepo: deviceRepo,
		thumbs:     make(map[int]cachedFrame),
		inflight:   make(map[int]*inflightFrame),
		client: &http.Client{
			Timeout: 0, // streaming (single-frame requests set their own context deadline)
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   3 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				MaxIdleConns:          256,
				MaxIdleConnsPerHost:   4,
				IdleConnTimeout:       60 * time.Second,
				ResponseHeaderTimeout: 5 * time.Second,
			},
		},
	}
}

// Proxy streams GET /api/devices/{id}/screen?hd=&single=
func (h *ScreenProxyHandler) Proxy(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "invalid device id", http.StatusBadRequest)
		return
	}
	d, err := h.deviceRepo.GetByAssignedID(id)
	if err != nil || d == nil {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	ip := extractDeviceIP(d.LocalIP)
	if ip == "" {
		http.Error(w, "device has no IP", http.StatusBadGateway)
		return
	}

	q := r.URL.Query()
	hd := q.Get("hd")
	single := q.Get("single")

	// Fast path for grid thumbnails: deduplicate concurrent fetches & serve tiny TTL cache.
	if single == "1" && hd != "1" {
		data, code, err := h.fetchThumbnail(r.Context(), id, ip)
		if err != nil {
			http.Error(w, err.Error(), code)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}

	ctx := r.Context()
	if single == "1" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
	}

	upstream := fmt.Sprintf("http://%s:8090/screen?hd=%s&single=%s", ip, hd, single)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp, err := h.client.Do(req)
	if err != nil {
		log.Printf("[screen-proxy] device %d %s: %v", id, upstream, err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		if strings.EqualFold(k, "Connection") || strings.EqualFold(k, "Transfer-Encoding") {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	flusher, ok := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			if ok {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				log.Printf("[screen-proxy] device %d read: %v", id, readErr)
			}
			return
		}
	}
}

func (h *ScreenProxyHandler) fetchThumbnail(callerCtx context.Context, id int, ip string) ([]byte, int, error) {
	h.mu.Lock()
	if cached, ok := h.thumbs[id]; ok && time.Since(cached.fetchedAt) < screenThumbTTL {
		h.mu.Unlock()
		return cached.data, http.StatusOK, nil
	}
	if flight, ok := h.inflight[id]; ok {
		h.mu.Unlock()
		select {
		case <-flight.done:
			return flight.data, flight.code, flight.err
		case <-callerCtx.Done():
			return nil, http.StatusGatewayTimeout, callerCtx.Err()
		}
	}
	flight := &inflightFrame{done: make(chan struct{})}
	h.inflight[id] = flight
	h.mu.Unlock()

	defer func() {
		close(flight.done)
		h.mu.Lock()
		delete(h.inflight, id)
		h.mu.Unlock()
	}()

	// Independent short timeout so one cancelled browser tile doesn't abort
	// another viewer waiting on the same frame.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	upstream := fmt.Sprintf("http://%s:8090/screen?hd=0&single=1", ip)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream, nil)
	if err != nil {
		flight.code, flight.err = http.StatusInternalServerError, err
		return nil, flight.code, flight.err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		flight.code, flight.err = http.StatusBadGateway, fmt.Errorf("upstream unavailable")
		return nil, flight.code, flight.err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if readErr != nil {
		flight.code, flight.err = http.StatusBadGateway, fmt.Errorf("upstream read failed")
		return nil, flight.code, flight.err
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		flight.code, flight.err = resp.StatusCode, fmt.Errorf("%s", msg)
		return nil, flight.code, flight.err
	}
	h.mu.Lock()
	h.thumbs[id] = cachedFrame{data: body, fetchedAt: time.Now()}
	h.mu.Unlock()
	flight.data, flight.code = body, http.StatusOK
	return body, http.StatusOK, nil
}

// extractDeviceIP parses local_ip which may be a JSON array from fastfetch or a plain IP.
func extractDeviceIP(localIP string) string {
	localIP = strings.TrimSpace(localIP)
	if localIP == "" {
		return ""
	}
	// Plain IPv4/IPv6
	if ip := net.ParseIP(localIP); ip != nil {
		return ip.String()
	}
	// JSON array of interfaces
	var entries []struct {
		IPv4 string `json:"ipv4"`
		IP   string `json:"ip"`
		Addr string `json:"address"`
	}
	if err := json.Unmarshal([]byte(localIP), &entries); err == nil {
		for _, e := range entries {
			for _, cand := range []string{e.IPv4, e.IP, e.Addr} {
				cand = strings.TrimSpace(cand)
				if cand == "" {
					continue
				}
				// may be "192.168.1.1/24"
				if i := strings.Index(cand, "/"); i > 0 {
					cand = cand[:i]
				}
				if ip := net.ParseIP(cand); ip != nil && ip.To4() != nil && !ip.IsLoopback() {
					return ip.String()
				}
			}
		}
	}
	// Heuristic: first IPv4-looking token
	for _, part := range strings.FieldsFunc(localIP, func(r rune) bool {
		return r == '"' || r == ',' || r == '[' || r == ']' || r == '{' || r == '}' || r == ' '
	}) {
		if i := strings.Index(part, "/"); i > 0 {
			part = part[:i]
		}
		if ip := net.ParseIP(part); ip != nil && ip.To4() != nil && !ip.IsLoopback() {
			return ip.String()
		}
	}
	return ""
}
