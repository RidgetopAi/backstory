package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Sync attempt results, as stored in sync_attempts.result.
const (
	SyncResultOK          = "ok"
	SyncResultUnreachable = "unreachable"
	SyncResultError       = "error"
	SyncResultPaused      = "paused"
)

var (
	// ErrPairSelf is returned when a device is paired with itself.
	ErrPairSelf = errors.New("store: a device cannot be paired with itself")
	// ErrNoSuchPeer is returned when no paired peer matches a name or host.
	ErrNoSuchPeer = errors.New("store: no such paired peer")
)

// SyncPeerRow is one paired device: its devices row joined with sync_peers.
type SyncPeerRow struct {
	DeviceID   string
	Name       string
	SSHHost    string
	Enabled    bool
	PairedAt   time.Time
	PullCursor int64
	PushCursor int64
}

// Label is how the peer is named to a person: its device name.
func (p SyncPeerRow) Label() string {
	if p.Name != "" {
		return p.Name
	}
	return p.SSHHost
}

// SyncAttempt is one row of the append-only sync_attempts log.
type SyncAttempt struct {
	TS       time.Time
	Peer     string
	DeviceID string
	Result   string
	Detail   string
	Pulled   int
	Pushed   int
}

// PairSyncPeer records a peer device and enables it. A device already paired
// is re-enabled and renamed, and keeps its cursors.
func (s *Store) PairSyncPeer(deviceID, name, sshHost string) error {
	self, err := s.SelfDevice()
	if err != nil {
		return err
	}
	if deviceID == "" {
		return fmt.Errorf("store: pair: peer device id required")
	}
	if deviceID == self.ID {
		return ErrPairSelf
	}
	now := time.Now().UTC().UnixNano()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: pair: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO devices (id, name, is_self, first_seen) VALUES (?, ?, 0, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name`, deviceID, name, now); err != nil {
		return fmt.Errorf("store: pair: record device: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO sync_peers (device_id, ssh_host, enabled, paired_at) VALUES (?, ?, 1, ?)
		ON CONFLICT(device_id) DO UPDATE SET ssh_host = excluded.ssh_host, enabled = 1`, deviceID, sshHost, now); err != nil {
		return fmt.Errorf("store: pair: record peer: %w", err)
	}
	return tx.Commit()
}

const syncPeerSelect = `SELECT p.device_id, d.name, COALESCE(p.ssh_host, ''), p.enabled, COALESCE(p.paired_at, 0), p.pull_cursor, p.push_cursor
	FROM sync_peers p JOIN devices d ON d.id = p.device_id`

func scanSyncPeer(sc interface{ Scan(...any) error }) (SyncPeerRow, error) {
	var p SyncPeerRow
	var en int
	var paired int64
	if err := sc.Scan(&p.DeviceID, &p.Name, &p.SSHHost, &en, &paired, &p.PullCursor, &p.PushCursor); err != nil {
		return SyncPeerRow{}, err
	}
	p.Enabled = en == 1
	if paired != 0 {
		p.PairedAt = time.Unix(0, paired).UTC()
	}
	return p, nil
}

// SyncPeers lists every paired peer, enabled or not, oldest pairing first.
func (s *Store) SyncPeers() ([]SyncPeerRow, error) {
	rows, err := s.db.Query(syncPeerSelect + ` ORDER BY p.paired_at, d.name`)
	if err != nil {
		return nil, fmt.Errorf("store: sync peers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SyncPeerRow
	for rows.Next() {
		p, err := scanSyncPeer(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan sync peer: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// FindSyncPeer returns the paired peer whose device name, ssh host or device
// id equals ref. A ref matching several peers is an error naming them.
func (s *Store) FindSyncPeer(ref string) (SyncPeerRow, error) {
	peers, err := s.SyncPeers()
	if err != nil {
		return SyncPeerRow{}, err
	}
	var hits []SyncPeerRow
	for _, p := range peers {
		if p.Name == ref || p.SSHHost == ref || p.DeviceID == ref {
			hits = append(hits, p)
		}
	}
	switch len(hits) {
	case 0:
		return SyncPeerRow{}, fmt.Errorf("%w: %q", ErrNoSuchPeer, ref)
	case 1:
		return hits[0], nil
	}
	var names []string
	for _, p := range hits {
		names = append(names, p.Name+" ("+p.SSHHost+")")
	}
	return SyncPeerRow{}, fmt.Errorf("store: %q matches several peers: %s", ref, strings.Join(names, ", "))
}

// DisableSyncPeer stops syncing with a peer. Its imported records stay.
func (s *Store) DisableSyncPeer(deviceID string) error {
	res, err := s.db.Exec(`UPDATE sync_peers SET enabled = 0 WHERE device_id = ?`, deviceID)
	if err != nil {
		return fmt.Errorf("store: disable peer: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchPeer
	}
	return nil
}

// SetSyncCursors persists a peer's two feed positions. Call it only with
// positions whose batches have committed.
func (s *Store) SetSyncCursors(deviceID string, pull, push int64) error {
	if _, err := s.db.Exec(`UPDATE sync_peers SET pull_cursor = ?, push_cursor = ? WHERE device_id = ?`, pull, push, deviceID); err != nil {
		return fmt.Errorf("store: set sync cursors: %w", err)
	}
	return nil
}

// AppendSyncAttempt appends one row to the sync_attempts log.
func (s *Store) AppendSyncAttempt(a SyncAttempt) error {
	ts := a.TS
	if ts.IsZero() {
		ts = time.Now()
	}
	var dev any
	if a.DeviceID != "" {
		dev = a.DeviceID
	}
	if _, err := s.db.Exec(`INSERT INTO sync_attempts (ts, peer, device_id, result, detail, pulled, pushed) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ts.UTC().UnixNano(), a.Peer, dev, a.Result, a.Detail, a.Pulled, a.Pushed); err != nil {
		return fmt.Errorf("store: append sync attempt: %w", err)
	}
	return nil
}

// SyncAttempts returns the attempts for one peer label, newest first (limit
// <= 0 means all).
func (s *Store) SyncAttempts(peer string, limit int) ([]SyncAttempt, error) {
	return s.syncAttemptsFor("", peer, limit)
}

// syncAttemptsFor matches by device id as well as label, so a renamed peer
// keeps its history; an empty deviceID matches nothing.
func (s *Store) syncAttemptsFor(deviceID, label string, limit int) ([]SyncAttempt, error) {
	if limit <= 0 {
		limit = -1 // sqlite: no limit
	}
	rows, err := s.db.Query(`SELECT ts, peer, COALESCE(device_id, ''), result, detail, pulled, pushed FROM sync_attempts
		WHERE device_id = ? OR peer = ? ORDER BY id DESC LIMIT ?`, deviceID, label, limit)
	if err != nil {
		return nil, fmt.Errorf("store: sync attempts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SyncAttempt
	for rows.Next() {
		var a SyncAttempt
		var ts int64
		if err := rows.Scan(&ts, &a.Peer, &a.DeviceID, &a.Result, &a.Detail, &a.Pulled, &a.Pushed); err != nil {
			return nil, err
		}
		a.TS = time.Unix(0, ts).UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

// SyncPeerStatus is a peer's last attempt and last success, for `status`.
type SyncPeerStatus struct {
	Peer       SyncPeerRow
	HasAttempt bool
	Last       SyncAttempt
	HasOK      bool
	LastOK     time.Time
}

// SyncStatus returns one entry per paired peer.
func (s *Store) SyncStatus() ([]SyncPeerStatus, error) {
	peers, err := s.SyncPeers()
	if err != nil {
		return nil, err
	}
	var out []SyncPeerStatus
	for _, p := range peers {
		st := SyncPeerStatus{Peer: p}
		last, err := s.syncAttemptsFor(p.DeviceID, p.Label(), 1)
		if err != nil {
			return nil, err
		}
		if len(last) > 0 {
			st.HasAttempt, st.Last = true, last[0]
		}
		var ts sql.NullInt64
		if err := s.db.QueryRow(`SELECT MAX(ts) FROM sync_attempts WHERE (device_id = ? OR peer = ?) AND result = 'ok'`, p.DeviceID, p.Label()).Scan(&ts); err != nil {
			return nil, err
		}
		if ts.Valid {
			st.HasOK, st.LastOK = true, time.Unix(0, ts.Int64).UTC()
		}
		out = append(out, st)
	}
	return out, nil
}
