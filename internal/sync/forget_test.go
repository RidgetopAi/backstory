package sync_test

import (
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
	bsync "github.com/RidgetopAi/backstory/internal/sync"
)

func startSession(t *testing.T, s *store.Store, project string) string {
	t.Helper()
	id, err := s.StartSession(store.StartSessionParams{Agent: "claude", CWD: project, ProjectKey: project,
		StartedAt: time.Now(), Origin: store.OriginLive})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func assertForgotten(t *testing.T, s *store.Store, name, id, word string) {
	t.Helper()
	got, err := s.GetRecord(id)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if got.TombstonedAt == nil || got.Text != "" {
		t.Fatalf("%s: record %s not forgotten: tombstoned=%v text=%q", name, id, got.TombstonedAt, got.Text)
	}
	if hits, _ := s.SearchRecords(word, 10); len(hits) != 0 {
		t.Fatalf("%s: FTS still finds %q", name, word)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM records_fts WHERE records_fts MATCH ?`, word); n != 0 {
		t.Fatalf("%s: FTS index still holds %q (%d)", name, word, n)
	}
}

// A record written on A, synced, then deleted on B (an imported record) is
// scrubbed on A after the next sync.
func TestDeleteOfImportedRecordReachesItsOrigin(t *testing.T) {
	a, b := openStore(t), openStore(t)
	addProject(t, a, "/a/widgets", remoteURL)
	addProject(t, b, "/b/widgets", remoteURL)
	id := note(t, a, human, "/a/widgets", "", "zebrafinch-secret decided")
	rep, _, err := link(t, b, human, a, human, bsync.Cursors{})
	if err != nil {
		t.Fatal(err)
	}
	if hits, _ := a.SearchRecords("zebrafinch", 10); len(hits) != 1 {
		t.Fatal("record not searchable on A before the delete")
	}
	if err := b.TombstoneRecord(id, human); err != nil {
		t.Fatal(err)
	}
	if _, _, err := link(t, b, human, a, human, rep.Cursors); err != nil {
		t.Fatal(err)
	}
	assertForgotten(t, a, "A", id, "zebrafinch")
}

// purge --project on A forgets every in-window record on B and stamps its
// session stubs; purge --session tombstones no records on either side.
func TestPurgeCrossesAsTombstonesAndPurgedStubs(t *testing.T) {
	a, b := openStore(t), openStore(t)
	addProject(t, a, "/a/widgets", remoteURL)
	addProject(t, b, "/b/widgets", remoteURL)
	keep := startSession(t, a, "/a/widgets")
	gone := startSession(t, a, "/a/widgets")
	keepRec := note(t, a, human, "/a/widgets", keep, "okapi-kept note")
	r1 := note(t, a, human, "/a/widgets", gone, "okapi-one gone")
	r2 := note(t, a, agent, "/a/widgets", "", "okapi-two gone")
	rep, _, err := link(t, b, human, a, human, bsync.Cursors{})
	if err != nil {
		t.Fatal(err)
	}

	// --session: stamps the stub on both sides, tombstones nothing.
	res, err := a.PurgeSessions(store.PurgeScope{SessionID: keep}, human)
	if err != nil || res.Records != 0 {
		t.Fatalf("session purge: %+v %v", res, err)
	}
	rep, _, err = link(t, b, human, a, human, rep.Cursors)
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]*store.Store{"A": a, "B": b} {
		if p, _ := s.SessionPurged(keep); !p {
			t.Fatalf("%s: session %s not stamped purged", name, keep)
		}
		if n := count(t, s, `SELECT COUNT(*) FROM records WHERE tombstoned_at IS NOT NULL`); n != 0 {
			t.Fatalf("%s: --session purge tombstoned %d records", name, n)
		}
	}

	// --project: every in-window record is tombstoned on B, stubs stamped.
	res, err = a.PurgeSessions(store.PurgeScope{ProjectKey: "/a/widgets"}, human)
	if err != nil || res.Records != 3 {
		t.Fatalf("project purge: %+v %v", res, err)
	}
	if _, _, err := link(t, b, human, a, human, rep.Cursors); err != nil {
		t.Fatal(err)
	}
	assertForgotten(t, b, "B", keepRec, "okapi")
	assertForgotten(t, b, "B", r1, "okapi")
	assertForgotten(t, b, "B", r2, "okapi")
	if p, _ := b.SessionPurged(gone); !p {
		t.Fatal("B: session stub not stamped purged")
	}
}

// A tombstone on A is never undone by C, which still has the text, syncing
// first; C is scrubbed on the next round.
func TestTombstonedRecordCannotBeResurrected(t *testing.T) {
	a, c := openStore(t), openStore(t)
	addProject(t, a, "/a/widgets", remoteURL)
	addProject(t, c, "/c/widgets", remoteURL)
	id := note(t, a, human, "/a/widgets", "", "quokka-secret text")
	if _, _, err := link(t, c, human, a, human, bsync.Cursors{}); err != nil {
		t.Fatal(err)
	}
	if err := a.TombstoneRecord(id, human); err != nil {
		t.Fatal(err)
	}

	// C's old copy reaches A before A's tombstone reaches C.
	changes, _, _, err := c.ChangesSince(0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	cdev, _ := c.SelfDevice()
	if _, err := a.ImportSyncChanges(human, store.SyncPeer{DeviceID: cdev.ID}, changes); err != nil {
		t.Fatal(err)
	}
	assertForgotten(t, a, "A", id, "quokka")

	if _, _, err := link(t, c, human, a, human, bsync.Cursors{}); err != nil {
		t.Fatal(err)
	}
	assertForgotten(t, c, "C", id, "quokka")
	assertForgotten(t, a, "A", id, "quokka")
}
