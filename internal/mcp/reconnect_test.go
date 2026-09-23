package mcp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// countSessions returns the total number of session rows the daemon has
// ever started, live or ended — DONE WHEN clause 4's "creates exactly one
// live session" is about connections, not calls, so this counts rows, not
// LiveSessionsInProject's "still open" subset.
func countSessions(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

// TestCallToolReconnectsOnceAfterDaemonClosesConnection is DONE WHEN clause
// 3: if the daemon closes the shim's connection between two tool calls
// (idle past the daemon's own reaping, or any other reason), the shim's
// next tool call still succeeds via one re-dial and one retry, rather than
// surfacing the dead connection as an error to its caller.
func TestCallToolReconnectsOnceAfterDaemonClosesConnection(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	if _, rerr := shim.CallTool(ToolStatus, nil); rerr != nil {
		t.Fatalf("CallTool(status) first call: %v", rerr)
	}

	// Force the daemon side to see this connection close, simulating the
	// daemon reaping an idle shim connection between two tool calls. shim's
	// own package-private daemonConn field is reachable directly here (same
	// package as server.go), which is exactly the connection callDaemon
	// would otherwise keep reusing.
	if err := shim.daemonConn.Close(); err != nil {
		t.Fatalf("force-close shim's daemon connection: %v", err)
	}

	// Give the daemon side a moment to observe the close and end the first
	// session before the retry dials a second one.
	time.Sleep(50 * time.Millisecond)

	raw, rerr := shim.CallTool(ToolNote, json.RawMessage(`{"kind":"note","text":"note after forced close"}`))
	if rerr != nil {
		t.Fatalf("CallTool(note) after forced close: %v, want the shim to re-dial and retry transparently", rerr)
	}
	var result NoteResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal NoteResult: %v", err)
	}
	if result.ID == "" {
		t.Error("NoteResult.ID is empty after reconnect")
	}

	rec, err := st.GetRecord(result.ID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if rec.Text != "note after forced close" {
		t.Errorf("stored record text = %q, want the note written after reconnect", rec.Text)
	}
}

// TestShimCreatesExactlyOneSessionAcrossSpacedCalls is DONE WHEN clause 4's
// first half: three tool calls spaced apart, with no forced close, reuse the
// same daemon connection and so create exactly one session row — not one per
// call.
func TestShimCreatesExactlyOneSessionAcrossSpacedCalls(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	for i := 0; i < 3; i++ {
		if _, rerr := shim.CallTool(ToolStatus, nil); rerr != nil {
			t.Fatalf("CallTool(status) call %d: %v", i, rerr)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if got := countSessions(t, st); got != 1 {
		t.Errorf("session count = %d after 3 spaced calls with no forced close, want 1", got)
	}
}

// TestShimThatNeverCallsAToolCreatesNoSession is DONE WHEN clause 4's second
// half: a shim that dials lazily but never actually makes a daemon-backed
// tool call must never open a daemon connection at all, so it creates no
// session.
func TestShimThatNeverCallsAToolCreatesNoSession(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	_ = dialShim(t, sockPath)

	time.Sleep(50 * time.Millisecond)

	if got := countSessions(t, st); got != 0 {
		t.Errorf("session count = %d for a shim that never called a tool, want 0", got)
	}
}
