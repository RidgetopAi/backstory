package mcp

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/socket"
	"github.com/RidgetopAi/backstory/internal/store"
)

// TestNoteAndStatusSurviveAnotherProcessRekey: one daemon connection keeps
// working, and status reports the canonical key, after `git init` plus a
// second Store's Open re-keys the connection's project (task 725021e4).
func TestNoteAndStatusSurviveAnotherProcessRekey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BACKSTORY_WORKSPACE_DIRS", "")
	dir := filepath.Join(home, "plain")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(home, "backstory.db")
	st, err := store.Open(dbPath, nil, project.RealGit{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	key := project.Key(dir, project.RealGit{}, nil)
	if key != dir {
		t.Fatalf("plain key = %q, want %q", key, dir)
	}
	selfPID := os.Getpid()
	procfs := fakeProcFS{
		status: map[int]ident.Status{selfPID: {PPid: 1, Name: "claude"}},
		cwd:    map[int]string{selfPID: dir},
	}
	resolver := &ident.Resolver{ProcFS: procfs, ProjectKey: func(string) string { return key }}
	sessions := NewSessionRegistry()
	sockPath := filepath.Join(t.TempDir(), "sock")
	srv, err := socket.Listen(sockPath, resolver, func(id ident.Identity, conn net.Conn) {
		defer func() { _ = conn.Close() }()
		ServeDaemonConn(id, conn, st, procfs, project.RealGit{}, nil, sessions, captureNeverOff, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	go func() { _ = srv.Serve() }()
	shim := dialShim(t, sockPath)

	note := func() NoteResult {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"kind": "note", "text": "hello"})
		raw, rerr := shim.CallTool(ToolNote, b)
		if rerr != nil {
			t.Fatalf("note: %v", rerr)
		}
		var res NoteResult
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatal(err)
		}
		return res
	}
	note()

	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "i"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil { //nolint:gosec // fixed args, test-owned temp dir
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	canon := project.Key(dir, project.RealGit{}, nil)
	if canon == key {
		t.Fatal("git init did not change the project key")
	}
	other, err := store.Open(dbPath, nil, project.RealGit{}) // the CLI process
	if err != nil {
		t.Fatal(err)
	}
	_ = other.Close()

	if res := note(); res.ProjectKey != canon {
		t.Errorf("note landed under %q, want %q", res.ProjectKey, canon)
	}
	raw, rerr := shim.CallTool(ToolStatus, nil)
	if rerr != nil {
		t.Fatalf("status: %v", rerr)
	}
	var sr StatusResult
	if err := json.Unmarshal(raw, &sr); err != nil {
		t.Fatal(err)
	}
	if sr.ProjectKey != canon {
		t.Errorf("status project_key = %q, want %q", sr.ProjectKey, canon)
	}
}
