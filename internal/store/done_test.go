package store

import (
	"errors"
	"testing"
	"time"
)

func mustDone(t *testing.T, s *Store, handoffID string) string {
	t.Helper()
	id, err := s.Confirm(ConfirmParams{
		Identity: Identity{Kind: IdentityHuman, Actor: "human"}, Action: ConfirmDone,
		RecordID: handoffID, ProjectKey: freshnessProjectKey,
	})
	if err != nil {
		t.Fatalf("Confirm done: %v", err)
	}
	return id
}

// A done handoff writes one human-declared record linked to it, deletes
// nothing, and is no longer possibly stale; a non-handoff target and a
// non-human identity are rejected and write nothing.
func TestDoneClearsStaleWritesOneRecordRejectsBadTargets(t *testing.T) {
	s, sessionID := newFreshnessFixture(t)
	h := mustInsertHandoff(t, s, sessionID, []string{"a.go"})
	note := mustInsertNoteAbout(t, s, sessionID, []string{"a.go"})
	if reasons, _ := s.HandoffFreshness(h, nil); len(reasons) == 0 {
		t.Fatal("fixture: handoff should be flagged before done")
	}

	records, edges := countRecords(t, s), countEdges(t, s)
	if _, err := s.Confirm(ConfirmParams{Identity: Identity{Kind: IdentityHuman}, Action: ConfirmDone, RecordID: note, ProjectKey: freshnessProjectKey}); !errors.Is(err, ErrDoneNotAHandoff) {
		t.Fatalf("done on a note: err = %v, want ErrDoneNotAHandoff", err)
	}
	if _, err := s.Confirm(ConfirmParams{Identity: Identity{Kind: IdentityAgent}, Action: ConfirmDone, RecordID: h.ID, ProjectKey: freshnessProjectKey}); !errors.Is(err, ErrDoneHumanOnly) {
		t.Fatalf("done by an agent: err = %v, want ErrDoneHumanOnly", err)
	}
	if countRecords(t, s) != records || countEdges(t, s) != edges {
		t.Fatal("a rejected done wrote a record or edge")
	}

	doneID := mustDone(t, s, h.ID)
	if countRecords(t, s) != records+1 || countEdges(t, s) != edges+1 {
		t.Fatalf("done wrote %d records / %d edges, want exactly 1 / 1", countRecords(t, s)-records, countEdges(t, s)-edges)
	}
	rec, err := s.GetRecord(doneID)
	if err != nil || rec.Kind != KindConfirm || rec.Tier != TierHumanDeclared {
		t.Fatalf("done record = %+v, %v; want human-declared confirm", rec, err)
	}
	if got, err := s.GetRecord(h.ID); err != nil || got.TombstonedAt != nil {
		t.Fatalf("handoff after done: %+v, %v; nothing may be deleted", got, err)
	}
	if reasons, err := s.HandoffFreshness(h, nil); err != nil || len(reasons) != 0 {
		t.Fatalf("HandoffFreshness after done = %+v, %v; want none", reasons, err)
	}
	if _, done, _ := s.HandoffDone(h.ID); !done {
		t.Fatal("HandoffDone = false after done")
	}
}

// Marking the newest handoff done leaves it the resolved latest handoff
// (never the older one); a handoff written after it is the resume point.
func TestDoneNewestHandoffStaysLatestAndLaterHandoffIsNotDone(t *testing.T) {
	s := mustOpen(t, tempDBPath(t))
	home := "workspace:" + labelsWorkspaceDir
	mustUpsertProject(t, s, home)
	sess, err := s.StartSession(StartSessionParams{
		Agent: "claude", CWD: labelsWorkspaceDir + "/omarcade", ProjectKey: home, StartedAt: time.Now(), Origin: OriginLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	write := func(text string) string {
		t.Helper()
		time.Sleep(2 * time.Millisecond)
		id, err := s.InsertRecord(InsertRecordParams{Identity: Identity{Kind: IdentityAgent}, Kind: KindHandoff, Text: text, SessionID: sess, ProjectKey: home})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	latest := func() (Record, bool) {
		t.Helper()
		r, ok, err := s.LatestHandoffAt(labelsWorkspaceDir+"/omarcade", labelsGit, labelsWorkspaces)
		if err != nil || !ok {
			t.Fatalf("LatestHandoffAt = %v, %v", ok, err)
		}
		_, done, err := s.HandoffDone(r.ID)
		if err != nil {
			t.Fatal(err)
		}
		return r, done
	}
	h1 := write("H1")
	h2 := write("H2")
	if _, err := s.Confirm(ConfirmParams{Identity: Identity{Kind: IdentityHuman}, Action: ConfirmDone, RecordID: h2, ProjectKey: home}); err != nil {
		t.Fatal(err)
	}
	if r, done := latest(); r.ID != h2 || !done {
		t.Fatalf("latest = %s done=%v, want H2 (%s) done — never H1 (%s)", r.ID, done, h2, h1)
	}
	h3 := write("H3")
	if r, done := latest(); r.ID != h3 || done {
		t.Fatalf("latest = %s done=%v, want H3 (%s) not done", r.ID, done, h3)
	}
}

// Tombstoning the done record makes the handoff not-done again and its
// stale reasons come back; the done record never acts as an affirm.
func TestDoneUndoneByTombstoningDoneRecord(t *testing.T) {
	s, sessionID := newFreshnessFixture(t)
	h := mustInsertHandoff(t, s, sessionID, []string{"a.go"})
	mustInsertNoteAbout(t, s, sessionID, []string{"a.go"})
	doneID := mustDone(t, s, h.ID)
	if err := s.TombstoneRecord(doneID, Identity{Kind: IdentityHuman, Actor: "human"}); err != nil {
		t.Fatal(err)
	}
	if _, done, _ := s.HandoffDone(h.ID); done {
		t.Fatal("HandoffDone = true after tombstoning the done record")
	}
	reasons, err := s.HandoffFreshness(h, nil)
	if err != nil || len(reasons) == 0 {
		t.Fatalf("HandoffFreshness after undo = %+v, %v; want the stale reasons back", reasons, err)
	}
}
