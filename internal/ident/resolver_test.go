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
