package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/model"
)

func testDistributionManager(test *testing.T) *DistributionManager {
	test.Helper()
	root := test.TempDir()
	db, err := data.NewDB(filepath.Join(root, "test.db"))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { db.Close() })
	return NewDistributionManager(biz.NewHub(data.NewDeviceRepo(db)), filepath.Join(root, "uploads"))
}

func TestProgressRequiresGenerationAndVerifiedReceipt(test *testing.T) {
	mgr := testDistributionManager(test)
	task := &DistributeTask{TaskID: "task", Status: "running", ActiveFile: "second", Hashes: map[string]string{"second": "hash"}, Results: map[string]map[int]string{"second": {}}, Progresses: map[int]*ClientProgress{1: {DeviceID: 1, Status: "downloading", TransferID: "current"}}}
	mgr.activeTask = task
	mgr.HandleProgressReport(model.DistributeProgressMessage{TaskID: "task", DeviceID: 1, TransferID: "previous", Status: "completed", SHA256: "hash"})
	if task.Progresses[1].Status != "downloading" {
		test.Fatal("old file receipt applied to next file")
	}
	mgr.HandleProgressReport(model.DistributeProgressMessage{TaskID: "task", DeviceID: 1, TransferID: "current", Status: "completed", SHA256: "wrong"})
	if task.Progresses[1].Status != "failed" {
		test.Fatal("invalid hash counted as complete")
	}
	mgr.HandleProgressReport(model.DistributeProgressMessage{TaskID: "task", DeviceID: 1, TransferID: "current", Status: "completed", SHA256: "hash"})
	mgr.HandleProgressReport(model.DistributeProgressMessage{TaskID: "task", DeviceID: 1, TransferID: "current", Status: "downloading"})
	if task.Results["second"][1] != "completed" || task.Progresses[1].Status != "completed" {
		test.Fatal("verified completion regressed")
	}
}

func TestOfflineTargetFailsAndSurvivesRestart(test *testing.T) {
	mgr := testDistributionManager(test)
	mgr.fileTimeout = 50 * time.Millisecond
	if err := os.WriteFile(filepath.Join(mgr.uploadDir, "file.txt"), []byte("content"), 0600); err != nil {
		test.Fatal(err)
	}
	task, err := mgr.StartTask([]string{"file.txt"}, "/tmp", []int{1, 1, 2}, "127.0.0.1", "")
	if err != nil {
		test.Fatal(err)
	}
	select {
	case <-task.done:
	case <-time.After(5 * time.Second):
		test.Fatal("distribution did not terminate")
	}
	if task.Status != "failed" || len(task.Progresses) != 2 {
		test.Fatalf("silent success or duplicate targets: %s", task.Status)
	}
	encoded, err := json.Marshal(task)
	if err != nil || !json.Valid(encoded) {
		test.Fatal(err)
	}
	restored := NewDistributionManager(mgr.hub, mgr.uploadDir).GetActiveTask()
	if restored == nil || restored.Status != "failed" || len(restored.TargetIDs) != 2 {
		test.Fatal("missing target ledger after restart")
	}
}
