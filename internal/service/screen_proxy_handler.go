package service

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ICPCRemoteControl/internal/data"
)

// ScreenProxyHandler proxies contestant screen streams through the server
// so the admin browser does not need direct access to each machine :8090.
type ScreenProxyHandler struct {
	deviceRepo *data.DeviceRepo
	client     *http.Client
}

// NewScreenProxyHandler creates a ScreenProxyHandler.
func NewScreenProxyHandler(deviceRepo *data.DeviceRepo) *ScreenProxyHandler {
	return &ScreenProxyHandler{
		deviceRepo: deviceRepo,
		client: &http.Client{
			Timeout: 0, // streaming
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   3 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
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
	upstream := fmt.Sprintf("http://%s:8090/screen?hd=%s&single=%s", ip, hd, single)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream, nil)
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
		// Skip hop-by-hop
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
