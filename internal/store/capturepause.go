package store

import (
	"encoding/json"
	"fmt"
	"time"
)

// settingCapturePauses is the settings key holding every recorded capture
// pause window (SCHEMA.md invariant 8): a JSON array of CapturePause, oldest
// first. Backfill reads it so a session that started while capture was paused
// is never imported, even after capture is back on.
const settingCapturePauses = "capture_pauses"

// MaxCapturePauses bounds the stored window list: the oldest windows are
// dropped past it. A transcript older than the oldest retained window is
// long since backfilled, so the bound costs nothing in practice.
const MaxCapturePauses = 500

// CapturePause is one pause window. End is nil while capture is still off.
type CapturePause struct {
	Start time.Time  `json:"start"`
	End   *time.Time `json:"end,omitempty"`
}

// Contains reports whether ts falls inside the window (start inclusive, end
// exclusive; an open window extends forever).
func (p CapturePause) Contains(ts time.Time) bool {
	if ts.Before(p.Start) {
		return false
	}
	return p.End == nil || ts.Before(*p.End)
}

// CapturePauses returns every recorded pause window, oldest first.
func (s *Store) CapturePauses() ([]CapturePause, error) {
	raw, ok, err := s.GetSetting(settingCapturePauses)
	if err != nil || !ok || raw == "" {
		return nil, err
	}
	var ps []CapturePause
	if err := json.Unmarshal([]byte(raw), &ps); err != nil {
		return nil, fmt.Errorf("store: decode %s: %w", settingCapturePauses, err)
	}
	return ps, nil
}

func (s *Store) saveCapturePauses(ps []CapturePause) error {
	if len(ps) > MaxCapturePauses {
		ps = ps[len(ps)-MaxCapturePauses:]
	}
	b, err := json.Marshal(ps)
	if err != nil {
		return fmt.Errorf("store: encode %s: %w", settingCapturePauses, err)
	}
	return s.SetSetting(settingCapturePauses, string(b))
}

// BeginCapturePause records that capture went off at ts. Idempotent: if the
// latest window is still open nothing changes.
func (s *Store) BeginCapturePause(ts time.Time) error {
	ps, err := s.CapturePauses()
	if err != nil {
		return err
	}
	if n := len(ps); n > 0 && ps[n-1].End == nil {
		return nil
	}
	return s.saveCapturePauses(append(ps, CapturePause{Start: ts.UTC()}))
}

// EndCapturePause records that capture came back on at ts, closing the open
// window if there is one.
func (s *Store) EndCapturePause(ts time.Time) error {
	ps, err := s.CapturePauses()
	if err != nil {
		return err
	}
	n := len(ps)
	if n == 0 || ps[n-1].End != nil {
		return nil
	}
	end := ts.UTC()
	ps[n-1].End = &end
	return s.saveCapturePauses(ps)
}

// CapturePausedAt reports whether ts falls inside any recorded pause window.
func (s *Store) CapturePausedAt(ts time.Time) (bool, error) {
	ps, err := s.CapturePauses()
	if err != nil {
		return false, err
	}
	for _, p := range ps {
		if p.Contains(ts) {
			return true, nil
		}
	}
	return false, nil
}
