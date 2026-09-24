package data

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Power schedule statuses.
const (
	ScheduleStatusPending   = "pending"
	ScheduleStatusFired     = "fired"
	ScheduleStatusCancelled = "cancelled"
	ScheduleStatusFailed    = "failed"
)

// PowerSchedule is a future shutdown/reboot/wol action.
type PowerSchedule struct {
	ID         int64  `json:"id"`
	Action     string `json:"action"`      // "shutdown" | "reboot" | "wol"
	RunAt      string `json:"run_at"`      // RFC3339
	TargetType string `json:"target_type"` // "all" | "list"
	TargetIDs  []int  `json:"target_ids"`
	Status     string `json:"status"`
	Note       string `json:"note"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  string `json:"created_at"`
	FiredAt    string `json:"fired_at"`
}

// PowerScheduleRepo persists scheduled power actions.
type PowerScheduleRepo struct {
	db *sql.DB
}

func NewPowerScheduleRepo(db *sql.DB) *PowerScheduleRepo {
	return &PowerScheduleRepo{db: db}
}

func (r *PowerScheduleRepo) Create(s *PowerSchedule) error {
	ids, err := json.Marshal(s.TargetIDs)
	if err != nil {
		return fmt.Errorf("marshal target ids: %w", err)
	}
	if s.Status == "" {
		s.Status = ScheduleStatusPending
	}
	s.CreatedAt = time.Now().Format(time.RFC3339)
	res, err := r.db.Exec(
		`INSERT INTO power_schedules (action, run_at, target_type, target_ids, status, note, created_by, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.Action, s.RunAt, s.TargetType, string(ids), s.Status, s.Note, s.CreatedBy, s.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert power schedule: %w", err)
	}
	s.ID, _ = res.LastInsertId()
	return nil
}

func (r *PowerScheduleRepo) List(limit int) ([]PowerSchedule, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.Query(
		`SELECT id, action, run_at, target_type, target_ids, status, note, created_by,
		        created_at, COALESCE(fired_at,'')
		 FROM power_schedules ORDER BY run_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list power schedules: %w", err)
	}
	defer rows.Close()
	return scanSchedules(rows)
}

// HasPendingFleet reports whether a not-yet-fired all-device schedule already
// covers this action at this instant. A joiner must not get a second row.
func (r *PowerScheduleRepo) HasPendingFleet(action string, runAt time.Time) (bool, error) {
	rows, err := r.db.Query(`SELECT run_at FROM power_schedules WHERE status=? AND target_type='all' AND action=?`, ScheduleStatusPending, action)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return false, err
		}
		stored, err := time.Parse(time.RFC3339, raw)
		if err == nil && stored.Equal(runAt) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// DuePending returns pending schedules whose run_at has passed.
func (r *PowerScheduleRepo) DuePending(now time.Time) ([]PowerSchedule, error) {
	rows, err := r.db.Query(
		`SELECT id, action, run_at, target_type, target_ids, status, note, created_by,
		        created_at, COALESCE(fired_at,'')
		 FROM power_schedules WHERE status=? AND run_at <= ? ORDER BY run_at`,
		ScheduleStatusPending, now.Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("due power schedules: %w", err)
	}
	defer rows.Close()
	return scanSchedules(rows)
}

func scanSchedules(rows *sql.Rows) ([]PowerSchedule, error) {
	out := []PowerSchedule{}
	for rows.Next() {
		var s PowerSchedule
		var ids string
		if err := rows.Scan(&s.ID, &s.Action, &s.RunAt, &s.TargetType, &ids, &s.Status,
			&s.Note, &s.CreatedBy, &s.CreatedAt, &s.FiredAt); err != nil {
			return nil, fmt.Errorf("scan power schedule: %w", err)
		}
		if ids != "" {
			_ = json.Unmarshal([]byte(ids), &s.TargetIDs)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *PowerScheduleRepo) SetStatus(id int64, status, note string) error {
	fired := ""
	if status == ScheduleStatusFired || status == ScheduleStatusFailed {
		fired = time.Now().Format(time.RFC3339)
	}
	_, err := r.db.Exec(
		`UPDATE power_schedules SET status=?, note=CASE WHEN ?='' THEN note ELSE ? END, fired_at=?
		 WHERE id=?`, status, note, note, fired, id)
	return err
}

func (r *PowerScheduleRepo) Delete(id int64) error {
	_, err := r.db.Exec(`DELETE FROM power_schedules WHERE id=?`, id)
	return err
}

// DeleteOlderThan trims finished schedules.
func (r *PowerScheduleRepo) DeleteOlderThan(cutoff time.Time) (int64, error) {
	res, err := r.db.Exec(
		`DELETE FROM power_schedules WHERE status<>? AND run_at < ?`,
		ScheduleStatusPending, cutoff.Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("trim power schedules: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
