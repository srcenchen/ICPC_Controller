package biz

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"ICPCRemoteControl/internal/data"
)

func TestIdentityDoesNotTrustCopiedNumericID(test *testing.T) {
	db, err := data.NewDB(filepath.Join(test.TempDir(), "test.db"))
	if err != nil {
		test.Fatal(err)
	}
	defer db.Close()
	repo := data.NewDeviceRepo(db)
	assigner := NewIDAssigner(repo)
	first, _, _, err := assigner.AssignIdentity("02:00:00:00:00:01", "hardware-a", nil)
	if err != nil {
		test.Fatal(err)
	}
	second, _, _, err := assigner.AssignIdentity("02:00:00:00:00:02", "hardware-b", &first)
	if err != nil {
		test.Fatal(err)
	}
	if first == second {
		test.Fatal("different machines reused the copied ID")
	}
	reconnected, _, created, err := assigner.AssignIdentity("02:00:00:00:00:01", "hardware-a", nil)
	if err != nil || created || reconnected != first {
		test.Fatalf("identity not reserved before system_info: %d %v", reconnected, err)
	}
	clonedMAC, _, _, err := assigner.AssignIdentity("02:00:00:00:00:01", "hardware-c", &first)
	if err != nil || clonedMAC == first {
		test.Fatalf("distinct hardware sharing MAC merged: %v", err)
	}
	if _, _, _, err := assigner.AssignIdentity("", "", &first); err == nil {
		test.Fatal("identity-less registration accepted")
	}
}

func TestConcurrentIdentityAllocation(test *testing.T) {
	db, err := data.NewDB(filepath.Join(test.TempDir(), "test.db"))
	if err != nil {
		test.Fatal(err)
	}
	defer db.Close()
	assigner := NewIDAssigner(data.NewDeviceRepo(db))
	var workers sync.WaitGroup
	identities := make(chan int, 50)
	for index := 0; index < 50; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			id, _, _, err := assigner.AssignIdentity(fmt.Sprintf("02:00:00:00:00:%02x", index+1), fmt.Sprintf("machine-%d", index), nil)
			if err != nil {
				test.Error(err)
				return
			}
			identities <- id
		}(index)
	}
	workers.Wait()
	close(identities)
	seen := make(map[int]bool)
	for id := range identities {
		if seen[id] {
			test.Fatalf("duplicate ID %d", id)
		}
		seen[id] = true
	}
	if len(seen) != 50 {
		test.Fatalf("allocated %d IDs", len(seen))
	}
}

func TestLegacyIdentityMigrationAndRoomStart(test *testing.T) {
	db, err := data.NewDB(filepath.Join(test.TempDir(), "test.db"))
	if err != nil {
		test.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO devices(assigned_id,mac_address) VALUES(8,'02:00:00:00:00:08')`)
	if err != nil {
		test.Fatal(err)
	}
	_, _ = db.Exec(`INSERT INTO settings(key,value) VALUES('device_id_start','101')`)
	assigner := NewIDAssigner(data.NewDeviceRepo(db))
	id, _, created, err := assigner.AssignIdentity("02:00:00:00:00:08", "new-hardware-key", nil)
	if err != nil || created || id != 8 {
		test.Fatalf("legacy migration: %d %v", id, err)
	}
	id, _, _, err = assigner.AssignIdentity("02:00:00:00:00:09", "other-key", nil)
	if err != nil || id != 101 {
		test.Fatalf("local numbering start: %d %v", id, err)
	}
}
