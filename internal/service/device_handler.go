package service

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/data"

	"github.com/xuri/excelize/v2"
)

// DeviceHandler handles REST API requests for devices.
type DeviceHandler struct {
	repo      *data.DeviceRepo
	hub       *biz.Hub
	eventRepo *data.DeviceEventRepo
}

// NewDeviceHandler creates a new DeviceHandler.
func NewDeviceHandler(repo *data.DeviceRepo, hub *biz.Hub, eventRepo *data.DeviceEventRepo) *DeviceHandler {
	return &DeviceHandler{repo: repo, hub: hub, eventRepo: eventRepo}
}

// Events returns the online/offline history for a device (GET /api/devices/{id}/events).
func (h *DeviceHandler) Events(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid device id"})
		return
	}
	if h.eventRepo == nil {
		writeJSON(w, http.StatusOK, []data.DeviceEvent{})
		return
	}
	events, err := h.eventRepo.ListByDevice(id, 100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, events)
}

// List returns all devices as JSON (GET /api/devices).
func (h *DeviceHandler) List(w http.ResponseWriter, r *http.Request) {
	devices, err := h.repo.GetAll()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, devices)
}

// Get returns a single device by assigned ID (GET /api/devices/{id}).
func (h *DeviceHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid device id"})
		return
	}

	device, err := h.repo.GetByAssignedID(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "device not found"})
		return
	}
	writeJSON(w, http.StatusOK, device)
}

// Delete removes a device record (DELETE /api/devices/{id}).
func (h *DeviceHandler) Delete(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid device id"})
		return
	}

	if err := h.repo.Delete(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Kick the client if connected, so the TCP handler's disconnect cleanup
	// marks in-flight commands as failed.
	if h.hub != nil {
		h.hub.Kick(id)
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "deleted"})
}

// Reset clears all device records and kicks all connected clients (POST /api/devices/reset).
func (h *DeviceHandler) Reset(w http.ResponseWriter, r *http.Request) {
	if err := h.repo.ResetAll(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	log.Println("[device] all device records cleared")

	// Kick all connected clients so they reconnect with fresh IDs.
	if h.hub != nil {
		h.hub.KickAll()
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "all devices reset"})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// ExportXLSX exports all devices details to an Excel file (GET /api/devices/export).
func (h *DeviceHandler) ExportXLSX(w http.ResponseWriter, r *http.Request) {
	devices, err := h.repo.GetAllFull()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	f := excelize.NewFile()
	defer f.Close()

	sheetName := "Sheet1"

	headers := []string{
		"编号", "主机名", "用户名", "MAC地址", "IP地址", "操作系统",
		"CPU型号", "物理核心数", "逻辑核心数", "GPU信息", "内存大小(GB)",
		"在线状态", "上次上线时间", "首次发现时间", "签到状态", "学生姓名", "学号",
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

		memGB := float64(d.MemoryTotal) / 1024 / 1024 / 1024

		f.SetCellValue(sheetName, getCell(1, row), d.AssignedID)
		f.SetCellValue(sheetName, getCell(2, row), d.Hostname)
		f.SetCellValue(sheetName, getCell(3, row), d.Username)
		f.SetCellValue(sheetName, getCell(4, row), d.MacAddress)
		f.SetCellValue(sheetName, getCell(5, row), d.LocalIP)
		f.SetCellValue(sheetName, getCell(6, row), d.OSPrettyName)
		f.SetCellValue(sheetName, getCell(7, row), d.CPUModel)
		f.SetCellValue(sheetName, getCell(8, row), d.CPUPhysicalCores)
		f.SetCellValue(sheetName, getCell(9, row), d.CPULogicalCores)
		f.SetCellValue(sheetName, getCell(10, row), d.GPUInfo)
		f.SetCellFloat(sheetName, getCell(11, row), memGB, 2, 64)
		f.SetCellValue(sheetName, getCell(12, row), onlineStr)
		f.SetCellValue(sheetName, getCell(13, row), formatTime(d.LastSeen))
		f.SetCellValue(sheetName, getCell(14, row), formatTime(d.FirstSeen))
		f.SetCellValue(sheetName, getCell(15, row), checkinStr)
		f.SetCellValue(sheetName, getCell(16, row), d.StudentName)
		f.SetCellValue(sheetName, getCell(17, row), d.StudentNum)
	}

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", "attachment; filename=devices_export.xlsx")

	if err := f.Write(w); err != nil {
		log.Println("[device-export] write error:", err)
	}
}

func formatTime(val string) string {
	if val == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return val
	}
	return t.Format("2006-01-02 15:04:05")
}

func getCell(col, row int) string {
	cell, _ := excelize.CoordinatesToCellName(col, row)
	return cell
}
