package data

import (
	"database/sql"
	"fmt"
	"time"

	"ICPCRemoteControl/internal/model"
)

// CommandRepo handles CRUD operations for the command_log table.
type CommandRepo struct {
	db *sql.DB
}

// NewCommandRepo creates a new CommandRepo.
func NewCommandRepo(db *sql.DB) *CommandRepo {
	return &CommandRepo{db: db}
}

// Create inserts a new command log entry.
func (r *CommandRepo) Create(cmd *model.CommandLog) error {
	now := time.Now().Format(time.RFC3339)
	cmd.CreatedAt = now

	query := `INSERT INTO command_log (
		parent_id, target_type, target_id, command, status, output, error_output,
		executed_by, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	result, err := r.db.Exec(query,
		cmd.ParentID, cmd.TargetType, cmd.TargetID, cmd.Command, cmd.Status,
		cmd.Output, cmd.ErrorOutput, cmd.ExecutedBy, cmd.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert command: %w", err)
	}
	id, _ := result.LastInsertId()
	cmd.ID = id
	return nil
}

// UpdateStatus updates the status, output, duration, and timestamps of a command.
func (r *CommandRepo) UpdateStatus(cmd *model.CommandLog) error {
	now := time.Now().Format(time.RFC3339)
	if cmd.Status == model.CommandStatusDispatched && cmd.DispatchedAt == "" {
		cmd.DispatchedAt = now
	}
	if cmd.Status == model.CommandStatusCompleted || cmd.Status == model.CommandStatusFailed || cmd.Status == model.CommandStatusTimeout {
		cmd.CompletedAt = now
	}

	_, err := r.db.Exec(
		`UPDATE command_log SET status=?, output=?, error_output=?, dispatched_at=?,
		completed_at=?, duration_ms=? WHERE id=?`,
		cmd.Status, cmd.Output, cmd.ErrorOutput, cmd.DispatchedAt,
		cmd.CompletedAt, cmd.DurationMS, cmd.ID,
	)
	return err
}

// UpdateOutput writes only the output column. Used by the throttled streaming
// path so a running command doesn't rewrite status/timestamps on every flush.
func (r *CommandRepo) UpdateOutput(id int64, output string) error {
	_, err := r.db.Exec(`UPDATE command_log SET output=? WHERE id=?`, output, id)
	return err
}

// DeleteOlderThan removes command logs (and their children) created before cutoff.
func (r *CommandRepo) DeleteOlderThan(cutoff time.Time) (int64, error) {
	res, err := r.db.Exec(`DELETE FROM command_log WHERE created_at < ?`, cutoff.Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("delete old commands: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DeleteBeyondCount keeps the newest keep top-level commands (with their
// children) and deletes the rest.
func (r *CommandRepo) DeleteBeyondCount(keep int) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	res, err := r.db.Exec(`
		DELETE FROM command_log
		WHERE id NOT IN (
			SELECT id FROM command_log WHERE parent_id IS NULL ORDER BY id DESC LIMIT ?
		)
		AND COALESCE(parent_id, -1) NOT IN (
			SELECT id FROM command_log WHERE parent_id IS NULL ORDER BY id DESC LIMIT ?
		)`, keep, keep)
	if err != nil {
		return 0, fmt.Errorf("trim commands: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// GetByID retrieves a command log by its ID.
func (r *CommandRepo) GetByID(id int64) (*model.CommandLog, error) {
	query := `SELECT id, parent_id, target_type, target_id, command, status, output, error_output,
		executed_by, created_at, COALESCE(dispatched_at,''), COALESCE(completed_at,''), duration_ms
	FROM command_log WHERE id=?`

	cmd := &model.CommandLog{}
	var parentID sql.NullInt64
	var targetID sql.NullInt64
	err := r.db.QueryRow(query, id).Scan(
		&cmd.ID, &parentID, &cmd.TargetType, &targetID, &cmd.Command, &cmd.Status,
		&cmd.Output, &cmd.ErrorOutput, &cmd.ExecutedBy, &cmd.CreatedAt,
		&cmd.DispatchedAt, &cmd.CompletedAt, &cmd.DurationMS,
	)
	if err != nil {
		return nil, fmt.Errorf("get command by id %d: %w", id, err)
	}
	if parentID.Valid {
		cmd.ParentID = &parentID.Int64
	}
	if targetID.Valid {
		id := int(targetID.Int64)
		cmd.TargetID = &id
	}
	return cmd, nil
}

// GetByParentID retrieves all child commands for a broadcast parent.
func (r *CommandRepo) GetByParentID(parentID int64) ([]*model.CommandLog, error) {
	query := `SELECT id, parent_id, target_type, target_id, command, status, output, error_output,
		executed_by, created_at, COALESCE(dispatched_at,''), COALESCE(completed_at,''), duration_ms
	FROM command_log WHERE parent_id=? ORDER BY id`

	rows, err := r.db.Query(query, parentID)
	if err != nil {
		return nil, fmt.Errorf("get children for parent %d: %w", parentID, err)
	}
	defer rows.Close()

	var children []*model.CommandLog
	for rows.Next() {
		var cmd model.CommandLog
		var pID sql.NullInt64
		var tID sql.NullInt64
		if err := rows.Scan(
			&cmd.ID, &pID, &cmd.TargetType, &tID, &cmd.Command, &cmd.Status,
			&cmd.Output, &cmd.ErrorOutput, &cmd.ExecutedBy, &cmd.CreatedAt,
			&cmd.DispatchedAt, &cmd.CompletedAt, &cmd.DurationMS,
		); err != nil {
			return nil, fmt.Errorf("scan child command: %w", err)
		}
		if pID.Valid {
			cmd.ParentID = &pID.Int64
		}
		if tID.Valid {
			id := int(tID.Int64)
			cmd.TargetID = &id
		}
		children = append(children, &cmd)
	}
	if children == nil {
		children = []*model.CommandLog{}
	}
	return children, rows.Err()
}

// GetAll returns paginated command history, newest first.
// Only returns top-level commands (parent_id IS NULL) — children are nested.
func (r *CommandRepo) GetAll(limit, offset int) ([]model.CommandLog, error) {
	query := `SELECT id, parent_id, target_type, target_id, command, status, output, error_output,
		executed_by, created_at, COALESCE(dispatched_at,''), COALESCE(completed_at,''), duration_ms
	FROM command_log WHERE parent_id IS NULL ORDER BY created_at DESC LIMIT ? OFFSET ?`

	rows, err := r.db.Query(query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("get all commands: %w", err)
	}
	defer rows.Close()

	var cmds []model.CommandLog
	for rows.Next() {
		var cmd model.CommandLog
		var pID sql.NullInt64
		var tID sql.NullInt64
		if err := rows.Scan(
			&cmd.ID, &pID, &cmd.TargetType, &tID, &cmd.Command, &cmd.Status,
			&cmd.Output, &cmd.ErrorOutput, &cmd.ExecutedBy, &cmd.CreatedAt,
			&cmd.DispatchedAt, &cmd.CompletedAt, &cmd.DurationMS,
		); err != nil {
			return nil, fmt.Errorf("scan command: %w", err)
		}
		if pID.Valid {
			cmd.ParentID = &pID.Int64
		}
		if tID.Valid {
			id := int(tID.Int64)
			cmd.TargetID = &id
		}
		cmds = append(cmds, cmd)
	}
	if cmds == nil {
		cmds = []model.CommandLog{}
	}
	return cmds, rows.Err()
}

// GetRecent returns the most recent N top-level commands.
func (r *CommandRepo) GetRecent(n int) ([]model.CommandLog, error) {
	return r.GetAll(n, 0)
}

// GetTotalCount returns the total number of top-level command logs.
func (r *CommandRepo) GetTotalCount() (int, error) {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM command_log WHERE parent_id IS NULL`).Scan(&count)
	return count, err
}

// CountByParentAndStatus counts children of a parent with a specific status.
func (r *CommandRepo) CountByParentAndStatus(parentID int64, status string) (int, error) {
	var count int
	err := r.db.QueryRow(
		`SELECT COUNT(*) FROM command_log WHERE parent_id=? AND status=?`,
		parentID, status,
	).Scan(&count)
	return count, err
}

// ClearAll deletes all command log records.
func (r *CommandRepo) ClearAll() error {
	_, err := r.db.Exec(`DELETE FROM command_log`)
	return err
}
