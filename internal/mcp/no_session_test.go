package mcp

import (
	"encoding/json"
	"testing"
)

// The installer's health probe asks status for no session: nothing is
// minted, the status answer still comes back. The same flag on a write
// method is ignored (a note still gets its session).
func TestNoSessionStatusMintsNothingButAnswers(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "not-a-known-harness", "/home/brian/build-clone", "clone-key")

	probe := dialShim(t, sockPath).WithoutSession()
	raw, rerr := probe.CallTool(ToolStatus, nil)
	if rerr != nil {
		t.Fatalf("status: %v", rerr)
	}
	var res StatusResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if !res.CaptureOn {
		t.Error("status did not answer normally")
	}
	for _, q := range []string{`SELECT count(*) FROM sessions`, `SELECT count(*) FROM projects`} {
		var n int
		if err := st.DB().QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s = %d after a no-session status", q, n)
		}
	}

	// Without the flag the same call mints a session (control), and the
	// flag does not ride on a note.
	if _, rerr := dialShim(t, sockPath).CallTool(ToolStatus, nil); rerr != nil {
		t.Fatal(rerr)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT count(*) FROM sessions`).Scan(&n); err != nil || n != 1 {
		t.Errorf("control: sessions = %d (err %v), want 1", n, err)
	}
	if got := declinesSession([]byte(`{"method":"note","no_session":true}`)); got {
		t.Error("no_session honoured on a write method")
	}
}
