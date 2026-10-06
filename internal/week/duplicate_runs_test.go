package week

import (
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// TestBuildWeekGridCountsDuplicateRunRowsOnce is task a757b754's DONE WHEN
// clause 3 (this-week half): two session rows already on disk with the same
// agent and harness_session_id are one run, so one session in the week grid.
//
// RA-MUTATION-PROBE: count dayStats' sessions by SessionID again (e.Run()
// -> e.SessionID) -> RED (Sessions = 2).
func TestBuildWeekGridCountsDuplicateRunRowsOnce(t *testing.T) {
	st := openTestStore(t)
	const proj = "proj-dup"
	upsertProject(t, st, proj, "/home/brian/dup")
	day := time.Now().UTC().Truncate(24 * time.Hour)
	for _, id := range []string{"dup-live", "dup-backfilled"} {
		sid, err := st.StartSession(store.StartSessionParams{
			ID: id, Agent: "claude", HarnessSessionID: "H", CWD: "/home/brian/dup",
			ProjectKey: proj, StartedAt: day, Origin: store.OriginLive,
		})
		if err != nil {
			t.Fatal(err)
		}
		appendEvent(t, st, store.Event{TS: day.Add(time.Minute), Kind: "tool.use", SessionID: sid, Source: "shell", Payload: `{"name":"Edit","path":"main.go"}`})
	}
	res, err := Build(Params{Store: st, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range res.Week {
		if d.ProjectKey == proj {
			if d.Sessions != 1 {
				t.Fatalf("Sessions = %d, want 1 (two rows, one run)", d.Sessions)
			}
			return
		}
	}
	t.Fatalf("no week entry for %s", proj)
}
