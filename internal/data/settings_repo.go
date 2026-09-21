package data

import (
	"database/sql"
	"fmt"
)

// SettingsRepo persists server settings as key-value pairs in SQLite.
type SettingsRepo struct {
	db *sql.DB
}

// NewSettingsRepo creates a new SettingsRepo.
func NewSettingsRepo(db *sql.DB) *SettingsRepo {
	return &SettingsRepo{db: db}
}

// Get returns the value for a given key, or empty string if not found.
func (r *SettingsRepo) Get(key string) (string, error) {
	var value string
	err := r.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get setting %q: %w", key, err)
	}
	return value, nil
}

// Set upserts a key-value pair.
func (r *SettingsRepo) Set(key, value string) error {
	_, err := r.db.Exec(
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value,
	)
	if err != nil {
		return fmt.Errorf("save setting %q: %w", key, err)
	}
	return nil
}

func (repo *SettingsRepo) SetMany(values map[string]string) error {
	transaction, err := repo.db.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	for key, value := range values {
		if _, err := transaction.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
			return err
		}
	}
	return transaction.Commit()
}
