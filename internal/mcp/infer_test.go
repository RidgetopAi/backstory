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

// inferFixture is task fd14c6cc's world: a temp HOME whose HOME/projects
// workspace holds git repos app-a and app-b (one commit each, no remote) and
// a NON-git folder notes-x. It sets its own HOME and clears
// BACKSTORY_WORKSPACE_DIRS, so results never depend on the invoking env.
type inferFixture struct {
	home, ws, appA, appB, notesX string
	wsKey, keyA, keyB            string
	workspaces                   []string
	st                           *store.Store
}

func newInferFixture(t *testing.T) *inferFixture {
	t.Helper()
	f := &inferFixture{home: t.TempDir()}
	t.Setenv("HOME", f.home)
	t.Setenv("BACKSTORY_WORKSPACE_DIRS", "")
	f.ws = filepath.Join(f.home, "projects")
	f.appA = filepath.Join(f.ws, "app-a")
	f.appB = filepath.Join(f.ws, "app-b")
	f.notesX = filepath.Join(f.ws, "notes-x")
	for _, d := range []string{f.appA, f.appB} {
		initGitRepo(t, d)
		if err := os.WriteFile(filepath.Join(d, "x.go"), []byte("package x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init"}} {
			if out, err := exec.Command("git", append([]string{"-C", d}, args...)...).CombinedOutput(); err != nil { //nolint:gosec // fixed args, test-owned temp dir
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
	}
	if err := os.MkdirAll(f.notesX, 0o750); err != nil {
		t.Fatal(err)
	}
	f.workspaces = []string{f.ws}
	git := project.RealGit{}
	f.wsKey = project.Key(f.ws, git, f.workspaces)
	f.keyA = project.Key(f.appA, git, f.workspaces)
	f.keyB = project.Key(f.appB, git, f.workspaces)
	if !project.IsWorkspaceKey(f.wsKey) || project.IsWorkspaceKey(f.keyA) {
		t.Fatalf("fixture keys wrong: ws=%q a=%q", f.wsKey, f.keyA)
	}
	f.st = mustOpenStore(t)
	return f
}

// session starts a fresh daemon session (own registry → own session row)
// whose cwd is cwd, with the real git resolver, and returns its shim.
func (f *inferFixture) session(t *testing.T, cwd string) *Server {
	t.Helper()
	key := project.Key(cwd, project.RealGit{}, f.workspaces)
	selfPID := os.Getpid()
	procfs := fakeProcFS{
		status: map[int]ident.Status{selfPID: {PPid: 1, Name: "claude"}},
		cwd:    map[int]string{selfPID: cwd},
	}
	resolver := &ident.Resolver{ProcFS: procfs, ProjectKey: func(string) string { return key }}
	sessions := NewSessionRegistry()
	sockPath := filepath.Join(t.TempDir(), "sock")
	srv, err := socket.Listen(sockPath, resolver, func(id ident.Identity, conn net.Conn) {
		defer func() { _ = conn.Close() }()
		ServeDaemonConn(id, conn, f.st, procfs, project.RealGit{}, nil, sessions, captureNeverOff, f.workspaces)
	})
	if err != nil {
		t.Fatalf("socket.Listen: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	go func() { _ = srv.Serve() }()
	return dialShim(t, sockPath)
}

func edit(t *testing.T, shim *Server, path string) {
	t.Helper()
	params, err := json.Marshal(PostToolUseParams{ToolName: "Edit", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, rerr := shim.callDaemon(DaemonMethodPostToolUse, params); rerr != nil {
		t.Fatalf("post_tool_use: %v", rerr)
	}
}

// noteAt writes a note and returns the result plus the stored record's key.
func (f *inferFixture) noteAt(t *testing.T, shim *Server, kind string, about ...string) (NoteResult, string) {
	t.Helper()
	args := map[string]any{"kind": kind, "text": "t-" + kind}
	if len(about) > 0 {
		args["about"] = about
	}
	b, _ := json.Marshal(args)
	raw, rerr := shim.CallTool(ToolNote, b)
	if rerr != nil {
		t.Fatalf("CallTool(note): %v", rerr)
	}
	var res NoteResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	rec, err := f.st.GetRecord(res.ID)
	if err != nil {
		t.Fatal(err)
	}
	return res, rec.ProjectKey
}

// TestWorkspaceSessionNoteFiledUnderEditedRepoAndRecalled is clauses 1 and 2.
func TestWorkspaceSessionNoteFiledUnderEditedRepoAndRecalled(t *testing.T) {
	f := newInferFixture(t)
	ws := f.session(t, f.ws)
	edit(t, ws, filepath.Join(f.appA, "x.go"))
	res, stored := f.noteAt(t, ws, "decision")
	if stored != f.keyA {
		t.Errorf("stored project_key = %q, want app-a's key %q", stored, f.keyA)
	}
	if res.ProjectKey != f.keyA {
		t.Errorf("NoteResult.project_key = %q, want %q", res.ProjectKey, f.keyA)
	}

	inA := f.session(t, f.appA)
	got := callRecall(t, inA, nil)
	if !containsID(got.Items, res.ID) {
		t.Errorf("recall from app-a cwd items %v lack decision %s", recallItemIDs(got.Items), res.ID)
	}
}

// TestWorkspaceSessionNoteKeyInference is clauses 3 and 4.
func TestWorkspaceSessionNoteKeyInference(t *testing.T) {
	f := newInferFixture(t)
	cases := []struct {
		name  string
		edits []string
		about []string
		kind  string
		want  string
	}{
		{"edits in two repos keep workspace", []string{filepath.Join(f.appA, "x.go"), filepath.Join(f.appB, "x.go")}, nil, "decision", f.wsKey},
		{"no edits, about in app-b", nil, []string{filepath.Join(f.appB, "y.go")}, "decision", f.keyB},
		{"no edits, no about", nil, nil, "decision", f.wsKey},
		{"edits only under non-git folder", []string{filepath.Join(f.notesX, "n.md")}, nil, "decision", f.wsKey},
		{"edits win over about", []string{filepath.Join(f.appA, "x.go")}, []string{filepath.Join(f.appB, "y.go")}, "note", f.keyA},
		{"handoff stays on workspace", []string{filepath.Join(f.appA, "x.go")}, nil, "handoff", f.wsKey},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			shim := f.session(t, f.ws)
			for _, e := range c.edits {
				edit(t, shim, e)
			}
			res, stored := f.noteAt(t, shim, c.kind, c.about...)
			if stored != c.want || res.ProjectKey != c.want {
				t.Errorf("stored=%q result=%q, want %q", stored, res.ProjectKey, c.want)
			}
		})
	}

	t.Run("app-a cwd session keeps its own key", func(t *testing.T) {
		shim := f.session(t, f.appA)
		edit(t, shim, filepath.Join(f.appB, "x.go"))
		for _, kind := range []string{"decision", "handoff"} {
			_, stored := f.noteAt(t, shim, kind)
			want := f.keyA
			if kind == "handoff" {
				want = f.wsKey // unchanged rule: handoffs home on the workspace
			}
			if stored != want {
				t.Errorf("%s stored under %q, want %q", kind, stored, want)
			}
		}
	})
}

