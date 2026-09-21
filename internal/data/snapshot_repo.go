package data

import (
	"database/sql"
	"fmt"
	"time"
)

// OperationSnapshot is a named, long-lived queue of broadcast operations that
// must be re-applied to every device (or room) that joins later.
type OperationSnapshot struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Scope     string `json:"scope"` // "local" (机房) or "cloud" (云端)
	ScopeID   string `json:"scope_id"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"created_at"`
	EndedAt   string `json:"ended_at"`
	OpCount   int    `json:"op_count"`
}

// SnapshotOp is a single recorded broadcast operation inside a snapshot.
type SnapshotOp struct {
	ID         int64  `json:"id"`
	SnapshotID int64  `json:"snapshot_id"`
	Kind       string `json:"kind"`
	Payload    string `json:"payload"`
	Summary    string `json:"summary"`
	CreatedAt  string `json:"created_at"`
	Delivered  int    `json:"delivered"`
}

// SnapshotRepo persists operation snapshots and their recorded operations.
type SnapshotRepo struct {
	db *sql.DB
}

func NewSnapshotRepo(db *sql.DB) *SnapshotRepo {
	return &SnapshotRepo{db: db}
}

// StartSnapshot creates a new active snapshot for the scope. Any previously
// active snapshot of the same scope is automatically ended first.
func (r *SnapshotRepo) StartSnapshot(name, scope, scopeID string) (*OperationSnapshot, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE operation_snapshots SET active=0, ended_at=? WHERE scope=? AND active=1`, now, scope); err != nil {
		return nil, err
	}
	result, err := tx.Exec(`INSERT INTO operation_snapshots(name,scope,scope_id,active,created_at) VALUES(?,?,?,1,?)`, name, scope, scopeID, now)
	if err != nil {
		return nil, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &OperationSnapshot{ID: id, Name: name, Scope: scope, ScopeID: scopeID, Active: true, CreatedAt: now}, nil
}

// ActiveSnapshot returns the currently active snapshot for the scope, or nil.
func (r *SnapshotRepo) ActiveSnapshot(scope string) (*OperationSnapshot, error) {
	row := r.db.QueryRow(`SELECT id,name,scope,scope_id,active,created_at,ended_at,
		(SELECT COUNT(*) FROM snapshot_ops WHERE snapshot_id=operation_snapshots.id)
		FROM operation_snapshots WHERE scope=? AND active=1 ORDER BY id DESC LIMIT 1`, scope)
	return scanSnapshot(row)
}

// GetSnapshot returns a snapshot by id.
func (r *SnapshotRepo) GetSnapshot(id int64) (*OperationSnapshot, error) {
	row := r.db.QueryRow(`SELECT id,name,scope,scope_id,active,created_at,ended_at,
		(SELECT COUNT(*) FROM snapshot_ops WHERE snapshot_id=operation_snapshots.id)
		FROM operation_snapshots WHERE id=?`, id)
	return scanSnapshot(row)
}

// ListSnapshots returns recent snapshots for a scope.
func (r *SnapshotRepo) ListSnapshots(scope string, limit int) ([]OperationSnapshot, error) {
	rows, err := r.db.Query(`SELECT id,name,scope,scope_id,active,created_at,ended_at,
		(SELECT COUNT(*) FROM snapshot_ops WHERE snapshot_id=operation_snapshots.id)
		FROM operation_snapshots WHERE scope=? ORDER BY id DESC LIMIT ?`, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]OperationSnapshot, 0)
	for rows.Next() {
		snapshot, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		if snapshot != nil {
			out = append(out, *snapshot)
		}
	}
	return out, rows.Err()
}

// EndSnapshot marks a snapshot inactive.
func (r *SnapshotRepo) EndSnapshot(id int64) error {
	_, err := r.db.Exec(`UPDATE operation_snapshots SET active=0, ended_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// DeleteSnapshot removes a snapshot and all of its operations and deliveries.
func (r *SnapshotRepo) DeleteSnapshot(id int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM snapshot_deliveries WHERE op_id IN (SELECT id FROM snapshot_ops WHERE snapshot_id=?)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM snapshot_ops WHERE snapshot_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM operation_snapshots WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// AddOp appends an operation to a snapshot.
func (r *SnapshotRepo) AddOp(snapshotID int64, kind, payload, summary string) (int64, error) {
	result, err := r.db.Exec(`INSERT INTO snapshot_ops(snapshot_id,kind,payload,summary,created_at) VALUES(?,?,?,?,?)`,
		snapshotID, kind, payload, summary, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// ListOps returns all operations of a snapshot in creation order, with a count
// of how many targets have already received each one.
func (r *SnapshotRepo) ListOps(snapshotID int64) ([]SnapshotOp, error) {
	rows, err := r.db.Query(`SELECT id,snapshot_id,kind,payload,summary,created_at,
		(SELECT COUNT(*) FROM snapshot_deliveries WHERE op_id=snapshot_ops.id)
		FROM snapshot_ops WHERE snapshot_id=? ORDER BY id`, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SnapshotOp, 0)
	for rows.Next() {
		var op SnapshotOp
		if err := rows.Scan(&op.ID, &op.SnapshotID, &op.Kind, &op.Payload, &op.Summary, &op.CreatedAt, &op.Delivered); err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// DeleteOp removes one operation from a snapshot.
func (r *SnapshotRepo) DeleteOp(snapshotID, opID int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM snapshot_deliveries WHERE op_id=? AND op_id IN (SELECT id FROM snapshot_ops WHERE snapshot_id=?)`, opID, snapshotID); err != nil {
		return err
	}
	result, err := tx.Exec(`DELETE FROM snapshot_ops WHERE id=? AND snapshot_id=?`, opID, snapshotID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return fmt.Errorf("操作不存在")
	}
	return tx.Commit()
}

// MarkDelivered records that an operation reached a target. Idempotent.
func (r *SnapshotRepo) MarkDelivered(opID int64, targetKind, targetID string) error {
	_, err := r.db.Exec(`INSERT OR IGNORE INTO snapshot_deliveries(op_id,target_kind,target_id,delivered_at) VALUES(?,?,?,?)`,
		opID, targetKind, targetID, time.Now().UTC().Format(time.RFC3339))
	return err
}

// IsDelivered reports whether an operation already reached a target.
func (r *SnapshotRepo) IsDelivered(opID int64, targetKind, targetID string) (bool, error) {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM snapshot_deliveries WHERE op_id=? AND target_kind=? AND target_id=?`, opID, targetKind, targetID).Scan(&count)
	return count > 0, err
}

func scanSnapshot(row interface{ Scan(...interface{}) error }) (*OperationSnapshot, error) {
	var snapshot OperationSnapshot
	var active int
	err := row.Scan(&snapshot.ID, &snapshot.Name, &snapshot.Scope, &snapshot.ScopeID, &active, &snapshot.CreatedAt, &snapshot.EndedAt, &snapshot.OpCount)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	snapshot.Active = active == 1
	return &snapshot, nil
}
