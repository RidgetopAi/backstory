package ident_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
)

// fakeProcFS is a fully synthetic /proc tree; no test in this file touches
// the real filesystem (AGENT-CONTRACT.md §Observed identity).
type fakeProcFS struct {
	status map[int]ident.Status
	cwd    map[int]string
}

func (f fakeProcFS) Status(pid int) (ident.Status, error) {
	st, ok := f.status[pid]
	if !ok {
		return ident.Status{}, fmt.Errorf("fakeProcFS: no status for pid %d", pid)
	}
	return st, nil
}

func (f fakeProcFS) Cwd(pid int) (string, error) {
	cwd, ok := f.cwd[pid]
	if !ok {
		return "", fmt.Errorf("fakeProcFS: no cwd for pid %d", pid)
	}
	return cwd, nil
}

func (f fakeProcFS) Cmdline(int) ([]string, error) { return nil, nil }

// callLogProcFS wraps a fakeProcFS and records every pid a Status call was
// made for, in order, so a test can assert the walk actually visited every
// hop instead of inferring it from the final Harness value alone (critic T2
// on task 73333c8c: TestResolveNoKnownHarness stayed green under a mutation
// that stopped the walk at the first hop, because HarnessUnknown is also
// Resolve's pre-walk seed value).
type callLogProcFS struct {
	fakeProcFS
	calls *[]int
}

func (f callLogProcFS) Status(pid int) (ident.Status, error) {
	*f.calls = append(*f.calls, pid)
	return f.fakeProcFS.Status(pid)
}

// TestResolveKnownHarness is the tree from the punch's acceptance clause 1:
// pid 900 shim <- 800 claude <- 700 tmux <- 600 alacritty <- 1.
func TestResolveKnownHarness(t *testing.T) {
	procfs := fakeProcFS{
		status: map[int]ident.Status{
			900: {PPid: 800, Name: "shim"},
			800: {PPid: 700, Name: "claude"},
			700: {PPid: 600, Name: "tmux"},
			600: {PPid: 1, Name: "alacritty"},
		},
		cwd: map[int]string{800: "/home/brian/proj"},
	}

	r := &ident.Resolver{
		ProcFS: procfs,
		ProjectKey: func(cwd string) string {
			return "key:" + cwd
		},
	}

	id := r.Resolve(ident.PeerCreds{UID: 1000, PID: 900})

	if id.Kind != ident.KindAgent {
		t.Errorf("Kind = %v, want KindAgent", id.Kind)
	}
	if id.UID != 1000 {
		t.Errorf("UID = %d, want 1000", id.UID)
	}
	if id.PID != 900 {
		t.Errorf("PID = %d, want 900", id.PID)
	}
	if id.Harness != "claude" {
		t.Errorf("Harness = %q, want claude", id.Harness)
	}
	if id.HarnessPID != 800 {
		t.Errorf("HarnessPID = %d, want 800", id.HarnessPID)
	}
	if id.CWD != "/home/brian/proj" {
		t.Errorf("CWD = %q, want /home/brian/proj", id.CWD)
	}
	if id.ProjectKey != "key:/home/brian/proj" {
		t.Errorf("ProjectKey = %q, want key:/home/brian/proj", id.ProjectKey)
	}
}

// TestResolveNoKnownHarness covers a tree that never meets a harness in
// KnownHarnesses before running off the top.
func TestResolveNoKnownHarness(t *testing.T) {
	procfs := fakeProcFS{
		status: map[int]ident.Status{
			500: {PPid: 400, Name: "bash"},
			400: {PPid: 300, Name: "sshd"},
			300: {PPid: 1, Name: "systemd"},
		},
		cwd: map[int]string{500: "/tmp"},
	}

	r := &ident.Resolver{ProcFS: procfs}
	id := r.Resolve(ident.PeerCreds{UID: 1000, PID: 500})

	if id.Kind != ident.KindAgent {
		t.Errorf("Kind = %v, want KindAgent", id.Kind)
	}
	if id.Harness != ident.HarnessUnknown {
		t.Errorf("Harness = %q, want %q", id.Harness, ident.HarnessUnknown)
	}
	if id.HarnessPID != 0 {
		t.Errorf("HarnessPID = %d, want 0", id.HarnessPID)
	}
}

// TestResolveWalkStopsAtPID1 makes sure a chain that runs straight up to pid
// 1 without a match terminates instead of trying to read pid 1's status.
func TestResolveWalkStopsAtPID1(t *testing.T) {
	procfs := fakeProcFS{
		status: map[int]ident.Status{
			2: {PPid: 1, Name: "init-child"},
			// deliberately no entry for pid 1: a call to Status(1) would fail
			// the test via fakeProcFS's "no status" error, proving the walk
			// never makes that call.
		},
	}

	r := &ident.Resolver{ProcFS: procfs}

	done := make(chan ident.Identity, 1)
	go func() { done <- r.Resolve(ident.PeerCreds{UID: 1, PID: 2}) }()

	select {
	case id := <-done:
		if id.Harness != ident.HarnessUnknown {
			t.Errorf("Harness = %q, want %q", id.Harness, ident.HarnessUnknown)
		}
	case <-time.After(time.Second):
		t.Fatal("Resolve did not terminate walking to pid 1")
	}
}

// TestResolveCycleNoPanic is a PPid cycle: it must terminate rather than
// loop forever, and must not panic.
func TestResolveCycleNoPanic(t *testing.T) {
	procfs := fakeProcFS{
		status: map[int]ident.Status{
			10: {PPid: 20, Name: "a"},
			20: {PPid: 10, Name: "b"},
		},
	}

	r := &ident.Resolver{ProcFS: procfs}

	done := make(chan ident.Identity, 1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				t.Errorf("Resolve panicked: %v", rec)
				done <- ident.Identity{}
			}
		}()
		done <- r.Resolve(ident.PeerCreds{UID: 1, PID: 10})
	}()

	select {
	case id := <-done:
		if id.Harness != ident.HarnessUnknown {
			t.Errorf("Harness = %q, want %q", id.Harness, ident.HarnessUnknown)
		}
	case <-time.After(time.Second):
		t.Fatal("Resolve did not terminate on a PPid cycle")
	}
}

// TestResolveKnownHarnessTwoHopsUp guards against the walk stopping at the
// first hop (critic T2 on task 73333c8c): the starting pid's own process
// name ("wrapper") is not a known harness, nor is its parent's ("shim"); the
// known harness ("claude") sits two hops above the peer's own pid. RED if
// the walk only ever checks the starting pid, or only walks one hop up.
func TestResolveKnownHarnessTwoHopsUp(t *testing.T) {
	procfs := fakeProcFS{
		status: map[int]ident.Status{
			950: {PPid: 900, Name: "wrapper"},
			900: {PPid: 800, Name: "shim"},
			800: {PPid: 700, Name: "claude"},
			700: {PPid: 1, Name: "tmux"},
		},
		cwd: map[int]string{800: "/home/brian/proj"},
	}

	r := &ident.Resolver{ProcFS: procfs}
	id := r.Resolve(ident.PeerCreds{UID: 1000, PID: 950})

	if id.Harness != "claude" {
		t.Errorf("Harness = %q, want claude (walk must not stop before hop two)", id.Harness)
	}
	if id.HarnessPID != 800 {
		t.Errorf("HarnessPID = %d, want 800", id.HarnessPID)
	}
}

// TestResolveNoKnownHarnessVisitsEveryHop strengthens
// TestResolveNoKnownHarness: it doesn't just assert the final Harness value
// (which is also Resolve's pre-walk seed, so it stays HarnessUnknown even if
// the walk never ran at all) — it asserts, via the fake's call log, that
// every hop up to pid 1 was actually visited. RED if the walk stops at the
// first hop, or never walks.
func TestResolveNoKnownHarnessVisitsEveryHop(t *testing.T) {
	var calls []int
	procfs := callLogProcFS{
		fakeProcFS: fakeProcFS{
			status: map[int]ident.Status{
				500: {PPid: 400, Name: "bash"},
				400: {PPid: 300, Name: "sshd"},
				300: {PPid: 1, Name: "systemd"},
			},
			cwd: map[int]string{500: "/tmp"},
		},
		calls: &calls,
	}

	r := &ident.Resolver{ProcFS: procfs}
	id := r.Resolve(ident.PeerCreds{UID: 1000, PID: 500})

	if id.Harness != ident.HarnessUnknown {
		t.Errorf("Harness = %q, want %q", id.Harness, ident.HarnessUnknown)
	}

	want := []int{500, 400, 300}
	if len(calls) != len(want) {
		t.Fatalf("Status call log = %v, want every hop visited %v", calls, want)
	}
	for i, pid := range want {
		if calls[i] != pid {
			t.Errorf("Status call log[%d] = %d, want %d (calls: %v)", i, calls[i], pid, calls)
		}
	}
}

// TestResolveCwdErrorSetsReason is the punch's acceptance clause 1 (task
// f2718b5b): when a known harness is found but its cwd can't be read — what
// a mount-sandboxed systemd --user unit's implicit user namespace produces
// for /proc/<harness_pid>/cwd — Resolve must not silently return CWD="" and
// ProjectKey="" indistinguishable from "no harness found"; it names the
// error in Reason instead, and ProjectKey is never computed from an unread
// cwd.
func TestResolveCwdErrorSetsReason(t *testing.T) {
	procfs := fakeProcFS{
		status: map[int]ident.Status{
			800: {PPid: 700, Name: "claude"},
			700: {PPid: 1, Name: "tmux"},
		},
		// deliberately no cwd entry for 800: fakeProcFS.Cwd returns an error,
		// standing in for the unreadable /proc/800/cwd probe 2 measured.
	}
	projectKeyCalled := false

	r := &ident.Resolver{
		ProcFS: procfs,
		ProjectKey: func(cwd string) string {
			projectKeyCalled = true
			return "key:" + cwd
		},
	}

	id := r.Resolve(ident.PeerCreds{UID: 1000, PID: 800})

	if id.Harness != "claude" {
		t.Errorf("Harness = %q, want claude", id.Harness)
	}
	if id.CWD != "" {
		t.Errorf("CWD = %q, want empty", id.CWD)
	}
	if id.ProjectKey != "" {
		t.Errorf("ProjectKey = %q, want empty", id.ProjectKey)
	}
	if projectKeyCalled {
		t.Error("ProjectKey func was called despite the cwd read failing")
	}
	if id.Reason == "" {
		t.Error("Reason is empty, want a non-empty reason naming the cwd error")
	}
}

// TestResolveCwdSuccessLeavesReasonEmpty is
// TestResolveCwdErrorSetsReason's control: a readable cwd must leave Reason
// empty, so a non-empty Reason reliably signals the failure case.
func TestResolveCwdSuccessLeavesReasonEmpty(t *testing.T) {
	procfs := fakeProcFS{
		status: map[int]ident.Status{
			800: {PPid: 700, Name: "claude"},
			700: {PPid: 1, Name: "tmux"},
		},
		cwd: map[int]string{800: "/home/brian/proj"},
	}

	r := &ident.Resolver{ProcFS: procfs}
	id := r.Resolve(ident.PeerCreds{UID: 1000, PID: 800})

	if id.Reason != "" {
		t.Errorf("Reason = %q, want empty", id.Reason)
	}
}

// TestResolveDoesNotSetDeclared makes sure Resolve never populates Declared
// itself: that is strictly the socket layer's job, from the connection's
// request, never from anything Resolve touches.
func TestResolveDoesNotSetDeclared(t *testing.T) {
	procfs := fakeProcFS{
		status: map[int]ident.Status{900: {PPid: 1, Name: "shim"}},
	}
	r := &ident.Resolver{ProcFS: procfs}
	id := r.Resolve(ident.PeerCreds{UID: 1, PID: 900})
	if id.Declared != nil {
		t.Errorf("Declared = %v, want nil", id.Declared)
	}
}
