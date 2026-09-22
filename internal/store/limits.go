package store

import "fmt"

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
