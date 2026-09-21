package service

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/model"
)

const maxTCPConns = 5000

// readTimeout is the maximum interval between messages from a client before the
// server considers the connection dead. The client sends a heartbeat every 15s.
// Combined with TCP keepalive (5s probes), dead connections are detected quickly.
const readTimeout = 30 * time.Second

// Command output is accumulated in memory and flushed to the DB at most once
// per outputFlushInterval. Writing the whole accumulated string on every line
// (the previous behaviour) is O(n²) for chatty commands.
const (
	outputFlushInterval = time.Second
	maxOutputBytes      = 256 * 1024
	outputTruncMarker   = "\n… [输出超过 256KB，已截断] …\n"
)

// cmdOutput is the in-memory buffer for one running command.
type cmdOutput struct {
	buf       []byte
	truncated bool
	dirty     bool
	lastFlush time.Time
}

// TCPHandler handles TCP connections from contestant machines.
type TCPHandler struct {
	hub           *biz.Hub
	deviceRepo    *data.DeviceRepo
	commandRepo   *data.CommandRepo
	idAssigner    *biz.IDAssigner
	dispatcher    *biz.CommandDispatcher
	outputBuf     map[int64]*cmdOutput // command_id → accumulated streaming output
	obMu          sync.Mutex
	connCount     int
	connMu        sync.Mutex
	settings      *ServerSettings
	broadcastRepo *data.BroadcastRepo
	eventRepo     *data.DeviceEventRepo
}

func NewTCPHandler(hub *biz.Hub, deviceRepo *data.DeviceRepo, commandRepo *data.CommandRepo, idAssigner *biz.IDAssigner, dispatcher *biz.CommandDispatcher, settings *ServerSettings, broadcastRepo *data.BroadcastRepo, eventRepo *data.DeviceEventRepo) *TCPHandler {
	return &TCPHandler{
		hub: hub, deviceRepo: deviceRepo, commandRepo: commandRepo,
		idAssigner: idAssigner, dispatcher: dispatcher,
		outputBuf:     make(map[int64]*cmdOutput),
		settings:      settings,
		broadcastRepo: broadcastRepo,
		eventRepo:     eventRepo,
	}
}

// appendOutput accumulates a line and reports whether the DB should be updated
// now (throttled). The returned string is only valid when flush is true.
func (h *TCPHandler) appendOutput(cmdID int64, line string) (out string, flush bool) {
	h.obMu.Lock()
	defer h.obMu.Unlock()
	co, ok := h.outputBuf[cmdID]
	if !ok {
		co = &cmdOutput{}
		h.outputBuf[cmdID] = co
	}
	if !co.truncated {
		if len(co.buf)+len(line)+1 > maxOutputBytes {
			co.buf = append(co.buf, outputTruncMarker...)
			co.truncated = true
		} else {
			co.buf = append(co.buf, line...)
			co.buf = append(co.buf, '\n')
		}
		co.dirty = true
	}
	if !co.dirty || time.Since(co.lastFlush) < outputFlushInterval {
		return "", false
	}
	co.lastFlush = time.Now()
	co.dirty = false
	return string(co.buf), true
}

// takeOutput removes and returns the buffered output for a finished command.
func (h *TCPHandler) takeOutput(cmdID int64) (string, bool) {
	h.obMu.Lock()
	defer h.obMu.Unlock()
	co, ok := h.outputBuf[cmdID]
	if !ok {
		return "", false
	}
	delete(h.outputBuf, cmdID)
	return string(co.buf), true
}

// healthAlert returns a human-readable alert when a metric crosses its
// configured threshold, or "" when everything is within limits.
func (h *TCPHandler) healthAlert(hr model.HealthReportMessage) string {
	cfg := h.settings.GetMaintenance()
	var alerts []string
	if cfg.DiskAlertPct > 0 && hr.DiskPct >= cfg.DiskAlertPct {
		alerts = append(alerts, fmt.Sprintf("磁盘 %.0f%%", hr.DiskPct))
	}
	if cfg.TempAlertC > 0 && hr.TempC >= cfg.TempAlertC {
		alerts = append(alerts, fmt.Sprintf("温度 %.0f°C", hr.TempC))
	}
	if cfg.MemAlertPct > 0 && hr.MemPct >= cfg.MemAlertPct {
		alerts = append(alerts, fmt.Sprintf("内存 %.0f%%", hr.MemPct))
	}
	if len(alerts) == 0 {
		return ""
	}
	return strings.Join(alerts, " / ")
}

// dropOutputs discards buffers for commands abandoned by a disconnect.
func (h *TCPHandler) dropOutputs(cmdIDs map[int64]bool) {
	h.obMu.Lock()
	for id := range cmdIDs {
		delete(h.outputBuf, id)
	}
	h.obMu.Unlock()
}

func (h *TCPHandler) incConn() bool {
	h.connMu.Lock()
	defer h.connMu.Unlock()
	if h.connCount >= maxTCPConns {
		return false
	}
	h.connCount++
	return true
}

func (h *TCPHandler) decConn() {
	h.connMu.Lock()
	h.connCount--
	h.connMu.Unlock()
}

func (h *TCPHandler) Handle(conn net.Conn) {
	defer func() {
		conn.Close()
		h.decConn()
	}()
	if h.settings.GetDeployment().Mode == "cloud" {
		return
	}

	// Enable TCP keepalive so the OS detects dead connections within seconds.
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(5 * time.Second)
	}

	conn.SetDeadline(time.Now().Add(30 * time.Second))
	reader := bufio.NewReader(conn)

	// --- Registration ---
	line, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	var regReq model.RegisterRequest
	if err := json.Unmarshal([]byte(line), &regReq); err != nil || regReq.Type != "register_request" {
		return
	}

	// Atomic ID assignment — prevents concurrent clients getting the same ID.
	assignedID, existingDevice, _, err := h.idAssigner.AssignIdentity(regReq.MacAddress, regReq.IdentityKey, regReq.AssignedID)
	if err != nil {
		log.Printf("[tcp] id assignment error: %v", err)
		return
	}

	clientConn := &biz.ClientConn{
		AssignedID: assignedID,
		Conn:       conn,
		Send:       make(chan []byte, 64),
		Hub:        h.hub,
	}

	// Write register_response via Send channel (buffered, before write pump starts).
	regResp := model.RegisterResponse{Type: "register_response", AssignedID: assignedID, HostnamePrefix: h.settings.GetHostnamePrefix()}
	respData, _ := json.Marshal(regResp)
	respData = append(respData, '\n')
	clientConn.Send <- respData

	h.hub.Register(clientConn)
	defer h.hub.Unregister(clientConn)

	// --- System info ---
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	line, err = reader.ReadString('\n')
	if err != nil {
		return
	}
	var sysMsg model.SystemInfoMessage
	if err := json.Unmarshal([]byte(line), &sysMsg); err != nil || sysMsg.Type != "system_info" {
		return
	}

	rawJSON, _ := json.Marshal(sysMsg.Info)
	device, err := model.ParseFastFetch(rawJSON)
	if err != nil {
		device = &model.Device{AssignedID: assignedID, FastfetchRaw: string(rawJSON)}
	}
	device.AssignedID = assignedID
	device.MacAddress = regReq.MacAddress
	device.Connected = true

	// Preserve check-in state from the existing DB record so reconnects
	// don't overwrite checkin_status / student_name / checkin_time etc.
	if existingDevice != nil {
		device.ID = existingDevice.ID
		device.CheckinStatus = existingDevice.CheckinStatus
		device.StudentName = existingDevice.StudentName
		device.StudentNum = existingDevice.StudentNum
		device.CheckinTime = existingDevice.CheckinTime
		device.CheckoutTime = existingDevice.CheckoutTime
		h.deviceRepo.Update(device)
	} else if existing, err := h.deviceRepo.GetByAssignedID(assignedID); err == nil {
		device.ID = existing.ID
		device.CheckinStatus = existing.CheckinStatus
		device.StudentName = existing.StudentName
		device.StudentNum = existing.StudentNum
		device.CheckinTime = existing.CheckinTime
		device.CheckoutTime = existing.CheckoutTime
		h.deviceRepo.Update(device)
	} else {
		h.deviceRepo.Create(device)
	}

	h.hub.BroadcastAdminEvent("device_updated", map[string]interface{}{"assigned_id": assignedID})
	log.Printf("[tcp] device %d registered", assignedID)

	// Push check-in config to client.
	cfg := h.settings.GetCheckinConfig()
	cfgData, _ := json.Marshal(model.CheckinConfigMessage{
		Type:            "checkin_config",
		WelcomeText:     cfg.WelcomeText,
		WarningText:     cfg.WarningText,
		PostCheckinMsg:  cfg.PostCheckinMsg,
		PostCheckoutCmd: cfg.PostCheckoutCmd,
		PostCheckoutMsg: cfg.PostCheckoutMsg,
	})
	cfgData = append(cfgData, '\n')
	select {
	case clientConn.Send <- cfgData:
	default:
	}

	// Push screen monitor config to client.
	smEnabled := h.settings.GetScreenMonitorEnabled()
	smData, _ := json.Marshal(struct {
		Type    string `json:"type"`
		Enabled bool   `json:"enabled"`
	}{
		Type:    "screen_monitor_config",
		Enabled: smEnabled,
	})
	smData = append(smData, '\n')
	select {
	case clientConn.Send <- smData:
	default:
	}

	// --- Main loop ---
	// Set a read deadline so we detect silent client disconnects within readTimeout.
	// The deadline is refreshed on every successful message (ping, command output, etc.).

	// Track in-flight command IDs for this device so we can fail them on disconnect.
	inFlightCmds := make(map[int64]bool)
	lastClientVersion := ""

	for {
		conn.SetReadDeadline(time.Now().Add(readTimeout))
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				log.Printf("[tcp] device %d: read: %v", assignedID, err)
			}
			h.failInFlightCommands(inFlightCmds)
			h.dropOutputs(inFlightCmds)
			return
		}

		var base struct{ Type string }
		if err := json.Unmarshal([]byte(line), &base); err != nil {
			log.Printf("[tcp] device %d: unmarshal message type: %v", assignedID, err)
			continue
		}

		switch base.Type {
		case "command_output":
			var msg model.CommandOutputMessage
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				log.Printf("[tcp] device %d: unmarshal command_output: %v", assignedID, err)
				continue
			}
			inFlightCmds[msg.CommandID] = true
			// Accumulate in memory; persist at most once per second so a chatty
			// command doesn't rewrite its whole output on every line.
			if buf, flush := h.appendOutput(msg.CommandID, msg.Line); flush {
				if err := h.commandRepo.UpdateOutput(msg.CommandID, buf); err != nil {
					log.Printf("[tcp] device %d: persist output for cmd %d: %v", assignedID, msg.CommandID, err)
				}
			}
			// Live lines always go straight to admins (no DB involved).
			h.hub.BroadcastAdminEvent("command_output", model.CommandOutputEvent{
				CommandID: msg.CommandID,
				DeviceID:  assignedID,
				Stream:    msg.Stream,
				Line:      msg.Line,
			})

		case "command_result":
			var cr model.CommandResultMessage
			if err := json.Unmarshal([]byte(line), &cr); err != nil {
				log.Printf("[tcp] device %d: unmarshal command_result: %v", assignedID, err)
				continue
			}
			delete(inFlightCmds, cr.CommandID)
			cmd, err := h.commandRepo.GetByID(cr.CommandID)
			if err == nil {
				cmd.Status = cr.Status
				cmd.ErrorOutput = cr.ErrorOutput
				cmd.DurationMS = cr.DurationMS
				// Authoritative final write of the accumulated output.
				if buf, ok := h.takeOutput(cr.CommandID); ok {
					cmd.Output = buf
				}
				if err := h.commandRepo.UpdateStatus(cmd); err != nil {
					log.Printf("[tcp] device %d: persist result for cmd %d: %v", assignedID, cr.CommandID, err)
				}
				if cmd.ParentID != nil {
					h.dispatcher.UpdateBroadcastParentStatus(*cmd.ParentID)
				}
			}
			h.hub.BroadcastAdminEvent("command_result", map[string]interface{}{
				"command_id":   cr.CommandID,
				"device_id":    assignedID,
				"status":       cr.Status,
				"error_output": cr.ErrorOutput,
				"duration_ms":  cr.DurationMS,
			})

		case "terminal_output":
			var msg model.TerminalOutputMessage
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				log.Printf("[tcp] device %d: unmarshal terminal_output: %v", assignedID, err)
				continue
			}
			TerminalHub.Broadcast(msg.SessionID, msg.Data)

		case "terminal_closed":
			var msg model.TerminalClosedMessage
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				log.Printf("[tcp] device %d: unmarshal terminal_closed: %v", assignedID, err)
				continue
			}
			TerminalHub.Broadcast(msg.SessionID, []byte("\x1b[31mSession closed\x1b[0m\r\n"))
			TerminalHub.Close(msg.SessionID)

		case "query_broadcast_state":
			var msg model.BroadcastQueryMessage
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				log.Printf("[tcp] device %d: unmarshal query_broadcast_state: %v", assignedID, err)
				continue
			}
			pushedState, _ := h.broadcastRepo.GetConfig("pushed_state")
			baseURL, _ := h.broadcastRepo.GetConfig("base_url")
			if baseURL == "" {
				baseURL = "http://icpc-server.local:8082"
			}
			cmd := ""
			if pushedState != "" {
				cmd = fmt.Sprintf("full-firefox %s/broadcast/%s", baseURL, pushedState)
			}
			resp := model.BroadcastQueryResponse{
				Type:          "query_broadcast_response",
				CorrelationID: msg.CorrelationID,
				PushedState:   pushedState,
				BaseURL:       baseURL,
				Command:       cmd,
			}
			respData, _ := json.Marshal(resp)
			respData = append(respData, '\n')
			select {
			case clientConn.Send <- respData:
			default:
			}

		case "query_checkin_config":
			var msg model.CheckinConfigMessage
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				log.Printf("[tcp] device %d: unmarshal query_checkin_config: %v", assignedID, err)
				continue
			}
			cfg := h.settings.GetCheckinConfig()
			resp := model.CheckinConfigMessage{
				Type:            "checkin_config",
				CorrelationID:   msg.CorrelationID,
				WelcomeText:     cfg.WelcomeText,
				WarningText:     cfg.WarningText,
				PostCheckinMsg:  cfg.PostCheckinMsg,
				PostCheckoutCmd: cfg.PostCheckoutCmd,
				PostCheckoutMsg: cfg.PostCheckoutMsg,
			}
			respCfgData, _ := json.Marshal(resp)
			respCfgData = append(respCfgData, '\n')
			select {
			case clientConn.Send <- respCfgData:
			default:
			}

		case "checkin":
			var msg model.CheckinMessage
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				log.Printf("[tcp] device %d: unmarshal checkin: %v", assignedID, err)
				continue
			}
			err := h.deviceRepo.Checkin(assignedID, msg.StudentName, msg.StudentNum)
			cfg := h.settings.GetCheckinConfig()
			resp := model.CheckinResponseMessage{
				Type: "checkin_response", CorrelationID: msg.CorrelationID, Success: err == nil,
				PostCheckinMsg: cfg.PostCheckinMsg,
			}
			if err != nil {
				resp.Message = err.Error()
			} else {
				resp.Message = "checkin success"
				h.hub.BroadcastAdminEvent("checkin_updated", map[string]interface{}{
					"assigned_id": assignedID,
				})
			}
			respData, _ := json.Marshal(resp)
			respData = append(respData, '\n')
			select {
			case clientConn.Send <- respData:
			default:
			}
			log.Printf("[tcp] device %d: checkin name=%s num=%s success=%v", assignedID, msg.StudentName, msg.StudentNum, err == nil)

		case "checkin_query":
			var msg model.CheckinMessage
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			resp := model.CheckinResponseMessage{
				Type: "checkin_response", CorrelationID: msg.CorrelationID, Success: true,
			}
			// Populate actual check-in state from the database.
			if dev, err := h.deviceRepo.GetByAssignedID(assignedID); err == nil {
				resp.CheckinStatus = dev.CheckinStatus
				resp.StudentName = dev.StudentName
				resp.StudentNum = dev.StudentNum
				resp.CheckinTime = dev.CheckinTime
				resp.CheckoutTime = dev.CheckoutTime
			}
			respData, _ := json.Marshal(resp)
			respData = append(respData, '\n')
			select {
			case clientConn.Send <- respData:
			default:
			}

		case "checkout":
			var msg model.CheckinMessage
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			err := h.deviceRepo.Checkout(assignedID)
			cfg := h.settings.GetCheckinConfig()
			resp := model.CheckinResponseMessage{
				Type: "checkin_response", CorrelationID: msg.CorrelationID, Success: err == nil,
				PostCheckoutCmd: cfg.PostCheckoutCmd,
				PostCheckoutMsg: cfg.PostCheckoutMsg,
			}
			if err != nil {
				resp.Message = err.Error()
			} else {
				h.hub.BroadcastAdminEvent("checkin_updated", map[string]interface{}{
					"assigned_id": assignedID,
				})
			}
			respData, _ := json.Marshal(resp)
			respData = append(respData, '\n')
			select {
			case clientConn.Send <- respData:
			default:
			}
			log.Printf("[tcp] device %d: checkout success=%v", assignedID, err == nil)

		case "ping":
			pongData, _ := json.Marshal(model.PongMessage{Type: "pong"})
			pongData = append(pongData, '\n')
			select {
			case clientConn.Send <- pongData:
			default:
			}

			// Throttle database updates for last_seen to once every 60 seconds per client.
			if clientConn.ShouldUpdateLastSeen(60 * time.Second) {
				if err := h.deviceRepo.UpdateConnected(assignedID, true); err != nil {
					log.Printf("[tcp] failed to update last_seen for device %d: %v", assignedID, err)
				}
				// Also notify admins of status update to refresh last_seen on web UI
				h.hub.BroadcastAdminEvent("device_status_changed", map[string]interface{}{
					"assigned_id": assignedID,
					"connected":   true,
				})
			}

		case "health_report":
			var hr model.HealthReportMessage
			if err := json.Unmarshal([]byte(line), &hr); err != nil {
				log.Printf("[tcp] device %d: unmarshal health_report: %v", assignedID, err)
				continue
			}
			if err := h.deviceRepo.UpdateHealth(assignedID, hr.CPUPct, hr.MemPct, hr.DiskPct, hr.TempC, hr.Load1); err != nil {
				log.Printf("[tcp] device %d: persist health: %v", assignedID, err)
			}
			if hr.ClientVersion != "" && hr.ClientVersion != lastClientVersion {
				lastClientVersion = hr.ClientVersion
				if err := h.deviceRepo.UpdateClientVersion(assignedID, hr.ClientVersion); err != nil {
					log.Printf("[tcp] device %d: persist client version: %v", assignedID, err)
				}
			}
			ev := model.HealthEvent{
				AssignedID: assignedID,
				CPUPct:     hr.CPUPct,
				MemPct:     hr.MemPct,
				DiskPct:    hr.DiskPct,
				TempC:      hr.TempC,
				Load1:      hr.Load1,
			}
			ev.Alert = h.healthAlert(hr)
			if ev.Alert != "" {
				log.Printf("[tcp] device %d health alert: %s", assignedID, ev.Alert)
				if h.eventRepo != nil {
					if err := h.eventRepo.Add(assignedID, "alert", ev.Alert); err != nil {
						log.Printf("[tcp] device %d: record alert: %v", assignedID, err)
					}
				}
			}
			h.hub.BroadcastAdminEvent("device_health", ev)

		case "distribute_progress":
			var msg model.DistributeProgressMessage
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				log.Printf("[tcp] device %d: unmarshal distribute_progress: %v", assignedID, err)
				continue
			}
			if DistributionMgr != nil {
				msg.DeviceID = assignedID
				DistributionMgr.HandleProgressReport(msg)
			}

		case "distribute_precheck_response":
			var msg struct {
				DeviceID int    `json:"device_id"`
				Success  bool   `json:"success"`
				Error    string `json:"error"`
			}
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				log.Printf("[tcp] device %d: unmarshal distribute_precheck_response: %v", assignedID, err)
				continue
			}
			if DistributionMgr != nil {
				DistributionMgr.HandlePrecheckReport(msg.DeviceID, msg.Success, msg.Error)
			}

		default:
			log.Printf("[tcp] device %d: unknown type: %s", assignedID, base.Type)
		}
	}
}

// failInFlightCommands marks all in-flight commands as failed due to client disconnect.
func (h *TCPHandler) failInFlightCommands(inFlight map[int64]bool) {
	for cmdID := range inFlight {
		cmd, err := h.commandRepo.GetByID(cmdID)
		if err != nil {
			continue
		}
		// Only mark if still in a non-terminal state.
		if cmd.Status == model.CommandStatusDispatched || cmd.Status == model.CommandStatusPending {
			cmd.Status = model.CommandStatusFailed
			cmd.ErrorOutput = "client disconnected"
			h.commandRepo.UpdateStatus(cmd)
			if cmd.ParentID != nil {
				h.dispatcher.UpdateBroadcastParentStatus(*cmd.ParentID)
			}
		}
	}
}

// --- TCP listener ---

func StartTCPListener(addr string, handler *TCPHandler) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("tcp listen: %w", err)
	}
	log.Printf("[tcp] listening on %s", addr)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				log.Printf("[tcp] accept: %v", err)
				continue
			}
			if !handler.incConn() {
				log.Printf("[tcp] connection limit reached (%d), rejecting %s", maxTCPConns, conn.RemoteAddr())
				conn.Close()
				continue
			}
			log.Printf("[tcp] new connection from %s (%d/%d)", conn.RemoteAddr(), handler.connCount, maxTCPConns)
			go handler.Handle(conn)
		}
	}()
	return nil
}
