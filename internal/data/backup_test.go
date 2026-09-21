package data

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupRestoreRoundTrip(test *testing.T) {
	root := test.TempDir()
	dbPath := filepath.Join(root, "icpc.db")
	dataDir := filepath.Join(root, "data")
	db, err := NewDB(dbPath)
	if err != nil {
		test.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO devices(assigned_id,hostname) VALUES(7,'original')`)
	if err != nil {
		test.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "broadcast", "images"), 0755); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "broadcast", "images", "test.png"), []byte("image-bytes"), 0600); err != nil {
		test.Fatal(err)
	}
	var archive bytes.Buffer
	if err := WriteBackup(db, dataDir, true, &archive); err != nil {
		test.Fatal(err)
	}
	_, _ = db.Exec(`UPDATE devices SET hostname='changed'`)
	db.Close()
	archivePath := filepath.Join(root, "backup.zip")
	if err := os.WriteFile(archivePath, archive.Bytes(), 0600); err != nil {
		test.Fatal(err)
	}
	if err := RestoreBackup(archivePath, dbPath, dataDir); err != nil {
		test.Fatal(err)
	}
	db, err = NewDB(dbPath)
	if err != nil {
		test.Fatal(err)
	}
	defer db.Close()
	var hostname string
	if err := db.QueryRow(`SELECT hostname FROM devices WHERE assigned_id=7`).Scan(&hostname); err != nil {
		test.Fatal(err)
	}
	if hostname != "original" {
		test.Fatal("database snapshot was not restored")
	}
	image, err := os.ReadFile(filepath.Join(dataDir, "broadcast", "images", "test.png"))
	if err != nil || string(image) != "image-bytes" {
		test.Fatalf("asset restore: %v", err)
	}
	previous, _ := filepath.Glob(dbPath + ".pre-restore-*")
	if len(previous) != 1 {
		test.Fatal("previous database not retained")
	}
}

func TestRestoreRejectsTraversalAndTampering(test *testing.T) {
	for _, name := range []string{"data/../../escape", "icpc.db"} {
		test.Run(name, func(test *testing.T) {
			root := test.TempDir()
			path := filepath.Join(root, "backup.zip")
			file, err := os.Create(path)
			if err != nil {
				test.Fatal(err)
			}
			archive := zip.NewWriter(file)
			entry, _ := archive.Create(name)
			_, _ = entry.Write([]byte("tampered"))
			manifest, _ := archive.Create("manifest.json")
			_ = json.NewEncoder(manifest).Encode(BackupManifest{Version: 1, Files: map[string]string{"icpc.db": "invalid", name: "invalid"}})
			archive.Close()
			file.Close()
			if err := RestoreBackup(path, filepath.Join(root, "db"), filepath.Join(root, "data")); err == nil {
				test.Fatal("invalid archive accepted")
			}
		})
	}
}

func TestDuplicateBroadcastPageIsIndependent(test *testing.T) {
	db, err := NewDB(filepath.Join(test.TempDir(), "test.db"))
	if err != nil {
		test.Fatal(err)
	}
	defer db.Close()
	_, _ = db.Exec(`INSERT INTO broadcast_pages(id,mode,title,sort_order) VALUES(1,'before','original',7)`)
	_, _ = db.Exec(`INSERT INTO broadcast_items(page_id,item_type,content) VALUES(1,'text','hello')`)
	repo := NewBroadcastRepo(db)
	id, err := repo.DuplicatePage(1)
	if err != nil {
		test.Fatal(err)
	}
	_, _ = db.Exec(`UPDATE broadcast_items SET content='changed' WHERE page_id=?`, id)
	items, err := repo.ListItems(1)
	if err != nil || len(items) != 1 || items[0].Content != "hello" {
		test.Fatal("copy changed original")
	}
}
