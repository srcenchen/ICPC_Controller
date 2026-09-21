package service

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/model"
	"github.com/google/uuid"
	"go-silver-core/pkg/gosilver"
)

var DistributionMgr *DistributionManager

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
	Status      string  `json:"status"`
	Error       string  `json:"error,omitempty"`
	UpdatedAt   string  `json:"updated_at"`
	TransferID  string  `json:"transfer_id"`
	Attempts    int     `json:"attempts"`
}

type DistributeTask struct {
	TaskID       string                    `json:"task_id"`
	Files        []string                  `json:"files"`
	SaveDir      string                    `json:"save_dir"`
	ServerIP     string                    `json:"server_ip"`
	PostCmd      string                    `json:"post_cmd"`
	TargetIDs    []int                     `json:"target_ids"`
	ActiveFile   string                    `json:"active_file"`
	ActiveIdx    int                       `json:"active_idx"`
	Status       string                    `json:"status"`
	Hashes       map[string]string         `json:"hashes"`
	Results      map[string]map[int]string `json:"results"`
	Progresses   map[int]*ClientProgress   `json:"progresses"`
	activeServer *gosilver.Server
	done         chan struct{}
	mu           sync.RWMutex
}

func (task *DistributeTask) MarshalJSON() ([]byte, error) {
	task.mu.RLock()
	defer task.mu.RUnlock()
	return json.Marshal(map[string]interface{}{
		"task_id": task.TaskID, "files": task.Files, "save_dir": task.SaveDir,
		"server_ip": task.ServerIP, "post_cmd": task.PostCmd, "target_ids": task.TargetIDs,
		"active_file": task.ActiveFile, "active_idx": task.ActiveIdx, "status": task.Status,
		"hashes": task.Hashes, "results": task.Results, "progresses": task.Progresses,
	})
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
	uploadDir       string
	hub             *biz.Hub
	activeTask      *DistributeTask
	taskMu          sync.Mutex
	persistMu       sync.Mutex
	fileMu          sync.Mutex
	lastPersist     time.Time
	lastPersistTask string
	transferPort    string
	targetLookup    func() []int
	activePrecheck  *PrecheckSession
	hostnameLookup  func(int) string
	fileTimeout     time.Duration
	stallTimeout    time.Duration
}

func NewDistributionManager(hub *biz.Hub, uploadDir string) *DistributionManager {
	_ = os.MkdirAll(uploadDir, 0755)
	mode := os.Getenv("ICPC_P2P_PEER_MODE")
	if mode == "" {
		mode = "all"
	}
	gosilver.SetPeerSelectMode(mode)
	manager := &DistributionManager{uploadDir: uploadDir, hub: hub, fileTimeout: 30 * time.Minute, stallTimeout: 3 * time.Minute, transferPort: "48080"}
	if raw, err := os.ReadFile(manager.statePath()); err == nil {
		var task DistributeTask
		if json.Unmarshal(raw, &task) == nil && task.TaskID != "" {
			if task.Status == "running" {
				task.Status = "failed"
			}
			if task.Results == nil {
				task.Results = make(map[string]map[int]string)
			}
			manager.activeTask = &task
		}
	}
	return manager
}

func (mgr *DistributionManager) statePath() string {
	return filepath.Join(filepath.Dir(mgr.uploadDir), "distribution-task.json")
}
func (mgr *DistributionManager) SetHostnameLookup(lookup func(int) string) {
	mgr.hostnameLookup = lookup
}
func (mgr *DistributionManager) SetTargetLookup(lookup func() []int) { mgr.targetLookup = lookup }
func (mgr *DistributionManager) SetTransferPort(port string)         { mgr.transferPort = port }
func (mgr *DistributionManager) GetActiveTask() *DistributeTask {
	mgr.taskMu.Lock()
	defer mgr.taskMu.Unlock()
	return mgr.activeTask
}
func (mgr *DistributionManager) IsRunning() bool {
	task := mgr.GetActiveTask()
	if task == nil {
		return false
	}
	task.mu.RLock()
	defer task.mu.RUnlock()
	if task.done != nil {
		select {
		case <-task.done:
		default:
			return true
		}
	}
	return task.Status == "running"
}
func (mgr *DistributionManager) publish(task *DistributeTask, event string) {
	mgr.persistMu.Lock()
	raw, err := json.Marshal(task)
	if err == nil && (event == "distribute_task_finished" || task.TaskID != mgr.lastPersistTask || time.Since(mgr.lastPersist) > 2*time.Second) {
		if os.WriteFile(mgr.statePath()+".tmp", raw, 0600) == nil {
			_ = os.Rename(mgr.statePath()+".tmp", mgr.statePath())
		}
		mgr.lastPersist = time.Now()
		mgr.lastPersistTask = task.TaskID
	}
	mgr.persistMu.Unlock()
	mgr.hub.BroadcastAdminEvent(event, task)
}

func (mgr *DistributionManager) GetActiveTaskSnapshot() map[string]interface{} {
	task := mgr.GetActiveTask()
	if task == nil {
		return nil
	}
	task.mu.RLock()
	defer task.mu.RUnlock()
	completed, failed := 0, 0
	var percentage float64
	for _, progress := range task.Progresses {
		percentage += progress.Percentage
		if progress.Status == "completed" {
			completed++
		}
		if progress.Status == "failed" || progress.Status == "stalled" || progress.Status == "cancelled" {
			failed++
		}
	}
	if len(task.Progresses) > 0 {
		percentage /= float64(len(task.Progresses))
	}
	return map[string]interface{}{"task_id": task.TaskID, "status": task.Status, "active_file": task.ActiveFile, "active_idx": task.ActiveIdx, "files": task.Files, "total": len(task.Progresses), "completed": completed, "failed": failed, "avg_pct": percentage}
}

func (mgr *DistributionManager) GetUploadedFiles() ([]FileInfo, error) {
	entries, err := os.ReadDir(mgr.uploadDir)
	if err != nil {
		return nil, err
	}
	files := make([]FileInfo, 0)
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, FileInfo{Name: info.Name(), Size: info.Size(), ModTime: info.ModTime().Format(time.RFC3339)})
	}
	return files, nil
}
func (mgr *DistributionManager) DeleteFile(name string) error {
	mgr.fileMu.Lock()
	defer mgr.fileMu.Unlock()
	if mgr.IsRunning() {
		return fmt.Errorf("分发中不可删除文件")
	}
	return os.Remove(filepath.Join(mgr.uploadDir, filepath.Base(name)))
}
func (mgr *DistributionManager) ClearAllFiles() error {
	files, err := mgr.GetUploadedFiles()
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := mgr.DeleteFile(file.Name); err != nil {
			return err
		}
	}
	return nil
}
func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func (mgr *DistributionManager) StartTask(files []string, saveDir string, targetIDs []int, serverIP, postCmd string) (*DistributeTask, error) {
	mgr.fileMu.Lock()
	defer mgr.fileMu.Unlock()
	mgr.taskMu.Lock()
	defer mgr.taskMu.Unlock()
	if mgr.activeTask != nil {
		mgr.activeTask.mu.RLock()
		busy := mgr.activeTask.Status == "running"
		if mgr.activeTask.done != nil {
			select {
			case <-mgr.activeTask.done:
			default:
				busy = true
			}
		}
		mgr.activeTask.mu.RUnlock()
		if busy {
			return nil, fmt.Errorf("已有分发任务正在运行")
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("请选择文件")
	}
	if len(targetIDs) == 0 {
		if mgr.targetLookup != nil {
			targetIDs = mgr.targetLookup()
		} else {
			targetIDs = mgr.hub.OnlineIDs()
		}
	}
	if len(targetIDs) == 0 {
		return nil, fmt.Errorf("没有可分发的设备")
	}
	if saveDir == "" {
		saveDir = "./downloads"
	}
	task := &DistributeTask{TaskID: uuid.NewString(), SaveDir: saveDir, ServerIP: serverIP, PostCmd: postCmd, Status: "running", Hashes: make(map[string]string), Results: make(map[string]map[int]string), Progresses: make(map[int]*ClientProgress), done: make(chan struct{})}
	for _, name := range files {
		if name != filepath.Base(name) || strings.HasPrefix(name, ".") {
			return nil, fmt.Errorf("非法文件名")
		}
		if _, exists := task.Hashes[name]; exists {
			continue
		}
		info, err := os.Lstat(filepath.Join(mgr.uploadDir, name))
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("文件不存在或不是常规文件：%s", name)
		}
		digest, err := fileSHA256(filepath.Join(mgr.uploadDir, name))
		if err != nil {
			return nil, err
		}
		task.Files = append(task.Files, name)
		task.Hashes[name] = digest
		task.Results[name] = make(map[int]string)
	}
	for _, deviceID := range targetIDs {
		if deviceID <= 0 {
			return nil, fmt.Errorf("非法设备号")
		}
		if _, exists := task.Progresses[deviceID]; exists {
			continue
		}
		hostname := fmt.Sprintf("#%d", deviceID)
		if mgr.hostnameLookup != nil {
			if name := mgr.hostnameLookup(deviceID); name != "" {
				hostname = name
			}
		}
		task.TargetIDs = append(task.TargetIDs, deviceID)
		task.Progresses[deviceID] = &ClientProgress{DeviceID: deviceID, Hostname: hostname, Status: "idle"}
	}
	mgr.activeTask = task
	mgr.publish(task, "distribute_progress_update")
	go task.run(mgr)
	return task, nil
}

func (mgr *DistributionManager) StopTask() error {
	task := mgr.GetActiveTask()
	if task == nil {
		return fmt.Errorf("没有分发任务")
	}
	task.mu.Lock()
	task.Status = "stopped"
	for _, progress := range task.Progresses {
		if progress.Status != "completed" {
			progress.Status = "cancelled"
		}
	}
	task.mu.Unlock()
	msg, _ := json.Marshal(model.DistributeCancelMessage{Type: "distribute_cancel", TaskID: task.TaskID})
	for _, deviceID := range task.TargetIDs {
		mgr.hub.TrySend(deviceID, append(msg, '\n'))
	}
	mgr.publish(task, "distribute_progress_update")
	return nil
}

func (mgr *DistributionManager) HandleProgressReport(msg model.DistributeProgressMessage) {
	task := mgr.GetActiveTask()
	if task == nil || task.TaskID != msg.TaskID {
		return
	}
	task.mu.Lock()
	progress := task.Progresses[msg.DeviceID]
	if task.Status == "running" && progress != nil && msg.TransferID == "" {
		progress.Status = "failed"
		progress.Attempts = 3
		progress.Error = "客户端版本不支持校验回执，请先升级客户端"
		task.mu.Unlock()
		mgr.publish(task, "distribute_progress_update")
		return
	}
	if task.Status != "running" || progress == nil || msg.TransferID != progress.TransferID || progress.Status == "completed" {
		task.mu.Unlock()
		return
	}
	if msg.Status != "downloading" && msg.Status != "completed" && msg.Status != "failed" && msg.Status != "cancelled" && msg.Status != "verifying" && msg.Status != "executing" {
		task.mu.Unlock()
		return
	}
	if msg.Status == "completed" && msg.SHA256 != task.Hashes[task.ActiveFile] {
		msg.Status = "failed"
		msg.Error = "SHA-256 校验回执不匹配"
	}
	if msg.Downloaded > progress.Downloaded || progress.Status != msg.Status {
		progress.UpdatedAt = time.Now().Format(time.RFC3339Nano)
	}
	progress.Downloaded, progress.TotalChunks, progress.Percentage = msg.Downloaded, msg.TotalChunks, msg.Percentage
	progress.SpeedMbps, progress.Status, progress.Error = msg.SpeedMbps, msg.Status, msg.Error
	if msg.Status == "completed" {
		task.Results[task.ActiveFile][msg.DeviceID] = "completed"
	}
	task.mu.Unlock()
	mgr.publish(task, "distribute_progress_update")
}

func (task *DistributeTask) sendLocked(mgr *DistributionManager, progress *ClientProgress) {
	progress.Attempts++
	progress.TransferID = uuid.NewString()
	progress.Status, progress.Error = "downloading", ""
	progress.UpdatedAt = time.Now().Format(time.RFC3339Nano)
	serverIP := task.ServerIP
	if serverIP == "" {
		serverIP = getOutboundIP()
	}
	postCmd := ""
	if task.ActiveIdx == len(task.Files)-1 {
		postCmd = task.PostCmd
	}
	msg, _ := json.Marshal(model.DistributeStartMessage{Type: "distribute_start", TaskID: task.TaskID, TransferID: progress.TransferID, SHA256: task.Hashes[task.ActiveFile], FileName: task.ActiveFile, SenderAddr: net.JoinHostPort(serverIP, mgr.transferPort), SaveDir: task.SaveDir, PostCmd: postCmd})
	if !mgr.hub.TrySend(progress.DeviceID, append(msg, '\n')) {
		progress.Status = "failed"
		progress.Error = "设备离线或发送队列已满"
	}
}

func (task *DistributeTask) run(mgr *DistributionManager) {
	defer func() {
		task.mu.Lock()
		if task.activeServer != nil {
			task.activeServer.Stop()
			task.activeServer = nil
		}
		if task.Status == "running" {
			task.Status = "completed"
		}
		task.mu.Unlock()
		mgr.publish(task, "distribute_task_finished")
		close(task.done)
	}()
	for index, name := range task.Files {
		task.mu.Lock()
		if task.Status != "running" {
			task.mu.Unlock()
			return
		}
		task.ActiveFile, task.ActiveIdx = name, index
		server := gosilver.NewServer(":"+mgr.transferPort, filepath.Join(mgr.uploadDir, name))
		task.activeServer = server
		task.mu.Unlock()
		if err := server.Start(); err != nil {
			task.mu.Lock()
			task.Status = "failed"
			for _, progress := range task.Progresses {
				progress.Status = "failed"
				progress.Error = err.Error()
			}
			task.mu.Unlock()
			return
		}
		task.mu.Lock()
		if task.Status != "running" {
			task.mu.Unlock()
			return
		}
		for _, progress := range task.Progresses {
			progress.Downloaded, progress.Percentage, progress.Attempts = 0, 0, 0
			if task.Results[name][progress.DeviceID] == "completed" {
				progress.Status = "completed"
				progress.Percentage = 100
				continue
			}
			task.sendLocked(mgr, progress)
		}
		task.mu.Unlock()
		mgr.publish(task, "distribute_progress_update")
		deadline := time.Now().Add(mgr.fileTimeout)
		for {
			time.Sleep(time.Second)
			task.mu.Lock()
			if task.Status != "running" {
				task.mu.Unlock()
				return
			}
			complete, exhausted := true, true
			for _, progress := range task.Progresses {
				if progress.Status == "completed" {
					continue
				}
				complete = false
				updated, _ := time.Parse(time.RFC3339Nano, progress.UpdatedAt)
				if time.Since(updated) > mgr.stallTimeout {
					progress.Status = "stalled"
					progress.Error = "长时间无有效进度；请确认客户端已升级"
				}
				if progress.Attempts < 3 && time.Since(updated) >= 10*time.Second && (progress.Status == "failed" || progress.Status == "stalled" || progress.Status == "cancelled") {
					task.sendLocked(mgr, progress)
				}
				if progress.Attempts < 3 || progress.Status == "downloading" || progress.Status == "verifying" || progress.Status == "executing" {
					exhausted = false
				}
			}
			if !complete && (exhausted || time.Now().After(deadline)) {
				task.Status = "failed"
				for _, progress := range task.Progresses {
					if progress.Status != "completed" {
						progress.Status = "failed"
						if progress.Error == "" {
							progress.Error = "分发超时，未确认送达"
						}
					}
				}
				task.mu.Unlock()
				return
			}
			task.mu.Unlock()
			if complete {
				break
			}
		}
		server.Stop()
		task.mu.Lock()
		task.activeServer = nil
		task.mu.Unlock()
	}
}

func (mgr *DistributionManager) RetryDevice(deviceID int) error {
	task := mgr.GetActiveTask()
	if task == nil {
		return fmt.Errorf("没有分发任务")
	}
	task.mu.Lock()
	if deviceID != 0 && task.Progresses[deviceID] == nil {
		task.mu.Unlock()
		return fmt.Errorf("设备不属于此任务")
	}
	if task.Status == "running" {
		progress := task.Progresses[deviceID]
		if progress == nil || progress.Status == "completed" {
			task.mu.Unlock()
			return fmt.Errorf("设备不在待重试列表")
		}
		task.sendLocked(mgr, progress)
		task.mu.Unlock()
		return nil
	}
	if task.done != nil {
		select {
		case <-task.done:
		default:
			task.mu.Unlock()
			return fmt.Errorf("上一个任务正在停止，请稍后重试")
		}
	}
	for _, name := range task.Files {
		digest, err := fileSHA256(filepath.Join(mgr.uploadDir, name))
		if err != nil || digest != task.Hashes[name] {
			task.mu.Unlock()
			return fmt.Errorf("源文件已变更，请新建分发任务")
		}
	}
	task.Status = "running"
	task.done = make(chan struct{})
	task.mu.Unlock()
	go task.run(mgr)
	return nil
}

func (mgr *DistributionManager) ResetTask() error {
	if mgr.IsRunning() {
		return fmt.Errorf("请先停止正在运行的分发")
	}
	mgr.taskMu.Lock()
	mgr.activeTask = nil
	mgr.taskMu.Unlock()
	_ = os.Remove(mgr.statePath())
	return nil
}

func getOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err == nil {
		defer conn.Close()
		return conn.LocalAddr().(*net.UDPAddr).IP.String()
	}
	addresses, _ := net.InterfaceAddrs()
	for _, address := range addresses {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP.String()
		}
	}
	return "127.0.0.1"
}

func (mgr *DistributionManager) HandlePrecheckReport(deviceID int, success bool, message string) {
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
	session.Results[deviceID] = PrecheckResult{DeviceID: deviceID, Success: success, Error: message}
	if len(session.Results) == len(session.TargetIDs) {
		select {
		case session.Done <- struct{}{}:
		default:
		}
	}
}

func (mgr *DistributionManager) RunPrecheck(serverIP string, targetIDs []int) ([]PrecheckResult, error) {
	if len(targetIDs) == 0 {
		targetIDs = mgr.hub.OnlineIDs()
	}
	if len(targetIDs) == 0 {
		return nil, fmt.Errorf("没有目标设备")
	}
	if serverIP == "" {
		serverIP = getOutboundIP()
	}
	session := &PrecheckSession{TargetIDs: make(map[int]bool), Results: make(map[int]PrecheckResult), Done: make(chan struct{}, 1)}
	for _, deviceID := range targetIDs {
		session.TargetIDs[deviceID] = true
	}
	mgr.taskMu.Lock()
	if mgr.activePrecheck != nil {
		mgr.taskMu.Unlock()
		return nil, fmt.Errorf("连通性检查正在运行")
	}
	mgr.activePrecheck = session
	mgr.taskMu.Unlock()
	defer func() { mgr.taskMu.Lock(); mgr.activePrecheck = nil; mgr.taskMu.Unlock() }()
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, err
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	message, _ := json.Marshal(map[string]interface{}{"type": "distribute_precheck", "server_ip": serverIP, "port": listener.Addr().(*net.TCPAddr).Port})
	for _, deviceID := range targetIDs {
		if !mgr.hub.TrySend(deviceID, append(message, '\n')) {
			mgr.HandlePrecheckReport(deviceID, false, "设备离线或繁忙")
		}
	}
	select {
	case <-session.Done:
	case <-time.After(5 * time.Second):
	}
	session.Mu.Lock()
	defer session.Mu.Unlock()
	results := make([]PrecheckResult, 0, len(session.TargetIDs))
	for deviceID := range session.TargetIDs {
		result, ok := session.Results[deviceID]
		if !ok {
			result = PrecheckResult{DeviceID: deviceID, Error: "未收到连通性回执"}
		}
		results = append(results, result)
	}
	return results, nil
}
