package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// openWorkspaceStore opens h's store with the workspace list the CLI will use.
func (h syncHome) openWorkspaceStore(t *testing.T, ws string) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(h.xdg, "backstory", "backstory.db"), []string{ws}, project.RealGit{})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// workspaceHandoff files a handoff under repo's key from a fresh active session
// in repo (a workspace child).
func (h syncHome) workspaceHandoff(t *testing.T, ws, repo, text string) string {
	t.Helper()
	st := h.openWorkspaceStore(t, ws)
	defer func() { _ = st.Close() }()
	repoKey := project.Key(repo, project.RealGit{}, []string{ws})
	// Only a project with a remote syncs; the session's cwd is under the
	// workspace, so this-week resolves the row via store.LatestHandoffAt.
	if err := st.UpsertProject(store.Project{Key: repoKey, RemoteURL: "git@github.com:acme/demo.git", Toplevel: repo, FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	sid, err := st.StartSession(store.StartSessionParams{Agent: "claude", CWD: repo, ProjectKey: repoKey, StartedAt: time.Now(), Origin: store.OriginLive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendEvent(store.Event{TS: time.Now(), Kind: "tool.use", SessionID: sid, Source: "shell", Payload: `{}`}); err != nil {
		t.Fatal(err)
	}
	id, err := st.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff,
		Text: text, SessionID: sid, ProjectKey: repoKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// F12: B writes H1, A writes H2 later; a real `backstory sync` over the
// BACKSTORY_SYNC_SSH local-subprocess transport inserts H1 after H2 on A (and
// H2 after H1 on B). The workspace session routes this-week through
// store.LatestHandoffAt; on both machines it must show H2, as Resume does.
func TestThisWeekHandoffMatchesResumeAfterSyncSubprocess(t *testing.T) {
	bin := buildBackstory(t)
	home := t.TempDir()
	ws := filepath.Join(home, "projects")
	repo := filepath.Join(ws, "demo")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil { //nolint:gosec // fixed args
		t.Fatalf("git init: %v\n%s", err, out)
	}
	t.Setenv("HOME", home)
	t.Setenv("BACKSTORY_WORKSPACE_DIRS", ws)
	// this-week hides everything under os.TempDir(); point it at a sibling so
	// the fixture's home (itself a temp dir) is not excluded.
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", "")

	a, b := syncHome{xdg: t.TempDir()}, syncHome{xdg: t.TempDir()}
	h1 := b.workspaceHandoff(t, ws, repo, "H1 older handoff from B")
	time.Sleep(20 * time.Millisecond)
	h2 := a.workspaceHandoff(t, ws, repo, "H2 newer handoff from A")

	useTransport(t, a, serveScript(bin, b))
	if code, out, errs := runLinkCLI(t, "pair", "laptop"); code != 0 {
		t.Fatalf("pair: code=%d out=%q err=%q", code, out, errs)
	}
	if code, out, errs := runLinkCLI(t, "sync"); code != 0 {
		t.Fatalf("sync: code=%d out=%q err=%q", code, out, errs)
	}
	if !a.has(t, h1) || !b.has(t, h2) {
		t.Fatal("handoffs did not cross")
	}

	for name, m := range map[string]syncHome{"A": a, "B": b} {
		t.Setenv("XDG_DATA_HOME", m.xdg)
		out, code := runCLI(t, repo, "this-week", "--json")
		if code != 0 {
			t.Fatalf("%s: this-week exit %d", name, code)
		}
		if !strings.Contains(out, `"handoff_id": "`+h2+`"`) && !strings.Contains(out, `"handoff_id":"`+h2+`"`) {
			t.Errorf("%s: this-week lacks H2 %s:\n%s", name, h2, out)
		}
		if strings.Contains(out, h1) {
			t.Errorf("%s: this-week shows older H1 %s", name, h1)
		}
		st := m.openWorkspaceStore(t, ws)
		res, err := block.Render(block.Params{
			Store: st, ProjectKey: project.Key(repo, project.RealGit{}, []string{ws}), SessionID: "caller",
			CWD: repo, Git: project.RealGit{}, WorkspaceDirs: []string{ws}, Now: time.Now(),
		})
		_ = st.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(res, "H2 newer handoff") {
			t.Errorf("%s: Resume lacks H2:\n%s", name, res)
		}
		if name == "A" && !strings.Contains(res, "H1 older handoff") {
			t.Errorf("A: Resume lacks H1 as also:\n%s", res)
		}
	}
}
