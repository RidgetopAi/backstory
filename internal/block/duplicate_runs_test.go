package block_test

import (
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/store"
)

// TestDeltaCountsDuplicateRunRowsOnce is task a757b754's DONE WHEN clause 3
// (Delta half): a store already holding two session rows with the same agent
// and harness_session_id reads as 1 session in the SessionStart Delta; a row
// for a different run still counts separately.
//
// RA-MUTATION-PROBE: count duplicate rows separately in deltaSlot
// (e.Run() -> e.SessionID) -> RED ("3 sessions").
func TestDeltaCountsDuplicateRunRowsOnce(t *testing.T) {
	s := newTestStore(t)
	const key = "/home/alice/dup"
	mustUpsertProject(t, s, key)
	now := time.Now()
	for _, c := range []struct{ id, hsid string }{{"row-1", "H"}, {"row-2", "H"}, {"row-3", "OTHER"}} {
		sid, err := s.StartSession(store.StartSessionParams{
			ID: c.id, Agent: "claude", HarnessSessionID: c.hsid, CWD: key, ProjectKey: key,
			StartedAt: now, Origin: store.OriginBackfilled,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.AppendEvent(store.Event{TS: now, Kind: "session.start", SessionID: sid, Source: "backfill", Payload: `{}`}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.EventsSinceID(key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := block.DeltaSummary(events, nil, "self"), "Delta: 2 sessions"; got != want {
		t.Fatalf("Delta = %q, want %q (rows 1 and 2 are one run)", got, want)
	}
}
