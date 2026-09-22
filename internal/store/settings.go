package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// SetSetting inserts or updates a setting. Settings are a human-only power
// (AGENT-CONTRACT.md §The never-list, item 5); the socket API this backs
// exposes no settings write to agents.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("store: set setting %s: %w", key, err)
	}
	return nil
}

// GetSetting returns a setting's value and whether it was present.
func (s *Store) GetSetting(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: get setting %s: %w", key, err)
	}
	return v, true, nil
}
