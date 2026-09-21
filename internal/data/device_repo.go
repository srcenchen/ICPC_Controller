package data

import (
	"database/sql"
	"fmt"
	"time"

	"ICPCRemoteControl/internal/model"
)

// DeviceRepo handles CRUD operations for the devices table.
type DeviceRepo struct {
	db *sql.DB
}

// NewDeviceRepo creates a new DeviceRepo.
func NewDeviceRepo(db *sql.DB) *DeviceRepo {
	return &DeviceRepo{db: db}
}

// QueryRow exposes the underlying DB for atomic ID allocation.
func (r *DeviceRepo) QueryRow(query string, args ...interface{}) *sql.Row {
	return r.db.QueryRow(query, args...)
}

// Exec exposes the underlying DB for atomic ID allocation.
func (r *DeviceRepo) Exec(query string, args ...interface{}) error {
	_, err := r.db.Exec(query, args...)
	return err
}

// Create inserts a new device record. Returns the device with its DB-assigned ID.
func (r *DeviceRepo) Create(d *model.Device) error {
	now := time.Now().Format(time.RFC3339)
	d.FirstSeen = now
	d.LastSeen = now
	d.UpdatedAt = now

	query := `INSERT INTO devices (
		assigned_id, mac_address, hostname, username, os_name, os_version, os_pretty_name,
		kernel_release, kernel_arch, cpu_model, cpu_physical_cores, cpu_logical_cores,
		cpu_packages, gpu_info, memory_total, memory_used, disk_info, local_ip,
		de_name, wm_name, shell, terminal, display_info, uptime, packages,
		fastfetch_raw, connected, last_seen, first_seen, updated_at,
		checkin_status, student_name, student_num, checkin_time, checkout_time
	) VALUES (
		?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
	)`

	result, err := r.db.Exec(query,
		d.AssignedID, d.MacAddress, d.Hostname, d.Username, d.OSName, d.OSVersion, d.OSPrettyName,
		d.KernelRelease, d.KernelArch, d.CPUModel, d.CPUPhysicalCores, d.CPULogicalCores,
		d.CPUPackages, d.GPUInfo, d.MemoryTotal, d.MemoryUsed, d.DiskInfo, d.LocalIP,
		d.DEName, d.WMName, d.Shell, d.Terminal, d.DisplayInfo, d.Uptime, d.Packages,
		d.FastfetchRaw, boolToInt(d.Connected), d.LastSeen, d.FirstSeen, d.UpdatedAt,
		d.CheckinStatus, d.StudentName, d.StudentNum, d.CheckinTime, d.CheckoutTime,
	)
	if err != nil {
		return fmt.Errorf("insert device: %w", err)
	}
	id, _ := result.LastInsertId()
	d.ID = id
	return nil
}

// Update updates an existing device record.
func (r *DeviceRepo) Update(d *model.Device) error {
	now := time.Now().Format(time.RFC3339)
	d.UpdatedAt = now
	if d.Connected {
		d.LastSeen = now
	} else if d.LastSeen == "" {
		var existingLastSeen string
		_ = r.db.QueryRow(`SELECT last_seen FROM devices WHERE assigned_id=?`, d.AssignedID).Scan(&existingLastSeen)
		if existingLastSeen != "" {
			d.LastSeen = existingLastSeen
		} else {
			d.LastSeen = now
		}
	}

	query := `UPDATE devices SET
		mac_address=?, hostname=?, username=?, os_name=?, os_version=?, os_pretty_name=?,
		kernel_release=?, kernel_arch=?, cpu_model=?, cpu_physical_cores=?, cpu_logical_cores=?,
		cpu_packages=?, gpu_info=?, memory_total=?, memory_used=?, disk_info=?, local_ip=?,
		de_name=?, wm_name=?, shell=?, terminal=?, display_info=?, uptime=?, packages=?,
		fastfetch_raw=?, connected=?, last_seen=?, updated_at=?,
		checkin_status=?, student_name=?, student_num=?, checkin_time=?, checkout_time=?
	WHERE assigned_id=?`

	_, err := r.db.Exec(query,
		d.MacAddress, d.Hostname, d.Username, d.OSName, d.OSVersion, d.OSPrettyName,
		d.KernelRelease, d.KernelArch, d.CPUModel, d.CPUPhysicalCores, d.CPULogicalCores,
		d.CPUPackages, d.GPUInfo, d.MemoryTotal, d.MemoryUsed, d.DiskInfo, d.LocalIP,
		d.DEName, d.WMName, d.Shell, d.Terminal, d.DisplayInfo, d.Uptime, d.Packages,
		d.FastfetchRaw, boolToInt(d.Connected), d.LastSeen, d.UpdatedAt,
		d.CheckinStatus, d.StudentName, d.StudentNum, d.CheckinTime, d.CheckoutTime,
		d.AssignedID,
	)
	if err != nil {
		return fmt.Errorf("update device: %w", err)
	}
	return nil
}

// GetByAssignedID retrieves a device by its assigned ID.
func (r *DeviceRepo) GetByAssignedID(assignedID int) (*model.Device, error) {
	query := `SELECT
		id, assigned_id, mac_address, hostname, username, os_name, os_version, os_pretty_name,
		kernel_release, kernel_arch, cpu_model, cpu_physical_cores, cpu_logical_cores,
		cpu_packages, gpu_info, memory_total, memory_used, disk_info, local_ip,
		de_name, wm_name, shell, terminal, display_info, uptime, packages,
		fastfetch_raw, connected, last_seen, first_seen, updated_at,
		checkin_status, student_name, student_num, checkin_time, checkout_time,
		cpu_pct, mem_pct, disk_pct, temp_c, load1, health_at, client_version
	FROM devices WHERE assigned_id=?`

	d := &model.Device{}
	var connected int
	err := r.db.QueryRow(query, assignedID).Scan(
		&d.ID, &d.AssignedID, &d.MacAddress, &d.Hostname, &d.Username, &d.OSName, &d.OSVersion, &d.OSPrettyName,
		&d.KernelRelease, &d.KernelArch, &d.CPUModel, &d.CPUPhysicalCores, &d.CPULogicalCores,
		&d.CPUPackages, &d.GPUInfo, &d.MemoryTotal, &d.MemoryUsed, &d.DiskInfo, &d.LocalIP,
		&d.DEName, &d.WMName, &d.Shell, &d.Terminal, &d.DisplayInfo, &d.Uptime, &d.Packages,
		&d.FastfetchRaw, &connected, &d.LastSeen, &d.FirstSeen, &d.UpdatedAt,
		&d.CheckinStatus, &d.StudentName, &d.StudentNum, &d.CheckinTime, &d.CheckoutTime,
		&d.CPUPct, &d.MemPct, &d.DiskPct, &d.TempC, &d.Load1, &d.HealthAt, &d.ClientVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("get device by assigned_id %d: %w", assignedID, err)
	}
	d.Connected = connected != 0
	return d, nil
}

// GetByMacAddress retrieves a device by its MAC address.
func (r *DeviceRepo) GetByMacAddress(mac string) (*model.Device, error) {
	if mac == "" {
		return nil, sql.ErrNoRows
	}
	query := `SELECT
		id, assigned_id, mac_address, hostname, username, os_name, os_version, os_pretty_name,
		kernel_release, kernel_arch, cpu_model, cpu_physical_cores, cpu_logical_cores,
		cpu_packages, gpu_info, memory_total, memory_used, disk_info, local_ip,
		de_name, wm_name, shell, terminal, display_info, uptime, packages,
		fastfetch_raw, connected, last_seen, first_seen, updated_at,
		checkin_status, student_name, student_num, checkin_time, checkout_time,
		cpu_pct, mem_pct, disk_pct, temp_c, load1, health_at, client_version
	FROM devices WHERE mac_address=?`

	d := &model.Device{}
	var connected int
	err := r.db.QueryRow(query, mac).Scan(
		&d.ID, &d.AssignedID, &d.MacAddress, &d.Hostname, &d.Username, &d.OSName, &d.OSVersion, &d.OSPrettyName,
		&d.KernelRelease, &d.KernelArch, &d.CPUModel, &d.CPUPhysicalCores, &d.CPULogicalCores,
		&d.CPUPackages, &d.GPUInfo, &d.MemoryTotal, &d.MemoryUsed, &d.DiskInfo, &d.LocalIP,
		&d.DEName, &d.WMName, &d.Shell, &d.Terminal, &d.DisplayInfo, &d.Uptime, &d.Packages,
		&d.FastfetchRaw, &connected, &d.LastSeen, &d.FirstSeen, &d.UpdatedAt,
		&d.CheckinStatus, &d.StudentName, &d.StudentNum, &d.CheckinTime, &d.CheckoutTime,
		&d.CPUPct, &d.MemPct, &d.DiskPct, &d.TempC, &d.Load1, &d.HealthAt, &d.ClientVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("get device by mac %s: %w", mac, err)
	}
	d.Connected = connected != 0
	return d, nil
}

// GetAll returns summaries of all devices.
func (r *DeviceRepo) GetAll() ([]model.DeviceSummary, error) {
	query := `SELECT assigned_id, hostname, username, os_name, cpu_model,
		memory_total, memory_used, local_ip, connected, last_seen,
		checkin_status, student_name, student_num,
		cpu_pct, mem_pct, disk_pct, temp_c, load1, health_at
	FROM devices ORDER BY assigned_id`

	rows, err := r.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("get all devices: %w", err)
	}
	defer rows.Close()

	var devices []model.DeviceSummary
	for rows.Next() {
		var d model.DeviceSummary
		var connected int
		if err := rows.Scan(&d.AssignedID, &d.Hostname, &d.Username, &d.OSName,
			&d.CPUModel, &d.MemoryTotal, &d.MemoryUsed, &d.LocalIP, &connected, &d.LastSeen,
			&d.CheckinStatus, &d.StudentName, &d.StudentNum,
			&d.CPUPct, &d.MemPct, &d.DiskPct, &d.TempC, &d.Load1, &d.HealthAt); err != nil {
			return nil, fmt.Errorf("scan device summary: %w", err)
		}
		d.Connected = connected != 0
		devices = append(devices, d)
	}
	if devices == nil {
		devices = []model.DeviceSummary{}
	}
	return devices, rows.Err()
}

// GetAllFull returns all devices with all fields.
func (r *DeviceRepo) GetAllFull() ([]model.Device, error) {
	query := `SELECT id, assigned_id, mac_address, hostname, username, os_name, os_version, os_pretty_name,
		kernel_release, kernel_arch, cpu_model, cpu_physical_cores, cpu_logical_cores,
		cpu_packages, gpu_info, memory_total, memory_used, disk_info, local_ip,
		de_name, wm_name, shell, terminal, display_info, uptime, packages,
		fastfetch_raw, connected, last_seen, first_seen, updated_at,
		checkin_status, student_name, student_num, checkin_time, checkout_time,
		cpu_pct, mem_pct, disk_pct, temp_c, load1, health_at, client_version
		FROM devices ORDER BY assigned_id`

	rows, err := r.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("get all full: %w", err)
	}
	defer rows.Close()

	var devices []model.Device
	for rows.Next() {
		var d model.Device
		var connected int
		err := rows.Scan(
			&d.ID, &d.AssignedID, &d.MacAddress, &d.Hostname, &d.Username, &d.OSName, &d.OSVersion, &d.OSPrettyName,
			&d.KernelRelease, &d.KernelArch, &d.CPUModel, &d.CPUPhysicalCores, &d.CPULogicalCores,
			&d.CPUPackages, &d.GPUInfo, &d.MemoryTotal, &d.MemoryUsed, &d.DiskInfo, &d.LocalIP,
			&d.DEName, &d.WMName, &d.Shell, &d.Terminal, &d.DisplayInfo, &d.Uptime, &d.Packages,
			&d.FastfetchRaw, &connected, &d.LastSeen, &d.FirstSeen, &d.UpdatedAt,
			&d.CheckinStatus, &d.StudentName, &d.StudentNum, &d.CheckinTime, &d.CheckoutTime,
			&d.CPUPct, &d.MemPct, &d.DiskPct, &d.TempC, &d.Load1, &d.HealthAt, &d.ClientVersion,
			&d.CPUPct, &d.MemPct, &d.DiskPct, &d.TempC, &d.Load1, &d.HealthAt, &d.ClientVersion,
		)
		if err != nil {
			return nil, fmt.Errorf("scan full device: %w", err)
		}
		d.Connected = connected != 0
		devices = append(devices, d)
	}
	if devices == nil {
		devices = []model.Device{}
	}
	return devices, rows.Err()
}

// UpdateConnected sets the connected status and last_seen time for a device.
func (r *DeviceRepo) UpdateConnected(assignedID int, connected bool) error {
	now := time.Now().Format(time.RFC3339)
	_, err := r.db.Exec(
		`UPDATE devices SET connected=?, last_seen=?, updated_at=? WHERE assigned_id=?`,
		boolToInt(connected), now, now, assignedID,
	)
	return err
}

// UpdateHealth stores the latest heartbeat health metrics. Values below zero
// mean "unsupported on this machine" and are stored as-is so the UI can tell
// missing sensors apart from a real 0%.
func (r *DeviceRepo) UpdateHealth(assignedID int, cpuPct, memPct, diskPct, tempC, load1 float64) error {
	now := time.Now().Format(time.RFC3339)
	_, err := r.db.Exec(
		`UPDATE devices SET cpu_pct=?, mem_pct=?, disk_pct=?, temp_c=?, load1=?, health_at=?
		 WHERE assigned_id=?`,
		cpuPct, memPct, diskPct, tempC, load1, now, assignedID,
	)
	return err
}

// UpdateClientVersion records the client build the device is running.
func (r *DeviceRepo) UpdateClientVersion(assignedID int, version string) error {
	_, err := r.db.Exec(`UPDATE devices SET client_version=? WHERE assigned_id=?`, version, assignedID)
	return err
}

// DeleteGhostRows removes placeholder rows left behind when a client registered
// but dropped before sending system_info (hostname 'pending', empty MAC).
func (r *DeviceRepo) DeleteGhostRows() (int64, error) {
	res, err := r.db.Exec(
		`DELETE FROM devices WHERE identity_key='' AND mac_address='' AND hostname IN ('pending','') AND connected=0`)
	if err != nil {
		return 0, fmt.Errorf("delete ghost rows: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Delete removes a device record by assigned ID.
func (r *DeviceRepo) Delete(assignedID int) error {
	_, err := r.db.Exec(`DELETE FROM devices WHERE assigned_id=?`, assignedID)
	return err
}

// MarkAllOffline sets all devices to offline. Called on server startup.
func (r *DeviceRepo) MarkAllOffline() error {
	_, err := r.db.Exec(`UPDATE devices SET connected=0`)
	return err
}

// ResetAll deletes all device records and resets the autoincrement counter.
func (r *DeviceRepo) ResetAll() error {
	if _, err := r.db.Exec(`DELETE FROM devices`); err != nil {
		return err
	}
	// Reset the autoincrement so IDs start from 1 again.
	if _, err := r.db.Exec(`DELETE FROM sqlite_sequence WHERE name='devices'`); err != nil {
		// Ignore if it doesn't exist.
		return nil
	}
	return nil
}

// GetNextAssignedID returns the next available assigned ID (MAX + 1).
func (r *DeviceRepo) GetNextAssignedID() (int, error) {
	var maxID sql.NullInt64
	err := r.db.QueryRow(`SELECT MAX(assigned_id) FROM devices`).Scan(&maxID)
	if err != nil {
		return 0, fmt.Errorf("get next assigned id: %w", err)
	}
	if !maxID.Valid {
		return 1, nil
	}
	return int(maxID.Int64) + 1, nil
}

// IsAssignedIDOnline checks if a device with the given assigned_id is currently connected.
func (r *DeviceRepo) IsAssignedIDOnline(assignedID int) (bool, error) {
	var connected int
	err := r.db.QueryRow(`SELECT connected FROM devices WHERE assigned_id=?`, assignedID).Scan(&connected)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return connected != 0, nil
}

// GetStats returns total and online device counts.
func (r *DeviceRepo) GetStats() (total int, online int, err error) {
	err = r.db.QueryRow(`SELECT COUNT(*) FROM devices`).Scan(&total)
	if err != nil {
		return 0, 0, err
	}
	err = r.db.QueryRow(`SELECT COUNT(*) FROM devices WHERE connected=1`).Scan(&online)
	if err != nil {
		return 0, 0, err
	}
	return total, online, nil
}

// GetCheckinAll returns all devices with check-in fields for the check-in management page.
func (r *DeviceRepo) GetCheckinAll() ([]model.DeviceSummary, error) {
	query := `SELECT assigned_id, hostname, username, os_name, cpu_model,
		memory_total, memory_used, local_ip, connected, last_seen,
		checkin_status, student_name, student_num,
		cpu_pct, mem_pct, disk_pct, temp_c, load1, health_at
	FROM devices ORDER BY assigned_id`

	rows, err := r.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("get checkin all: %w", err)
	}
	defer rows.Close()

	var devices []model.DeviceSummary
	for rows.Next() {
		var d model.DeviceSummary
		var connected int
		if err := rows.Scan(&d.AssignedID, &d.Hostname, &d.Username, &d.OSName,
			&d.CPUModel, &d.MemoryTotal, &d.MemoryUsed, &d.LocalIP, &connected, &d.LastSeen,
			&d.CheckinStatus, &d.StudentName, &d.StudentNum,
			&d.CPUPct, &d.MemPct, &d.DiskPct, &d.TempC, &d.Load1, &d.HealthAt); err != nil {
			return nil, fmt.Errorf("scan checkin device: %w", err)
		}
		d.Connected = connected != 0
		devices = append(devices, d)
	}
	if devices == nil {
		devices = []model.DeviceSummary{}
	}
	return devices, rows.Err()
}

// Checkin marks a device as checked in with student info.
func (r *DeviceRepo) Checkin(assignedID int, name, num string) error {
	now := time.Now().Format(time.RFC3339)
	_, err := r.db.Exec(
		`UPDATE devices SET checkin_status=1, student_name=?, student_num=?, checkin_time=?, updated_at=? WHERE assigned_id=?`,
		name, num, now, now, assignedID,
	)
	return err
}

// Checkout marks a device as checked out.
func (r *DeviceRepo) Checkout(assignedID int) error {
	now := time.Now().Format(time.RFC3339)
	_, err := r.db.Exec(
		`UPDATE devices SET checkin_status=2, checkout_time=?, updated_at=? WHERE assigned_id=?`,
		now, now, assignedID,
	)
	return err
}

// RestoreCheckout restores a checked-out device back to checked-in status (1).
func (r *DeviceRepo) RestoreCheckout(assignedID int) error {
	now := time.Now().Format(time.RFC3339)
	_, err := r.db.Exec(
		`UPDATE devices SET checkin_status=1, checkout_time='', updated_at=? WHERE assigned_id=?`,
		now, assignedID,
	)
	return err
}

// ResetCheckin resets a device's check-in status back to not checked in.
func (r *DeviceRepo) ResetCheckin(assignedID int) error {
	now := time.Now().Format(time.RFC3339)
	_, err := r.db.Exec(
		`UPDATE devices SET checkin_status=0, student_name='', student_num='', checkin_time='', checkout_time='', updated_at=? WHERE assigned_id=?`,
		now, assignedID,
	)
	return err
}

// ResetAllCheckin resets all devices' check-in status back to not checked in.
func (r *DeviceRepo) ResetAllCheckin() (int64, error) {
	now := time.Now().Format(time.RFC3339)
	result, err := r.db.Exec(
		`UPDATE devices SET checkin_status=0, student_name='', student_num='', checkin_time='', checkout_time='', updated_at=? WHERE checkin_status != 0`,
		now,
	)
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return n, nil
}

// SwapCheckin moves check-in info from one device to another.
func (r *DeviceRepo) SwapCheckin(oldAssignedID, newAssignedID int) error {
	// Read old device's check-in info.
	var name, num, checkinTime string
	err := r.db.QueryRow(
		`SELECT student_name, student_num, checkin_time FROM devices WHERE assigned_id=?`,
		oldAssignedID,
	).Scan(&name, &num, &checkinTime)
	if err != nil {
		return fmt.Errorf("read old checkin info: %w", err)
	}

	now := time.Now().Format(time.RFC3339)

	// Move check-in info to new device.
	_, err = r.db.Exec(
		`UPDATE devices SET checkin_status=1, student_name=?, student_num=?, checkin_time=?, checkout_time='', updated_at=? WHERE assigned_id=?`,
		name, num, checkinTime, now, newAssignedID,
	)
	if err != nil {
		return fmt.Errorf("move checkin to new device: %w", err)
	}

	// Reset old device.
	_, err = r.db.Exec(
		`UPDATE devices SET checkin_status=0, student_name='', student_num='', checkin_time='', checkout_time='', updated_at=? WHERE assigned_id=?`,
		now, oldAssignedID,
	)
	return err
}

// GetCheckinStats returns check-in statistics.
func (r *DeviceRepo) GetCheckinStats() (total, checkedIn, checkedOut int, err error) {
	err = r.db.QueryRow(`SELECT COUNT(*) FROM devices`).Scan(&total)
	if err != nil {
		return 0, 0, 0, err
	}
	err = r.db.QueryRow(`SELECT COUNT(*) FROM devices WHERE checkin_status=1`).Scan(&checkedIn)
	if err != nil {
		return 0, 0, 0, err
	}
	err = r.db.QueryRow(`SELECT COUNT(*) FROM devices WHERE checkin_status=2`).Scan(&checkedOut)
	if err != nil {
		return 0, 0, 0, err
	}
	return total, checkedIn, checkedOut, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
