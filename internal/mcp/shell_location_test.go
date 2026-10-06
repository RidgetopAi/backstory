package mcp

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/socket"
	"github.com/RidgetopAi/backstory/internal/store"
)

// locShellDaemon is a real daemon whose process table lets a test move the
// emitting peer's observed cwd (setCwd) while one shell, pid 4001, stays the
// emitters' session leader; the project key is derived from the observed cwd.
func locShellDaemon(t *testing.T, st *store.Store) (string, *shellProcFS) {
	t.Helper()
	procfs := &shellProcFS{self: os.Getpid(), shells: map[int]bool{}}
	procfs.setParent(4001)
	resolver := &ident.Resolver{ProcFS: procfs, ProjectKey: func(cwd string) string { return "key:" + cwd }}
	sessions := NewSessionRegistry()
	sockPath := filepath.Join(t.TempDir(), "sock")
	srv, err := socket.Listen(sockPath, resolver, func(id ident.Identity, conn net.Conn) {
		defer func() { _ = conn.Close() }()
		ServeDaemonConn(id, conn, st, procfs, fakeGit{}, nil, sessions, captureNeverOff, nil)
	})
	if err != nil {
		t.Fatalf("socket.Listen: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	go func() { _ = srv.Serve() }()
	return sockPath, procfs
}

func (f *shellProcFS) setCwd(dir string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cwd = dir
}

func emitShellExit(t *testing.T, sockPath, cmd, claimedCWD string, exit int) {
	t.Helper()
	params, err := json.Marshal(ShellEmitParams{Cmd: cmd, CWD: claimedCWD, Exit: exit})
	if err != nil {
		t.Fatal(err)
	}
	if _, rerr := dialShim(t, sockPath).callDaemon(DaemonMethodShellEmit, params); rerr != nil {
		t.Fatalf("shell_emit %q: %v", cmd, rerr)
	}
}

func blockAt(t *testing.T, sockPath string, procfs *shellProcFS, dir string) string {
	t.Helper()
	procfs.setCwd(dir)
	raw, rerr := dialShim(t, sockPath).callDaemon(DaemonMethodBlock, nil)
	if rerr != nil {
		t.Fatalf("block at %s: %v", dir, rerr)
	}
	var res BlockResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	return res.Block
}

// TestShellFailureLandsInTheDirectoryItRanIn: one shell session runs a command
// in directory A, then a failing command in repo R. R's SessionStart block
// shows it as Last failure; A's does not (task 149d6cd4, N7).
func TestShellFailureLandsInTheDirectoryItRanIn(t *testing.T) {
	st := mustOpenStore(t)
	sockPath, procfs := locShellDaemon(t, st)
	dirA, repoR := "/home/brian/scratch-a", "/home/brian/repo-r"

	procfs.setCwd(dirA)
	emitShellExit(t, sockPath, "ls", "", 0)
	procfs.setCwd(repoR)
	emitShellExit(t, sockPath, "go test ./...", "", 1)

	if got := blockAt(t, sockPath, procfs, repoR); !strings.Contains(got, "Last failure: go test ./... exit 1") {
		t.Errorf("repo R's block lacks the failure:\n%s", got)
	}
	if got := blockAt(t, sockPath, procfs, dirA); strings.Contains(got, "Last failure") {
		t.Errorf("directory A's block shows a failure that ran in R:\n%s", got)
	}
}

// TestShellEmitAttributedToObservedCwd: an emit claiming a different cwd than
// its process really has is recorded at the real one, in the real one's project.
func TestShellEmitAttributedToObservedCwd(t *testing.T) {
	st := mustOpenStore(t)
	sockPath, procfs := locShellDaemon(t, st)
	real := "/home/brian/real-repo"
	procfs.setCwd(real)
	emitShellExit(t, sockPath, "make", "/home/brian/claimed-elsewhere", 2)

	var pl, projectKey string
	if err := st.DB().QueryRow(`SELECT e.payload, s.project_key FROM timeline_events e JOIN sessions s ON s.id = e.session_id WHERE e.kind = ?`,
		payload.KindShellCommand).Scan(&pl, &projectKey); err != nil {
		t.Fatalf("read event: %v", err)
	}
	var sc payload.ShellCommand
	if err := json.Unmarshal([]byte(pl), &sc); err != nil {
		t.Fatal(err)
	}
	if sc.CWD != real {
		t.Errorf("event cwd = %q, want observed %q", sc.CWD, real)
	}
	if projectKey != "key:"+real {
		t.Errorf("event's project = %q, want %q", projectKey, "key:"+real)
	}
}
