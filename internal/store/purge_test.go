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

// A backfill-source append to a purged session is refused; a live-source
// append to the same session, and a backfill append to an unpurged one, are
// not. (Live capture of a purged session is the daemon's own decision; the
// gate exists because backfill replays history the human erased.)
func TestAppendEventRefusesBackfillOnPurgedSession(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mk := func(h string) string {
		id, err := s.StartSession(StartSessionParams{Agent: "codex", HarnessSessionID: h, CWD: "/w", StartedAt: time.Now(), Origin: OriginLive})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	purged, other := mk("p"), mk("o")
	if _, err := s.PurgeSessions(PurgeScope{SessionID: purged}, Identity{Kind: IdentityHuman, Actor: "human"}); err != nil {
		t.Fatal(err)
	}
	ev := func(sid, src string) error {
		_, err := s.AppendEvent(Event{TS: time.Now(), Kind: "tool.use", SessionID: sid, Source: src, Payload: "{}"})
		return err
	}
	if err := ev(purged, SourceBackfill); !errors.Is(err, ErrSessionPurged) {
		t.Errorf("backfill append to purged = %v, want ErrSessionPurged", err)
	}
	if err := ev(purged, "posttooluse"); err != nil {
		t.Errorf("live append to purged session refused: %v", err)
	}
	if err := ev(other, SourceBackfill); err != nil {
		t.Errorf("backfill append to unpurged session refused: %v", err)
	}
}

// leakEvents appends n events to a (purged) session by a direct store
// append that bypasses the backfill gate (source "daemon"), simulating the
// leak a pre-3a28a070 backfill left behind.
func leakEvents(t *testing.T, s *Store, id string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := s.AppendEvent(Event{TS: time.Now(), Kind: "leak", SessionID: id, Source: "daemon", Payload: `{}`}); err != nil {
			t.Fatal(err)
		}
	}
}

func sessionEvents(t *testing.T, s *Store, id string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM timeline_events WHERE session_id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func purgedAt(t *testing.T, s *Store, id string) int64 {
	t.Helper()
	var at int64
	if err := s.db.QueryRow(`SELECT purged_at FROM sessions WHERE id = ?`, id).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

// Re-purging an already-purged session erases events still attached to it
// (leaked before the backfill gate), counts 0 sessions but M events, keeps
// the original purged_at; --dry-run reports the same and erases nothing.
//
// RA-MUTATION-PROBE: purgeSelection selects only purged_at IS NULL -> RED.
func TestRepurgeSessionErasesLeakedEvents(t *testing.T) {
	human := Identity{Kind: IdentityHuman}
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	id := seedPurgeSession(t, s, 2)
	if _, err := s.PurgeSessions(PurgeScope{SessionID: id}, human); err != nil {
		t.Fatal(err)
	}
	at := purgedAt(t, s, id)
	leakEvents(t, s, id, 3)

	sc := PurgeScope{SessionID: id}
	prev, err := s.PurgePreview(sc)
	if err != nil {
		t.Fatal(err)
	}
	if prev != (PurgeCounts{Sessions: 0, Events: 3}) {
		t.Fatalf("dry-run = %+v, want 0 sessions 3 events", prev)
	}
	if n := sessionEvents(t, s, id); n != 3 {
		t.Fatalf("dry-run erased events: %d left, want 3", n)
	}
	got, err := s.PurgeSessions(sc, human)
	if err != nil {
		t.Fatal(err)
	}
	if got != prev {
		t.Errorf("purge = %+v, want %+v", got, prev)
	}
	if n := sessionEvents(t, s, id); n != 0 {
		t.Errorf("%d leaked events remain", n)
	}
	if got := purgedAt(t, s, id); got != at {
		t.Errorf("purged_at changed %d -> %d", at, got)
	}
}

// A project purge also erases leaked events in already-purged in-scope
// sessions, and leaves out-of-scope sessions untouched.
//
// RA-MUTATION-PROBE: purgeSelection selects only purged_at IS NULL -> RED.
func TestRepurgeProjectErasesLeaksInScopeOnly(t *testing.T) {
	human := Identity{Kind: IdentityHuman}
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	in := seedProjectPurgeSession(t, s, "proj-in", 1)
	out := seedProjectPurgeSession(t, s, "proj-out", 1)
	for _, id := range []string{in, out} {
		if _, err := s.PurgeSessions(PurgeScope{SessionID: id}, human); err != nil {
			t.Fatal(err)
		}
		leakEvents(t, s, id, 2)
	}
	got, err := s.PurgeSessions(PurgeScope{ProjectKey: "proj-in"}, human)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sessions != 0 || got.Events != 2 {
		t.Errorf("purge = %+v, want 0 sessions 2 events", got)
	}
	if n := sessionEvents(t, s, in); n != 0 {
		t.Errorf("in-scope leak: %d events remain", n)
	}
	if n := sessionEvents(t, s, out); n != 2 {
		t.Errorf("out-of-scope session has %d events, want 2 untouched", n)
	}
}
