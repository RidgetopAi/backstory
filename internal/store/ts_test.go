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
// The fixture is anchored at the UNIX EPOCH (wholeSecond = 1970-01-01
// 00:00:00Z, ns == 0) rather than an arbitrary later date on purpose: as
// unix nanos, wholeSecond is 0 (a 1-digit text "0") and plus100ms is
// 100000000 (a 9-digit text "100000000") -- different digit counts, so a
// TEXT ts column (round-1 critic FAIL: reverting migration 0002's ts column
// to TEXT left this test GREEN) compares them lexically wrong ("0" <
// "100000000" survives, but the rate-cap window's `ts >= ?` bind-parameter
// comparison does not -- see recentRecordCount below). A same-digit-width
// pair (any date from 2001 on, giving a fixed 19-digit nanos value for both
// timestamps) compares identically under TEXT and INTEGER and cannot kill
// that mutation. This exact pair is also RED on origin/main, which still
// formats ts as RFC3339Nano text ("1970-01-01T00:00:00Z" vs
// "1970-01-01T00:00:00.1Z" -- the whole-second form sorts after the
// fractional one as a string).
//
// This test inserts its two fixture rows directly with SQL rather than via
// InsertRecord, which always stamps ts = time.Now(): the bug only
// reproduces for a specific pair of timestamps (one with ns == 0), which no
// amount of retrying a real insert can reliably hit.
func TestTsOrderingWholeSecondVsPlus100ms(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	sessionID := mustStartSession(t, s)

	wholeSecond := time.Unix(0, 0).UTC() // the unix epoch: ns == 0
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
