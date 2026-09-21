package data

import (
	"path/filepath"
	"testing"

	"ICPCRemoteControl/internal/model"
)

func TestBroadcastSnapshotAtomicAndRoomLocalConfig(test *testing.T) {
	database, err := NewDB(filepath.Join(test.TempDir(), "broadcast.db"))
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	repo := NewBroadcastRepo(database)
	if err := repo.SetConfig("base_url", "http://room.local:8080"); err != nil {
		test.Fatal(err)
	}
	if err := repo.SetConfig("pushed_state", "after"); err != nil {
		test.Fatal(err)
	}
	snapshot := &BroadcastSnapshot{Source: "cloud-one", Revision: 1, Config: map[string]string{"base_url": "https://cloud.invalid", "active_font": "test.woff2", "countdown_target": "2026-09-30T10:00:00+08:00"}, Fonts: []model.BroadcastFont{{ID: 1, Name: "font", Filename: "test.woff2", Format: "woff2"}}, Pages: []model.BroadcastPage{{ID: 10, Mode: "before", Title: "synced", Items: []model.BroadcastItem{{ID: 20, ItemType: "text", Content: "hello"}}}}}
	applied, err := repo.ApplySnapshot(snapshot, "sync", "before")
	if err != nil || !applied {
		test.Fatalf("sync: %v %v", applied, err)
	}
	baseURL, _ := repo.GetConfig("base_url")
	state, _ := repo.GetConfig("pushed_state")
	if baseURL != "http://room.local:8080" || state != "after" {
		test.Fatalf("overwrote room-local config: %q %q", baseURL, state)
	}
	exported, err := repo.Snapshot("another-cloud")
	if err != nil || len(exported.Pages) != 1 || len(exported.Pages[0].Items) != 1 || exported.Pages[0].Items[0].PageID != 10 || len(exported.Fonts) != 1 || exported.Config["base_url"] != "" {
		test.Fatalf("incomplete export: %+v %v", exported, err)
	}
	snapshot.Revision = 2
	snapshot.Pages[0].Title = "broken"
	snapshot.Pages[0].Items = append(snapshot.Pages[0].Items, snapshot.Pages[0].Items[0])
	if _, err := repo.ApplySnapshot(snapshot, "start", "before"); err == nil {
		test.Fatal("accepted duplicate primary key")
	}
	pages, _ := repo.GetPagesWithItems("before")
	if len(pages) != 1 || pages[0].Title != "synced" {
		test.Fatal("failed import partially changed live broadcast")
	}
	snapshot.Pages[0].Items = snapshot.Pages[0].Items[:1]
	snapshot.Pages[0].Title = "newest"
	if applied, err := repo.ApplySnapshot(snapshot, "start", "before"); err != nil || !applied {
		test.Fatalf("failed transaction consumed revision: %v %v", applied, err)
	}
	snapshot.Revision = 1
	if applied, err := repo.ApplySnapshot(snapshot, "stop", "before"); err != nil || applied {
		test.Fatalf("stale publication changed current state: %v %v", applied, err)
	}
	state, _ = repo.GetConfig("pushed_state")
	if state != "before" {
		test.Fatalf("unexpected broadcast state: %s", state)
	}
}
