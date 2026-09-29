package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedPurgeSession(t *testing.T, s *Store, events int) string {
	t.Helper()
	id, err := s.StartSession(StartSessionParams{Agent: "claude", CWD: "/x", StartedAt: time.Now(), Origin: OriginBackfilled})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < events; i++ {
		if _, err := s.AppendEvent(Event{TS: time.Now(), Kind: "probe", SessionID: id, Source: "daemon", Payload: `{}`}); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func rowCount(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestPurgeSessionsRequiresHuman is DONE WHEN clause 3 (first half).
//
// RA-MUTATION-PROBE: the identity check in PurgeSessions removed -> RED.
func TestPurgeSessionsRequiresHuman(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	id := seedPurgeSession(t, s, 2)
	for _, kind := range []IdentityKind{IdentityAgent, IdentityInference} {
		_, err := s.PurgeSessions(PurgeScope{SessionID: id}, Identity{Kind: kind})
		if !errors.Is(err, ErrPurgeRequiresHuman) {
			t.Fatalf("PurgeSessions(%v) = %v, want ErrPurgeRequiresHuman", kind, err)
		}
	}
	if n := rowCount(t, s, "timeline_events"); n != 2 {
		t.Errorf("events = %d, want 2 untouched", n)
	}
	if n := rowCount(t, s, "purge_log"); n != 0 {
		t.Errorf("purge_log = %d, want 0", n)
	}
	if p, _ := s.SessionPurged(id); p {
		t.Error("session stamped purged by a refused purge")
	}
}

// TestPurgeSessionsRestoresNoDeleteTrigger is DONE WHEN clause 3 (second
// half).
//
// RA-MUTATION-PROBE: the trigger recreate skipped -> RED.
func TestPurgeSessionsRestoresNoDeleteTrigger(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	id := seedPurgeSession(t, s, 2)
	other := seedPurgeSession(t, s, 1)
	got, err := s.PurgeSessions(PurgeScope{SessionID: id}, Identity{Kind: IdentityHuman})
	if err != nil {
		t.Fatal(err)
	}
	if got != (PurgeCounts{Sessions: 1, Events: 2}) {
		t.Errorf("counts = %+v", got)
	}
	_, err = s.db.Exec(`DELETE FROM timeline_events`)
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("raw DELETE after purge = %v, want the append-only abort", err)
	}
	if n := rowCount(t, s, "timeline_events"); n != 1 {
		t.Errorf("events = %d, want 1 (other session)", n)
	}
	if p, _ := s.SessionPurged(other); p {
		t.Error("other session stamped purged")
	}
}
