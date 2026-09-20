package model

// Command status constants.
const (
	CommandStatusPending    = "pending"
	CommandStatusDispatched = "dispatched"
	CommandStatusRunning    = "running"
	CommandStatusCompleted  = "completed"
	CommandStatusFailed     = "failed"
	CommandStatusTimeout    = "timeout"
)

// CommandLog represents a command execution record.
type CommandLog struct {
	ID           int64         `json:"id"`
	ParentID     *int64        `json:"parent_id,omitempty"` // points to parent broadcast command (non-nil = child)
	TargetType   string        `json:"target_type"`         // "single" or "broadcast"
	TargetID     *int          `json:"target_id,omitempty"` // assigned device ID
	Command      string        `json:"command"`
	Status       string        `json:"status"`
	Output       string        `json:"output"`
	ErrorOutput  string        `json:"error_output"`
	ExecutedBy   string        `json:"executed_by"`
	CreatedAt    string        `json:"created_at"`
	DispatchedAt string        `json:"dispatched_at,omitempty"`
	CompletedAt  string        `json:"completed_at,omitempty"`
	DurationMS   int64         `json:"duration_ms"`
	Children     []*CommandLog `json:"children,omitempty"` // populated for broadcast parents
}
