package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestTsOrderingWholeSecondVsPlus100ms is the critic's reproduction (round 1
// walkthrough on 7c4dfc90, PLAN.md §Phase 1 critic T2): a whole-second
// timestamp formatted as RFC3339Nano ("...22Z") sorts AFTER a timestamp
// 100ms later ("...22.1Z") as a TEXT string, even though it is earlier as a
// time. Both the rate-cap window (`ts >= ?`) and SearchRecords' rank-tie
// order compare ts; both must treat the whole-second record as EARLIER.
//
// This test inserts its two fixture rows directly with SQL rather than via
// InsertRecord, which always stamps ts = time.Now(): the bug only
// reproduces for a specific pair of timestamps (one with ns == 0), which no
// amount of retrying a real insert can reliably hit.
func TestTsOrderingWholeSecondVsPlus100ms(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	sessionID := mustStartSession(t, s)

	wholeSecond := time.Date(2024, 6, 15, 12, 34, 56, 0, time.UTC) // ns == 0
	plus100ms := wholeSecond.Add(100 * time.Millisecond)

	earlierID := insertRecordFixture(t, s, sessionID, wholeSecond, "ts ordering probe shared text")
	laterID := insertRecordFixture(t, s, sessionID, plus100ms, "ts ordering probe shared text")

	t.Run("rate-cap window", func(t *testing.T) {
		// A window starting strictly between the two timestamps must count
		// only the later record: wholeSecond < windowStart <= plus100ms.
		windowStart := wholeSecond.Add(50 * time.Millisecond)
		n, err := s.recentRecordCount(sessionID, windowStart)
		if err != nil {
			t.Fatalf("recentRecordCount: %v", err)
		}
		if n != 1 {
			t.Fatalf("recentRecordCount(session, wholeSecond+50ms) = %d, want 1 (only the later record is >= the window start)", n)
		}
	})

	t.Run("SearchRecords ordering", func(t *testing.T) {
		results, err := s.SearchRecords("shared", 10)
		if err != nil {
			t.Fatalf("SearchRecords: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("SearchRecords(%q) returned %d results, want 2", "shared", len(results))
		}
		if results[0].ID != earlierID || results[1].ID != laterID {
			t.Fatalf("SearchRecords order = [%s, %s], want [%s, %s] (whole-second record first, as the earlier one)",
				results[0].ID, results[1].ID, earlierID, laterID)
		}
		if !results[0].TS.Before(results[1].TS) {
			t.Fatalf("SearchRecords[0].TS = %v, want strictly before SearchRecords[1].TS = %v", results[0].TS, results[1].TS)
		}
	})
}

// insertRecordFixture inserts a records row with a caller-chosen ts,
// bypassing InsertRecord (which always stamps ts = time.Now()).
func insertRecordFixture(t *testing.T, s *Store, sessionID string, ts time.Time, text string) string {
	t.Helper()
	id := uuid.NewString()
	_, err := s.db.Exec(`INSERT INTO records
		(id, ts, kind, tier, text, about, session_id, project_key, evidence, outcome, promoter, expires_at)
		VALUES (?, ?, ?, ?, ?, '[]', ?, NULL, '[]', NULL, NULL, NULL)`,
		id, tsToNanos(ts), string(KindNote), string(TierAgentDeclared), text, sessionID)
	if err != nil {
		t.Fatalf("insert record fixture: %v", err)
	}
	return id
}
