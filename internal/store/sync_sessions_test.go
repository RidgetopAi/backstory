package store

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func startSyncSession(t *testing.T, s *Store, project string) string {
	t.Helper()
	id, err := s.StartSession(StartSessionParams{Agent: "claude", CWD: "/secret/cwd", ProjectKey: project,
		StartedAt: time.Now(), Origin: OriginLive})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func servedSessions(t *testing.T, s *Store) []SyncSession {
	t.Helper()
	changes, _, _, err := s.ChangesSince(0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []SyncSession
	for _, c := range changes {
		if c.Kind == SyncKindSession {
			out = append(out, *c.Session)
		}
	}
	return out
}

func importFrom(t *testing.T, s *Store, ss []SyncSession) {
	t.Helper()
	var ch []SyncChange
	for i := range ss {
		ch = append(ch, SyncChange{Kind: SyncKindSession, Session: &ss[i]})
	}
	human := Identity{Kind: IdentityHuman, Actor: humanActor}
	if _, err := s.ImportSyncChanges(human, SyncPeer{DeviceID: "dev-a"}, ch); err != nil {
		t.Fatal(err)
	}
}

// A note-less session of a shared project is served and carries its project;
// a session of an unshared folder is not served.
func TestNotelessSharedSessionServedWithProject(t *testing.T) {
	a := mustOpen(t, filepath.Join(t.TempDir(), "a.db"))
	b := mustOpen(t, filepath.Join(t.TempDir(), "b.db"))
	seedRemoteProject(t, a, "/a/widgets", "https://github.com/acme/widgets")
	seedRemoteProject(t, a, "/a/loose", "")
	seedRemoteProject(t, b, "/b/widgets", "git@github.com:acme/widgets.git")
	s1 := startSyncSession(t, a, "/a/widgets")
	s2 := startSyncSession(t, a, "/a/loose")

	served := servedSessions(t, a)
	if len(served) != 1 || served[0].ID != s1 || served[0].ProjectSyncID != "github.com/acme/widgets" {
		t.Fatalf("served %+v, want only S1 with its project", served)
	}
	importFrom(t, b, served)

	var key string
	if err := b.DB().QueryRow(`SELECT project_key FROM sessions WHERE id = ?`, s1).Scan(&key); err != nil || key != "/b/widgets" {
		t.Fatalf("S1 project_key=%q err=%v, want /b/widgets", key, err)
	}
	var n int
	if err := b.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = ?`, s2).Scan(&n); err != nil || n != 0 {
		t.Fatalf("S2 present on B: %d %v", n, err)
	}

	// B's projects row counts the session.
	sums, err := b.ProjectSummaries()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ps := range sums {
		if ps.Key == "/b/widgets" {
			found = true
			if ps.Sessions < 1 || ps.LastActivity.IsZero() {
				t.Fatalf("summary %+v, want sessions>=1 and last activity", ps)
			}
		}
	}
	if !found {
		t.Fatal("no summary for /b/widgets")
	}
}

// With no local project for the identity the stub lands under the placeholder
// and moves when the project arrives.
func TestSessionPlaceholderThenRehome(t *testing.T) {
	a := mustOpen(t, filepath.Join(t.TempDir(), "a.db"))
	b := mustOpen(t, filepath.Join(t.TempDir(), "b.db"))
	human := Identity{Kind: IdentityHuman, Actor: humanActor}
	seedRemoteProject(t, a, "/a/widgets", "https://github.com/acme/widgets")
	s1 := startSyncSession(t, a, "/a/widgets")
	importFrom(t, b, servedSessions(t, a))

	var key string
	want := SyncPlaceholderPrefix + "github.com/acme/widgets"
	if err := b.DB().QueryRow(`SELECT project_key FROM sessions WHERE id = ?`, s1).Scan(&key); err != nil || key != want {
		t.Fatalf("project_key=%q err=%v, want %q", key, err, want)
	}
	seedRemoteProject(t, b, "/b/widgets", "https://github.com/acme/widgets")
	if err := b.SetProjectLink(human, "github.com/acme/widgets", "/b/widgets"); err != nil {
		t.Fatal(err)
	}
	if err := b.DB().QueryRow(`SELECT project_key FROM sessions WHERE id = ?`, s1).Scan(&key); err != nil || key != "/b/widgets" {
		t.Fatalf("after rehome project_key=%q err=%v, want /b/widgets", key, err)
	}
}

// An unshared project that gains a remote serves its earlier note-less sessions.
func TestSessionsRequeuedWhenProjectGainsIdentity(t *testing.T) {
	a := mustOpen(t, filepath.Join(t.TempDir(), "a.db"))
	human := Identity{Kind: IdentityHuman, Actor: humanActor}
	seedRemoteProject(t, a, "/a/loose", "")
	startSyncSession(t, a, "/a/loose")
	if got := servedSessions(t, a); len(got) != 0 {
		t.Fatalf("served before identity: %+v", got)
	}
	if err := a.SetProjectLink(human, "loose-notes", "/a/loose"); err != nil {
		t.Fatal(err)
	}
	got := servedSessions(t, a)
	if len(got) != 1 || got[0].ProjectSyncID != "loose-notes" {
		t.Fatalf("after identity served %+v", got)
	}
}

// A stub with a project fills a NULL key on a known session, never overwrites.
func TestSessionProjectFilledNeverOverwritten(t *testing.T) {
	b := mustOpen(t, filepath.Join(t.TempDir(), "b.db"))
	seedRemoteProject(t, b, "/b/widgets", "https://github.com/acme/widgets")
	seedRemoteProject(t, b, "/b/other", "https://github.com/acme/other")
	importFrom(t, b, []SyncSession{{ID: "s1", Agent: "claude", OriginDevice: "dev-a", StartedAt: 1}})
	var key *string
	if err := b.DB().QueryRow(`SELECT project_key FROM sessions WHERE id = 's1'`).Scan(&key); err != nil || key != nil {
		t.Fatalf("old-peer stub key=%v err=%v, want NULL", key, err)
	}
	importFrom(t, b, []SyncSession{{ID: "s1", Agent: "claude", OriginDevice: "dev-a", StartedAt: 1, ProjectSyncID: "github.com/acme/widgets"}})
	if err := b.DB().QueryRow(`SELECT project_key FROM sessions WHERE id = 's1'`).Scan(&key); err != nil || key == nil || *key != "/b/widgets" {
		t.Fatalf("filled key=%v err=%v", key, err)
	}
	importFrom(t, b, []SyncSession{{ID: "s1", Agent: "claude", OriginDevice: "dev-a", StartedAt: 1, ProjectSyncID: "github.com/acme/other"}})
	if err := b.DB().QueryRow(`SELECT project_key FROM sessions WHERE id = 's1'`).Scan(&key); err != nil || *key != "/b/widgets" {
		t.Fatalf("overwritten key=%v err=%v", *key, err)
	}
}

// The wire stub carries exactly the allowed keys.
func TestSyncSessionWireKeys(t *testing.T) {
	a := mustOpen(t, filepath.Join(t.TempDir(), "a.db"))
	seedRemoteProject(t, a, "/a/widgets", "https://github.com/acme/widgets")
	s1 := startSyncSession(t, a, "/a/widgets")
	if err := a.EndSession(s1, time.Now(), "exit"); err != nil {
		t.Fatal(err)
	}
	served := servedSessions(t, a)
	if len(served) != 1 {
		t.Fatalf("served %d", len(served))
	}
	raw, err := json.Marshal(served[0])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"agent", "ended_at_ns", "id", "origin_device", "project_sync_id", "started_at_ns"}
	if len(keys) != len(want) {
		t.Fatalf("keys %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("keys %v, want %v", keys, want)
		}
	}
}
