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

func TestSnapshotRepoRemoteSyncInsertAndReorder(test *testing.T) {
	database, err := NewDB(filepath.Join(test.TempDir(), "snapshot-merge.db"))
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	repo := NewSnapshotRepo(database)

	// 1. Sync initial remote snapshot from Cloud: [R10, R20].
	localSnapID, err := repo.SyncRemoteSnapshot("room-a", &RemoteSnapshotState{
		ID:   7,
		Name: "2026 省赛",
		Ops: []RemoteSnapshotOp{
			{ID: 10, Kind: "command", Payload: `{"kind":"command","command":"r1"}`, Summary: "R1"},
			{ID: 20, Kind: "command", Payload: `{"kind":"command","command":"r2"}`, Summary: "R2"},
		},
	})
	if err != nil || localSnapID == 0 {
		test.Fatalf("initial sync: %d %v", localSnapID, err)
	}
	ops, err := repo.ListOps(localSnapID)
	if err != nil || len(ops) != 2 {
		test.Fatalf("ops after sync: %+v %v", ops, err)
	}

	// 2. Room inserts L_head at beginning (after=0) and L_mid after R10.
	zero := int64(0)
	headID, err := repo.InsertOp(localSnapID, "command", `{"kind":"command","command":"l_head"}`, "L_head", &zero)
	if err != nil {
		test.Fatal(err)
	}
	r10LocalID := ops[0].ID
	midID, err := repo.InsertOp(localSnapID, "command", `{"kind":"command","command":"l_mid"}`, "L_mid", &r10LocalID)
	if err != nil {
		test.Fatal(err)
	}
	// Mark device 1 delivered on R10 and L_mid to verify deliveries survive syncs.
	_ = repo.MarkDelivered(r10LocalID, "device", "1")
	_ = repo.MarkDelivered(midID, "device", "1")

	assertSummaries(test, repo, localSnapID, []string{"L_head", "R1", "L_mid", "R2"})

	// 3. Cloud appends R30 and swaps R10/R20 -> [R20, R10, R30].
	// Room's L_head stays at front, L_mid stays anchored right after R10!
	if _, err := repo.SyncRemoteSnapshot("room-a", &RemoteSnapshotState{
		ID:   7,
		Name: "2026 省赛",
		Ops: []RemoteSnapshotOp{
			{ID: 20, Kind: "command", Payload: `{"kind":"command","command":"r2"}`, Summary: "R2"},
			{ID: 10, Kind: "command", Payload: `{"kind":"command","command":"r1"}`, Summary: "R1"},
			{ID: 30, Kind: "command", Payload: `{"kind":"command","command":"r3"}`, Summary: "R3"},
		},
	}); err != nil {
		test.Fatal(err)
	}
	assertSummaries(test, repo, localSnapID, []string{"L_head", "R2", "R1", "L_mid", "R3"})
	if ok, _ := repo.IsDelivered(r10LocalID, "device", "1"); !ok {
		test.Fatal("delivery on R1 lost across remote sync")
	}
	if ok, _ := repo.IsDelivered(midID, "device", "1"); !ok {
		test.Fatal("delivery on L_mid lost across remote sync")
	}

	// 4. Room reorders: swapping two remote ops must be rejected when locked,
	// whereas moving L_mid after R3 is allowed and updates its anchor to R30.
	ops, _ = repo.ListOps(localSnapID)
	if err := repo.ReorderOps(localSnapID, []int64{ops[0].ID, ops[2].ID, ops[1].ID, ops[3].ID, ops[4].ID}, true); err == nil {
		test.Fatal("expected swapping remote ops in room mode to be rejected")
	}
	// Move L_mid (index 3) to the end (after R3, index 4).
	if err := repo.ReorderOps(localSnapID, []int64{ops[0].ID, ops[1].ID, ops[2].ID, ops[4].ID, ops[3].ID}, true); err != nil {
		test.Fatalf("valid room reorder failed: %v", err)
	}
	assertSummaries(test, repo, localSnapID, []string{"L_head", "R2", "R1", "R3", "L_mid"})

	// 5. Room skips R2 locally; subsequent Cloud sync must NOT resurrect R2,
	// and RestoreSkippedRemoteOps brings R2 back in its slot.
	r2LocalID := ops[1].ID
	if err := repo.DeleteOp(localSnapID, r2LocalID); err != nil {
		test.Fatal(err)
	}
	assertSummaries(test, repo, localSnapID, []string{"L_head", "R1", "R3", "L_mid"})
	if _, err := repo.SyncRemoteSnapshot("room-a", &RemoteSnapshotState{
		ID:   7,
		Name: "2026 省赛",
		Ops: []RemoteSnapshotOp{
			{ID: 20, Kind: "command", Payload: `{"kind":"command","command":"r2"}`, Summary: "R2"},
			{ID: 10, Kind: "command", Payload: `{"kind":"command","command":"r1"}`, Summary: "R1"},
			{ID: 30, Kind: "command", Payload: `{"kind":"command","command":"r3"}`, Summary: "R3"},
		},
	}); err != nil {
		test.Fatal(err)
	}
	assertSummaries(test, repo, localSnapID, []string{"L_head", "R1", "R3", "L_mid"})
	snap, _ := repo.GetSnapshot(localSnapID)
	if snap == nil || snap.SkippedRemoteCount != 1 {
		test.Fatalf("expected SkippedRemoteCount=1, got %+v", snap)
	}
	if err := repo.RestoreSkippedRemoteOps(localSnapID); err != nil {
		test.Fatal(err)
	}
	assertSummaries(test, repo, localSnapID, []string{"L_head", "R2", "R1", "R3", "L_mid"})

	// 6. Cloud deletes R30 -> L_mid re-anchors cleanly after R10, and deleting L_head removes it.
	if _, err := repo.SyncRemoteSnapshot("room-a", &RemoteSnapshotState{
		ID:   7,
		Name: "2026 省赛",
		Ops: []RemoteSnapshotOp{
			{ID: 20, Kind: "command", Payload: `{"kind":"command","command":"r2"}`, Summary: "R2"},
			{ID: 10, Kind: "command", Payload: `{"kind":"command","command":"r1"}`, Summary: "R1"},
		},
	}); err != nil {
		test.Fatal(err)
	}
	if err := repo.DeleteOp(localSnapID, headID); err != nil {
		test.Fatal(err)
	}
	assertSummaries(test, repo, localSnapID, []string{"R2", "R1", "L_mid"})

	// 7. Cloud ends active snapshot -> room's remote-backed snapshot ends too.
	if _, err := repo.SyncRemoteSnapshot("room-a", nil); err != nil {
		test.Fatal(err)
	}
	if active, _ := repo.ActiveSnapshot("local"); active != nil {
		test.Fatalf("expected remote-backed snapshot to end, got %+v", active)
	}
}

func assertSummaries(test *testing.T, repo *SnapshotRepo, snapID int64, want []string) {
	test.Helper()
	ops, err := repo.ListOps(snapID)
	if err != nil {
		test.Fatal(err)
	}
	if len(ops) != len(want) {
		test.Fatalf("len(ops)=%d (%+v), want %v", len(ops), ops, want)
	}
	for i, w := range want {
		if ops[i].Summary != w {
			test.Fatalf("ops[%d].Summary=%q, want %q (all=%+v)", i, ops[i].Summary, w, ops)
		}
	}
}
