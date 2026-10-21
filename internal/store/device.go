package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
)

// fallbackDeviceName names the self device when the hostname is unavailable.
const fallbackDeviceName = "this-device"

// Device is one row of the devices table.
type Device struct {
	ID        string
	Name      string
	IsSelf    bool
	FirstSeen time.Time
}

// ensureSelfDevice mints this store's self device on first open and leaves it
// alone afterwards: the id is a UUID minted once and never re-minted. The
// partial unique index devices_one_self makes a concurrent double-mint
// impossible; the losing Open simply finds the winner's row.
func (s *Store) ensureSelfDevice() error {
	if _, err := s.SelfDevice(); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	name, err := os.Hostname()
	if err != nil || name == "" {
		name = fallbackDeviceName
	}
	_, err = s.db.Exec(`INSERT INTO devices (id, name, is_self, first_seen) VALUES (?, ?, 1, ?)`,
		uuid.NewString(), name, time.Now().UTC().UnixNano())
	if err != nil {
		if _, serr := s.SelfDevice(); serr == nil {
			return nil // lost a race to another process's Open
		}
		return fmt.Errorf("store: mint self device: %w", err)
	}
	return nil
}

// SelfDevice returns this store's own device row.
func (s *Store) SelfDevice() (Device, error) {
	var d Device
	var first sql.NullInt64
	var self int
	err := s.db.QueryRow(`SELECT id, name, is_self, first_seen FROM devices WHERE is_self = 1`).
		Scan(&d.ID, &d.Name, &self, &first)
	if err != nil {
		return Device{}, err
	}
	d.IsSelf = self == 1
	if first.Valid {
		d.FirstSeen = time.Unix(0, first.Int64).UTC()
	}
	return d, nil
}
