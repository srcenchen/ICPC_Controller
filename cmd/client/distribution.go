package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"ICPCRemoteControl/internal/model"
	"go-silver-core/pkg/gosilver"
)

func startVerifiedDistribution(sender *safeSender, message *model.DistributeStartMessage) {
	state.mu.Lock()
	deviceID := state.assignedID
	state.mu.Unlock()
	report := func(status, reason string, progress gosilver.ProgressInfo) {
		sendJSON(sender, model.DistributeProgressMessage{Type: "distribute_progress", TaskID: message.TaskID, TransferID: message.TransferID, SHA256: message.SHA256, DeviceID: deviceID, Status: status, Error: reason, Downloaded: progress.Downloaded, TotalChunks: progress.TotalChunks, Percentage: progress.Percentage, SpeedMbps: progress.SpeedMbps})
	}
	if filepath.Base(message.FileName) != message.FileName || len(message.SHA256) != 64 {
		report("failed", "无效的文件清单", gosilver.ProgressInfo{})
		return
	}
	if err := os.MkdirAll(message.SaveDir, 0755); err != nil {
		report("failed", err.Error(), gosilver.ProgressInfo{})
		return
	}
	activeDistMu.Lock()
	if activeDistCancel != nil {
		activeDistCancel()
	}
	if activeDistClient != nil {
		activeDistClient.CancelDownload()
	}
	ctx, cancel := context.WithCancel(context.Background())
	activeDistCancel, activeDistTask = cancel, message.TaskID
	client := gosilver.NewClient(message.SenderAddr, message.SaveDir)
	client.SetExpectedFile(message.FileName, message.SHA256)
	activeDistClient = client
	progresses, err := client.StartDownload()
	activeDistMu.Unlock()
	defer cancel()
	if err != nil {
		report("failed", err.Error(), gosilver.ProgressInfo{})
		return
	}
	var last gosilver.ProgressInfo
	var reportedAt time.Time
	for progress := range progresses {
		last = progress
		if progress.Status == "completed" {
			continue
		}
		if progress.Status == "downloading" && time.Since(reportedAt) < 500*time.Millisecond {
			continue
		}
		reportedAt = time.Now()
		reason := ""
		if progress.Error != nil {
			reason = progress.Error.Error()
		}
		report(progress.Status, reason, progress)
	}
	if last.Status != "completed" || ctx.Err() != nil {
		return
	}
	if message.PostCmd != "" {
		receiptDir := "/var/lib/icpc-client/receipts"
		if err := os.MkdirAll(receiptDir, 0700); err != nil {
			report("failed", err.Error(), last)
			return
		}
		receipt := filepath.Join(receiptDir, fmt.Sprintf("%x", sha256.Sum256([]byte(message.TaskID+"|"+message.FileName+"|"+message.SHA256))))
		previous, _ := os.ReadFile(receipt)
		if string(previous) != "completed" {
			if len(previous) > 0 {
				report("failed", "后置命令曾执行但未成功确认；为避免重复执行，请检查后新建任务", last)
				return
			}
			if err := storeDistributionReceipt(receipt, "executing"); err != nil {
				report("failed", err.Error(), last)
				return
			}
			report("executing", "正在执行后置命令", last)
			commandCtx, commandCancel := context.WithTimeout(ctx, 2*time.Minute)
			command := exec.CommandContext(commandCtx, "sh", "-c", message.PostCmd)
			command.Dir = message.SaveDir
			command.Stdout, command.Stderr = io.Discard, io.Discard
			err := command.Run()
			commandCancel()
			if err != nil {
				report("failed", "后置命令失败："+err.Error(), last)
				return
			}
			if err := storeDistributionReceipt(receipt, "completed"); err != nil {
				report("failed", err.Error(), last)
				return
			}
		}
	}
	if ctx.Err() == nil {
		report("completed", "", last)
	}
}

func storeDistributionReceipt(path, status string) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".receipt-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.WriteString(status); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func cancelVerifiedDistribution(taskID string) {
	activeDistMu.Lock()
	defer activeDistMu.Unlock()
	if strings.TrimSpace(taskID) == "" || activeDistTask == taskID {
		if activeDistCancel != nil {
			activeDistCancel()
		}
		if activeDistClient != nil {
			activeDistClient.CancelDownload()
			activeDistClient = nil
		}
	}
}
