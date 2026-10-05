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

func seedProjectPurgeSession(t *testing.T, s *Store, project string, events int) string {
	t.Helper()
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO projects (key, toplevel, first_seen) VALUES (?, ?, ?)`, project, "/x", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	id, err := s.StartSession(StartSessionParams{Agent: "claude", CWD: "/x", ProjectKey: project, StartedAt: time.Now(), Origin: OriginBackfilled})
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

// TestPurgePreviewAfterPurgeReportsNothing: a re-check of an already-purged
// project or session scope reports 0 sessions, 0 events, 0 records, and the
// tombstoned session rows still exist and still block backfill re-import.
//
// RA-MUTATION-PROBE: the purged_at filter dropped from purgeSelection -> RED.
func TestPurgePreviewAfterPurgeReportsNothing(t *testing.T) {
	human := Identity{Kind: IdentityHuman}
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	a := seedProjectPurgeSession(t, s, "proj-a", 3)
	b := seedProjectPurgeSession(t, s, "proj-a", 2)
	got, err := s.PurgeSessions(PurgeScope{ProjectKey: "proj-a"}, human)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sessions != 2 || got.Events != 5 {
		t.Fatalf("purge counts = %+v, want 2 sessions 5 events", got)
	}
	for _, sc := range []PurgeScope{{ProjectKey: "proj-a"}, {SessionID: a}} {
		prev, err := s.PurgePreview(sc)
		if err != nil {
			t.Fatal(err)
		}
		if prev != (PurgeCounts{}) {
			t.Errorf("preview %s after purge = %+v, want zero", sc, prev)
		}
	}
	if n := rowCount(t, s, "sessions"); n != 2 {
		t.Errorf("session rows = %d, want 2 tombstones kept", n)
	}
	for _, id := range []string{a, b} {
		if p, err := s.SessionPurged(id); err != nil || !p {
			t.Errorf("SessionPurged(%s) = %v, %v; want true", id, p, err)
		}
	}
	again, err := s.PurgeSessions(PurgeScope{ProjectKey: "proj-a"}, human)
	if err != nil {
		t.Fatal(err)
	}
	if again != (PurgeCounts{}) {
		t.Errorf("re-purge = %+v, want zero", again)
	}
	if p, _ := s.SessionPurged(a); !p {
		t.Error("tombstone lost after re-purge")
	}
}

// TestPurgeMixedScopeSelectsOnlyUnpurged: one purged and one fresh session
// dry-run as 1 session, and the real purge purges exactly that one.
//
// RA-MUTATION-PROBE: the purged_at filter dropped from purgeSelection -> RED.
func TestPurgeMixedScopeSelectsOnlyUnpurged(t *testing.T) {
	human := Identity{Kind: IdentityHuman}
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	old := seedProjectPurgeSession(t, s, "proj-b", 3)
	if _, err := s.PurgeSessions(PurgeScope{SessionID: old}, human); err != nil {
		t.Fatal(err)
	}
	fresh := seedProjectPurgeSession(t, s, "proj-b", 2)
	sc := PurgeScope{ProjectKey: "proj-b"}
	prev, err := s.PurgePreview(sc)
	if err != nil {
		t.Fatal(err)
	}
	if prev.Sessions != 1 || prev.Events != 2 {
		t.Fatalf("preview = %+v, want 1 session 2 events", prev)
	}
	got, err := s.PurgeSessions(sc, human)
	if err != nil {
		t.Fatal(err)
	}
	if got != prev {
		t.Errorf("purge = %+v, want %+v", got, prev)
	}
	if p, _ := s.SessionPurged(fresh); !p {
		t.Error("fresh session not purged")
	}
}
