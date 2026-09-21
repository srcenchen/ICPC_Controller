package data

import (
	"archive/zip"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type BackupManifest struct {
	Version         int               `json:"version"`
	CreatedAt       string            `json:"created_at"`
	IncludesUploads bool              `json:"includes_uploads"`
	Files           map[string]string `json:"files"`
}

func WriteBackup(db *sql.DB, dataDir string, uploads bool, destination io.Writer) error {
	temporary, err := os.MkdirTemp("", "icpc-backup-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	snapshot := filepath.Join(temporary, "icpc.db")
	if _, err := db.Exec(`VACUUM INTO ?`, snapshot); err != nil {
		return fmt.Errorf("database snapshot: %w", err)
	}
	archive := zip.NewWriter(destination)
	manifest := BackupManifest{Version: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339), IncludesUploads: uploads, Files: make(map[string]string)}
	add := func(name, path string) error {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		entry, err := archive.Create(name)
		if err != nil {
			return err
		}
		hash := sha256.New()
		if _, err := io.Copy(io.MultiWriter(entry, hash), file); err != nil {
			return err
		}
		manifest.Files[name] = hex.EncodeToString(hash.Sum(nil))
		return nil
	}
	if err := add("icpc.db", snapshot); err != nil {
		return err
	}
	err = filepath.WalkDir(dataDir, func(path string, entry os.DirEntry, walkErr error) error {
		if os.IsNotExist(walkErr) && path == dataDir {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(dataDir, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if (!uploads && relative == "uploads") || (relative != "." && strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".") || strings.HasSuffix(entry.Name(), ".tmp") {
			return nil
		}
		return add("data/"+filepath.ToSlash(relative), path)
	})
	if err != nil {
		return err
	}
	entry, err := archive.Create("manifest.json")
	if err != nil {
		return err
	}
	if err := json.NewEncoder(entry).Encode(manifest); err != nil {
		return err
	}
	return archive.Close()
}

func RestoreBackup(archivePath, dbPath, dataDir string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	var manifest BackupManifest
	for _, entry := range archive.File {
		if entry.Name == "manifest.json" {
			reader, err := entry.Open()
			if err != nil {
				return err
			}
			err = json.NewDecoder(io.LimitReader(reader, 8<<20)).Decode(&manifest)
			reader.Close()
			if err != nil {
				return err
			}
		}
	}
	if manifest.Version != 1 || manifest.Files["icpc.db"] == "" {
		return fmt.Errorf("不支持的备份格式或缺少数据库")
	}
	parent := filepath.Dir(dbPath)
	staging, err := os.MkdirTemp(parent, ".icpc-restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	seen := make(map[string]bool)
	var total uint64
	for _, entry := range archive.File {
		if entry.Name == "manifest.json" {
			continue
		}
		name := entry.Name
		if seen[name] || (name != "icpc.db" && !strings.HasPrefix(name, "data/")) || filepath.ToSlash(filepath.Clean(name)) != name || strings.Contains(name, "\\") || strings.Contains(name, "../") || !entry.Mode().IsRegular() {
			return fmt.Errorf("非法备份路径：%s", name)
		}
		expected, ok := manifest.Files[name]
		if !ok {
			return fmt.Errorf("备份文件未列入清单：%s", name)
		}
		total += entry.UncompressedSize64
		if total > 50<<30 {
			return fmt.Errorf("备份解压大小超过 50 GiB 限制")
		}
		path := filepath.Join(staging, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		reader, err := entry.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			reader.Close()
			return err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(reader, int64(entry.UncompressedSize64)+1))
		reader.Close()
		syncErr := output.Sync()
		output.Close()
		if copyErr != nil {
			return copyErr
		}
		if syncErr != nil {
			return syncErr
		}
		if hex.EncodeToString(hash.Sum(nil)) != expected {
			return fmt.Errorf("备份校验失败：%s", name)
		}
		seen[name] = true
	}
	if len(seen) != len(manifest.Files) {
		return fmt.Errorf("备份缺少清单文件")
	}
	checkDB, err := sql.Open("sqlite", filepath.Join(staging, "icpc.db"))
	if err != nil {
		return err
	}
	var integrity string
	err = checkDB.QueryRow(`PRAGMA integrity_check`).Scan(&integrity)
	checkDB.Close()
	if err != nil || integrity != "ok" {
		return fmt.Errorf("数据库完整性校验失败：%s", integrity)
	}
	if err := os.MkdirAll(filepath.Join(staging, "data"), 0700); err != nil {
		return err
	}
	suffix := ".pre-restore-" + time.Now().Format("20060102-150405.000000000")
	hadDB, hadData := false, false
	if _, err := os.Stat(dbPath); err == nil {
		if err := os.Rename(dbPath, dbPath+suffix); err != nil {
			return err
		}
		hadDB = true
	}
	if _, err := os.Stat(dataDir); err == nil {
		if err := os.Rename(dataDir, dataDir+suffix); err != nil {
			if hadDB {
				_ = os.Rename(dbPath+suffix, dbPath)
			}
			return err
		}
		hadData = true
	}
	rollback := func() {
		_ = os.Remove(dbPath)
		if hadDB {
			_ = os.Rename(dbPath+suffix, dbPath)
		}
		if hadData {
			_ = os.Rename(dataDir+suffix, dataDir)
		}
	}
	if err := os.Rename(filepath.Join(staging, "icpc.db"), dbPath); err != nil {
		rollback()
		return err
	}
	if err := os.Rename(filepath.Join(staging, "data"), dataDir); err != nil {
		rollback()
		return err
	}
	for _, extension := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(dbPath + extension); err == nil {
			_ = os.Rename(dbPath+extension, dbPath+extension+suffix)
		}
	}
	return nil
}
