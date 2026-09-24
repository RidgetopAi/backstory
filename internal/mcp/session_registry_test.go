package mcp

import (
	"fmt"
	"testing"

	"github.com/RidgetopAi/backstory/internal/ident"
)

// registrySweepProcFS is a minimal, mutable ProcFS double for exercising
// SessionRegistry.SessionFor's sweep in isolation, one level below the
// socket/store integration in session_test.go and status_test.go: no
// listener, no store, just the registry's own bookkeeping and the end
// callback it drives.
type registrySweepProcFS map[int]ident.Status

func (f registrySweepProcFS) Status(pid int) (ident.Status, error) {
	st, ok := f[pid]
	if !ok {
		return ident.Status{}, fmt.Errorf("registrySweepProcFS: no status for pid %d", pid)
	}
	return st, nil
}

func (registrySweepProcFS) Cwd(int) (string, error)       { return "", nil }
func (registrySweepProcFS) Cmdline(int) ([]string, error) { return nil, nil }

// endCall is one recorded invocation of the end callback SessionFor drives
// on eviction.
type endCall struct {
	sessionID string
	reason    string
}

// TestSessionRegistrySweepEvictsOnlyTheDeadEntry is a unit-level version of
// task 25b74537's DONE WHEN clause 1, isolating SessionRegistry from the
// socket/store layer: three harness processes are registered, one (pid 100)
// stops appearing in procfs between two SessionFor calls (simulating its
// exit) while another (pid 200) stays alive throughout — only pid 100's
// entry may be evicted, and it must be evicted exactly once, with the
// documented reason.
//
// RA-MUTATION-PROBE: SessionRegistry.sweep's `delete(r.sessions, key)`
// deleted (leaving end called but the entry still cached) -> RED (the next
// SessionFor call for pid 100 returns the ended session id instead of
// minting a new one); restored -> GREEN.
func TestSessionRegistrySweepEvictsOnlyTheDeadEntry(t *testing.T) {
	sessions := NewSessionRegistry()
	procfs := registrySweepProcFS{
		100: {StartTicks: 1},
		200: {StartTicks: 1},
	}
	var ended []endCall
	end := func(sessionID, reason string) { ended = append(ended, endCall{sessionID, reason}) }

	next := 0
	start := func() (string, error) {
		next++
		return fmt.Sprintf("session-%d", next), nil
	}

	idDead := ident.Identity{Harness: "claude", HarnessPID: 100, HarnessStartTicks: 1}
	idAlive := ident.Identity{Harness: "claude", HarnessPID: 200, HarnessStartTicks: 1}

	sidDead, err := sessions.SessionFor(idDead, procfs, end, start)
	if err != nil {
		t.Fatalf("SessionFor(dead, first call): %v", err)
	}
	sidAlive, err := sessions.SessionFor(idAlive, procfs, end, start)
	if err != nil {
		t.Fatalf("SessionFor(alive, first call): %v", err)
	}
	if len(ended) != 0 {
		t.Fatalf("end called %d time(s) before anything exited, want 0: %+v", len(ended), ended)
	}

	// pid 100 "exits": its /proc entry disappears. pid 200's stays put.
	delete(procfs, 100)

	// A third, unrelated connection is what notices, on its own SessionFor
	// call — exactly like a real daemon connection triggering the sweep.
	idThird := ident.Identity{Harness: "claude", HarnessPID: 300, HarnessStartTicks: 1}
	procfs[300] = ident.Status{StartTicks: 1}
	sidThird, err := sessions.SessionFor(idThird, procfs, end, start)
	if err != nil {
		t.Fatalf("SessionFor(third, triggers sweep): %v", err)
	}
	if sidThird == sidDead || sidThird == sidAlive {
		t.Fatalf("third identity's session %q collided with an existing one", sidThird)
	}

	if len(ended) != 1 {
		t.Fatalf("end called %d time(s), want exactly 1 (only pid 100's session): %+v", len(ended), ended)
	}
	if ended[0].sessionID != sidDead {
		t.Errorf("evicted session = %q, want the dead pid's session %q", ended[0].sessionID, sidDead)
	}
	if ended[0].reason != ReasonHarnessExited {
		t.Errorf("evicted reason = %q, want %q", ended[0].reason, ReasonHarnessExited)
	}

	// pid 200 (still alive) must still resolve to its original session, not
	// a new one, and must not itself have been swept.
	sidAliveAgain, err := sessions.SessionFor(idAlive, procfs, end, start)
	if err != nil {
		t.Fatalf("SessionFor(alive, second call): %v", err)
	}
	if sidAliveAgain != sidAlive {
		t.Errorf("still-alive pid 200's session changed (%q -> %q), want it reused", sidAlive, sidAliveAgain)
	}
	if len(ended) != 1 {
		t.Errorf("end called %d time(s) after the still-alive pid's second call, want still 1: %+v", len(ended), ended)
	}
}

// TestSessionRegistrySweepSkippedWhenProcFSNil documents SessionFor's escape
// hatch for a caller with no process table to check against (procfs == nil
// skips the sweep entirely, per its doc comment) — used by nothing in
// production (ServeDaemonConn always has a real ident.ProcFS) but exercised
// directly here so that guard has its own regression coverage.
func TestSessionRegistrySweepSkippedWhenProcFSNil(t *testing.T) {
	sessions := NewSessionRegistry()
	calls := 0
	start := func() (string, error) {
		calls++
		return fmt.Sprintf("session-%d", calls), nil
	}
	id := ident.Identity{Harness: "claude", HarnessPID: 100, HarnessStartTicks: 1}

	sid1, err := sessions.SessionFor(id, nil, nil, start)
	if err != nil {
		t.Fatalf("SessionFor (first, nil procfs): %v", err)
	}
	sid2, err := sessions.SessionFor(id, nil, nil, start)
	if err != nil {
		t.Fatalf("SessionFor (second, nil procfs): %v", err)
	}
	if sid1 != sid2 {
		t.Errorf("session changed across calls with procfs == nil (%q vs %q), want the cached entry reused untouched by any sweep", sid1, sid2)
	}
	if calls != 1 {
		t.Errorf("start called %d time(s), want exactly 1", calls)
	}
}
