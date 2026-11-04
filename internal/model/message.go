package model

import "encoding/json"

// ---- Client -> Server ----

type RegisterRequest struct {
	IdentityKey string `json:"identity_key,omitempty"`
	Type        string `json:"type"`
	AssignedID  *int   `json:"assigned_id,omitempty"`
	MacAddress  string `json:"mac_address"`
	Hostname    string `json:"hostname"`
}

type SystemInfoMessage struct {
	Type       string            `json:"type"`
	AssignedID int               `json:"assigned_id"`
	Info       []json.RawMessage `json:"info"`
}

// CommandOutputMessage is streaming output from a running command.
type CommandOutputMessage struct {
	Type      string `json:"type"` // "command_output"
	CommandID int64  `json:"command_id"`
	Stream    string `json:"stream"` // "stdout" or "stderr"
	Line      string `json:"line"`
}

// CommandResultMessage is sent when a command finishes.
type CommandResultMessage struct {
	Type        string `json:"type"` // "command_result"
	CommandID   int64  `json:"command_id"`
	Status      string `json:"status"`
	ErrorOutput string `json:"error_output,omitempty"`
	DurationMS  int64  `json:"duration_ms"`
}

// TerminalOutputMessage sends terminal output to server.
type TerminalOutputMessage struct {
	Type      string `json:"type"` // "terminal_output"
	SessionID string `json:"session_id"`
	Data      string `json:"data"`
}

// TerminalClosedMessage indicates the terminal session ended.
type TerminalClosedMessage struct {
	Type      string `json:"type"` // "terminal_closed"
	SessionID string `json:"session_id"`
}

// CheckinMessage is sent by the client when the contestant checks in.
type CheckinMessage struct {
	Type          string `json:"type"` // "checkin"
	CorrelationID string `json:"correlation_id"`
	StudentName   string `json:"student_name"`
	StudentNum    string `json:"student_num"`
}

// CheckinResponseMessage is the server's response to a checkin/checkout/query request.
type CheckinResponseMessage struct {
	Type            string `json:"type"` // "checkin_response"
	CorrelationID   string `json:"correlation_id"`
	Success         bool   `json:"success"`
	Message         string `json:"message,omitempty"`
	PostCheckinMsg  string `json:"post_checkin_msg,omitempty"`
	PostCheckoutCmd string `json:"post_checkout_cmd,omitempty"`
	PostCheckoutMsg string `json:"post_checkout_msg,omitempty"`
	// Check-in status fields (populated for checkin_query responses).
	CheckinStatus int    `json:"checkin_status,omitempty"`
	StudentName   string `json:"student_name,omitempty"`
	StudentNum    string `json:"student_num,omitempty"`
	CheckinTime   string `json:"checkin_time,omitempty"`
	CheckoutTime  string `json:"checkout_time,omitempty"`
}

type PingMessage struct {
	Type string `json:"type"` // "ping"
}

// HealthReportMessage carries periodic resource metrics from a client.
// A value below zero means the metric is unavailable on that machine.
type HealthReportMessage struct {
	Type          string  `json:"type"` // "health_report"
	CPUPct        float64 `json:"cpu_pct"`
	MemPct        float64 `json:"mem_pct"`
	DiskPct       float64 `json:"disk_pct"`
	TempC         float64 `json:"temp_c"`
	Load1         float64 `json:"load1"`
	UptimeSec     int64   `json:"uptime_sec"`
	ClientVersion string  `json:"client_version"`
}

// HealthEvent is pushed to admin browsers when a health report arrives.
type HealthEvent struct {
	AssignedID int     `json:"assigned_id"`
	CPUPct     float64 `json:"cpu_pct"`
	MemPct     float64 `json:"mem_pct"`
	DiskPct    float64 `json:"disk_pct"`
	TempC      float64 `json:"temp_c"`
	Load1      float64 `json:"load1"`
	Alert      string  `json:"alert,omitempty"`
}

// ---- Server -> Client ----

type RegisterResponse struct {
	Type           string `json:"type"`
	AssignedID     int    `json:"assigned_id"`
	HostnamePrefix string `json:"hostname_prefix,omitempty"`
}

type ExecuteMessage struct {
	Type      string `json:"type"` // "execute"
	CommandID int64  `json:"command_id"`
	Command   string `json:"command"`
}

type CancelMessage struct {
	Type      string `json:"type"` // "cancel"
	CommandID int64  `json:"command_id"`
}

// TerminalOpenMessage asks client to start an interactive shell.
type TerminalOpenMessage struct {
	Type      string `json:"type"` // "terminal_open"
	SessionID string `json:"session_id"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
}

// TerminalInputMessage sends stdin to a terminal session.
type TerminalInputMessage struct {
	Type      string `json:"type"` // "terminal_input"
	SessionID string `json:"session_id"`
	Data      string `json:"data"`
}

// TerminalResizeMessage changes terminal dimensions.
type TerminalResizeMessage struct {
	Type      string `json:"type"` // "terminal_resize"
	SessionID string `json:"session_id"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
}

// TerminalCloseMessage asks client to close a terminal session.
type TerminalCloseMessage struct {
	Type      string `json:"type"` // "terminal_close"
	SessionID string `json:"session_id"`
}

type AckMessage struct {
	Type    string `json:"type"` // "ack"
	Message string `json:"message,omitempty"`
}

// CheckinConfigMessage is sent by the server to push check-in page config to the client.
type CheckinConfigMessage struct {
	Type            string `json:"type"` // "checkin_config"
	CorrelationID   string `json:"correlation_id,omitempty"`
	WelcomeText     string `json:"welcome_text"`
	WarningText     string `json:"warning_text"`
	PostCheckinMsg  string `json:"post_checkin_msg"`
	PostCheckoutCmd string `json:"post_checkout_cmd"`
	PostCheckoutMsg string `json:"post_checkout_msg"`
}

type PongMessage struct {
	Type string `json:"type"` // "pong"
}

// UpdateClientMessage tells a client to self-update from URL.
type UpdateClientMessage struct {
	Type string `json:"type"` // "update_client"
	URL  string `json:"url"`
}

// RefreshSysInfoMessage forces the client to re-run fastfetch (spec cache bypass).
type RefreshSysInfoMessage struct {
	Type string `json:"type"` // "refresh_sysinfo"
}

// ---- Admin WebSocket (Server -> Browser) ----

type AdminEvent struct {
	Event string      `json:"event"`
	Data  interface{} `json:"data"`
}

// CommandOutputEvent is the streaming output sent to admin browsers.
type CommandOutputEvent struct {
	CommandID int64  `json:"command_id"`
	DeviceID  int    `json:"device_id"`
	Stream    string `json:"stream"`
	Line      string `json:"line"`
}

// ---- File Distribution Messages ----

// DistributeStartMessage (Server -> Client)
type DistributeStartMessage struct {
	TransferID string `json:"transfer_id"`
	SHA256     string `json:"sha256"`
	Type       string `json:"type"` // "distribute_start"
	TaskID     string `json:"task_id"`
	FileName   string `json:"file_name"`
	SenderAddr string `json:"sender_addr"` // e.g. "192.168.1.100:48080"
	SaveDir    string `json:"save_dir"`
	PostCmd    string `json:"post_cmd,omitempty"`
}

// DistributeCancelMessage (Server -> Client)
type DistributeCancelMessage struct {
	Type   string `json:"type"` // "distribute_cancel"
	TaskID string `json:"task_id"`
}

// DistributeProgressMessage (Client -> Server)
type DistributeProgressMessage struct {
	TransferID  string  `json:"transfer_id"`
	SHA256      string  `json:"sha256,omitempty"`
	Type        string  `json:"type"` // "distribute_progress"
	TaskID      string  `json:"task_id"`
	DeviceID    int     `json:"device_id"`
	Downloaded  int64   `json:"downloaded"`
	TotalChunks int64   `json:"total_chunks"`
	Percentage  float64 `json:"percentage"`
	SpeedMbps   int64   `json:"speed_mbps"`
	Status      string  `json:"status"` // "idle", "downloading", "completed", "failed", "cancelled"
	Error       string  `json:"error,omitempty"`
}

// BroadcastQueryMessage (Client -> Server)
type BroadcastQueryMessage struct {
	Type          string `json:"type"` // "query_broadcast_state"
	CorrelationID string `json:"correlation_id,omitempty"`
}

// BroadcastQueryResponse (Server -> Client)
type BroadcastQueryResponse struct {
	Type          string `json:"type"` // "query_broadcast_response"
	CorrelationID string `json:"correlation_id,omitempty"`
	PushedState   string `json:"pushed_state"` // e.g. "before", "contesting", "after", or ""
	BaseURL       string `json:"base_url"`     // e.g. "http://icpc-server.local:8082"
	Command       string `json:"command"`      // e.g. "full-firefox http://icpc-server.local:8082/broadcast/before"
}
