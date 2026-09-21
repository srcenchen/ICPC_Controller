package data

import (
	"path/filepath"
	"testing"
)

func TestSnapshotRepoLifecycle(test *testing.T) {
	database, err := NewDB(filepath.Join(test.TempDir(), "snapshot.db"))
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	repo := NewSnapshotRepo(database)

	first, err := repo.StartSnapshot("校赛A", "local", "room-1")
	if err != nil || first.ID == 0 {
		test.Fatalf("start: %+v %v", first, err)
	}
	second, err := repo.StartSnapshot("校赛B", "local", "room-1")
	if err != nil {
		test.Fatal(err)
	}
	// Starting a new snapshot must end the previous one.
	reloaded, _ := repo.GetSnapshot(first.ID)
	if reloaded == nil || reloaded.Active {
		test.Fatalf("previous snapshot still active: %+v", reloaded)
	}
	active, err := repo.ActiveSnapshot("local")
	if err != nil || active == nil || active.ID != second.ID {
		test.Fatalf("active snapshot: %+v %v", active, err)
	}
	if _, err := repo.ActiveSnapshot("cloud"); err != nil {
		test.Fatal(err)
	}

	opID, err := repo.AddOp(second.ID, "command", `{"kind":"command","command":"uptime"}`, "执行命令：uptime")
	if err != nil {
		test.Fatal(err)
	}
	if delivered, _ := repo.IsDelivered(opID, "device", "3"); delivered {
		test.Fatal("unexpected delivery")
	}
	if err := repo.MarkDelivered(opID, "device", "3"); err != nil {
		test.Fatal(err)
	}
	if err := repo.MarkDelivered(opID, "device", "3"); err != nil {
		test.Fatal("MarkDelivered must be idempotent:", err)
	}
	if delivered, _ := repo.IsDelivered(opID, "device", "3"); !delivered {
		test.Fatal("delivery not recorded")
	}
	ops, err := repo.ListOps(second.ID)
	if err != nil || len(ops) != 1 || ops[0].Delivered != 1 {
		test.Fatalf("list ops: %+v %v", ops, err)
	}

	if err := repo.DeleteOp(second.ID, opID); err != nil {
		test.Fatal(err)
	}
	ops, _ = repo.ListOps(second.ID)
	if len(ops) != 0 {
		test.Fatalf("op not deleted: %+v", ops)
	}
	if err := repo.DeleteSnapshot(second.ID); err != nil {
		test.Fatal(err)
	}
	if active, _ := repo.ActiveSnapshot("local"); active != nil {
		test.Fatalf("snapshot not deleted: %+v", active)
	}
}
