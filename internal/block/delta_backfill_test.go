package block_test

import (
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
)

// TestDeltaIgnoresEarlierSessionBackfilledAfterHandoff is task 8dac3c1d: a
// daemon restart backfills an earlier session's events after the handoff's
// cursor; that session already had events before it, so it is not "since the
// handoff". A new session with events only after the cursor still counts.
//
// RA-MUTATION-PROBE: drop the `!priorRuns[e.Run()]` check in deltaSlot -> RED
// ("Delta: 1 sessions" with no new session).
func TestDeltaIgnoresEarlierSessionBackfilledAfterHandoff(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	s1 := mustStartSession(t, s, "claude", "/proj", 100)
	now := time.Now()
	for i := 0; i < 10; i++ {
		mustAppendToolResult(t, s, s1, now, 0)
	}
	// The handoff is written by a later session (S2), as in the daemon-restart
	// case; S1 is not the handoff's own source, so only the cursor excludes it.
	s2 := mustStartSession(t, s, "claude", "/proj", 150)
	mustAppendToolResult(t, s, s2, now, 0)
	handoff := mustInsertHandoff(t, s, s2, "cut here")
	// Backfill appends S1's later events after the handoff's cursor.
	for i := 0; i < 6; i++ {
		mustAppendToolResult(t, s, s1, now, 0)
	}

	render := func(self string) string {
		out, err := block.Render(block.Params{
			Store: s, ProcFS: fakeProcFS{}, ProjectKey: testProjectKey, SessionID: self, Harness: "claude",
			Now: handoff.TS.Add(time.Hour),
		})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		return out
	}

	if out := render("reader"); strings.Contains(out, "sessions") && strings.Contains(out, "Delta:") {
		t.Fatalf("Delta counted the earlier session's backfilled events; got:\n%s", out)
	}

	s3 := mustStartSession(t, s, "claude", "/proj", 200)
	mustAppendToolResult(t, s, s3, now, 0)
	if out := render("reader"); !strings.Contains(out, "Delta: 1 sessions") {
		t.Fatalf("a new session after the cursor must count as 1; got:\n%s", out)
	}
}
