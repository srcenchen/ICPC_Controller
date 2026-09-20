package biz

import (
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/model"
)

// IDAssigner manages atomic assignment of numeric IDs to contestant machines.
type IDAssigner struct {
	repo *data.DeviceRepo
	mu   sync.Mutex
}

// NewIDAssigner creates a new IDAssigner.
func NewIDAssigner(repo *data.DeviceRepo) *IDAssigner {
	return &IDAssigner{repo: repo}
}

// AssignOrReuse atomically assigns an ID for a client, checking MAC and stored ID for reuse.
// Returns the assigned ID and optionally an existing device record (from MAC match).
// The third result reports whether a fresh placeholder row was inserted, so the
// caller can delete it if the client never reports its system info.
func (a *IDAssigner) AssignOrReuse(macAddress string, storedID *int) (assignedID int, existingDevice *model.Device, err error) {
	id, dev, _, err := a.assignOrReuse(macAddress, storedID)
	return id, dev, err
}

// AssignOrReuseTracked is AssignOrReuse plus the "newly allocated" flag.
func (a *IDAssigner) AssignOrReuseTracked(macAddress string, storedID *int) (assignedID int, existingDevice *model.Device, newlyAllocated bool, err error) {
	return a.assignOrReuse(macAddress, storedID)
}

func (a *IDAssigner) assignOrReuse(macAddress string, storedID *int) (assignedID int, existingDevice *model.Device, newlyAllocated bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 1. Try MAC-based dedup — check if this MAC is already registered.
	if macAddress != "" {
		existing, err := a.repo.GetByMacAddress(macAddress)
		if err == nil && existing != nil {
			log.Printf("[id] MAC %s matched existing device #%d", macAddress, existing.AssignedID)
			return existing.AssignedID, existing, false, nil
		}
	}

	// 2. Try stored ID reuse — only if the device exists and is currently offline.
	if storedID != nil {
		dev, err := a.repo.GetByAssignedID(*storedID)
		if err == nil && !dev.Connected {
			log.Printf("[id] reusing stored ID %d", *storedID)
			return *storedID, nil, false, nil
		}
	}

	// 3. Allocate a new ID atomically: SELECT MAX+1 and INSERT placeholder immediately.
	id, err := a.allocateNew()
	if err != nil {
		return 0, nil, false, fmt.Errorf("allocate id: %w", err)
	}
	log.Printf("[id] assigned new ID %d", id)
	return id, nil, true, nil
}

// allocateNew atomically gets the next ID and inserts a placeholder device record.
// Must be called with a.mu held.
func (a *IDAssigner) allocateNew() (int, error) {
	// Get next ID.
	var maxID sql.NullInt64
	err := a.repo.QueryRow(`SELECT MAX(assigned_id) FROM devices`).Scan(&maxID)
	if err != nil {
		return 0, err
	}
	nextID := 1
	if maxID.Valid {
		nextID = int(maxID.Int64) + 1
	}

	// Immediately insert a placeholder to claim this ID atomically.
	now := time.Now().Format(time.RFC3339)
	err = a.repo.Exec(
		`INSERT INTO devices (assigned_id, mac_address, hostname, first_seen, last_seen, updated_at)
		 VALUES (?, '', 'pending', ?, ?, ?)`,
		nextID, now, now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("insert placeholder for id %d: %w", nextID, err)
	}

	return nextID, nil
}

// ValidateReuse checks whether a previously assigned ID can be reused on reconnection.
func (a *IDAssigner) ValidateReuse(storedID int) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	dev, err := a.repo.GetByAssignedID(storedID)
	if err != nil {
		return false, nil
	}
	return !dev.Connected, nil
}
