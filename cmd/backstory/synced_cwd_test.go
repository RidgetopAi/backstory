package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// plainRepoSession seeds h with the shared remote-backed project at repo and
// one active session in it (plus a handoff),
func (h syncHome) plainRepoSession(t *testing.T, repo, text string) {
	t.Helper()
	st := h.open(t)
	defer func() { _ = st.Close() }()
	key := project.Key(repo, project.RealGit{}, nil)
	if err := st.UpsertProject(store.Project{Key: key, RemoteURL: "git@github.com:acme/demo.git", Toplevel: repo, FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	sid, err := st.StartSession(store.StartSessionParams{Agent: "claude", CWD: repo, ProjectKey: key, StartedAt: time.Now(), Origin: store.OriginLive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendEvent(store.Event{TS: time.Now(), Kind: "tool.use", SessionID: sid, Source: "shell", Payload: `{}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertRecord(store.InsertRecordParams{Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff, Text: text, SessionID: sid, ProjectKey: key}); err != nil {
		t.Fatal(err)
	}
}

// F13: the repo is cloned at DIFFERENT paths on A and B; B's session is the
// newest and arrives on A by a real `backstory sync` over the
// BACKSTORY_SYNC_SSH subprocess transport. this-week on A must show A's own
// path — not empty, not B's.
func TestThisWeekCWDIsLocalAfterSyncSubprocess(t *testing.T) {
	bin := buildBackstory(t)
	home := t.TempDir()
	repoA := filepath.Join(home, "alpha", "demo")
	repoB := filepath.Join(home, "beta", "demo")
	for _, r := range []string{repoA, repoB} {
		if err := os.MkdirAll(r, 0o750); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "-C", r, "init", "-q").CombinedOutput(); err != nil { //nolint:gosec // fixed args
			t.Fatalf("git init: %v\n%s", err, out)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", "")

	a, b := syncHome{xdg: t.TempDir()}, syncHome{xdg: t.TempDir()}
	a.plainRepoSession(t, repoA, "A older")
	time.Sleep(20 * time.Millisecond)
	b.plainRepoSession(t, repoB, "B newest")

	useTransport(t, a, serveScript(bin, b))
	if code, out, errs := runLinkCLI(t, "pair", "laptop"); code != 0 {
		t.Fatalf("pair: code=%d out=%q err=%q", code, out, errs)
	}
	if code, out, errs := runLinkCLI(t, "sync"); code != 0 {
		t.Fatalf("sync: code=%d out=%q err=%q", code, out, errs)
	}

	t.Setenv("XDG_DATA_HOME", a.xdg)
	text, code := runCLI(t, repoA, "this-week")
	if code != 0 {
		t.Fatalf("this-week exit %d", code)
	}
	if !strings.Contains(text, "demo ("+repoA+")") || strings.Contains(text, repoB) || strings.Contains(text, "()") {
		t.Errorf("text row must show A's path %s only:\n%s", repoA, text)
	}
	out, code := runCLI(t, repoA, "this-week", "--json")
	if code != 0 {
		t.Fatalf("this-week --json exit %d", code)
	}
	if strings.Contains(out, repoB) || !strings.Contains(out, `"cwd": "`+repoA+`"`) && !strings.Contains(out, `"cwd":"`+repoA+`"`) {
		t.Errorf("json cwd must be A's path %s:\n%s", repoA, out)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
}
