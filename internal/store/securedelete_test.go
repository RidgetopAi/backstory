package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// rawMarkerCount counts marker in the raw DB file and its -wal file — what
// a forensic read of the disk would see, bypassing every query.
func rawMarkerCount(t *testing.T, dbPath, marker string) int {
	t.Helper()
	n := 0
	for _, p := range []string{dbPath, dbPath + "-wal"} {
		b, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		n += bytes.Count(b, []byte(marker))
	}
	return n
}

func insertFiller(t *testing.T, s *Store, text string) string {
	t.Helper()
	id, err := s.InsertRecord(InsertRecordParams{Identity: Identity{Kind: IdentityAgent}, Kind: KindDecision, Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// RA-MUTATION-PROBE: remove secure_delete(ON) / the FTS 'secure-delete' step
// / the post-delete checkpoint -> RED.
func TestDeleteForgetsFromDBFileAndWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backstory.db")
	s := mustOpen(t, path)
	const marker = "bsproofzqxdeadbeef01"
	insertFiller(t, s, "keeper alpha unrelated text")
	id := insertFiller(t, s, "remember "+marker+" forever")
	insertFiller(t, s, "keeper beta unrelated text")
	if rawMarkerCount(t, path, marker) == 0 {
		t.Fatal("precondition: marker should be on disk before delete")
	}
	if err := s.TombstoneRecord(id, Identity{Kind: IdentityHuman}); err != nil {
		t.Fatal(err)
	}
	if n := rawMarkerCount(t, path, marker); n != 0 {
		t.Fatalf("marker occurs %d times in db/-wal after delete, want 0", n)
	}
}

// RA-MUTATION-PROBE: same three removals -> RED.
func TestPurgeForgetsEventPayloadFromDBFileAndWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backstory.db")
	s := mustOpen(t, path)
	const marker = "bsproofzqxdeadbeef02"
	id, err := s.StartSession(StartSessionParams{Agent: "claude", CWD: "/x", StartedAt: time.Now(), Origin: OriginBackfilled})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.AppendEvent(Event{TS: time.Now(), Kind: "probe", SessionID: id, Source: "daemon",
			Payload: `{"command":"echo ` + marker + `"}`}); err != nil {
			t.Fatal(err)
		}
	}
	if rawMarkerCount(t, path, marker) == 0 {
		t.Fatal("precondition: marker should be on disk before purge")
	}
	if _, err := s.PurgeSessions(PurgeScope{SessionID: id}, Identity{Kind: IdentityHuman}); err != nil {
		t.Fatal(err)
	}
	if n := rawMarkerCount(t, path, marker); n != 0 {
		t.Fatalf("marker occurs %d times in db/-wal after purge, want 0", n)
	}
}

// Control: secure delete removes only what was deleted.
func TestDeleteKeepsOtherRecordsSearchable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backstory.db")
	s := mustOpen(t, path)
	const keep, gone = "bsproofzqxkeepme03", "bsproofzqxgoneme03"
	keepID := insertFiller(t, s, "alpha "+keep+" omega")
	goneID := insertFiller(t, s, "alpha "+gone+" omega")
	if err := s.TombstoneRecord(goneID, Identity{Kind: IdentityHuman}); err != nil {
		t.Fatal(err)
	}
	res, err := s.SearchRecords(keep, 10)
	if err != nil || len(res) != 1 || res[0].ID != keepID {
		t.Fatalf("keeper search = %v, %v; want only %s", res, err, keepID)
	}
	if res, _ := s.SearchRecords(gone, 10); len(res) != 0 {
		t.Fatalf("deleted record still searchable: %v", res)
	}
	if rawMarkerCount(t, path, keep) == 0 {
		t.Error("keeper marker vanished from disk")
	}
}
