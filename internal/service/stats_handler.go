package service

import (
	"net/http"

	"ICPCRemoteControl/internal/data"
)

// StatsHandler handles the dashboard stats endpoint.
type StatsHandler struct {
	deviceRepo  *data.DeviceRepo
	commandRepo *data.CommandRepo
}

// NewStatsHandler creates a new StatsHandler.
func NewStatsHandler(deviceRepo *data.DeviceRepo, commandRepo *data.CommandRepo) *StatsHandler {
	return &StatsHandler{deviceRepo: deviceRepo, commandRepo: commandRepo}
}

// GetStats returns aggregated dashboard statistics (GET /api/stats).
func (h *StatsHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	total, online, err := h.deviceRepo.GetStats()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	_, checkedIn, _, err := h.deviceRepo.GetCheckinStats()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	totalCommands, err := h.commandRepo.GetTotalCount()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	recentCommands, err := h.commandRepo.GetRecent(10)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	stats := map[string]interface{}{
		"total_devices":   total,
		"online_devices":  online,
		"offline_devices": total - online,
		"checked_in":      checkedIn,
		"total_commands":  totalCommands,
		"recent_commands": recentCommands,
	}
	writeJSON(w, http.StatusOK, stats)
}
