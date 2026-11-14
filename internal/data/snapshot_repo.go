package data

import (
	"database/sql"
	"fmt"
	"time"
)

// OperationSnapshot is a named, long-lived queue of broadcast operations that
// must be re-applied to every device (or room) that joins later.
type OperationSnapshot struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Scope              string `json:"scope"` // "local" (机房) or "cloud" (云端)
	ScopeID            string `json:"scope_id"`
	Active             bool   `json:"active"`
	RemoteID           int64  `json:"remote_id,omitempty"`
	CreatedAt          string `json:"created_at"`
	EndedAt            string `json:"ended_at"`
	OpCount            int    `json:"op_count"`
	SkippedRemoteCount int    `json:"skipped_remote_count,omitempty"`
}

// SnapshotOp is a single recorded broadcast operation inside a snapshot.
type SnapshotOp struct {
	ID               int64  `json:"id"`
	SnapshotID       int64  `json:"snapshot_id"`
	Kind             string `json:"kind"`
	Payload          string `json:"payload"`
	Summary          string `json:"summary"`
	SortOrder        int    `json:"sort_order"`
	Source           string `json:"source"` // "local" or "remote"
	RemoteOpID       int64  `json:"remote_op_id,omitempty"`
	AnchorRemoteOpID int64  `json:"anchor_remote_op_id,omitempty"`
	RoomReady        bool   `json:"room_ready"`
	DeletedLocally   bool   `json:"-"`
	CreatedAt        string `json:"created_at"`
	Delivered        int    `json:"delivered"`
}

// RemoteSnapshotOp describes one operation from the Cloud's active snapshot.
type RemoteSnapshotOp struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Payload   string `json:"payload"`
	Summary   string `json:"summary"`
	CreatedAt string `json:"created_at"`
}

// RemoteSnapshotState is pushed from Cloud to Relay rooms so each room keeps an
// up-to-date base sequence onto which room-local operations are inserted.
type RemoteSnapshotState struct {
	ID        int64              `json:"id"`
	Name      string             `json:"name"`
	CreatedAt string             `json:"created_at"`
	Ops       []RemoteSnapshotOp `json:"ops"`
}

// SnapshotRepo persists operation snapshots and their recorded operations.
type SnapshotRepo struct {
	db *sql.DB
}

func NewSnapshotRepo(db *sql.DB) *SnapshotRepo {
	return &SnapshotRepo{db: db}
}

const snapshotSelectCols = `id,name,scope,scope_id,active,remote_id,created_at,ended_at,
	(SELECT COUNT(*) FROM snapshot_ops WHERE snapshot_id=operation_snapshots.id AND deleted_locally=0),
	(SELECT COUNT(*) FROM snapshot_ops WHERE snapshot_id=operation_snapshots.id AND source='remote' AND deleted_locally=1)`

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
	result, err := tx.Exec(`INSERT INTO operation_snapshots(name,scope,scope_id,active,remote_id,created_at) VALUES(?,?,?,1,0,?)`, name, scope, scopeID, now)
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
	row := r.db.QueryRow(`SELECT `+snapshotSelectCols+` FROM operation_snapshots WHERE scope=? AND active=1 ORDER BY id DESC LIMIT 1`, scope)
	return scanSnapshot(row)
}

// GetSnapshot returns a snapshot by id.
func (r *SnapshotRepo) GetSnapshot(id int64) (*OperationSnapshot, error) {
	row := r.db.QueryRow(`SELECT `+snapshotSelectCols+` FROM operation_snapshots WHERE id=?`, id)
	return scanSnapshot(row)
}

// ListSnapshots returns recent snapshots for a scope.
func (r *SnapshotRepo) ListSnapshots(scope string, limit int) ([]OperationSnapshot, error) {
	rows, err := r.db.Query(`SELECT `+snapshotSelectCols+` FROM operation_snapshots WHERE scope=? ORDER BY id DESC LIMIT ?`, scope, limit)
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

// AddOp appends a local operation to the end of a snapshot.
func (r *SnapshotRepo) AddOp(snapshotID int64, kind, payload, summary string) (int64, error) {
	return r.InsertOp(snapshotID, kind, payload, summary, nil)
}

// InsertOp inserts a local operation into a snapshot.
// afterOpID == nil appends at the end; *afterOpID == 0 inserts at the beginning;
// *afterOpID > 0 inserts right after the operation with that local ID.
func (r *SnapshotRepo) InsertOp(snapshotID int64, kind, payload, summary string, afterOpID *int64) (int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	existing, err := listAllOpsTx(tx, snapshotID)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := tx.Exec(
		`INSERT INTO snapshot_ops(snapshot_id,kind,payload,summary,sort_order,source,remote_op_id,anchor_remote_op_id,room_ready,deleted_locally,created_at) VALUES(?,?,?,?,0,'local',0,0,1,0,?)`,
		snapshotID, kind, payload, summary, now,
	)
	if err != nil {
		return 0, err
	}
	newID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	inserted := SnapshotOp{
		ID:         newID,
		SnapshotID: snapshotID,
		Kind:       kind,
		Payload:    payload,
		Summary:    summary,
		Source:     "local",
		RoomReady:  true,
		CreatedAt:  now,
	}

	ordered := make([]SnapshotOp, 0, len(existing)+1)
	if afterOpID != nil && *afterOpID == 0 {
		ordered = append(ordered, inserted)
		ordered = append(ordered, existing...)
	} else if afterOpID != nil && *afterOpID > 0 {
		found := false
		for _, op := range existing {
			ordered = append(ordered, op)
			if op.ID == *afterOpID && !op.DeletedLocally {
				ordered = append(ordered, inserted)
				found = true
			}
		}
		if !found {
			return 0, fmt.Errorf("参考插入位置的操作不存在")
		}
	} else {
		ordered = append(ordered, existing...)
		ordered = append(ordered, inserted)
	}

	if err := normalizeOpsTx(tx, ordered); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return newID, nil
}

// ListOps returns visible operations of a snapshot in execution order, with a
// count of how many targets have already received each one.
func (r *SnapshotRepo) ListOps(snapshotID int64) ([]SnapshotOp, error) {
	rows, err := r.db.Query(`SELECT id,snapshot_id,kind,payload,summary,sort_order,source,remote_op_id,anchor_remote_op_id,room_ready,deleted_locally,created_at,
		(SELECT COUNT(*) FROM snapshot_deliveries WHERE op_id=snapshot_ops.id)
		FROM snapshot_ops WHERE snapshot_id=? AND deleted_locally=0 ORDER BY sort_order ASC, id ASC`, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SnapshotOp, 0)
	for rows.Next() {
		op, err := scanOp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// GetOp returns one visible operation from a snapshot.
func (r *SnapshotRepo) GetOp(snapshotID, opID int64) (*SnapshotOp, error) {
	row := r.db.QueryRow(`SELECT id,snapshot_id,kind,payload,summary,sort_order,source,remote_op_id,anchor_remote_op_id,room_ready,deleted_locally,created_at,
		(SELECT COUNT(*) FROM snapshot_deliveries WHERE op_id=snapshot_ops.id)
		FROM snapshot_ops WHERE snapshot_id=? AND id=? AND deleted_locally=0`, snapshotID, opID)
	op, err := scanOp(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &op, nil
}

// DeleteOp removes one operation from a snapshot. Remote operations synced from
// Cloud are marked deleted_locally=1 so a subsequent Cloud sync does not revive
// them in this room.
func (r *SnapshotRepo) DeleteOp(snapshotID, opID int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var source string
	var deletedLocally int
	err = tx.QueryRow(`SELECT source, deleted_locally FROM snapshot_ops WHERE id=? AND snapshot_id=?`, opID, snapshotID).Scan(&source, &deletedLocally)
	if err == sql.ErrNoRows || deletedLocally == 1 {
		return fmt.Errorf("操作不存在")
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM snapshot_deliveries WHERE op_id=?`, opID); err != nil {
		return err
	}
	if source == "remote" {
		if _, err := tx.Exec(`UPDATE snapshot_ops SET deleted_locally=1 WHERE id=? AND snapshot_id=?`, opID, snapshotID); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`DELETE FROM snapshot_ops WHERE id=? AND snapshot_id=?`, opID, snapshotID); err != nil {
			return err
		}
	}
	remaining, err := listAllOpsTx(tx, snapshotID)
	if err != nil {
		return err
	}
	if err := normalizeOpsTx(tx, remaining); err != nil {
		return err
	}
	return tx.Commit()
}

// RestoreSkippedRemoteOps restores any remote operations that were skipped locally.
func (r *SnapshotRepo) RestoreSkippedRemoteOps(snapshotID int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE snapshot_ops SET deleted_locally=0 WHERE snapshot_id=? AND source='remote'`, snapshotID); err != nil {
		return err
	}
	all, err := listAllOpsTx(tx, snapshotID)
	if err != nil {
		return err
	}
	if err := normalizeOpsTx(tx, all); err != nil {
		return err
	}
	return tx.Commit()
}

// ReorderOps updates the execution order of visible operations in snapshotID.
// When lockRemoteRelativeOrder is true, the relative order among remote ops must
// remain unchanged (room-local insertions move around remote ops).
func (r *SnapshotRepo) ReorderOps(snapshotID int64, opIDs []int64, lockRemoteRelativeOrder bool) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	all, err := listAllOpsTx(tx, snapshotID)
	if err != nil {
		return err
	}
	visibleByID := make(map[int64]SnapshotOp, len(all))
	prevRemoteOrder := make([]int64, 0)
	pendingHidden := make([]SnapshotOp, 0)
	hiddenBefore := make(map[int64][]SnapshotOp)
	for _, op := range all {
		if op.DeletedLocally {
			pendingHidden = append(pendingHidden, op)
			continue
		}
		visibleByID[op.ID] = op
		if op.Source == "remote" {
			if len(pendingHidden) > 0 {
				hiddenBefore[op.ID] = pendingHidden
				pendingHidden = nil
			}
			prevRemoteOrder = append(prevRemoteOrder, op.ID)
		}
	}
	hiddenTail := pendingHidden

	if len(opIDs) != len(visibleByID) {
		return fmt.Errorf("操作列表已变更，请刷新后重试")
	}
	seen := make(map[int64]bool, len(opIDs))
	combined := make([]SnapshotOp, 0, len(all))
	nextRemoteOrder := make([]int64, 0, len(prevRemoteOrder))
	for _, id := range opIDs {
		op, ok := visibleByID[id]
		if !ok || seen[id] {
			return fmt.Errorf("无效的操作顺序")
		}
		seen[id] = true
		if op.Source == "remote" {
			combined = append(combined, hiddenBefore[op.ID]...)
			nextRemoteOrder = append(nextRemoteOrder, op.ID)
		}
		combined = append(combined, op)
	}
	combined = append(combined, hiddenTail...)
	if lockRemoteRelativeOrder && len(prevRemoteOrder) > 1 {
		for i := range prevRemoteOrder {
			if prevRemoteOrder[i] != nextRemoteOrder[i] {
				return fmt.Errorf("云端操作之间的先后顺序由云端维护，请调整本机房插入操作的位置")
			}
		}
	}

	if err := normalizeOpsTx(tx, combined); err != nil {
		return err
	}
	return tx.Commit()
}

// SyncRemoteSnapshot reconciles the room's local snapshot with Cloud's active
// snapshot while preserving room-local inserted operations and device deliveries.
func (r *SnapshotRepo) SyncRemoteSnapshot(scopeID string, remote *RemoteSnapshotState) (int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	active, err := activeSnapshotTx(tx, "local")
	if err != nil {
		return 0, err
	}

	if remote == nil || remote.ID <= 0 {
		if active != nil && active.RemoteID > 0 {
			if _, err := tx.Exec(`UPDATE operation_snapshots SET active=0, ended_at=? WHERE id=?`, now, active.ID); err != nil {
				return 0, err
			}
		}
		return 0, tx.Commit()
	}

	localSnapID, err := ensureRemoteBackedSnapshotTx(tx, scopeID, remote.ID, remote.Name, now, active)
	if err != nil {
		return 0, err
	}

	existing, err := listAllOpsTx(tx, localSnapID)
	if err != nil {
		return 0, err
	}

	incomingByID := make(map[int64]RemoteSnapshotOp, len(remote.Ops))
	for _, rop := range remote.Ops {
		incomingByID[rop.ID] = rop
	}

	existingRemoteByID := make(map[int64]SnapshotOp)
	survivingLocal := make([]SnapshotOp, 0)
	var lastSurvivingRemoteID int64

	for _, row := range existing {
		if row.Source == "remote" {
			if _, ok := incomingByID[row.RemoteOpID]; ok {
				existingRemoteByID[row.RemoteOpID] = row
				lastSurvivingRemoteID = row.RemoteOpID
			} else {
				if _, err := tx.Exec(`DELETE FROM snapshot_deliveries WHERE op_id=?`, row.ID); err != nil {
					return 0, err
				}
				if _, err := tx.Exec(`DELETE FROM snapshot_ops WHERE id=?`, row.ID); err != nil {
					return 0, err
				}
			}
		} else {
			if row.AnchorRemoteOpID != 0 {
				if _, ok := incomingByID[row.AnchorRemoteOpID]; !ok {
					row.AnchorRemoteOpID = lastSurvivingRemoteID
				}
			}
			survivingLocal = append(survivingLocal, row)
		}
	}

	remoteRows := make(map[int64]SnapshotOp, len(remote.Ops))
	for _, rop := range remote.Ops {
		if prev, ok := existingRemoteByID[rop.ID]; ok {
			if prev.Kind != rop.Kind || prev.Payload != rop.Payload || prev.Summary != rop.Summary {
				if _, err := tx.Exec(`UPDATE snapshot_ops SET kind=?, payload=?, summary=? WHERE id=?`, rop.Kind, rop.Payload, rop.Summary, prev.ID); err != nil {
					return 0, err
				}
				prev.Kind, prev.Payload, prev.Summary = rop.Kind, rop.Payload, rop.Summary
			}
			remoteRows[rop.ID] = prev
		} else {
			createdAt := rop.CreatedAt
			if createdAt == "" {
				createdAt = now
			}
			res, err := tx.Exec(
				`INSERT INTO snapshot_ops(snapshot_id,kind,payload,summary,sort_order,source,remote_op_id,anchor_remote_op_id,room_ready,deleted_locally,created_at) VALUES(?,?,?,?,0,'remote',?,0,0,0,?)`,
				localSnapID, rop.Kind, rop.Payload, rop.Summary, rop.ID, createdAt,
			)
			if err != nil {
				return 0, err
			}
			id, err := res.LastInsertId()
			if err != nil {
				return 0, err
			}
			remoteRows[rop.ID] = SnapshotOp{
				ID:         id,
				SnapshotID: localSnapID,
				Kind:       rop.Kind,
				Payload:    rop.Payload,
				Summary:    rop.Summary,
				Source:     "remote",
				RemoteOpID: rop.ID,
				RoomReady:  false,
				CreatedAt:  createdAt,
			}
		}
	}

	byAnchor := make(map[int64][]SnapshotOp)
	for _, loc := range survivingLocal {
		anchor := loc.AnchorRemoteOpID
		if anchor != 0 {
			if _, ok := remoteRows[anchor]; !ok {
				anchor = 0
				loc.AnchorRemoteOpID = 0
			}
		}
		byAnchor[anchor] = append(byAnchor[anchor], loc)
	}

	merged := make([]SnapshotOp, 0, len(remote.Ops)+len(survivingLocal))
	merged = append(merged, byAnchor[0]...)
	for _, rop := range remote.Ops {
		merged = append(merged, remoteRows[rop.ID])
		merged = append(merged, byAnchor[rop.ID]...)
	}

	for idx, op := range merged {
		order := idx + 1
		if _, err := tx.Exec(`UPDATE snapshot_ops SET sort_order=?, anchor_remote_op_id=? WHERE id=?`, order, op.AnchorRemoteOpID, op.ID); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return localSnapID, nil
}

// RecordRemoteDeliveryOnRoom marks a Cloud-originated snapshot op as ready in
// the room's local snapshot and records the devices that received the fanout.
func (r *SnapshotRepo) RecordRemoteDeliveryOnRoom(scopeID string, remoteSnapshotID int64, remoteSnapshotName string, remoteOpID int64, kind, payload, summary string, deviceIDs []int) (int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	active, err := activeSnapshotTx(tx, "local")
	if err != nil {
		return 0, err
	}

	var localSnapID int64
	if remoteSnapshotID > 0 {
		if remoteSnapshotName == "" {
			remoteSnapshotName = "云端快照"
		}
		localSnapID, err = ensureRemoteBackedSnapshotTx(tx, scopeID, remoteSnapshotID, remoteSnapshotName, now, active)
		if err != nil {
			return 0, err
		}
	} else if active != nil {
		localSnapID = active.ID
	} else {
		return 0, tx.Commit()
	}

	var localOpID int64
	err = tx.QueryRow(`SELECT id FROM snapshot_ops WHERE snapshot_id=? AND source='remote' AND remote_op_id=?`, localSnapID, remoteOpID).Scan(&localOpID)
	if err == sql.ErrNoRows {
		var maxOrder int
		_ = tx.QueryRow(`SELECT COALESCE(MAX(sort_order), 0) FROM snapshot_ops WHERE snapshot_id=?`, localSnapID).Scan(&maxOrder)
		res, err := tx.Exec(
			`INSERT INTO snapshot_ops(snapshot_id,kind,payload,summary,sort_order,source,remote_op_id,anchor_remote_op_id,room_ready,deleted_locally,created_at) VALUES(?,?,?,?,?,'remote',?,0,1,0,?)`,
			localSnapID, kind, payload, summary, maxOrder+1, remoteOpID, now,
		)
		if err != nil {
			return 0, err
		}
		localOpID, err = res.LastInsertId()
		if err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, err
	} else {
		if kind != "" && payload != "" {
			if _, err := tx.Exec(`UPDATE snapshot_ops SET room_ready=1, kind=?, payload=?, summary=CASE WHEN summary='' THEN ? ELSE summary END WHERE id=?`, kind, payload, summary, localOpID); err != nil {
				return 0, err
			}
		} else {
			if _, err := tx.Exec(`UPDATE snapshot_ops SET room_ready=1 WHERE id=?`, localOpID); err != nil {
				return 0, err
			}
		}
	}

	for _, id := range deviceIDs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO snapshot_deliveries(op_id,target_kind,target_id,delivered_at) VALUES(?,?,?,?)`, localOpID, "device", fmt.Sprintf("%d", id), now); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return localOpID, nil
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

func activeSnapshotTx(tx *sql.Tx, scope string) (*OperationSnapshot, error) {
	row := tx.QueryRow(`SELECT `+snapshotSelectCols+` FROM operation_snapshots WHERE scope=? AND active=1 ORDER BY id DESC LIMIT 1`, scope)
	return scanSnapshot(row)
}

func ensureRemoteBackedSnapshotTx(tx *sql.Tx, scopeID string, remoteID int64, remoteName, now string, active *OperationSnapshot) (int64, error) {
	if active != nil {
		if active.RemoteID == remoteID {
			if remoteName != "" && active.Name != remoteName {
				if _, err := tx.Exec(`UPDATE operation_snapshots SET name=? WHERE id=?`, remoteName, active.ID); err != nil {
					return 0, err
				}
			}
			return active.ID, nil
		}
		if active.RemoteID == 0 {
			name := active.Name
			if remoteName != "" {
				name = remoteName
			}
			if _, err := tx.Exec(`UPDATE operation_snapshots SET remote_id=?, name=? WHERE id=?`, remoteID, name, active.ID); err != nil {
				return 0, err
			}
			return active.ID, nil
		}
		if _, err := tx.Exec(`UPDATE operation_snapshots SET active=0, ended_at=? WHERE id=?`, now, active.ID); err != nil {
			return 0, err
		}
	}
	// If this room previously synced this same remote snapshot ID and was ended
	// temporarily, reactivate it instead of creating a duplicate.
	var existingID int64
	err := tx.QueryRow(`SELECT id FROM operation_snapshots WHERE scope='local' AND remote_id=? ORDER BY id DESC LIMIT 1`, remoteID).Scan(&existingID)
	if err == nil && existingID > 0 {
		if _, err := tx.Exec(`UPDATE operation_snapshots SET active=1, ended_at='', name=? WHERE id=?`, remoteName, existingID); err != nil {
			return 0, err
		}
		return existingID, nil
	}
	res, err := tx.Exec(`INSERT INTO operation_snapshots(name,scope,scope_id,active,remote_id,created_at) VALUES(?,'local',?,1,?,?)`, remoteName, scopeID, remoteID, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func listAllOpsTx(tx *sql.Tx, snapshotID int64) ([]SnapshotOp, error) {
	rows, err := tx.Query(`SELECT id,snapshot_id,kind,payload,summary,sort_order,source,remote_op_id,anchor_remote_op_id,room_ready,deleted_locally,created_at,
		(SELECT COUNT(*) FROM snapshot_deliveries WHERE op_id=snapshot_ops.id)
		FROM snapshot_ops WHERE snapshot_id=? ORDER BY sort_order ASC, id ASC`, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SnapshotOp, 0)
	for rows.Next() {
		op, err := scanOp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

func normalizeOpsTx(tx *sql.Tx, ops []SnapshotOp) error {
	var lastRemoteID int64
	for idx, op := range ops {
		order := idx + 1
		anchor := op.AnchorRemoteOpID
		if op.Source == "remote" {
			if !op.DeletedLocally {
				lastRemoteID = op.RemoteOpID
			}
			anchor = 0
		} else {
			anchor = lastRemoteID
		}
		if _, err := tx.Exec(`UPDATE snapshot_ops SET sort_order=?, anchor_remote_op_id=? WHERE id=?`, order, anchor, op.ID); err != nil {
			return err
		}
	}
	return nil
}

func scanSnapshot(row interface{ Scan(...interface{}) error }) (*OperationSnapshot, error) {
	var snapshot OperationSnapshot
	var active int
	err := row.Scan(&snapshot.ID, &snapshot.Name, &snapshot.Scope, &snapshot.ScopeID, &active, &snapshot.RemoteID, &snapshot.CreatedAt, &snapshot.EndedAt, &snapshot.OpCount, &snapshot.SkippedRemoteCount)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	snapshot.Active = active == 1
	return &snapshot, nil
}

func scanOp(row interface{ Scan(...interface{}) error }) (SnapshotOp, error) {
	var op SnapshotOp
	var ready, deleted int
	err := row.Scan(&op.ID, &op.SnapshotID, &op.Kind, &op.Payload, &op.Summary, &op.SortOrder, &op.Source, &op.RemoteOpID, &op.AnchorRemoteOpID, &ready, &deleted, &op.CreatedAt, &op.Delivered)
	op.RoomReady = ready == 1
	op.DeletedLocally = deleted == 1
	return op, err
}
