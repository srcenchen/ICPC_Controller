package service

import (
	"encoding/json"
	"net/http"
	"strconv"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/model"
)

// CommandHandler handles REST API requests for command execution.
type CommandHandler struct {
	repo       *data.CommandRepo
	dispatcher *biz.CommandDispatcher
	hub        *biz.Hub
	settings   *ServerSettings
}

// NewCommandHandler creates a new CommandHandler.
func NewCommandHandler(repo *data.CommandRepo, dispatcher *biz.CommandDispatcher, hub *biz.Hub, settings *ServerSettings) *CommandHandler {
	return &CommandHandler{repo: repo, dispatcher: dispatcher, hub: hub, settings: settings}
}

// ExecuteRequest is the JSON body for POST /api/commands.
type ExecuteRequest struct {
	TargetType string `json:"target_type"`         // "single" or "broadcast"
	TargetID   *int   `json:"target_id,omitempty"` // required for single
	Command    string `json:"command"`
}

// Execute runs a command on the specified target(s) (POST /api/commands).
func (h *CommandHandler) Execute(w http.ResponseWriter, r *http.Request) {
	var req ExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.Command == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "command is required"})
		return
	}
	const maxCommandLen = 64 * 1024 // 64KB
	if len(req.Command) > maxCommandLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "command too long"})
		return
	}
	if req.TargetType != "single" && req.TargetType != "broadcast" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target_type must be 'single' or 'broadcast'"})
		return
	}
	if req.TargetType == "single" && req.TargetID == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target_id is required for single target"})
		return
	}

	cmd := &model.CommandLog{
		TargetType: req.TargetType,
		TargetID:   req.TargetID,
		Command:    req.Command,
		Status:     model.CommandStatusDispatched,
		// Audit trail: there is a single admin identity, so record where the
		// request came from. Useful when reconstructing a contest incident.
		ExecutedBy: "admin@" + getClientIP(r),
	}

	if err := h.repo.Create(cmd); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Dispatch asynchronously.
	go func() {
		if cmd.TargetType == "broadcast" {
			h.dispatcher.DispatchBroadcast(cmd)
		} else {
			h.dispatcher.DispatchSingle(cmd)
		}
	}()

	writeJSON(w, http.StatusCreated, cmd)
}

// List returns paginated command history (GET /api/commands?limit=50&offset=0).
func (h *CommandHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0

	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 200 {
			limit = v
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}

	cmds, err := h.repo.GetAll(limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, cmds)
}

// Get returns a single command by ID (GET /api/commands/{id}).
// For broadcast commands, includes child results.
func (h *CommandHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid command id"})
		return
	}

	cmd, err := h.repo.GetByID(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "command not found"})
		return
	}

	// For broadcast parents, populate children.
	if cmd.TargetType == "broadcast" {
		children, err := h.repo.GetByParentID(cmd.ID)
		if err == nil {
			cmd.Children = children
		}
	}

	writeJSON(w, http.StatusOK, cmd)
}

// Clear deletes all command history (POST /api/commands/clear).
func (h *CommandHandler) Clear(w http.ResponseWriter, r *http.Request) {
	if err := h.repo.ClearAll(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "command history cleared"})
}

// Cancel sends a cancel signal to terminate a running command (POST /api/commands/{id}/cancel).
func (h *CommandHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid command id"})
		return
	}

	cmd, err := h.repo.GetByID(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "command not found"})
		return
	}

	// If broadcast, cancel all children.
	if cmd.TargetType == "broadcast" {
		children, _ := h.repo.GetByParentID(cmd.ID)
		for _, child := range children {
			h.sendCancelToClient(child)
		}
	} else {
		h.sendCancelToClient(cmd)
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "cancel sent"})
}

func (h *CommandHandler) sendCancelToClient(cmd *model.CommandLog) {
	if cmd.TargetID == nil {
		return
	}
	client := h.hub.GetClient(*cmd.TargetID)
	if client == nil {
		return
	}
	msg := model.CancelMessage{Type: "cancel", CommandID: cmd.ID}
	data, _ := json.Marshal(msg)
	data = append(data, '\n')
	select {
	case client.Send <- data:
	default:
	}
}

// Presets returns the list of preset commands (GET /api/presets).
func (h *CommandHandler) Presets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.settings.GetPresets())
}
