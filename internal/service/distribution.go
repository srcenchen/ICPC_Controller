package service

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/model"
	"go-silver-core/pkg/gosilver"
)

var (
	DistributionMgr *DistributionManager
)

type FileInfo struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

type ClientProgress struct {
	DeviceID    int     `json:"device_id"`
	Hostname    string  `json:"hostname"`
	Downloaded  int64   `json:"downloaded"`
	TotalChunks int64   `json:"total_chunks"`
	Percentage  float64 `json:"percentage"`
	SpeedMbps   int64   `json:"speed_mbps"`
	Status      string  `json:"status"` // "idle", "downloading", "completed", "failed", "cancelled"
	Error       string  `json:"error,omitempty"`
	UpdatedAt   string  `json:"updated_at"`
}

type DistributeTask struct {
	TaskID       string                  `json:"task_id"`
	Files        []string                `json:"files"`
	SaveDir      string                  `json:"save_dir"`
	ServerIP     string                  `json:"server_ip"`
	PostCmd      string                  `json:"post_cmd"`
	TargetIDs    []int                   `json:"target_ids"`
	ActiveFile   string                  `json:"active_file"`
	ActiveIdx    int                     `json:"active_idx"`
	Status       string                  `json:"status"` // "running", "completed", "stopped"
	Progresses   map[int]*ClientProgress `json:"progresses"`
	activeServer *gosilver.Server
	mu           sync.RWMutex
}

type PrecheckResult struct {
	DeviceID int    `json:"device_id"`
	Success  bool   `json:"success"`
	Error    string `json:"error,omitempty"`
}

type PrecheckSession struct {
	TargetIDs map[int]bool
	Results   map[int]PrecheckResult
	Mu        sync.Mutex
	Done      chan struct{}
}

type DistributionManager struct {
	uploadDir      string
	hub            *biz.Hub
	activeTask     *DistributeTask
	taskMu         sync.Mutex
	activePrecheck *PrecheckSession
}

func NewDistributionManager(hub *biz.Hub, uploadDir string) *DistributionManager {
	_ = os.MkdirAll(uploadDir, 0755)
	return &DistributionManager{
		uploadDir: uploadDir,
		hub:       hub,
	}
}

// GetUploadedFiles scans the data/uploads directory and returns a list of files
func (mgr *DistributionManager) GetUploadedFiles() ([]FileInfo, error) {
	entries, err := os.ReadDir(mgr.uploadDir)
	if err != nil {
		return nil, err
	}

	files := make([]FileInfo, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, FileInfo{
			Name:    info.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime().Format("2006-01-02 15:04:05"),
		})
	}
	return files, nil
}

// DeleteFile deletes a specific file in the uploads folder
func (mgr *DistributionManager) DeleteFile(name string) error {
	path := filepath.Join(mgr.uploadDir, filepath.Base(name))
	return os.Remove(path)
}

// ClearAllFiles deletes all files in the uploads folder
func (mgr *DistributionManager) ClearAllFiles() error {
	entries, err := os.ReadDir(mgr.uploadDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		_ = os.Remove(filepath.Join(mgr.uploadDir, entry.Name()))
	}
	return nil
}

// StartTask creates and runs a sequential P2P file distribution task
func (mgr *DistributionManager) StartTask(files []string, saveDir string, targetIDs []int, serverIP string, postCmd string) (*DistributeTask, error) {
	mgr.taskMu.Lock()
	mgr.taskMu.Unlock() // Wait, let's keep the original lock pattern!
	// Wait, let's look at lines 116-117 in the original:
	// mgr.taskMu.Lock()
	// defer mgr.taskMu.Unlock()
	// Let's use the precise code:
	mgr.taskMu.Lock()
	defer mgr.taskMu.Unlock()

	if mgr.activeTask != nil && mgr.activeTask.Status == "running" {
		return nil, fmt.Errorf("another distribution task is already running")
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no files selected for distribution")
	}

	if saveDir == "" {
		saveDir = "./downloads"
	}

	// Resolve targets
	var finalTargets []int
	if len(targetIDs) == 0 {
		// All online devices
		mgr.hub.BroadcastAdminEvent("distribute_log", "未指定目标设备，自动选择所有在线选手机进行分发")
		// Get all clients from hub
		// A simple read connection IDs is clean
		for i := 1; i <= 200; i++ {
			if mgr.hub.IsOnline(i) {
				finalTargets = append(finalTargets, i)
			}
		}
	} else {
		finalTargets = targetIDs
	}

	if len(finalTargets) == 0 {
		return nil, fmt.Errorf("no online devices available to distribute files to")
	}

	// Initialize progresses
	progresses := make(map[int]*ClientProgress)
	for _, deviceID := range finalTargets {
		hostname := fmt.Sprintf("#%d", deviceID)
		if client := mgr.hub.GetClient(deviceID); client != nil {
			// We don't have hostname in client connection directly, but we can query or let it resolve in UI
		}
		progresses[deviceID] = &ClientProgress{
			DeviceID:  deviceID,
			Hostname:  hostname,
			Status:    "idle",
			UpdatedAt: time.Now().Format("2006-01-02 15:04:05"),
		}
	}

	task := &DistributeTask{
		TaskID:     fmt.Sprintf("task_%d", time.Now().UnixNano()),
		Files:      files,
		SaveDir:    saveDir,
		ServerIP:   serverIP,
		PostCmd:    postCmd,
		TargetIDs:  finalTargets,
		Status:     "running",
		Progresses: progresses,
	}

	mgr.activeTask = task
	go task.run(mgr)

	return task, nil
}

// StopTask cancels the active task and stops any running GoSilver server
func (mgr *DistributionManager) StopTask() error {
	mgr.taskMu.Lock()
	t := mgr.activeTask
	mgr.taskMu.Unlock()

	if t == nil {
		return fmt.Errorf("no active task running")
	}

	t.mu.Lock()
	t.Status = "stopped"
	if t.activeServer != nil {
		t.activeServer.Stop()
	}
	t.mu.Unlock()

	// Notify clients to cancel
	cancelMsg := model.DistributeCancelMessage{
		Type:   "distribute_cancel",
		TaskID: t.TaskID,
	}
	msgBytes, _ := json.Marshal(cancelMsg)
	msgBytes = append(msgBytes, '\n')
	mgr.hub.BroadcastToClients(msgBytes)

	mgr.hub.BroadcastAdminEvent("distribute_progress_update", t)
	return nil
}

// GetActiveTask returns the currently running task, or nil
func (mgr *DistributionManager) GetActiveTask() *DistributeTask {
	mgr.taskMu.Lock()
	defer mgr.taskMu.Unlock()
	return mgr.activeTask
}

// HandleProgressReport handles real-time download status reports from clients
func (mgr *DistributionManager) HandleProgressReport(msg model.DistributeProgressMessage) {
	mgr.taskMu.Lock()
	t := mgr.activeTask
	mgr.taskMu.Unlock()

	if t == nil || t.TaskID != msg.TaskID {
		return
	}

	t.mu.Lock()
	p, ok := t.Progresses[msg.DeviceID]
	if ok {
		p.Downloaded = msg.Downloaded
		p.TotalChunks = msg.TotalChunks
		p.Percentage = msg.Percentage
		p.SpeedMbps = msg.SpeedMbps
		p.Status = msg.Status
		p.Error = msg.Error
		p.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")
	}
	t.mu.Unlock()

	mgr.hub.BroadcastAdminEvent("distribute_progress_update", t)
}

// RetryDevice re-triggers distribution to a failed client device
func (mgr *DistributionManager) RetryDevice(deviceID int) error {
	mgr.taskMu.Lock()
	t := mgr.activeTask
	mgr.taskMu.Unlock()

	if t == nil {
		return fmt.Errorf("no active task running")
	}

	t.mu.Lock()
	p, ok := t.Progresses[deviceID]
	if !ok {
		t.mu.Unlock()
		return fmt.Errorf("device not part of the active task")
	}

	if p.Status != "failed" {
		t.mu.Unlock()
		return fmt.Errorf("device is not in failed state (current status: %s)", p.Status)
	}

	p.Status = "downloading"
	p.Error = ""
	p.Downloaded = 0
	p.Percentage = 0
	p.SpeedMbps = 0
	p.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")

	lanIP := t.ServerIP
	if lanIP == "" {
		lanIP = getOutboundIP()
	}
	senderAddr := fmt.Sprintf("%s:48080", lanIP)

	startMsg := model.DistributeStartMessage{
		Type:       "distribute_start",
		TaskID:     t.TaskID,
		FileName:   t.ActiveFile,
		SenderAddr: senderAddr,
		SaveDir:    t.SaveDir,
		PostCmd:    t.PostCmd,
	}
	t.mu.Unlock()

	msgBytes, _ := json.Marshal(startMsg)
	msgBytes = append(msgBytes, '\n')

	clientConn := mgr.hub.GetClient(deviceID)
	if clientConn == nil {
		t.mu.Lock()
		p.Status = "failed"
		p.Error = "client offline"
		t.mu.Unlock()
		return fmt.Errorf("device is offline")
	}

	clientConn.Send <- msgBytes
	mgr.hub.BroadcastAdminEvent("distribute_progress_update", t)
	return nil
}

func (t *DistributeTask) run(mgr *DistributionManager) {
	defer func() {
		t.mu.Lock()
		if t.Status == "running" {
			t.Status = "completed"
		}
		if t.activeServer != nil {
			t.activeServer.Stop()
			t.activeServer = nil
		}
		t.mu.Unlock()

		// Keep the finished task in activeTask so frontend can read final completion progress.

		mgr.hub.BroadcastAdminEvent("distribute_task_finished", t)
	}()

	for idx, file := range t.Files {
		t.mu.Lock()
		// If task was stopped, break immediately
		if t.Status == "stopped" {
			t.mu.Unlock()
			return
		}

		t.ActiveFile = file
		t.ActiveIdx = idx

		filePath := filepath.Join(mgr.uploadDir, file)
		t.activeServer = gosilver.NewServer(":48080", filePath)
		t.mu.Unlock()

		log.Printf("[dist] starting GoSilver server for %s", file)
		if err := t.activeServer.Start(); err != nil {
			log.Printf("[dist] failed to start GoSilver server for %s: %v", file, err)
			t.mu.Lock()
			for _, p := range t.Progresses {
				p.Status = "failed"
				p.Error = fmt.Sprintf("server start failed: %v", err)
			}
			t.mu.Unlock()
			mgr.hub.BroadcastAdminEvent("distribute_progress_update", t)
			return
		}

		t.mu.Lock()
		lanIP := t.ServerIP
		if lanIP == "" {
			lanIP = getOutboundIP()
		}
		senderAddr := fmt.Sprintf("%s:48080", lanIP)

		// Reset progresses of active target devices for the current file
		for _, p := range t.Progresses {
			p.Downloaded = 0
			p.TotalChunks = 0
			p.Percentage = 0
			p.SpeedMbps = 0
			p.Status = "downloading"
			p.Error = ""
			p.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")
		}
		t.mu.Unlock()
		mgr.hub.BroadcastAdminEvent("distribute_progress_update", t)

		// Notify targets
		startMsg := model.DistributeStartMessage{
			Type:       "distribute_start",
			TaskID:     t.TaskID,
			FileName:   file,
			SenderAddr: senderAddr,
			SaveDir:    t.SaveDir,
			PostCmd:    t.PostCmd,
		}
		msgBytes, _ := json.Marshal(startMsg)
		msgBytes = append(msgBytes, '\n')

		t.mu.RLock()
		for id := range t.Progresses {
			clientConn := mgr.hub.GetClient(id)
			if clientConn != nil {
				clientConn.Send <- msgBytes
			} else {
				p := t.Progresses[id]
				p.Status = "failed"
				p.Error = "client offline"
			}
		}
		t.mu.RUnlock()
		mgr.hub.BroadcastAdminEvent("distribute_progress_update", t)

		// Wait loop
		for {
			time.Sleep(1 * time.Second)

			t.mu.RLock()
			isStopped := t.Status == "stopped"
			t.mu.RUnlock()

			if isStopped {
				return
			}

			// Check if all active clients finished (status != downloading)
			t.mu.RLock()
			allFinished := true
			for _, p := range t.Progresses {
				if p.Status == "downloading" || p.Status == "idle" {
					allFinished = false
					break
				}
			}
			t.mu.RUnlock()

			if allFinished {
				log.Printf("[dist] file %s distribution finished", file)
				break
			}
		}

		t.mu.Lock()
		if t.activeServer != nil {
			t.activeServer.Stop()
			t.activeServer = nil
		}
		t.mu.Unlock()

		time.Sleep(1 * time.Second)
	}
}

func getOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		addrs, err := net.InterfaceAddrs()
		if err == nil {
			for _, address := range addrs {
				if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
					if ipnet.IP.To4() != nil {
						return ipnet.IP.String()
					}
				}
			}
		}
		return "127.0.0.1"
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

func (mgr *DistributionManager) HandlePrecheckReport(deviceID int, success bool, errMsg string) {
	mgr.taskMu.Lock()
	session := mgr.activePrecheck
	mgr.taskMu.Unlock()

	if session == nil {
		return
	}

	session.Mu.Lock()
	defer session.Mu.Unlock()

	if !session.TargetIDs[deviceID] {
		return
	}

	session.Results[deviceID] = PrecheckResult{
		DeviceID: deviceID,
		Success:  success,
		Error:    errMsg,
	}

	// Check if all target IDs have reported
	if len(session.Results) == len(session.TargetIDs) {
		select {
		case session.Done <- struct{}{}:
		default:
		}
	}
}

func (mgr *DistributionManager) RunPrecheck(serverIP string, targetIDs []int) ([]PrecheckResult, error) {
	mgr.taskMu.Lock()
	if mgr.activeTask != nil {
		mgr.taskMu.Unlock()
		return nil, fmt.Errorf("another distribution task is already running")
	}
	if mgr.activePrecheck != nil {
		mgr.taskMu.Unlock()
		return nil, fmt.Errorf("another connectivity precheck is already running")
	}

	if serverIP == "" {
		serverIP = getOutboundIP()
	}

	// Resolve targets
	var finalTargets []int
	if len(targetIDs) == 0 {
		for i := 1; i <= 200; i++ {
			if mgr.hub.IsOnline(i) {
				finalTargets = append(finalTargets, i)
			}
		}
	} else {
		finalTargets = targetIDs
	}

	if len(finalTargets) == 0 {
		mgr.taskMu.Unlock()
		return nil, fmt.Errorf("no online devices available to check connectivity")
	}

	targetMap := make(map[int]bool)
	for _, id := range finalTargets {
		targetMap[id] = true
	}

	session := &PrecheckSession{
		TargetIDs: targetMap,
		Results:   make(map[int]PrecheckResult),
		Done:      make(chan struct{}, 1),
	}
	mgr.activePrecheck = session
	mgr.taskMu.Unlock()

	defer func() {
		mgr.taskMu.Lock()
		mgr.activePrecheck = nil
		mgr.taskMu.Unlock()
	}()

	// Start temporary TCP listener on 48080
	ln, err := net.Listen("tcp", ":48080")
	if err != nil {
		return nil, fmt.Errorf("failed to bind port 48080 on server: %w", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	// Send check request to clients
	precheckMsg := map[string]interface{}{
		"type":      "distribute_precheck",
		"server_ip": serverIP,
	}
	msgBytes, _ := json.Marshal(precheckMsg)
	msgBytes = append(msgBytes, '\n')

	for _, id := range finalTargets {
		clientConn := mgr.hub.GetClient(id)
		if clientConn != nil {
			clientConn.Send <- msgBytes
		} else {
			session.Mu.Lock()
			session.Results[id] = PrecheckResult{
				DeviceID: id,
				Success:  false,
				Error:    "client offline",
			}
			session.Mu.Unlock()
		}
	}

	// Wait for reports or timeout
	select {
	case <-session.Done:
	case <-time.After(3 * time.Second):
	}

	session.Mu.Lock()
	defer session.Mu.Unlock()

	// Fill in missing ones as failed (timeout)
	var finalResults []PrecheckResult
	for _, id := range finalTargets {
		res, ok := session.Results[id]
		if !ok {
			res = PrecheckResult{
				DeviceID: id,
				Success:  false,
				Error:    "check timeout (no response)",
			}
		}
		finalResults = append(finalResults, res)
	}

	return finalResults, nil
}

func (mgr *DistributionManager) ResetTask() error {
	mgr.taskMu.Lock()
	defer mgr.taskMu.Unlock()

	if mgr.activeTask != nil && mgr.activeTask.Status == "running" {
		return fmt.Errorf("cannot reset an actively running task")
	}
	mgr.activeTask = nil
	return nil
}
