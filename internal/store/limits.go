package store

import (
	"fmt"
	"time"
)

// Named caps for InsertRecord (AGENT-CONTRACT.md §The never-list, item 6).
// These are the only numbers a write is checked against; nothing in the
// insert path is a bare literal.
const (
	// MaxRecordsPerSessionPerMinute is the per-session write-rate cap.
	MaxRecordsPerSessionPerMinute = 60
	// MaxRecordTextBytes is the largest records.text InsertRecord accepts.
	MaxRecordTextBytes = 16384
)

// Cap names a CapError names, so a caller (and a test) can tell which cap
// fired without parsing prose.
const (
	CapRecordsPerMinute = "records-per-minute"
	CapMaxTextBytes     = "max-text-bytes"
)

// CapError is returned when a write is refused for exceeding a named cap.
// No record is inserted when this error is returned.
type CapError struct {
	Cap   string
	Limit int
}

func (e *CapError) Error() string {
	return fmt.Sprintf("store: cap exceeded: %s (limit %d)", e.Cap, e.Limit)
}

// RemainingWriteBudget reports how many more records sessionID may insert in
// the current per-minute window before InsertRecord starts refusing writes
// with a CapRecordsPerMinute CapError. An empty sessionID (no session to rate
// limit against) reports the full cap, matching InsertRecord's own
// rate-limit skip for session-less writes.
func (s *Store) RemainingWriteBudget(sessionID string) (int, error) {
	if sessionID == "" {
		return MaxRecordsPerSessionPerMinute, nil
	}
	n, err := s.recentRecordCount(sessionID, time.Now().Add(-time.Minute))
	if err != nil {
		return 0, err
	}
	remaining := MaxRecordsPerSessionPerMinute - n
	if remaining < 0 {
		remaining = 0
	}
	return remaining, nil
}
