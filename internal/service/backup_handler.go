package service

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"ICPCRemoteControl/internal/data"
)

type BackupHandler struct {
	db       *sql.DB
	settings *ServerSettings
	assets   sync.RWMutex
}

func NewBackupHandler(db *sql.DB, settings *ServerSettings) *BackupHandler {
	return &BackupHandler{db: db, settings: settings}
}

func (handler *BackupHandler) Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, httpRequest *http.Request) {
		if httpRequest.Method != "GET" && httpRequest.Method != "HEAD" {
			handler.assets.RLock()
			defer handler.assets.RUnlock()
		}
		next.ServeHTTP(writer, httpRequest)
	})
}

func (handler *BackupHandler) Download(writer http.ResponseWriter, httpRequest *http.Request) {
	if !handler.assets.TryLock() {
		writeJSON(writer, 409, map[string]string{"error": "正在写入数据，请稍后备份"})
		return
	}
	defer handler.assets.Unlock()
	file, err := os.CreateTemp("", "icpc-backup-*.zip")
	if err != nil {
		writeJSON(writer, 500, map[string]string{"error": err.Error()})
		return
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := data.WriteBackup(handler.db, "data", httpRequest.URL.Query().Get("uploads") == "true", file); err != nil {
		writeJSON(writer, 500, map[string]string{"error": err.Error()})
		return
	}
	if err := file.Sync(); err != nil {
		writeJSON(writer, 500, map[string]string{"error": err.Error()})
		return
	}
	_, _ = file.Seek(0, 0)
	writer.Header().Set("Content-Type", "application/zip")
	writer.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="icpc-backup-%s.zip"`, time.Now().Format("20060102-150405")))
	http.ServeContent(writer, httpRequest, "backup.zip", time.Now(), file)
}

func (handler *BackupHandler) Export(writer http.ResponseWriter, httpRequest *http.Request) {
	result := map[string]interface{}{"version": 1, "exported_at": time.Now().UTC().Format(time.RFC3339), "settings": handler.settings.Snapshot()}
	tx, err := handler.db.BeginTx(httpRequest.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		writeJSON(writer, 500, map[string]string{"error": err.Error()})
		return
	}
	defer tx.Rollback()
	for _, table := range []string{"devices", "command_log", "device_events", "power_schedules", "broadcast_config", "broadcast_pages", "broadcast_items", "broadcast_fonts", "cluster_rooms", "cluster_jobs", "cluster_receipts"} {
		rows, err := tx.QueryContext(httpRequest.Context(), "SELECT * FROM "+table)
		if err != nil {
			writeJSON(writer, 500, map[string]string{"error": err.Error()})
			return
		}
		columns, _ := rows.Columns()
		records := make([]map[string]interface{}, 0)
		for rows.Next() {
			values := make([]interface{}, len(columns))
			pointers := make([]interface{}, len(columns))
			for index := range pointers {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				writeJSON(writer, 500, map[string]string{"error": err.Error()})
				return
			}
			record := make(map[string]interface{})
			for index, name := range columns {
				if bytes, ok := values[index].([]byte); ok {
					record[name] = string(bytes)
				} else {
					record[name] = values[index]
				}
			}
			records = append(records, record)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			writeJSON(writer, 500, map[string]string{"error": err.Error()})
			return
		}
		result[table] = records
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Content-Disposition", `attachment; filename="icpc-export.json"`)
	_ = json.NewEncoder(writer).Encode(result)
}
