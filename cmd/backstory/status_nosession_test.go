package main

import (
	"strings"
	"testing"
)

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

// TestStatusPrintsSelfDevice (task 4f00e324): `backstory status` names the
// store's self device as `device: <name> (<id8>)`.
func TestStatusPrintsSelfDevice(t *testing.T) {
	bin := buildBackstory(t)
	dbPath, _, env := startTestDaemon(t, bin)
	s := mustOpenTestStore(t, dbPath)
	dev, err := s.SelfDevice()
	if err != nil {
		t.Fatal(err)
	}
	out, exitCode := runStatusSubprocess(t, bin, env)
	if exitCode != 0 {
		t.Fatalf("backstory status: exit %d: %s", exitCode, out)
	}
	want := "device: " + dev.Name + " (" + dev.ID[:8] + ")\n"
	if !strings.Contains(out, want) {
		t.Errorf("status output lacks %q:\n%s", want, out)
	}
}
