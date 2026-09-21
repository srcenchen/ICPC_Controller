package data

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// NewDB opens (or creates) the SQLite database at the given path and runs migrations.
func NewDB(path string) (*sql.DB, error) {
	// Use WAL mode for better concurrency with multiple readers.
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout=5000"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite WAL mode supports concurrent readers. Allow enough connections for
	// HTTP API, WebSocket updates, and TCP client communication to operate in parallel.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return db, nil
}

// migrate creates tables if they don't exist.
func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS cluster_rooms (id TEXT PRIMARY KEY, name TEXT NOT NULL, snapshot TEXT NOT NULL DEFAULT '{}', last_seen TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS cluster_jobs (id TEXT PRIMARY KEY, room_id TEXT NOT NULL, request TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'queued', response TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, sent_at TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS cluster_receipts (id TEXT PRIMARY KEY, status TEXT NOT NULL, response TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS devices (
			id                  INTEGER PRIMARY KEY AUTOINCREMENT,
			assigned_id         INTEGER NOT NULL UNIQUE,
			mac_address         TEXT    NOT NULL DEFAULT '',
			hostname            TEXT    NOT NULL DEFAULT '',
			username            TEXT    NOT NULL DEFAULT '',
			os_name             TEXT    NOT NULL DEFAULT '',
			os_version          TEXT    NOT NULL DEFAULT '',
			os_pretty_name      TEXT    NOT NULL DEFAULT '',
			kernel_release      TEXT    NOT NULL DEFAULT '',
			kernel_arch         TEXT    NOT NULL DEFAULT '',
			cpu_model           TEXT    NOT NULL DEFAULT '',
			cpu_physical_cores  INTEGER NOT NULL DEFAULT 0,
			cpu_logical_cores   INTEGER NOT NULL DEFAULT 0,
			cpu_packages        INTEGER NOT NULL DEFAULT 0,
			gpu_info            TEXT    NOT NULL DEFAULT '[]',
			memory_total        INTEGER NOT NULL DEFAULT 0,
			memory_used         INTEGER NOT NULL DEFAULT 0,
			disk_info           TEXT    NOT NULL DEFAULT '[]',
			local_ip            TEXT    NOT NULL DEFAULT '[]',
			de_name             TEXT    NOT NULL DEFAULT '',
			wm_name             TEXT    NOT NULL DEFAULT '',
			shell               TEXT    NOT NULL DEFAULT '',
			terminal            TEXT    NOT NULL DEFAULT '',
			display_info        TEXT    NOT NULL DEFAULT '[]',
			uptime              INTEGER NOT NULL DEFAULT 0,
			packages            TEXT    NOT NULL DEFAULT '{}',
			fastfetch_raw       TEXT    NOT NULL DEFAULT '[]',
			connected           INTEGER NOT NULL DEFAULT 0,
			last_seen           TEXT    NOT NULL DEFAULT '',
			first_seen          TEXT    NOT NULL DEFAULT (datetime('now')),
			updated_at          TEXT    NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_devices_assigned_id ON devices(assigned_id)`,
		`CREATE INDEX IF NOT EXISTS idx_devices_mac ON devices(mac_address)`,
		`CREATE INDEX IF NOT EXISTS idx_devices_connected ON devices(connected)`,
		`CREATE TABLE IF NOT EXISTS command_log (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			parent_id       INTEGER,
			target_type     TEXT    NOT NULL,
			target_id       INTEGER,
			command         TEXT    NOT NULL,
			status          TEXT    NOT NULL DEFAULT 'pending',
			output          TEXT    NOT NULL DEFAULT '',
			error_output    TEXT    NOT NULL DEFAULT '',
			executed_by     TEXT    NOT NULL DEFAULT '',
			created_at      TEXT    NOT NULL DEFAULT (datetime('now')),
			dispatched_at   TEXT,
			completed_at    TEXT,
			duration_ms     INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_command_log_target ON command_log(target_id)`,
		`CREATE INDEX IF NOT EXISTS idx_command_log_status ON command_log(status)`,
		`CREATE INDEX IF NOT EXISTS idx_command_log_created ON command_log(created_at)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT ''
		)`,
		// Broadcast system tables.
		`CREATE TABLE IF NOT EXISTS broadcast_config (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS broadcast_fonts (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			name          TEXT NOT NULL,
			filename      TEXT NOT NULL UNIQUE,
			original_name TEXT NOT NULL,
			format        TEXT NOT NULL,
			uploaded_at   TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS broadcast_pages (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			mode        TEXT NOT NULL,
			title       TEXT NOT NULL DEFAULT '',
			sort_order  INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 10000,
			bg_color    TEXT NOT NULL DEFAULT '#000000',
			transition  TEXT NOT NULL DEFAULT 'fade'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_broadcast_pages_mode ON broadcast_pages(mode)`,
		`CREATE TABLE IF NOT EXISTS broadcast_items (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			page_id       INTEGER NOT NULL,
			item_type     TEXT NOT NULL,
			content       TEXT NOT NULL DEFAULT '',
			pos_x         REAL NOT NULL DEFAULT 0,
			pos_y         REAL NOT NULL DEFAULT 0,
			width         REAL NOT NULL DEFAULT 20,
			height        REAL NOT NULL DEFAULT 10,
			font_size     TEXT NOT NULL DEFAULT '48px',
			font_color    TEXT NOT NULL DEFAULT '#ffffff',
			font_weight   TEXT NOT NULL DEFAULT 'normal',
			text_align    TEXT NOT NULL DEFAULT 'center',
			bg_color      TEXT NOT NULL DEFAULT 'transparent',
			border_radius TEXT NOT NULL DEFAULT '0',
			animation     TEXT NOT NULL DEFAULT '',
			z_index       INTEGER NOT NULL DEFAULT 0,
			extra_json    TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_broadcast_items_page ON broadcast_items(page_id)`,
		`CREATE TABLE IF NOT EXISTS device_events (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			assigned_id INTEGER NOT NULL,
			event       TEXT    NOT NULL,
			detail      TEXT    NOT NULL DEFAULT '',
			at          TEXT    NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_device_events_device ON device_events(assigned_id)`,
		`CREATE INDEX IF NOT EXISTS idx_device_events_at ON device_events(at)`,
		`CREATE TABLE IF NOT EXISTS power_schedules (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			action      TEXT    NOT NULL,
			run_at      TEXT    NOT NULL,
			target_type TEXT    NOT NULL DEFAULT 'all',
			target_ids  TEXT    NOT NULL DEFAULT '[]',
			status      TEXT    NOT NULL DEFAULT 'pending',
			note        TEXT    NOT NULL DEFAULT '',
			created_by  TEXT    NOT NULL DEFAULT '',
			created_at  TEXT    NOT NULL DEFAULT (datetime('now')),
			fired_at    TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_power_schedules_status ON power_schedules(status, run_at)`,
		// Offline operation snapshots (queues) applied to late-joining devices/rooms.
		`CREATE TABLE IF NOT EXISTS operation_snapshots (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			name       TEXT    NOT NULL,
			scope      TEXT    NOT NULL DEFAULT 'local',
			scope_id   TEXT    NOT NULL DEFAULT '',
			active     INTEGER NOT NULL DEFAULT 1,
			created_at TEXT    NOT NULL,
			ended_at   TEXT    NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_operation_snapshots_scope ON operation_snapshots(scope, active)`,
		`CREATE TABLE IF NOT EXISTS snapshot_ops (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			snapshot_id INTEGER NOT NULL,
			kind        TEXT    NOT NULL,
			payload     TEXT    NOT NULL DEFAULT '{}',
			summary     TEXT    NOT NULL DEFAULT '',
			created_at  TEXT    NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_snapshot_ops_snapshot ON snapshot_ops(snapshot_id)`,
		`CREATE TABLE IF NOT EXISTS snapshot_deliveries (
			op_id        INTEGER NOT NULL,
			target_kind  TEXT    NOT NULL,
			target_id    TEXT    NOT NULL,
			delivered_at TEXT    NOT NULL,
			PRIMARY KEY(op_id, target_kind, target_id)
		)`,
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec migration: %w\n%s", err, stmt)
		}
	}

	// Column additions for databases created by older versions. Checked against
	// the live schema rather than relying on ALTER TABLE error strings.
	addColumns := []struct{ table, column, def string }{
		{"devices", "mac_address", "TEXT NOT NULL DEFAULT ''"},
		{"devices", "identity_key", "TEXT NOT NULL DEFAULT ''"},
		{"devices", "checkin_status", "INTEGER NOT NULL DEFAULT 0"},
		{"devices", "student_name", "TEXT NOT NULL DEFAULT ''"},
		{"devices", "student_num", "TEXT NOT NULL DEFAULT ''"},
		{"devices", "checkin_time", "TEXT NOT NULL DEFAULT ''"},
		{"devices", "checkout_time", "TEXT NOT NULL DEFAULT ''"},
		// Health metrics reported by the client heartbeat.
		{"devices", "cpu_pct", "REAL NOT NULL DEFAULT -1"},
		{"devices", "mem_pct", "REAL NOT NULL DEFAULT -1"},
		{"devices", "disk_pct", "REAL NOT NULL DEFAULT -1"},
		{"devices", "temp_c", "REAL NOT NULL DEFAULT -1"},
		{"devices", "load1", "REAL NOT NULL DEFAULT -1"},
		{"devices", "health_at", "TEXT NOT NULL DEFAULT ''"},
		{"devices", "client_version", "TEXT NOT NULL DEFAULT ''"},
		{"command_log", "parent_id", "INTEGER"},
	}
	for _, ac := range addColumns {
		if err := addColumnIfMissing(db, ac.table, ac.column, ac.def); err != nil {
			return err
		}
	}

	// Indexes that depend on migrated columns.
	postIndexes := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_devices_identity ON devices(identity_key) WHERE identity_key != ''`,
		`CREATE INDEX IF NOT EXISTS idx_devices_checkin_status ON devices(checkin_status)`,
	}
	for _, stmt := range postIndexes {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec migration: %w\n%s", err, stmt)
		}
	}
	return nil
}

// addColumnIfMissing adds a column only when the table lacks it, using
// PRAGMA table_info instead of matching driver error strings.
func addColumnIfMissing(db *sql.DB, table, column, definition string) error {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return fmt.Errorf("table_info %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid        int
			name       string
			ctype      string
			notNull    int
			dfltValue  sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &primaryKey); err != nil {
			return fmt.Errorf("scan table_info %s: %w", table, err)
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate table_info %s: %w", table, err)
	}
	stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition)
	if _, err := db.Exec(stmt); err != nil {
		return fmt.Errorf("add column %s.%s: %w", table, column, err)
	}
	return nil
}
