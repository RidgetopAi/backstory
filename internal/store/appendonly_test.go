package store

import (
	"database/sql"
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
// and that every column except tombstoned_at and text (scrubbed to ”) is
// byte-identical before and after.
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
		after.Text != "" ||
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

func ftsMatchCount(t *testing.T, db *sql.DB, word string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM records_fts WHERE records_fts MATCH ?`, word).Scan(&n); err != nil {
		t.Fatalf("fts match %q: %v", word, err)
	}
	return n
}

func TestTombstoneRecordScrubsTextAndFTS(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	db := s.DB()
	id, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindNote, Text: "zebracorn secret plans",
	})
	if err != nil {
		t.Fatalf("InsertRecord: %v", err)
	}
	other, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindNote, Text: "bystander keepsake",
	})
	if err != nil {
		t.Fatalf("InsertRecord: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO edges (from_id, to_id, type, declared_by) VALUES (?, ?, 'informs', 'test')`, other, id); err != nil {
		t.Fatalf("insert edge: %v", err)
	}
	if n := ftsMatchCount(t, db, "zebracorn"); n != 1 {
		t.Fatalf("fts before tombstone = %d, want 1", n)
	}

	if err := s.TombstoneRecord(id, Identity{Kind: IdentityHuman}); err != nil {
		t.Fatalf("TombstoneRecord: %v", err)
	}

	var text string
	if err := db.QueryRow(`SELECT text FROM records WHERE id = ?`, id).Scan(&text); err != nil {
		t.Fatalf("row must still exist: %v", err)
	}
	if text != "" {
		t.Errorf("text after tombstone = %q, want ''", text)
	}
	if n := ftsMatchCount(t, db, "zebracorn"); n != 0 {
		t.Errorf("fts MATCH after tombstone = %d rows, want 0", n)
	}
	if n := ftsMatchCount(t, db, "bystander"); n != 1 {
		t.Errorf("fts for untouched record = %d, want 1", n)
	}
	var edges int
	if err := db.QueryRow(`SELECT COUNT(*) FROM edges WHERE to_id = ?`, id).Scan(&edges); err != nil || edges != 1 {
		t.Errorf("edges after tombstone = %d, %v; want 1", edges, err)
	}
	if _, err := db.Exec(`INSERT INTO records_fts(records_fts) VALUES('integrity-check')`); err != nil {
		t.Errorf("fts integrity-check after tombstone: %v", err)
	}
}

func TestTombstoneRecordNonHumanLeavesTextAndFTS(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	db := s.DB()
	id, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindNote, Text: "quokkaword survives",
	})
	if err != nil {
		t.Fatalf("InsertRecord: %v", err)
	}
	if err := s.TombstoneRecord(id, Identity{Kind: IdentityAgent}); err != ErrTombstoneRequiresHuman {
		t.Fatalf("err = %v, want ErrTombstoneRequiresHuman", err)
	}
	var text string
	if err := db.QueryRow(`SELECT text FROM records WHERE id = ?`, id).Scan(&text); err != nil || text != "quokkaword survives" {
		t.Errorf("text = %q, %v; want unchanged", text, err)
	}
	if n := ftsMatchCount(t, db, "quokkaword"); n != 1 {
		t.Errorf("fts = %d, want 1", n)
	}
}

func TestTombstoneRecordTwiceIsNoOp(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	id := newRecordForAppendOnlyTest(t, s)
	h := Identity{Kind: IdentityHuman}
	if err := s.TombstoneRecord(id, h); err != nil {
		t.Fatal(err)
	}
	var first int64
	if err := s.DB().QueryRow(`SELECT tombstoned_at FROM records WHERE id = ?`, id).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err := s.TombstoneRecord(id, h); err != nil {
		t.Fatalf("second tombstone: %v", err)
	}
	var second int64
	if err := s.DB().QueryRow(`SELECT tombstoned_at FROM records WHERE id = ?`, id).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("tombstoned_at changed on second tombstone: %d -> %d", first, second)
	}
	if n := ftsMatchCount(t, s.DB(), "probe"); n != 0 {
		t.Errorf("fts = %d after double tombstone, want 0", n)
	}
	if _, err := s.DB().Exec(`INSERT INTO records_fts(records_fts) VALUES('integrity-check')`); err != nil {
		t.Errorf("fts integrity-check: %v", err)
	}
}

func TestRawTombstoneShapedUpdatesAbort(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	db := s.DB()
	id := newRecordForAppendOnlyTest(t, s)
	mustAbort := func(name, q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: err = %v, want append-only abort", name, err)
		}
	}
	mustAbort("text=x", `UPDATE records SET text = 'x' WHERE id = ?`, id)
	mustAbort("text='' without tombstone", `UPDATE records SET text = '' WHERE id = ?`, id)
	mustAbort("text=x with tombstone", `UPDATE records SET text = 'x', tombstoned_at = 5 WHERE id = ?`, id)

	if err := (s).TombstoneRecord(id, Identity{Kind: IdentityHuman}); err != nil {
		t.Fatal(err)
	}
	mustAbort("re-tombstone", `UPDATE records SET tombstoned_at = 999 WHERE id = ?`, id)
	mustAbort("un-tombstone", `UPDATE records SET tombstoned_at = NULL WHERE id = ?`, id)
	mustAbort("resurrect text", `UPDATE records SET text = 'back' WHERE id = ?`, id)
}

// assertEveryRecordsColumnImmutable reads PRAGMA table_info(records) and
// proves a raw UPDATE of every column other than tombstoned_at and text
// aborts on a row that has a value to change.
func assertEveryRecordsColumnImmutable(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(records)`)
	if err != nil {
		t.Fatal(err)
	}
	type col struct{ name, typ string }
	var cols []col
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, col{name, typ})
	}
	_ = rows.Close()
	seen := map[string]bool{}
	for _, c := range cols {
		seen[c.name] = true
		if c.name == "tombstoned_at" || c.name == "text" {
			continue
		}
		// Set to a value that differs from the current one, whatever it is
		// (NULL and any non-NULL value both differ from 'mutated-x' / NULL).
		var q string
		switch c.typ {
		case "INTEGER":
			q = `UPDATE records SET ` + c.name + ` = COALESCE(` + c.name + `, 0) + 12345 WHERE id = ?`
		default:
			q = `UPDATE records SET ` + c.name + ` = COALESCE(` + c.name + ` || 'x', 'mutated-x') WHERE id = ?`
		}
		_, err := db.Exec(q, id)
		if err == nil {
			t.Errorf("raw UPDATE of records.%s succeeded, want append-only abort", c.name)
		} else if !strings.Contains(err.Error(), "append-only") {
			// CHECK / FK failures would mask a missing trigger clause.
			t.Errorf("UPDATE records.%s failed with %v, want the append-only trigger", c.name, err)
		}
	}
	for _, must := range []string{"event_cursor", "git_head"} {
		if !seen[must] {
			t.Errorf("PRAGMA table_info(records) lacks %s; guard is not covering it", must)
		}
	}
}

func TestRecordsColumnsImmutableAfterMigrate(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	id := newRecordForAppendOnlyTest(t, s)
	assertEveryRecordsColumnImmutable(t, s.DB(), id)
}

func TestRecordsColumnsImmutableAfterSweep(t *testing.T) {
	dbPath, w := seedLegacySweepFixture(t)
	s, err := Open(dbPath, []string{w}, fakeSweepGit{})
	if err != nil {
		t.Fatalf("Open (sweep): %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if n := countWhere(t, s.DB(), "records", "project_key", "workspace:"+w); n != 4 {
		t.Fatalf("sweep did not run: %d records under workspace key", n)
	}
	assertEveryRecordsColumnImmutable(t, s.DB(), "rec-legacy-decision")
}
