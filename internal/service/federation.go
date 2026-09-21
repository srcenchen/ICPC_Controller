package service

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/model"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type RoomSnapshot struct {
	ID             string                `json:"id"`
	Name           string                `json:"name"`
	Devices        []model.DeviceSummary `json:"devices"`
	Distribution   json.RawMessage       `json:"distribution,omitempty"`
	LastSeen       string                `json:"last_seen"`
	Online         bool                  `json:"online"`
	TransferPhase  string                `json:"transfer_phase,omitempty"`
	TotalCommands  int                   `json:"total_commands"`
	RecentCommands []model.CommandLog    `json:"recent_commands"`
}

type FederationMessage struct {
	DeviceID    int           `json:"device_id,omitempty"`
	Cols        int           `json:"cols,omitempty"`
	Rows        int           `json:"rows,omitempty"`
	Type        string        `json:"type"`
	ID          string        `json:"id,omitempty"`
	Method      string        `json:"method,omitempty"`
	Path        string        `json:"path,omitempty"`
	Body        []byte        `json:"body,omitempty"`
	Status      int           `json:"status,omitempty"`
	ContentType string        `json:"content_type,omitempty"`
	Disposition string        `json:"disposition,omitempty"`
	Snapshot    *RoomSnapshot `json:"snapshot,omitempty"`
}

type relayConnection struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (connection *relayConnection) send(message FederationMessage) error {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	_ = connection.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return connection.conn.WriteJSON(message)
}

type Federation struct {
	db            *sql.DB
	settings      *ServerSettings
	devices       *data.DeviceRepo
	distribution  *DistributionManager
	hub           *biz.Hub
	local         http.Handler
	mu            sync.Mutex
	connections   map[string]*relayConnection
	pending       map[string]chan FederationMessage
	terminals     map[string]chan FederationMessage
	linkStatus    string
	transferPhase string
	pullMu        sync.Mutex
	broadcastMu   sync.Mutex
	broadcast     *BroadcastHandler
	broadcastDir  string
	backup        *BackupHandler
}

func NewFederation(db *sql.DB, settings *ServerSettings, devices *data.DeviceRepo, distribution *DistributionManager, hub *biz.Hub) *Federation {
	_, _ = db.Exec(`UPDATE cluster_receipts SET status='unknown' WHERE status='processing'`)
	return &Federation{db: db, settings: settings, devices: devices, distribution: distribution, hub: hub, connections: make(map[string]*relayConnection), pending: make(map[string]chan FederationMessage), terminals: make(map[string]chan FederationMessage), linkStatus: "未连接", broadcastDir: broadcastDataDir}
}

func (federation *Federation) ConfigureBroadcast(handler *BroadcastHandler, backup *BackupHandler) {
	federation.broadcast, federation.backup = handler, backup
}

func (federation *Federation) Start(ctx context.Context, local http.Handler) {
	federation.local = local
	go federation.relayLoop(ctx)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				federation.deliverJobs()
			}
		}
	}()
}

func (federation *Federation) NodeAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, httpRequest *http.Request) {
		if federation.settings.GetDeployment().Mode == "cloud" && strings.HasPrefix(httpRequest.URL.Path, "/api/devices/") && strings.HasSuffix(httpRequest.URL.Path, "/screen") {
			writeJSON(writer, 403, map[string]string{"error": "屏幕监控仅在机房本地提供"})
			return
		}
		if httpRequest.URL.Path != "/api/cluster/connect" && !strings.HasPrefix(httpRequest.URL.Path, "/api/cluster/files/") && !strings.HasPrefix(httpRequest.URL.Path, "/api/cluster/broadcast-assets/") {
			next.ServeHTTP(writer, httpRequest)
			return
		}
		cfg := federation.settings.GetDeployment()
		token := strings.TrimPrefix(httpRequest.Header.Get("Authorization"), "Bearer ")
		if cfg.Mode != "cloud" || len(cfg.Token) < 32 || subtle.ConstantTimeCompare([]byte(token), []byte(cfg.Token)) != 1 {
			writeJSON(writer, 401, map[string]string{"error": "invalid node credentials"})
			return
		}
		if httpRequest.URL.Path == "/api/cluster/connect" {
			federation.Connect(writer, httpRequest)
		} else if strings.HasPrefix(httpRequest.URL.Path, "/api/cluster/broadcast-assets/") {
			federation.ServeBroadcastAsset(writer, httpRequest)
		} else {
			federation.ServeFile(writer, httpRequest)
		}
	})
}

func (federation *Federation) Status(writer http.ResponseWriter, httpRequest *http.Request) {
	cfg := federation.settings.GetDeployment()
	cfg.TokenSet = cfg.Token != ""
	cfg.Token = ""
	federation.mu.Lock()
	status := federation.linkStatus
	federation.mu.Unlock()
	writeJSON(writer, 200, map[string]interface{}{"deployment": cfg, "connection": status})
}

func (federation *Federation) Connect(writer http.ResponseWriter, httpRequest *http.Request) {
	initialConfig := federation.settings.GetDeployment()
	if httpRequest.Method != "GET" {
		writer.WriteHeader(405)
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(req *http.Request) bool { return req.Header.Get("Origin") == "" }}
	conn, err := upgrader.Upgrade(writer, httpRequest, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(16 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	var hello FederationMessage
	if conn.ReadJSON(&hello) != nil || hello.Type != "hello" || hello.Snapshot == nil {
		return
	}
	room := hello.Snapshot
	if _, err := uuid.Parse(room.ID); err != nil || strings.TrimSpace(room.Name) == "" || len(room.Name) > 100 {
		return
	}
	connection := &relayConnection{conn: conn}
	federation.mu.Lock()
	if previous := federation.connections[room.ID]; previous != nil {
		federation.mu.Unlock()
		_ = connection.send(FederationMessage{Type: "rejected", Body: []byte("中转身份已在线；请勿在多个机房复制使用同一数据库，或等待旧连接过期")})
		return
	}
	federation.connections[room.ID] = connection
	federation.mu.Unlock()
	defer func() {
		federation.mu.Lock()
		if federation.connections[room.ID] == connection {
			delete(federation.connections, room.ID)
		}
		federation.mu.Unlock()
	}()
	federation.storeSnapshot(room)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		var message FederationMessage
		if conn.ReadJSON(&message) != nil {
			return
		}
		currentConfig := federation.settings.GetDeployment()
		if currentConfig.Mode != "cloud" || currentConfig.Token != initialConfig.Token {
			return
		}
		switch message.Type {
		case "terminal_output", "terminal_closed":
			federation.mu.Lock()
			stream := federation.terminals[room.ID+":"+message.ID]
			if stream != nil {
				select {
				case stream <- message:
				default:
					delete(federation.terminals, room.ID+":"+message.ID)
					close(stream)
				}
			}
			federation.mu.Unlock()
		case "snapshot":
			if message.Snapshot != nil && message.Snapshot.ID == room.ID {
				message.Snapshot.Name = room.Name
				federation.storeSnapshot(message.Snapshot)
			}
		case "response":
			federation.mu.Lock()
			waiter := federation.pending[room.ID+":"+message.ID]
			federation.mu.Unlock()
			if waiter != nil {
				select {
				case waiter <- message:
				default:
				}
			}
			status := "completed"
			if message.Status >= 400 {
				status = "failed"
			}
			if message.Status == 409 {
				status = "unknown"
			}
			if message.Status == 102 {
				raw, _ := json.Marshal(message)
				_, _ = federation.db.Exec(`UPDATE cluster_jobs SET response=? WHERE id=? AND room_id=? AND status IN ('queued','sent')`, string(raw), message.ID, room.ID)
				continue
			}
			raw, _ := json.Marshal(message)
			_, _ = federation.db.Exec(`UPDATE cluster_jobs SET status=?,response=? WHERE id=? AND room_id=?`, status, string(raw), message.ID, room.ID)
		}
	}
}

func (federation *Federation) storeSnapshot(room *RoomSnapshot) {
	if room.Devices == nil {
		room.Devices = []model.DeviceSummary{}
	}
	room.LastSeen = time.Now().UTC().Format(time.RFC3339)
	raw, err := json.Marshal(room)
	if err != nil {
		return
	}
	_, err = federation.db.Exec(`INSERT INTO cluster_rooms(id,name,snapshot,last_seen) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,snapshot=excluded.snapshot,last_seen=excluded.last_seen`, room.ID, room.Name, string(raw), room.LastSeen)
	if err != nil {
		log.Printf("[cluster] snapshot: %v", err)
	}
}

func (federation *Federation) Rooms(writer http.ResponseWriter, httpRequest *http.Request) {
	rooms, err := federation.roomList()
	if err != nil {
		writeJSON(writer, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(writer, 200, rooms)
}

func (federation *Federation) roomList() ([]RoomSnapshot, error) {
	rows, err := federation.db.Query(`SELECT snapshot FROM cluster_rooms ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rooms := make([]RoomSnapshot, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var room RoomSnapshot
		if json.Unmarshal([]byte(raw), &room) != nil {
			continue
		}
		federation.mu.Lock()
		room.Online = federation.connections[room.ID] != nil && federation.settings.GetDeployment().Mode == "cloud"
		federation.mu.Unlock()
		if !room.Online {
			for index := range room.Devices {
				room.Devices[index].Connected = false
			}
		}
		rooms = append(rooms, room)
	}
	return rooms, rows.Err()
}

func allowedRelayRequest(method, path string) bool {
	parsed, err := url.ParseRequestURI(path)
	if err != nil || parsed.Host != "" || parsed.RawPath != "" || strings.Contains(parsed.Path, "..") {
		return false
	}
	path = parsed.Path
	if strings.Contains(path, "screen") || strings.HasPrefix(path, "/ws/") {
		return false
	}
	if method != "GET" && method != "POST" && method != "PUT" && method != "PATCH" && method != "DELETE" {
		return false
	}
	if path == "/api/broadcast/sync" {
		return method == "POST"
	}
	for _, prefix := range []string{"/api/devices", "/api/commands", "/api/checkin", "/api/network", "/api/power", "/api/distribution"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			if strings.HasSuffix(path, "/upload") || strings.HasSuffix(path, "/delete") || strings.HasSuffix(path, "/clear") {
				return false
			}
			return true
		}
	}
	return path == "/api/stats" || path == "/api/presets" || path == "/api/settings/presets" || path == "/api/settings/checkin"
}

func (federation *Federation) Proxy(writer http.ResponseWriter, httpRequest *http.Request) {
	path := "/api/" + httpRequest.PathValue("path")
	if httpRequest.URL.RawQuery != "" {
		path += "?" + httpRequest.URL.RawQuery
	}
	if !allowedRelayRequest(httpRequest.Method, path) || strings.HasPrefix(path, "/api/broadcast/") {
		writeJSON(writer, 403, map[string]string{"error": "此接口仅可在机房本地使用（屏幕流不上传云端）"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, httpRequest.Body, 2<<20))
	if err != nil {
		writeJSON(writer, 413, map[string]string{"error": err.Error()})
		return
	}
	request := FederationMessage{Type: "request", ID: uuid.NewString(), Method: httpRequest.Method, Path: path, Body: body}
	response, err := federation.call(httpRequest.Context(), httpRequest.PathValue("room"), request)
	if err != nil {
		writeJSON(writer, 502, map[string]string{"error": err.Error(), "request_id": request.ID})
		return
	}
	if response.ContentType != "" {
		writer.Header().Set("Content-Type", response.ContentType)
	}
	if response.Disposition != "" {
		writer.Header().Set("Content-Disposition", response.Disposition)
	}
	writer.WriteHeader(response.Status)
	_, _ = writer.Write(response.Body)
}

func (federation *Federation) call(ctx context.Context, room string, request FederationMessage) (FederationMessage, error) {
	if federation.settings.GetDeployment().Mode != "cloud" {
		return FederationMessage{}, fmt.Errorf("当前不是云端模式")
	}
	federation.mu.Lock()
	connection := federation.connections[room]
	channel := make(chan FederationMessage, 1)
	federation.pending[room+":"+request.ID] = channel
	federation.mu.Unlock()
	defer func() { federation.mu.Lock(); delete(federation.pending, room+":"+request.ID); federation.mu.Unlock() }()
	if connection == nil {
		return FederationMessage{}, fmt.Errorf("机房中转离线")
	}
	if err := connection.send(request); err != nil {
		return FederationMessage{}, err
	}
	select {
	case response := <-channel:
		return response, nil
	case <-ctx.Done():
		return FederationMessage{}, ctx.Err()
	case <-time.After(90 * time.Second):
		return FederationMessage{}, fmt.Errorf("中转响应超时；操作结果未知，请查看机房日志，勿重复执行")
	}
}

func (federation *Federation) relayLoop(ctx context.Context) {
	for ctx.Err() == nil {
		cfg := federation.settings.GetDeployment()
		if cfg.Mode == "relay" {
			federation.runRelay(ctx, cfg)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (federation *Federation) runRelay(ctx context.Context, cfg DeploymentConfig) {
	endpoint := strings.Replace(strings.Replace(cfg.CloudURL, "https://", "wss://", 1), "http://", "ws://", 1) + "/api/cluster/connect"
	headers := http.Header{"Authorization": []string{"Bearer " + cfg.Token}}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(ctx, endpoint, headers)
	federation.mu.Lock()
	if err != nil {
		federation.linkStatus = err.Error()
		federation.mu.Unlock()
		return
	}
	federation.linkStatus = "已连接云端"
	federation.mu.Unlock()
	defer func() {
		conn.Close()
		federation.mu.Lock()
		if federation.linkStatus == "已连接云端" {
			federation.linkStatus = "连接断开，正在重试"
		}
		federation.mu.Unlock()
	}()
	connection := &relayConnection{conn: conn}
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if connection.send(FederationMessage{Type: "hello", Snapshot: federation.snapshot(cfg)}) != nil {
		return
	}
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-sessionCtx.Done():
				conn.Close()
				return
			case <-ticker.C:
				if federation.settings.GetDeployment() != cfg {
					conn.Close()
					return
				}
				if connection.send(FederationMessage{Type: "snapshot", Snapshot: federation.snapshot(cfg)}) != nil {
					conn.Close()
					return
				}
			}
		}
	}()
	conn.SetReadLimit(4 << 20)
	workers := make(chan struct{}, 4)
	terminalSessions := make(map[string]context.CancelFunc)
	defer func() {
		for _, stop := range terminalSessions {
			stop()
		}
	}()
	for {
		var request FederationMessage
		if conn.ReadJSON(&request) != nil {
			return
		}
		if request.Type == "rejected" {
			federation.mu.Lock()
			federation.linkStatus = "云端拒绝：" + string(request.Body)
			federation.mu.Unlock()
			return
		}
		if request.Type != "request" {
			federation.handleRelayTerminal(sessionCtx, connection, request, terminalSessions)
			continue
		}
		select {
		case workers <- struct{}{}:
		case <-sessionCtx.Done():
			return
		}
		go func(message FederationMessage) {
			defer func() { <-workers }()
			response := federation.executeRelay(ctx, cfg, message)
			_ = connection.send(response)
		}(request)
	}
}

func (federation *Federation) snapshot(cfg DeploymentConfig) *RoomSnapshot {
	devices, _ := federation.devices.GetAll()
	var distribution json.RawMessage
	if task := federation.distribution.GetActiveTask(); task != nil {
		distribution, _ = json.Marshal(task)
	}
	federation.mu.Lock()
	phase := federation.transferPhase
	federation.mu.Unlock()
	commands := data.NewCommandRepo(federation.db)
	totalCommands, _ := commands.GetTotalCount()
	recentCommands, _ := commands.GetRecent(10)
	for index := range recentCommands {
		recentCommands[index].Output = ""
		recentCommands[index].ErrorOutput = ""
		recentCommands[index].Children = nil
		if len(recentCommands[index].Command) > 512 {
			recentCommands[index].Command = recentCommands[index].Command[:512]
		}
	}
	return &RoomSnapshot{ID: cfg.NodeID, Name: cfg.RoomName, Devices: devices, Distribution: distribution, TransferPhase: phase, TotalCommands: totalCommands, RecentCommands: recentCommands}
}

func (federation *Federation) executeRelay(ctx context.Context, cfg DeploymentConfig, request FederationMessage) FederationMessage {
	response := FederationMessage{Type: "response", ID: request.ID, Status: 500, ContentType: "application/json"}
	if !allowedRelayRequest(request.Method, request.Path) || federation.settings.GetDeployment().Mode != "relay" {
		response.Status = 403
		response.Body = []byte(`{"error":"relay operation forbidden"}`)
		return response
	}
	if _, err := uuid.Parse(request.ID); err != nil {
		response.Status = 400
		return response
	}
	if request.Method != "GET" {
		result, err := federation.db.Exec(`INSERT OR IGNORE INTO cluster_receipts(id,status) VALUES(?,'processing')`, request.ID)
		if err != nil {
			response.Body = []byte(`{"error":"cannot persist request receipt"}`)
			return response
		}
		inserted, _ := result.RowsAffected()
		if inserted == 0 {
			var status, raw string
			if err := federation.db.QueryRow(`SELECT status,response FROM cluster_receipts WHERE id=?`, request.ID).Scan(&status, &raw); err != nil {
				return response
			}
			if status == "done" && json.Unmarshal([]byte(raw), &response) == nil {
				return response
			}
			response.Status = 409
			if status == "processing" {
				response.Status = 102
			}
			response.Body = []byte(`{"error":"request already received; execution result not yet confirmed"}`)
			return response
		}
	}
	if request.Path == "/api/broadcast/sync" {
		response = federation.applyBroadcast(ctx, cfg, request)
		if response.Status == 503 {
			_, err := federation.db.Exec(`DELETE FROM cluster_receipts WHERE id=? AND status='processing'`, request.ID)
			if err == nil {
				response.Status = 102
			}
			return response
		}
	} else if request.Path == "/api/distribution/start" {
		federation.pullMu.Lock()
		defer federation.pullMu.Unlock()
		if err := federation.prepareRelayFiles(ctx, cfg, &request); err != nil {
			response.Status = 400
			response.Body, _ = json.Marshal(map[string]string{"error": err.Error()})
		} else {
			response = federation.localRequest(ctx, request)
		}
	} else {
		if request.Path == "/api/power/schedules" && request.Method == "POST" {
			var schedule scheduleRequest
			if json.Unmarshal(request.Body, &schedule) == nil {
				runAt, err := parseScheduleTime(schedule.RunAt)
				if err != nil || !runAt.After(time.Now()) {
					response.Status = 400
					response.Body = []byte(`{"error":"电源计划到达中转时已过期，未执行；请重新设置未来时间"}`)
				} else {
					response = federation.localRequest(ctx, request)
				}
			} else {
				response.Status = 400
				response.Body = []byte(`{"error":"invalid power schedule"}`)
			}
		} else {
			response = federation.localRequest(ctx, request)
		}
	}
	if request.Method != "GET" {
		raw, _ := json.Marshal(response)
		if _, err := federation.db.Exec(`UPDATE cluster_receipts SET status='done',response=? WHERE id=?`, string(raw), request.ID); err != nil {
			response.Status = 409
			response.Body = []byte(`{"error":"operation ran but receipt could not be saved; inspect local logs"}`)
		}
	}
	return response
}

func (federation *Federation) localRequest(ctx context.Context, request FederationMessage) FederationMessage {
	localRequest := httptest.NewRequest(request.Method, request.Path, bytes.NewReader(request.Body)).WithContext(ctx)
	localRequest.Header.Set("Content-Type", "application/json")
	localRequest.RemoteAddr = "cloud:0"
	recorder := httptest.NewRecorder()
	federation.local.ServeHTTP(recorder, localRequest)
	return FederationMessage{Type: "response", ID: request.ID, Status: recorder.Code, Body: recorder.Body.Bytes(), ContentType: recorder.Header().Get("Content-Type"), Disposition: recorder.Header().Get("Content-Disposition")}
}

type RelayFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func (federation *Federation) prepareRelayFiles(ctx context.Context, cfg DeploymentConfig, request *FederationMessage) error {
	if federation.distribution.IsRunning() {
		return fmt.Errorf("机房已有分发任务，请等待结束")
	}
	var body struct {
		Files     []string    `json:"files"`
		Manifest  []RelayFile `json:"manifest"`
		SaveDir   string      `json:"save_dir"`
		TargetIDs []int       `json:"target_ids"`
		PostCmd   string      `json:"post_cmd"`
		ServerIP  string      `json:"server_ip"`
	}
	if err := json.Unmarshal(request.Body, &body); err != nil {
		return err
	}
	if len(body.Manifest) > 0 {
		body.Files = nil
		for _, file := range body.Manifest {
			if file.Name != filepath.Base(file.Name) || strings.HasPrefix(file.Name, ".") || len(file.SHA256) != 64 || file.Size < 0 {
				return fmt.Errorf("非法文件清单")
			}
			federation.mu.Lock()
			federation.transferPhase = "云端 → 机房：" + file.Name
			federation.mu.Unlock()
			if err := federation.pullFile(ctx, cfg, file); err != nil {
				federation.mu.Lock()
				federation.transferPhase = "拉取失败：" + err.Error()
				federation.mu.Unlock()
				return err
			}
			body.Files = append(body.Files, file.Name)
		}
	}
	body.ServerIP = cfg.AdvertiseIP
	body.Manifest = nil
	raw, err := json.Marshal(body)
	request.Body = raw
	federation.mu.Lock()
	federation.transferPhase = "机房 → 选手机（P2P）"
	federation.mu.Unlock()
	return err
}

func (federation *Federation) pullFile(ctx context.Context, cfg DeploymentConfig, file RelayFile) error {
	path := filepath.Join(federation.distribution.uploadDir, file.Name)
	return pullVerifiedFile(ctx, cfg, file, path, "/api/cluster/files/"+url.PathEscape(file.Name))
}

func pullVerifiedFile(ctx context.Context, cfg DeploymentConfig, file RelayFile, path, endpoint string) error {
	if digest, err := fileSHA256(path); err == nil && digest == file.SHA256 {
		return nil
	}
	temporary := filepath.Join(filepath.Dir(path), "."+file.SHA256+".part")
	client := &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var offset int64
		if info, err := os.Stat(temporary); err == nil {
			offset = info.Size()
		}
		if offset > file.Size {
			_ = os.Remove(temporary)
			offset = 0
		}
		if offset < file.Size || file.Size == 0 {
			req, err := http.NewRequestWithContext(ctx, "GET", cfg.CloudURL+endpoint, nil)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+cfg.Token)
			if offset > 0 {
				req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
			}
			resp, err := client.Do(req)
			if err != nil {
				lastErr = err
				continue
			}
			if resp.StatusCode != 200 && resp.StatusCode != 206 {
				resp.Body.Close()
				return fmt.Errorf("云端文件下载 HTTP %d", resp.StatusCode)
			}
			if resp.StatusCode == 200 {
				offset = 0
			}
			flags := os.O_CREATE | os.O_WRONLY
			if offset == 0 {
				flags |= os.O_TRUNC
			} else {
				flags |= os.O_APPEND
			}
			output, err := os.OpenFile(temporary, flags, 0600)
			if err != nil {
				resp.Body.Close()
				return err
			}
			_, lastErr = io.Copy(output, io.LimitReader(resp.Body, file.Size-offset+1))
			resp.Body.Close()
			if err := output.Sync(); lastErr == nil {
				lastErr = err
			}
			output.Close()
			if lastErr != nil {
				continue
			}
		}
		info, err := os.Stat(temporary)
		if err != nil || info.Size() != file.Size {
			lastErr = fmt.Errorf("文件长度不一致")
			continue
		}
		digest, err := fileSHA256(temporary)
		if err == nil && digest == file.SHA256 {
			return os.Rename(temporary, path)
		}
		lastErr = fmt.Errorf("文件 SHA-256 不一致")
		_ = os.Remove(temporary)
	}
	return lastErr
}

func (federation *Federation) ServeFile(writer http.ResponseWriter, httpRequest *http.Request) {
	if httpRequest.Method != "GET" {
		writer.WriteHeader(405)
		return
	}
	name := strings.TrimPrefix(httpRequest.URL.Path, "/api/cluster/files/")
	if name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		http.Error(writer, "invalid name", 400)
		return
	}
	path := filepath.Join(federation.distribution.uploadDir, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(writer, httpRequest)
		return
	}
	http.ServeFile(writer, httpRequest, path)
}

func (federation *Federation) Queue(writer http.ResponseWriter, httpRequest *http.Request) {
	if federation.settings.GetDeployment().Mode != "cloud" {
		writeJSON(writer, 409, map[string]string{"error": "需要云端模式"})
		return
	}
	var request struct {
		RoomIDs   []string         `json:"room_ids"`
		Targets   map[string][]int `json:"targets"`
		Operation string           `json:"operation"`
		Command   string           `json:"command"`
		Files     []string         `json:"files"`
		SaveDir   string           `json:"save_dir"`
		PostCmd   string           `json:"post_cmd"`
		Rules     []NetworkRule    `json:"rules"`
		Action    string           `json:"action"`
		RunAt     string           `json:"run_at"`
		Note      string           `json:"note"`
	}
	if json.NewDecoder(http.MaxBytesReader(writer, httpRequest.Body, 2<<20)).Decode(&request) != nil {
		writeJSON(writer, 400, map[string]string{"error": "invalid request"})
		return
	}
	if request.Operation == "network_apply" {
		for _, rule := range request.Rules {
			if strings.TrimSpace(rule.Value) == "" || (rule.Type != "DOMAIN" && rule.Type != "DOMAIN-SUFFIX" && rule.Type != "DOMAIN-KEYWORD") {
				writeJSON(writer, 400, map[string]string{"error": "无效的网络白名单规则"})
				return
			}
		}
		request.Command = buildApplyCommand(request.Rules)
		request.Operation = "command"
	} else if request.Operation == "network_remove" {
		request.Command, request.Operation = buildRemoveCommand(), "command"
	}
	if len(request.RoomIDs) == 0 || (request.Operation != "command" && request.Operation != "distribute" && request.Operation != "wol" && request.Operation != "schedule") {
		writeJSON(writer, 400, map[string]string{"error": "请显式选择机房和操作"})
		return
	}
	if request.Operation == "schedule" {
		runAt, err := parseScheduleTime(request.RunAt)
		if err != nil || !runAt.After(time.Now()) || (request.Action != "shutdown" && request.Action != "reboot" && request.Action != "wol") {
			writeJSON(writer, 400, map[string]string{"error": "请选择有效的未来执行时间与电源操作"})
			return
		}
	}
	if request.Operation == "command" && (strings.TrimSpace(request.Command) == "" || len(request.Command) > 64<<10) {
		writeJSON(writer, 400, map[string]string{"error": "命令为空或过长"})
		return
	}
	manifest := make([]RelayFile, 0)
	if request.Operation == "distribute" {
		if len(request.Files) == 0 {
			writeJSON(writer, 400, map[string]string{"error": "请选择云端文件"})
			return
		}
		for _, name := range request.Files {
			if name != filepath.Base(name) || strings.HasPrefix(name, ".") {
				writeJSON(writer, 400, map[string]string{"error": "invalid filename"})
				return
			}
			path := filepath.Join(federation.distribution.uploadDir, name)
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				writeJSON(writer, 400, map[string]string{"error": "文件不存在"})
				return
			}
			digest, err := fileSHA256(path)
			if err != nil {
				writeJSON(writer, 500, map[string]string{"error": err.Error()})
				return
			}
			manifest = append(manifest, RelayFile{Name: name, SHA256: digest, Size: info.Size()})
		}
	}
	tx, err := federation.db.BeginTx(httpRequest.Context(), nil)
	if err != nil {
		writeJSON(writer, 500, map[string]string{"error": err.Error()})
		return
	}
	defer tx.Rollback()
	ids := make([]string, 0)
	seen := make(map[string]bool)
	for _, room := range request.RoomIDs {
		if seen[room] {
			continue
		}
		seen[room] = true
		var exists int
		if tx.QueryRow(`SELECT COUNT(*) FROM cluster_rooms WHERE id=?`, room).Scan(&exists) != nil || exists != 1 {
			writeJSON(writer, 400, map[string]string{"error": "未知机房"})
			return
		}
		targets, present := request.Targets[room]
		if !present || len(targets) == 0 {
			writeJSON(writer, 400, map[string]string{"error": "每个机房必须显式选择设备，防止误广播"})
			return
		}
		for _, target := range targets {
			if target <= 0 {
				writeJSON(writer, 400, map[string]string{"error": "无效的设备号"})
				return
			}
		}
		messages := make([]FederationMessage, 0)
		if request.Operation == "command" {
			selected := make(map[int]bool)
			for _, deviceID := range targets {
				if deviceID <= 0 || selected[deviceID] {
					continue
				}
				selected[deviceID] = true
				body, _ := json.Marshal(ExecuteRequest{TargetType: "single", TargetID: &deviceID, Command: request.Command})
				messages = append(messages, FederationMessage{Path: "/api/commands", Body: body})
			}
		} else if request.Operation == "distribute" {
			body, _ := json.Marshal(map[string]interface{}{"manifest": manifest, "save_dir": request.SaveDir, "post_cmd": request.PostCmd, "target_ids": targets})
			messages = append(messages, FederationMessage{Path: "/api/distribution/start", Body: body})
		} else if request.Operation == "wol" {
			body, _ := json.Marshal(wolRequest{TargetType: "list", DeviceIDs: targets})
			messages = append(messages, FederationMessage{Path: "/api/power/wol", Body: body})
		} else {
			body, _ := json.Marshal(scheduleRequest{TargetType: "list", DeviceIDs: targets, Action: request.Action, RunAt: request.RunAt, Note: request.Note})
			messages = append(messages, FederationMessage{Path: "/api/power/schedules", Body: body})
		}
		for _, message := range messages {
			message.Type, message.Method, message.ID = "request", "POST", uuid.NewString()
			raw, _ := json.Marshal(message)
			if _, err := tx.Exec(`INSERT INTO cluster_jobs(id,room_id,request,created_at) VALUES(?,?,?,?)`, message.ID, room, string(raw), time.Now().UTC().Format(time.RFC3339)); err != nil {
				writeJSON(writer, 500, map[string]string{"error": err.Error()})
				return
			}
			ids = append(ids, message.ID)
		}
	}
	if err := tx.Commit(); err != nil {
		writeJSON(writer, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(writer, 202, map[string]interface{}{"job_ids": ids, "message": "已入队；离线机房恢复后投递，执行结果请查看机房命令记录"})
}

func (federation *Federation) deliverJobs() {
	if federation.settings.GetDeployment().Mode != "cloud" {
		return
	}
	rows, err := federation.db.Query(`SELECT id,room_id,request FROM cluster_jobs WHERE status IN ('queued','sent') AND (sent_at='' OR sent_at<?) ORDER BY created_at LIMIT 100`, time.Now().Add(-20*time.Second).UTC().Format(time.RFC3339))
	if err != nil {
		return
	}
	type queued struct{ id, room, raw string }
	var jobs []queued
	for rows.Next() {
		var job queued
		if rows.Scan(&job.id, &job.room, &job.raw) == nil {
			jobs = append(jobs, job)
		}
	}
	rows.Close()
	for _, job := range jobs {
		federation.mu.Lock()
		connection := federation.connections[job.room]
		federation.mu.Unlock()
		if connection == nil {
			continue
		}
		var request FederationMessage
		if json.Unmarshal([]byte(job.raw), &request) != nil {
			continue
		}
		_, err := federation.db.Exec(`UPDATE cluster_jobs SET status='sent',sent_at=? WHERE id=? AND status IN ('queued','sent')`, time.Now().UTC().Format(time.RFC3339), job.id)
		if err == nil {
			_ = connection.send(request)
		}
	}
}

func (federation *Federation) Jobs(writer http.ResponseWriter, httpRequest *http.Request) {
	rows, err := federation.db.Query(`SELECT id,room_id,status,response,created_at FROM cluster_jobs ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		writeJSON(writer, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()
	jobs := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id, room, status, response, created string
		if rows.Scan(&id, &room, &status, &response, &created) != nil {
			continue
		}
		var result FederationMessage
		_ = json.Unmarshal([]byte(response), &result)
		jobs = append(jobs, map[string]interface{}{"id": id, "room_id": room, "status": status, "response": string(result.Body), "created_at": created})
	}
	writeJSON(writer, 200, jobs)
}

func (federation *Federation) CancelJob(writer http.ResponseWriter, httpRequest *http.Request) {
	result, err := federation.db.Exec(`UPDATE cluster_jobs SET status='cancelled' WHERE id=? AND status='queued'`, httpRequest.PathValue("id"))
	if err != nil {
		writeJSON(writer, 500, map[string]string{"error": err.Error()})
		return
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		writeJSON(writer, 409, map[string]string{"error": "仅可取消尚未投递的任务；已投递任务请在机房管理页停止"})
		return
	}
	writeJSON(writer, 200, map[string]string{"message": "已取消"})
}
