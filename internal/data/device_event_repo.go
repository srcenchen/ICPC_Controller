package data

import (
	"database/sql"
	"fmt"
	"time"
)

// DeviceEvent is one online/offline (or notable lifecycle) event for a device.
type DeviceEvent struct {
	ID         int64  `json:"id"`
	AssignedID int    `json:"assigned_id"`
	Event      string `json:"event"` // "online" | "offline" | "registered" | ...
	Detail     string `json:"detail"`
	At         string `json:"at"`
}

// DeviceEventRepo records device connectivity history.
type DeviceEventRepo struct {
	db *sql.DB
}

func NewDeviceEventRepo(db *sql.DB) *DeviceEventRepo {
	return &DeviceEventRepo{db: db}
}

// Add appends an event. Errors are returned but callers usually just log them:
// losing a history row must never break the connection path.
func (r *DeviceEventRepo) Add(assignedID int, event, detail string) error {
	_, err := r.db.Exec(
		`INSERT INTO device_events (assigned_id, event, detail, at) VALUES (?, ?, ?, ?)`,
		assignedID, event, detail, time.Now().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("add device event: %w", err)
	}
	return nil
}

// ListByDevice returns the newest events for one device.
func (r *DeviceEventRepo) ListByDevice(assignedID, limit int) ([]DeviceEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.Query(
		`SELECT id, assigned_id, event, detail, at FROM device_events
		 WHERE assigned_id=? ORDER BY id DESC LIMIT ?`, assignedID, limit)
	if err != nil {
		return nil, fmt.Errorf("list device events: %w", err)
	}
	defer rows.Close()

	events := []DeviceEvent{}
	for rows.Next() {
		var e DeviceEvent
		if err := rows.Scan(&e.ID, &e.AssignedID, &e.Event, &e.Detail, &e.At); err != nil {
			return nil, fmt.Errorf("scan device event: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// DeleteOlderThan trims history beyond the retention window.
func (r *DeviceEventRepo) DeleteOlderThan(cutoff time.Time) (int64, error) {
	res, err := r.db.Exec(`DELETE FROM device_events WHERE at < ?`, cutoff.Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("trim device events: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
