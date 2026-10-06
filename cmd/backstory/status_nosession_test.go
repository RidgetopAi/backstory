package main

import "testing"

// TestStatusMintsNoSession: `backstory status` is a read-only CLI call and
// must not add a row to sessions, with or without BACKSTORY_NO_SESSION.
//
// RA-MUTATION-PROBE: remove srv.WithoutSession() in runStatus -> RED (a
// session row appears); restored -> GREEN.
func TestStatusMintsNoSession(t *testing.T) {
	bin := buildBackstory(t)
	dbPath, _, env := startTestDaemon(t, bin)
	s := mustOpenTestStore(t, dbPath)

	count := func() int {
		t.Helper()
		var n int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	if out, exitCode := runStatusSubprocess(t, bin, env); exitCode != 0 {
		t.Fatalf("backstory status: exit %d: %s", exitCode, out)
	}
	if after := count(); after != before {
		t.Errorf("sessions rows = %d after `backstory status`, want %d", after, before)
	}
}
