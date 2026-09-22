package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newRecordForAppendOnlyTest(t *testing.T, s *Store) string {
	t.Helper()
	id, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent},
		Kind:     KindNote,
		Text:     "append-only probe",
	})
	if err != nil {
		t.Fatalf("InsertRecord: %v", err)
	}
	return id
}

func newEventForAppendOnlyTest(t *testing.T, s *Store) int64 {
	t.Helper()
	id, err := s.AppendEvent(Event{
		TS:      time.Now(),
		Kind:    "probe",
		Source:  "daemon",
		Payload: `{}`,
	})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	return id
}

func TestRawUpdateOfRecordsTextFails(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	id := newRecordForAppendOnlyTest(t, s)

	_, err := s.DB().Exec(`UPDATE records SET text = 'tampered' WHERE id = ?`, id)
	if err == nil {
		t.Fatal("raw UPDATE records.text succeeded, want the append-only trigger to abort it")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("UPDATE error = %v, want it to name append-only", err)
	}
}

func TestRawDeleteOfRecordsFails(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	id := newRecordForAppendOnlyTest(t, s)

	_, err := s.DB().Exec(`DELETE FROM records WHERE id = ?`, id)
	if err == nil {
		t.Fatal("raw DELETE FROM records succeeded, want the append-only trigger to abort it")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("DELETE error = %v, want it to name append-only", err)
	}
}

func TestRawUpdateOfTimelineEventsFails(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	id := newEventForAppendOnlyTest(t, s)

	_, err := s.DB().Exec(`UPDATE timeline_events SET payload = '{"tampered":true}' WHERE id = ?`, id)
	if err == nil {
		t.Fatal("raw UPDATE timeline_events succeeded, want the append-only trigger to abort it")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("UPDATE error = %v, want it to name append-only", err)
	}
}

func TestRawDeleteOfTimelineEventsFails(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	id := newEventForAppendOnlyTest(t, s)

	_, err := s.DB().Exec(`DELETE FROM timeline_events WHERE id = ?`, id)
	if err == nil {
		t.Fatal("raw DELETE FROM timeline_events succeeded, want the append-only trigger to abort it")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("DELETE error = %v, want it to name append-only", err)
	}
}

// TestTombstoneRecordSetsOnlyTombstonedAt proves TombstoneRecord succeeds
// and that every column except tombstoned_at is byte-identical before and
// after.
func TestTombstoneRecordSetsOnlyTombstonedAt(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	id := newRecordForAppendOnlyTest(t, s)

	before, err := s.GetRecord(id)
	if err != nil {
		t.Fatalf("GetRecord before: %v", err)
	}
	if before.TombstonedAt != nil {
		t.Fatalf("TombstonedAt before tombstone = %v, want nil", before.TombstonedAt)
	}

	if err := s.TombstoneRecord(id, Identity{Kind: IdentityHuman}); err != nil {
		t.Fatalf("TombstoneRecord: %v", err)
	}

	after, err := s.GetRecord(id)
	if err != nil {
		t.Fatalf("GetRecord after: %v", err)
	}
	if after.TombstonedAt == nil {
		t.Fatal("TombstonedAt after tombstone = nil, want set")
	}

	if after.ID != before.ID ||
		!after.TS.Equal(before.TS) ||
		after.Kind != before.Kind ||
		after.Tier != before.Tier ||
		after.Text != before.Text ||
		after.SessionID != before.SessionID ||
		after.ProjectKey != before.ProjectKey ||
		after.Promoter != before.Promoter {
		t.Errorf("TombstoneRecord changed a column other than tombstoned_at:\nbefore = %+v\nafter  = %+v", before, after)
	}
}

func TestTombstoneRecordRequiresHumanIdentity(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	id := newRecordForAppendOnlyTest(t, s)

	err := s.TombstoneRecord(id, Identity{Kind: IdentityAgent})
	if err != ErrTombstoneRequiresHuman {
		t.Fatalf("TombstoneRecord with agent identity = %v, want ErrTombstoneRequiresHuman", err)
	}

	rec, err := s.GetRecord(id)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if rec.TombstonedAt != nil {
		t.Fatal("TombstonedAt set after a refused non-human tombstone attempt")
	}
}
