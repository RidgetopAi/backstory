package mcp

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/socket"
	"github.com/RidgetopAi/backstory/internal/store"
)

// shellProcFS is a fakeProcFS whose process table a test can edit while the
// daemon runs: the emitting peer is this test process (pid selfPID), whose
// parent — the "interactive shell" — is whatever setShell last set.
type shellProcFS struct {
	mu     sync.Mutex
	self   int
	shells map[int]bool // shell pids that currently exist
	parent int
	cwd    string
}

func (f *shellProcFS) setParent(pid int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.parent = pid
	f.shells[pid] = true
}

func (f *shellProcFS) exit(pid int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.shells, pid)
}

func (f *shellProcFS) Status(pid int) (ident.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case pid == f.self:
		return ident.Status{PPid: f.parent, Name: "backstory", StartTicks: 1}, nil
	case f.shells[pid]:
		return ident.Status{PPid: 1, Name: "bash", StartTicks: uint64(pid)}, nil
	}
	return ident.Status{}, fmt.Errorf("shellProcFS: no status for pid %d", pid)
}

func (f *shellProcFS) Cwd(int) (string, error)       { return f.cwd, nil }
func (f *shellProcFS) Cmdline(int) ([]string, error) { return nil, nil }

func shellDaemon(t *testing.T, st *store.Store, git fakeGit, cwd string) (string, *shellProcFS) {
	t.Helper()
	procfs := &shellProcFS{self: os.Getpid(), shells: map[int]bool{}, cwd: cwd}
	resolver := &ident.Resolver{ProcFS: procfs, ProjectKey: func(string) string { return "proj-key" }}
	sessions := NewSessionRegistry()
	sockPath := filepath.Join(t.TempDir(), "sock")
	srv, err := socket.Listen(sockPath, resolver, func(id ident.Identity, conn net.Conn) {
		defer func() { _ = conn.Close() }()
		ServeDaemonConn(id, conn, st, procfs, git, nil, sessions, captureNeverOff, nil)
	})
	if err != nil {
		t.Fatalf("socket.Listen: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	go func() { _ = srv.Serve() }()
	return sockPath, procfs
}

func emitShell(t *testing.T, sockPath, cmd string) {
	t.Helper()
	params, err := json.Marshal(ShellEmitParams{Cmd: cmd})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// One fresh connection per emit, exactly like `backstory shell emit`.
	shim := dialShim(t, sockPath)
	if _, rerr := shim.callDaemon(DaemonMethodShellEmit, params); rerr != nil {
		t.Fatalf("shell_emit %q: %v", cmd, rerr)
	}
}

func countSessionsByAgent(t *testing.T, st *store.Store, agent string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE agent = ?`, agent).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

// TestShellEmitsFromOneShellShareOneSession: 5 emits whose parent is the same
// shell pid make exactly one session (agent shell) and 5 events; a second
// shell pid makes a second session.
func TestShellEmitsFromOneShellShareOneSession(t *testing.T) {
	st := mustOpenStore(t)
	sockPath, procfs := shellDaemon(t, st, fakeGit{}, "/home/brian/proj")

	procfs.setParent(4001)
	for i := 0; i < 5; i++ {
		emitShell(t, sockPath, fmt.Sprintf("cmd %d", i))
	}
	if got := countSessionsByAgent(t, st, ident.HarnessShell); got != 1 {
		t.Fatalf("shell sessions after 5 emits from one shell = %d, want 1", got)
	}
	var total int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&total); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if total != 1 {
		t.Fatalf("total sessions = %d, want 1", total)
	}
	if got := countTimelineEvents(t, st); got != 5 {
		t.Fatalf("timeline events = %d, want 5", got)
	}

	procfs.setParent(4002)
	emitShell(t, sockPath, "from the second shell")
	if got := countSessionsByAgent(t, st, ident.HarnessShell); got != 2 {
		t.Fatalf("shell sessions after a second shell pid = %d, want 2", got)
	}
}

// TestEndingShellSessionWritesNoGitState: a shell session ends when its shell
// process is gone (the registry sweep), and ending it observes no git state
// even when the cwd is a dirty working tree.
func TestEndingShellSessionWritesNoGitState(t *testing.T) {
	st := mustOpenStore(t)
	cwd := "/home/brian/proj"
	sockPath, procfs := shellDaemon(t, st, fakeGit{cwd: {Branch: "main", Uncommitted: 3}}, cwd)

	procfs.setParent(4001)
	emitShell(t, sockPath, "ls")
	var shellSID string
	if err := st.DB().QueryRow(`SELECT id FROM sessions WHERE agent = ?`, ident.HarnessShell).Scan(&shellSID); err != nil {
		t.Fatalf("shell session: %v", err)
	}

	procfs.exit(4001)
	procfs.setParent(4002)
	emitShell(t, sockPath, "pwd") // the next connection sweeps the dead shell's session

	deadline := time.Now().Add(2 * time.Second)
	for {
		var exitKind *string
		if err := st.DB().QueryRow(`SELECT exit_kind FROM sessions WHERE id = ?`, shellSID).Scan(&exitKind); err != nil {
			t.Fatalf("read session: %v", err)
		}
		if exitKind != nil {
			if *exitKind != ReasonHarnessExited {
				t.Fatalf("exit_kind = %q, want %q", *exitKind, ReasonHarnessExited)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shell session not ended after its shell exited")
		}
		time.Sleep(10 * time.Millisecond)
	}

	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM timeline_events WHERE kind = ?`, payload.KindSessionGitState).Scan(&n); err != nil {
		t.Fatalf("count git_state: %v", err)
	}
	if n != 0 {
		t.Fatalf("session.git_state events after ending a shell session = %d, want 0", n)
	}
}
