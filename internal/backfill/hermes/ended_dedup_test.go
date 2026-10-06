package hermes

import (
	"path/filepath"
	"testing"
	"time"
)

// TestImportSameRunTwiceAfterEndYieldsOneSession is task a757b754's DONE WHEN
// clause 2 for Hermes: importing the same finished run again (cursor gone)
// yields one session and no duplicate events.
//
// RA-MUTATION-PROBE: drop the RunSessionByHarnessSessionID match -> RED.
func TestImportSameRunTwiceAfterEndYieldsOneSession(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ended := t0.Add(10 * time.Minute)
	dbPath := filepath.Join(t.TempDir(), "state.db")
	newFixtureDB(t, dbPath,
		[]fixtureSession{{id: "D", cwd: "/work/repo", startedAt: t0, endedAt: &ended}},
		[]fixtureMessage{
			{sessionID: "D", role: "user", content: "hi", ts: t0, active: true},
			{sessionID: "D", role: "assistant",
				toolCalls: `[{"id":"call_D","name":"Bash","arguments":{"command":"ls"}}]`,
				ts:        t0.Add(time.Second), active: true},
		})
	st := mustOpenStore(t)
	opts := Options{Path: dbPath, Git: fakeGit{}, Workspaces: []string{}}
	if _, err := Import(st, opts); err != nil {
		t.Fatal(err)
	}
	events := countRows(t, st, `SELECT COUNT(*) FROM timeline_events`)
	if _, err := st.DB().Exec(`DELETE FROM backfill_cursors`); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(st, opts); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM sessions WHERE harness_session_id = 'D'`); n != 1 {
		t.Errorf("sessions = %d, want 1", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM timeline_events`); n != events {
		t.Errorf("events after re-import = %d, want %d", n, events)
	}
}
