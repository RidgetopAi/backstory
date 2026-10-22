package store

import (
	"fmt"
	"path/filepath"
	"testing"
)

const requeueRecords = 26

// seedRequeueProject makes a shared project holding requeueRecords records.
func seedRequeueProject(t *testing.T, s *Store, key string) []string {
	t.Helper()
	seedRemoteProject(t, s, key, "git@github.com:acme/widgets.git")
	var ids []string
	for i := 0; i < requeueRecords; i++ {
		id, err := s.InsertRecordWithEdges(InsertRecordParams{
			Identity: Identity{Kind: IdentityHuman, Actor: humanActor}, Kind: KindDecision,
			Text: fmt.Sprintf("record %d", i), ProjectKey: key,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func feedCount(t *testing.T, s *Store, ref string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sync_changes WHERE ref = ?`, ref).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func recordIDs(changes []SyncChange) []string {
	var out []string
	for _, c := range changes {
		if c.Record != nil {
			out = append(out, c.Record.ID)
		}
	}
	return out
}

// Requeueing a project twice leaves one feed entry per record, and peers at
// cursor 0 and between the old and new copies both still end with every record.
func TestRequeueLeavesOneEntryPerRef(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "a.db"))
	key := "/a/wobble"
	ids := seedRequeueProject(t, s, key)
	var midCursor int64
	for i := 0; i < 2; i++ {
		if i == 1 {
			var err error
			if midCursor, err = s.SyncMaxSeq(); err != nil {
				t.Fatal(err)
			}
			midCursor -= requeueRecords / 2 // between the copies
		}
		if err := s.requeueProjectChanges(key); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.requeueProjectChanges(key); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if n := feedCount(t, s, id); n != 1 {
			t.Fatalf("record %s has %d feed entries, want 1", id, n)
		}
	}
	for _, since := range []int64{0, midCursor} {
		changes, _, _, err := s.ChangesSince(since, 1000)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(recordIDs(changes)); got != requeueRecords {
			t.Fatalf("cursor %d: served %d records, want %d", since, got, requeueRecords)
		}
	}
}

// c60db1d1: copies straddling a batch boundary re-served records in batch 2.
func TestRequeueNoReservedRecordsAcrossBatches(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "a.db"))
	key := "/a/wobble"
	seedRequeueProject(t, s, key)
	if err := s.requeueProjectChanges(key); err != nil {
		t.Fatal(err)
	}
	const batch = 20
	seen := map[string]bool{}
	var since int64
	reserved := 0
	for {
		changes, next, more, err := s.ChangesSince(since, batch)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range recordIDs(changes) {
			if seen[id] {
				reserved++
			}
			seen[id] = true
		}
		since = next
		if !more {
			break
		}
	}
	if reserved != 0 || len(seen) != requeueRecords {
		t.Fatalf("re-served %d records, saw %d distinct, want 0 and %d", reserved, len(seen), requeueRecords)
	}
}
