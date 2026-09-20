package service

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/data"

	"github.com/xuri/excelize/v2"
)

// CheckinHandler handles check-in management REST API endpoints.
type CheckinHandler struct {
	repo *data.DeviceRepo
	hub  *biz.Hub
}

// NewCheckinHandler creates a new CheckinHandler.
func NewCheckinHandler(repo *data.DeviceRepo, hub *biz.Hub) *CheckinHandler {
	return &CheckinHandler{repo: repo, hub: hub}
}

// List returns all devices with check-in info (GET /api/checkin).
func (h *CheckinHandler) List(w http.ResponseWriter, r *http.Request) {
	devices, err := h.repo.GetCheckinAll()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, devices)
}

// Stats returns check-in statistics (GET /api/checkin/stats).
func (h *CheckinHandler) Stats(w http.ResponseWriter, r *http.Request) {
	total, checkedIn, checkedOut, err := h.repo.GetCheckinStats()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"total":       total,
		"checked_in":  checkedIn,
		"checked_out": checkedOut,
		"not_checked": total - checkedIn - checkedOut,
	})
}

// DoCheckin performs check-in for a device (POST /api/checkin/{id}/checkin).
func (h *CheckinHandler) DoCheckin(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid device id"})
		return
	}

	var body struct {
		StudentName string `json:"student_name"`
		StudentNum  string `json:"student_num"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if body.StudentName == "" || body.StudentNum == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "student_name and student_num are required"})
		return
	}

	if err := h.repo.Checkin(id, body.StudentName, body.StudentNum); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.broadcastCheckinUpdated(id)
	writeJSON(w, http.StatusOK, map[string]string{"message": "checkin success"})
}

// DoCheckout performs check-out for a device (POST /api/checkin/{id}/checkout).
func (h *CheckinHandler) DoCheckout(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid device id"})
		return
	}

	if err := h.repo.Checkout(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.broadcastCheckinUpdated(id)
	writeJSON(w, http.StatusOK, map[string]string{"message": "checkout success"})
}

// DoRestoreCheckout performs restore check-in (undo checkout) for a device (POST /api/checkin/{id}/restore).
func (h *CheckinHandler) DoRestoreCheckout(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid device id"})
		return
	}

	if err := h.repo.RestoreCheckout(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.broadcastCheckinUpdated(id)
	writeJSON(w, http.StatusOK, map[string]string{"message": "restore checkin success"})
}

// Reset resets a device's check-in status (POST /api/checkin/{id}/reset).
func (h *CheckinHandler) Reset(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid device id"})
		return
	}

	if err := h.repo.ResetCheckin(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.broadcastCheckinUpdated(id)
	writeJSON(w, http.StatusOK, map[string]string{"message": "checkin reset"})
}

// ResetAll resets all devices' check-in status (POST /api/checkin/reset-all).
func (h *CheckinHandler) ResetAll(w http.ResponseWriter, r *http.Request) {
	n, err := h.repo.ResetAllCheckin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.broadcastCheckinUpdated(0) // 0 = all devices affected
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":        "all checkins reset",
		"affected_count": n,
	})
}

// Swap moves check-in info from one device to another (POST /api/checkin/swap).
func (h *CheckinHandler) Swap(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FromAssignedID int `json:"from_assigned_id"`
		ToAssignedID   int `json:"to_assigned_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if body.FromAssignedID <= 0 || body.ToAssignedID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from_assigned_id and to_assigned_id are required"})
		return
	}

	if err := h.repo.SwapCheckin(body.FromAssignedID, body.ToAssignedID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.broadcastCheckinUpdated(body.FromAssignedID)
	h.broadcastCheckinUpdated(body.ToAssignedID)
	writeJSON(w, http.StatusOK, map[string]string{"message": "swap success"})
}

func (h *CheckinHandler) broadcastCheckinUpdated(assignedID int) {
	if h.hub != nil {
		h.hub.BroadcastAdminEvent("checkin_updated", map[string]interface{}{
			"assigned_id": assignedID,
		})
	}
}

// ExportXLSX exports check-in logs to an Excel file (GET /api/checkin/export).
func (h *CheckinHandler) ExportXLSX(w http.ResponseWriter, r *http.Request) {
	devices, err := h.repo.GetAllFull()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	f := excelize.NewFile()
	defer f.Close()

	sheetName := "Sheet1"

	headers := []string{
		"编号", "主机名", "学生姓名", "学号", "签到状态", "在线状态", "签到时间", "签退时间",
	}

	for colIdx, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(colIdx+1, 1)
		f.SetCellValue(sheetName, cell, header)
	}

	for rowIdx, d := range devices {
		row := rowIdx + 2

		onlineStr := "离线"
		if d.Connected {
			onlineStr = "在线"
		}

		checkinStr := "未签到"
		if d.CheckinStatus == 1 {
			checkinStr = "已签到"
		} else if d.CheckinStatus == 2 {
			checkinStr = "已签退"
		}

		f.SetCellValue(sheetName, getCell(1, row), d.AssignedID)
		f.SetCellValue(sheetName, getCell(2, row), d.Hostname)
		f.SetCellValue(sheetName, getCell(3, row), d.StudentName)
		f.SetCellValue(sheetName, getCell(4, row), d.StudentNum)
		f.SetCellValue(sheetName, getCell(5, row), checkinStr)
		f.SetCellValue(sheetName, getCell(6, row), onlineStr)
		f.SetCellValue(sheetName, getCell(7, row), formatTime(d.CheckinTime))
		f.SetCellValue(sheetName, getCell(8, row), formatTime(d.CheckoutTime))
	}

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", "attachment; filename=checkin_export.xlsx")

	if err := f.Write(w); err != nil {
		log.Println("[checkin-export] write error:", err)
	}
}
