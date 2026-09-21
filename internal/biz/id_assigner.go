package biz

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
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

func (a *IDAssigner) AssignOrReuse(macAddress string, storedID *int) (assignedID int, existingDevice *model.Device, err error) {
	id, dev, _, err := a.assignOrReuse(macAddress, storedID)
	return id, dev, err
}

// AssignOrReuseTracked is AssignOrReuse plus the "newly allocated" flag.
func (a *IDAssigner) AssignOrReuseTracked(macAddress string, storedID *int) (assignedID int, existingDevice *model.Device, newlyAllocated bool, err error) {
	return a.assignOrReuse(macAddress, storedID)
}

func (a *IDAssigner) assignOrReuse(macAddress string, storedID *int) (assignedID int, existingDevice *model.Device, newlyAllocated bool, err error) {
	return a.AssignIdentity(macAddress, "", storedID)
}

func (a *IDAssigner) AssignIdentity(macAddress, identityKey string, storedID *int) (assignedID int, existingDevice *model.Device, newlyAllocated bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	macAddress = strings.ToLower(strings.TrimSpace(macAddress))
	if macAddress != "" {
		mac, parseErr := net.ParseMAC(macAddress)
		if parseErr != nil || len(mac) != 6 || mac[0]&1 != 0 || mac.String() == "00:00:00:00:00:00" {
			return 0, nil, false, fmt.Errorf("invalid device MAC")
		}
		macAddress = mac.String()
	}
	identityKey = strings.TrimSpace(identityKey)
	if len(identityKey) > 128 || (identityKey == "" && macAddress == "") {
		return 0, nil, false, fmt.Errorf("device identity is required; refusing numeric ID-only registration")
	}
	if identityKey == "" {
		identityKey = "mac:" + macAddress
	}
	var existingID int
	err = a.repo.QueryRow(`SELECT assigned_id FROM devices WHERE identity_key=?`, identityKey).Scan(&existingID)
	if err == nil {
		device, getErr := a.repo.GetByAssignedID(existingID)
		return existingID, device, false, getErr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, nil, false, err
	}
	if macAddress != "" {
		var matches int
		err = a.repo.QueryRow(`SELECT COUNT(*), COALESCE(MIN(assigned_id),0) FROM devices WHERE lower(mac_address)=? AND identity_key=''`, macAddress).Scan(&matches, &existingID)
		if err != nil {
			return 0, nil, false, err
		}
		if matches == 1 {
			if err = a.repo.Exec(`UPDATE devices SET identity_key=? WHERE assigned_id=?`, identityKey, existingID); err != nil {
				return 0, nil, false, err
			}
			device, getErr := a.repo.GetByAssignedID(existingID)
			return existingID, device, false, getErr
		}
	}
	id, err := a.allocateNew(macAddress, identityKey)
	if err != nil {
		return 0, nil, false, fmt.Errorf("allocate id: %w", err)
	}
	log.Printf("[id] assigned new ID %d", id)
	return id, nil, true, nil
}

// allocateNew atomically gets the next ID and inserts a placeholder device record.
// Must be called with a.mu held.
func (a *IDAssigner) allocateNew(macAddress, identityKey string) (int, error) {
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
	var start int
	_ = a.repo.QueryRow(`SELECT CAST(value AS INTEGER) FROM settings WHERE key='device_id_start'`).Scan(&start)
	if start > nextID {
		nextID = start
	}

	// Immediately insert a placeholder to claim this ID atomically.
	now := time.Now().Format(time.RFC3339)
	err = a.repo.Exec(
		`INSERT INTO devices (assigned_id, mac_address, identity_key, hostname, first_seen, last_seen, updated_at)
		 VALUES (?, ?, ?, 'pending', ?, ?, ?)`,
		nextID, macAddress, identityKey, now, now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("insert placeholder for id %d: %w", nextID, err)
	}

	return nextID, nil
}
