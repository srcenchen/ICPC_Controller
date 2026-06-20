package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/model"
)

const (
	broadcastDataDir = "data/broadcast"
	maxFontSize      = 10 << 20 // 10 MB
	maxImageSize     = 20 << 20 // 20 MB
)

var allowedFontExts = map[string]string{
	".ttf": "truetype", ".woff": "woff", ".woff2": "woff2",
}

var allowedImageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true, ".webp": true,
}

// BroadcastHandler handles broadcast REST API and file uploads.
type BroadcastHandler struct {
	repo *data.BroadcastRepo
}

// NewBroadcastHandler creates a new BroadcastHandler.
func NewBroadcastHandler(repo *data.BroadcastRepo) *BroadcastHandler {
	os.MkdirAll(filepath.Join(broadcastDataDir, "fonts"), 0755)
	os.MkdirAll(filepath.Join(broadcastDataDir, "images"), 0755)
	h := &BroadcastHandler{repo: repo}
	// Wire up the carousel page provider.
	BroadcastWS.PageProvider = func(mode string) []pageInfo {
		pages, _ := repo.ListPages(mode)
		var infos []pageInfo
		for _, p := range pages {
			infos = append(infos, pageInfo{DurationMs: p.DurationMs})
		}
		return infos
	}
	return h
}

// ---- Pages ----

func (h *BroadcastHandler) ListPages(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "before"
	}
	pages, err := h.repo.GetPagesWithItems(mode)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Include sync info: server time + when this mode was started.
	startedAt, _ := h.repo.GetConfig("broadcast_started_at_" + mode)
	serverNow := time.Now().Format(time.RFC3339Nano)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"pages":      pages,
		"server_time": serverNow,
		"started_at":  startedAt,
	})
}

func (h *BroadcastHandler) CreatePage(w http.ResponseWriter, r *http.Request) {
	var p model.BroadcastPage
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if p.Mode == "" {
		p.Mode = "before"
	}
	if p.BgColor == "" {
		p.BgColor = "#000000"
	}
	if p.Transition == "" {
		p.Transition = "fade"
	}
	if err := h.repo.CreatePage(&p); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.pushToDisplayClients(p.Mode)
	writeJSON(w, http.StatusCreated, p)
}

func (h *BroadcastHandler) UpdatePage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	var p model.BroadcastPage
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	p.ID = id
	if err := h.repo.UpdatePage(&p); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.pushToAllModes()
	writeJSON(w, http.StatusOK, p)
}

func (h *BroadcastHandler) DeletePage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	if err := h.repo.DeletePage(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.pushToAllModes()
	writeJSON(w, http.StatusOK, map[string]string{"message": "deleted"})
}

func (h *BroadcastHandler) ReorderPages(w http.ResponseWriter, r *http.Request) {
	var pages []model.BroadcastPage
	if err := json.NewDecoder(r.Body).Decode(&pages); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if err := h.repo.UpdatePageOrder(pages); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.pushToAllModes()
	writeJSON(w, http.StatusOK, map[string]string{"message": "reordered"})
}

// ---- Items ----

func (h *BroadcastHandler) ListItems(w http.ResponseWriter, r *http.Request) {
	pageIDStr := r.URL.Query().Get("page_id")
	pageID, err := strconv.ParseInt(pageIDStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid page_id"})
		return
	}
	items, err := h.repo.ListItems(pageID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *BroadcastHandler) CreateItem(w http.ResponseWriter, r *http.Request) {
	var it model.BroadcastItem
	if err := json.NewDecoder(r.Body).Decode(&it); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if err := h.repo.CreateItem(&it); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.pushToAllModes()
	writeJSON(w, http.StatusCreated, it)
}

func (h *BroadcastHandler) UpdateItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	var it model.BroadcastItem
	if err := json.NewDecoder(r.Body).Decode(&it); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	it.ID = id
	if err := h.repo.UpdateItem(&it); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.pushToAllModes()
	writeJSON(w, http.StatusOK, it)
}

func (h *BroadcastHandler) DeleteItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	// If the item is an image, delete the image file from disk.
	item, err := h.repo.GetItemByID(id)
	if err == nil && item.ItemType == "image" && item.Content != "" {
		// Content is like "/broadcast/images/filename.png" — extract filename.
		filename := filepath.Base(item.Content)
		os.Remove(filepath.Join(broadcastDataDir, "images", filename))
	}
	if err := h.repo.DeleteItem(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.pushToAllModes()
	writeJSON(w, http.StatusOK, map[string]string{"message": "deleted"})
}

// ---- Fonts ----

func (h *BroadcastHandler) ListFonts(w http.ResponseWriter, r *http.Request) {
	fonts, err := h.repo.ListFonts()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, fonts)
}

func (h *BroadcastHandler) UploadFont(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxFontSize); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file too large (max 10MB)"})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file"})
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	_, ok := allowedFontExts[ext]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported font format, allowed: .ttf, .woff, .woff2"})
		return
	}

	fontName := r.FormValue("name")
	if fontName == "" {
		fontName = strings.TrimSuffix(header.Filename, ext)
	}

	// Generate unique filename to avoid collisions.
	safeName := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
	fontPath := filepath.Join(broadcastDataDir, "fonts", safeName)

	dst, err := os.Create(fontPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save font"})
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		os.Remove(fontPath)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save font"})
		return
	}

	font := &model.BroadcastFont{
		Name: fontName, Filename: safeName,
		OriginalName: header.Filename, Format: ext[1:], // strip the dot
	}
	if err := h.repo.CreateFont(font); err != nil {
		os.Remove(fontPath)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, font)
}

func (h *BroadcastHandler) DeleteFont(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	font, err := h.repo.GetFontByID(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "font not found"})
		return
	}
	// Remove file from disk (ignore error if missing).
	os.Remove(filepath.Join(broadcastDataDir, "fonts", font.Filename))
	// Clear active font reference if this was the active one.
	if active, _ := h.repo.GetConfig("active_font"); active == font.Filename {
		h.repo.SetConfig("active_font", "")
	}
	if err := h.repo.DeleteFont(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "deleted"})
}

// ---- Position-only update (for drag/resize saves) ----

func (h *BroadcastHandler) UpdateItemPosition(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	var body struct {
		PosX   float64 `json:"pos_x"`
		PosY   float64 `json:"pos_y"`
		Width  float64 `json:"width"`
		Height float64 `json:"height"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if err := h.repo.UpdateItemPositionAndSize(id, body.PosX, body.PosY, body.Width, body.Height); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.pushToAllModes()
	writeJSON(w, http.StatusOK, map[string]string{"message": "updated"})
}

// ---- Images ----

func (h *BroadcastHandler) UploadImage(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxImageSize); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file too large (max 20MB)"})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file"})
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !allowedImageExts[ext] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported image format"})
		return
	}

	safeName := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
	imgPath := filepath.Join(broadcastDataDir, "images", safeName)
	dst, err := os.Create(imgPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save image"})
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		os.Remove(imgPath)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save image"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"filename": safeName,
		"url":      "/broadcast/images/" + safeName,
	})
}

// ---- Config ----

func (h *BroadcastHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	activeFont, _ := h.repo.GetConfig("active_font")
	countdownTarget, _ := h.repo.GetConfig("countdown_target")
	baseURL, _ := h.repo.GetConfig("base_url")
	refWidth, _ := h.repo.GetConfig("reference_width")
	pushedState, _ := h.repo.GetConfig("pushed_state")
	if baseURL == "" {
		baseURL = "http://icpc-server.local:8082"
	}
	if refWidth == "" {
		refWidth = "1280"
	}
	writeJSON(w, http.StatusOK, model.BroadcastConfig{
		ActiveFont:      activeFont,
		CountdownTarget: countdownTarget,
		BaseURL:         baseURL,
		ReferenceWidth:  refWidth,
		PushedState:     pushedState,
	})
}

func (h *BroadcastHandler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ActiveFont      *string `json:"active_font"`
		CountdownTarget *string `json:"countdown_target"`
		BaseURL         *string `json:"base_url"`
		ReferenceWidth  *string `json:"reference_width"`
		SyncReset       *string `json:"sync_reset"`
		PushedState     *string `json:"pushed_state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.ActiveFont != nil {
		h.repo.SetConfig("active_font", *req.ActiveFont)
	}
	if req.CountdownTarget != nil {
		h.repo.SetConfig("countdown_target", *req.CountdownTarget)
	}
	if req.BaseURL != nil {
		h.repo.SetConfig("base_url", *req.BaseURL)
	}
	if req.ReferenceWidth != nil {
		h.repo.SetConfig("reference_width", *req.ReferenceWidth)
	}
	if req.SyncReset != nil {
		h.repo.SetConfig("broadcast_started_at_"+*req.SyncReset, time.Now().Format(time.RFC3339Nano))
		h.pushSyncReset(*req.SyncReset)
	}
	if req.PushedState != nil {
		h.repo.SetConfig("pushed_state", *req.PushedState)
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "config updated"})
}

// ---- Countdown ----

func (h *BroadcastHandler) GetCountdown(w http.ResponseWriter, r *http.Request) {
	target, serverNow, err := h.repo.GetCountdownTarget()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"target":      target,
		"server_time": serverNow,
	})
}

// ---- Static file serving ----

// ServeFont serves a font file from disk.
func (h *BroadcastHandler) ServeFont(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")
	ext := strings.ToLower(filepath.Ext(filename))
	contentType := "application/octet-stream"
	switch ext {
	case ".ttf":
		contentType = "font/ttf"
	case ".woff":
		contentType = "font/woff"
	case ".woff2":
		contentType = "font/woff2"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, filepath.Join(broadcastDataDir, "fonts", filename))
}

// ServeImage serves an uploaded image from disk.
func (h *BroadcastHandler) ServeImage(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, filepath.Join(broadcastDataDir, "images", filename))
}


// pushToDisplayClients sends updated pages to all WebSocket display clients for a mode.
func (h *BroadcastHandler) pushToDisplayClients(mode string) {
	pages, err := h.repo.GetPagesWithItems(mode)
	if err != nil {
		return
	}
	startedAt, _ := h.repo.GetConfig("broadcast_started_at_" + mode)
	msg, _ := json.Marshal(map[string]interface{}{
		"type":        "pages_updated",
		"mode":        mode,
		"pages":       pages,
		"server_time": time.Now().Format(time.RFC3339Nano),
		"started_at":  startedAt,
	})
	BroadcastWS.Broadcast(mode, msg)
}


func (h *BroadcastHandler) pushSyncReset(mode string) {
	startedAt, _ := h.repo.GetConfig("broadcast_started_at_" + mode)
	msg, _ := json.Marshal(map[string]interface{}{
		"type":        "sync_reset",
		"mode":        mode,
		"server_time": time.Now().Format(time.RFC3339Nano),
		"started_at":  startedAt,
	})
	BroadcastWS.Broadcast(mode, msg)
	// Start/restart server-side carousel for this mode.
	BroadcastWS.StartCarousel(mode)
}

func (h *BroadcastHandler) pushToAllModes() {
	for _, m := range []string{"before", "contesting", "after"} {
		h.pushToDisplayClients(m)
		BroadcastWS.StopCarousel(m)
		// Auto-restart carousel after pages update.
		go func(mode string) {
			time.Sleep(200 * time.Millisecond)
			BroadcastWS.StartCarousel(mode)
		}(m)
	}
}
