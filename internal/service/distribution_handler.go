package service

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

type DistributionHandler struct {
	mgr *DistributionManager
}

func NewDistributionHandler(mgr *DistributionManager) *DistributionHandler {
	return &DistributionHandler{mgr: mgr}
}

// ListFiles returns files available on the server (GET /api/distribution/files)
func (h *DistributionHandler) ListFiles(w http.ResponseWriter, r *http.Request) {
	files, err := h.mgr.GetUploadedFiles()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, files)
}

// UploadFile handles file upload using chunked/streaming multi-part form (POST /api/distribution/upload)
func (h *DistributionHandler) UploadFile(w http.ResponseWriter, r *http.Request) {
	// Parse up to 10MB in memory, rest goes to disk temp files automatically
	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to parse multipart form"})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no file found in request"})
		return
	}
	defer file.Close()

	destPath := filepath.Join(h.mgr.uploadDir, filepath.Base(header.Filename))
	out, err := os.Create(destPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create target file"})
		return
	}
	defer out.Close()

	_, err = io.Copy(out, file)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to write file content"})
		return
	}

	log.Printf("[dist-upload] successfully uploaded: %s (%d bytes)", header.Filename, header.Size)
	writeJSON(w, http.StatusOK, map[string]string{"message": "upload successful", "filename": header.Filename})
}

// DeleteFiles deletes selected files (POST /api/distribution/delete)
func (h *DistributionHandler) DeleteFiles(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Filenames []string `json:"filenames"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	for _, name := range body.Filenames {
		if err := h.mgr.DeleteFile(name); err != nil {
			log.Printf("[dist] failed to delete file %s: %v", name, err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "selected files deleted"})
}

// ClearFiles deletes all files (POST /api/distribution/clear)
func (h *DistributionHandler) ClearFiles(w http.ResponseWriter, r *http.Request) {
	if err := h.mgr.ClearAllFiles(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "all files cleared"})
}

// StartTask triggers file distribution to client targets (POST /api/distribution/start)
func (h *DistributionHandler) StartTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Files     []string `json:"files"`
		SaveDir   string   `json:"save_dir"`
		TargetIDs []int    `json:"target_ids"` // empty = broadcast to all online
		ServerIP  string   `json:"server_ip"`
		PostCmd   string   `json:"post_cmd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	task, err := h.mgr.StartTask(body.Files, body.SaveDir, body.TargetIDs, body.ServerIP, body.PostCmd)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, task)
}

// StopTask cancels the current distribution task (POST /api/distribution/stop)
func (h *DistributionHandler) StopTask(w http.ResponseWriter, r *http.Request) {
	if err := h.mgr.StopTask(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "distribution stopped"})
}

// GetStatus returns the current task progress (GET /api/distribution/status)
func (h *DistributionHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	task := h.mgr.GetActiveTask()
	if task == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":       "idle",
			"suggested_ip": getOutboundIP(),
		})
		return
	}

	task.mu.RLock()
	defer task.mu.RUnlock()

	writeJSON(w, http.StatusOK, task)
}

// RetryDevice re-starts distribution download for a failed client (POST /api/distribution/retry)
func (h *DistributionHandler) RetryDevice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceID int `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if err := h.mgr.RetryDevice(body.DeviceID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "retry command sent"})
}

// Precheck triggers connectivity checks to client targets (POST /api/distribution/precheck)
func (h *DistributionHandler) Precheck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ServerIP  string `json:"server_ip"`
		TargetIDs []int  `json:"target_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	results, err := h.mgr.RunPrecheck(body.ServerIP, body.TargetIDs)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, results)
}

// ResetTask clears the finished task status (POST /api/distribution/reset)
func (h *DistributionHandler) ResetTask(w http.ResponseWriter, r *http.Request) {
	if err := h.mgr.ResetTask(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "task status reset"})
}
