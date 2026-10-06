package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

const zeroTimeNanos int64 = -6795364578871345152 // time.Time{}.UnixNano()

// TestMigrationV14RepairsZeroEndedAt (task 456410f0): a v9 store (the fixture builder runs SQL only, so it stops before the data-hook migrations) holding a
// session whose ended_at is Go's zero time is migrated so ended_at equals
// that session's latest valid event ts, or NULL when it has none; a sound
// session is untouched, and SchemaVersion has moved past 13.
func TestMigrationV14RepairsZeroEndedAt(t *testing.T) {
	if SchemaVersion < 14 {
		t.Fatalf("SchemaVersion = %d, want >= 14", SchemaVersion)
	}
	rawPath := filepath.Join(t.TempDir(), "v9fixture.db")
	db := buildFixtureThroughVersion(t, rawPath, 9)
	good := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC).UnixNano()
	later := good + int64(time.Minute)

	seedSession := func(id string, ended any) {
		if _, err := db.Exec(`INSERT INTO sessions (id, agent, cwd, started_at, ended_at, origin) VALUES (?, 'claude', '/x', ?, ?, 'backfilled')`,
			id, good, ended); err != nil {
			t.Fatalf("seed session %s: %v", id, err)
		}
	}
	seedEvent := func(sid string, ts int64) {
		if _, err := db.Exec(`INSERT INTO timeline_events (ts, kind, session_id, source, payload) VALUES (?, 'tool.use', ?, 'backfill', '{}')`, ts, sid); err != nil {
			t.Fatalf("seed event: %v", err)
		}
	}
	seedSession("s-with-events", zeroTimeNanos)
	seedEvent("s-with-events", good)
	seedEvent("s-with-events", later)
	seedEvent("s-with-events", zeroTimeNanos) // the invalid event is ignored, and left in place
	seedSession("s-no-events", zeroTimeNanos)
	seedSession("s-sound", later)
	seedSession("s-open", nil)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s := mustOpen(t, rawPath)
	if v, err := s.Version(); err != nil || v != SchemaVersion {
		t.Fatalf("Version() = %d, %v; want %d", v, err, SchemaVersion)
	}
	want := map[string]sql.NullInt64{
		"s-with-events": {Int64: later, Valid: true},
		"s-no-events":   {},
		"s-sound":       {Int64: later, Valid: true},
		"s-open":        {},
	}
	for id, w := range want {
		var got sql.NullInt64
		if err := s.DB().QueryRow(`SELECT ended_at FROM sessions WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != w {
			t.Errorf("%s ended_at = %v, want %v", id, got, w)
		}
	}
	// timeline_events is append-only: the pre-existing invalid row stays.
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM timeline_events WHERE ts <= 0`).Scan(&n); err != nil || n != 1 {
		t.Errorf("events with ts <= 0 = %d (err %v), want 1 left in place", n, err)
	}
}
