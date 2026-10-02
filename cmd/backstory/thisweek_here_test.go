package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

type hereEnv struct {
	home, dataDir, fakeBin, proc string
	foo, bar, qux, wtFoo         string
	fooKey, barKey, quxKey       string
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil { //nolint:gosec // test script must be executable
		t.Fatal(err)
	}
}

// newHereEnv builds a temp HOME with repos projects/foo (+ worktree wt-foo)
// and projects/bar, a store with a handoff for each plus the usual this-week
// fixture projects, a fake /proc root and a fake-binaries dir at the front
// of PATH. The environment is pinned so the test is hermetic whether or not
// the invoking shell has Hyprland / workspace variables set.
func newHereEnv(t *testing.T) *hereEnv {
	t.Helper()
	e := &hereEnv{home: t.TempDir(), dataDir: t.TempDir(), fakeBin: t.TempDir(), proc: t.TempDir()}
	e.foo = filepath.Join(e.home, "projects", "foo")
	e.bar = filepath.Join(e.home, "projects", "bar")
	e.qux = filepath.Join(e.home, "projects", "qux")
	e.wtFoo = filepath.Join(e.home, "wt-foo")
	for _, d := range []string{e.foo, e.bar, e.qux} {
		if err := os.MkdirAll(d, 0o755); err != nil { //nolint:gosec // test dir
			t.Fatal(err)
		}
		gitIn(t, d, "init", "-q")
		gitIn(t, d, "commit", "-q", "--allow-empty", "-m", "init")
	}
	gitIn(t, e.foo, "worktree", "add", "-q", e.wtFoo, "-b", "wt")
	e.fooKey = gitIn(t, e.foo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	e.barKey = gitIn(t, e.bar, "rev-parse", "--path-format=absolute", "--git-common-dir")
	e.quxKey = gitIn(t, e.qux, "rev-parse", "--path-format=absolute", "--git-common-dir")

	// HOME lives under the OS temp dir, which This Week's location rules
	// exclude; point TMPDIR at a sibling so the fixture projects show.
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("HOME", e.home)
	t.Setenv("XDG_DATA_HOME", e.dataDir)
	t.Setenv("BACKSTORY_WORKSPACE_DIRS", filepath.Join(e.home, "projects"))
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "")
	t.Setenv("PATH", e.fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	orig, origProc := thisWeekNow, thisWeekProcRoot
	thisWeekNow = func() time.Time { return thisWeekFixtureNow }
	thisWeekProcRoot = e.proc
	t.Cleanup(func() { thisWeekNow, thisWeekProcRoot = orig, origProc })

	buildThisWeekFixtureStore(t, e.dataDir)
	st, err := store.Open(filepath.Join(e.dataDir, "backstory", "backstory.db"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	for i, p := range []struct{ key, top string }{{e.fooKey, e.foo}, {e.barKey, e.bar}, {e.quxKey, e.qux}} {
		if err := st.UpsertProject(store.Project{Key: p.key, Toplevel: p.top, FirstSeen: date(4, 9, 0)}); err != nil {
			t.Fatal(err)
		}
		sess, err := st.StartSession(store.StartSessionParams{ID: fmt.Sprintf("here-s%d", i), Agent: "claude", CWD: p.top,
			ProjectKey: p.key, StartedAt: date(5+i, 9, 0), Origin: store.OriginLive})
		if err != nil {
			t.Fatal(err)
		}
		mustInsertThisWeekRecord(t, st, fixtureRecord{ID: fmt.Sprintf("here-h%d", i), ProjectKey: p.key, SessionID: sess,
			TS: date(5+i, 10, 0), Kind: store.KindHandoff, Tier: store.TierAgentDeclared, Text: "left off"})
	}
	return e
}

// proc adds a fake /proc/<pid> entry.
func (e *hereEnv) addProc(t *testing.T, pid, ppid int, name, cwd, tty string) {
	t.Helper()
	d := filepath.Join(e.proc, fmt.Sprint(pid))
	if err := os.MkdirAll(filepath.Join(d, "fd"), 0o755); err != nil { //nolint:gosec // test dir
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "status"), []byte(fmt.Sprintf("Name:\t%s\nPPid:\t%d\n", name, ppid)), 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	if err := os.Symlink(cwd, filepath.Join(d, "cwd")); err != nil {
		t.Fatal(err)
	}
	if tty != "" {
		if err := os.Symlink(tty, filepath.Join(d, "fd", "0")); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *hereEnv) fakeHyprctl(t *testing.T, active, clients string) {
	t.Helper()
	body := "case \"$1\" in\nactivewindow) echo '" + active + "';;\nclients) echo '" + clients + "';;\nesac"
	if active == "FAIL" {
		body = "exit 1"
	}
	writeScript(t, filepath.Join(e.fakeBin, "hyprctl"), body)
}

func (e *hereEnv) run(t *testing.T, args ...string) (map[string]any, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(append([]string{"this-week", "--json"}, args...), bytes.NewReader(nil), &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	var m map[string]any
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out.String())
	}
	return m, out.String()
}

func hereOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	h, ok := m["here"].(map[string]any)
	if !ok {
		t.Fatalf("no here object in %v", m)
	}
	return h
}

func TestHereArgResolvesWorktreeToMainKeyAndLeavesOutputUnchanged(t *testing.T) {
	e := newHereEnv(t)
	m, _ := e.run(t, "--here", e.wtFoo)
	h := hereOf(t, m)
	if h["project_key"] != e.fooKey || h["source"] != "arg" || h["cwd"] != e.wtFoo {
		t.Fatalf("here = %v, want key %s source arg cwd %s", h, e.fooKey, e.wtFoo)
	}
	if h["display_name"] != "projects/foo" {
		t.Fatalf("display_name = %v", h["display_name"])
	}
	// Without --here: no here key, no window key anywhere.
	_, plain := e.run(t)
	if strings.Contains(plain, `"here"`) || strings.Contains(plain, `"window"`) {
		t.Fatalf("plain output grew here/window: %s", plain)
	}
	// ...and the same JSON, byte for byte, as the committed golden's
	// fixture (the existing golden test covers the fixed-HOME run).
	_, withArg := e.run(t, "--here", e.wtFoo)
	var wm map[string]any
	_ = json.Unmarshal([]byte(withArg), &wm)
	delete(wm, "here")
	a, _ := json.Marshal(wm)
	var pm map[string]any
	_ = json.Unmarshal([]byte(plain), &pm)
	b, _ := json.Marshal(pm)
	if string(a) != string(b) {
		t.Fatalf("--here DIR changed more than the here key")
	}
}

func TestHereArgNonGitWorkspaceChildResolvesToWorkspaceKey(t *testing.T) {
	e := newHereEnv(t)
	dir := filepath.Join(e.home, "projects", "scratch")
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // test dir
		t.Fatal(err)
	}
	h := hereOf(t, mustRun(t, e, "--here", dir))
	if want := "workspace:" + filepath.Join(e.home, "projects"); h["project_key"] != want {
		t.Fatalf("project_key = %v, want %s", h["project_key"], want)
	}
}

func mustRun(t *testing.T, e *hereEnv, args ...string) map[string]any {
	t.Helper()
	m, _ := e.run(t, args...)
	return m
}

func TestHereAutoFocusedShell(t *testing.T) {
	e := newHereEnv(t)
	e.fakeHyprctl(t, `{"address":"0xa","class":"com.mitchellh.ghostty","pid":100}`, `[]`)
	e.addProc(t, 100, 1, "ghostty", e.home, "")
	e.addProc(t, 101, 100, "bash", e.foo, "")
	h := hereOf(t, mustRun(t, e, "--here", "auto"))
	if h["project_key"] != e.fooKey || h["source"] != "focused" {
		t.Fatalf("here = %v", h)
	}
}

func TestHereAutoDeepestNewestDescendantWins(t *testing.T) {
	e := newHereEnv(t)
	e.fakeHyprctl(t, `{"address":"0xa","pid":100}`, `[]`)
	e.addProc(t, 100, 1, "ghostty", e.home, "")
	e.addProc(t, 101, 100, "bash", e.bar, "")
	e.addProc(t, 102, 101, "vim", e.foo, "")
	h := hereOf(t, mustRun(t, e, "--here", "auto"))
	if h["project_key"] != e.fooKey {
		t.Fatalf("deepest descendant not used: %v", h)
	}
}

func TestHereAutoTmuxClientUsesPanePath(t *testing.T) {
	e := newHereEnv(t)
	e.fakeHyprctl(t, `{"address":"0xa","pid":200}`, `[]`)
	e.addProc(t, 200, 1, "ghostty", e.home, "")
	e.addProc(t, 201, 200, "tmux: client", e.home, "/dev/pts/7")
	writeScript(t, filepath.Join(e.fakeBin, "tmux"),
		`[ "$1" = display-message ] && [ "$2" = -c ] && [ "$3" = /dev/pts/7 ] && [ "$4" = -p ] && [ "$5" = '#{pane_current_path}' ] && echo `+e.foo+` || exit 1`)
	h := hereOf(t, mustRun(t, e, "--here", "auto"))
	if h["project_key"] != e.fooKey || h["source"] != "focused" {
		t.Fatalf("here = %v", h)
	}
}

func recentKey(t *testing.T, m map[string]any) string {
	t.Helper()
	rows := m["where_left_off"].([]any)
	r := rows[0].(map[string]any)
	if p, ok := r["project"].(map[string]any); ok {
		return p["project_key"].(string)
	}
	return r["children"].([]any)[0].(map[string]any)["project_key"].(string)
}

func TestHereAutoFallsBackToRecent(t *testing.T) {
	t.Run("hyprctl fails", func(t *testing.T) {
		e := newHereEnv(t)
		e.fakeHyprctl(t, "FAIL", "")
		m := mustRun(t, e, "--here", "auto")
		h := hereOf(t, m)
		if h["source"] != "recent" || h["project_key"] != recentKey(t, m) {
			t.Fatalf("here = %v", h)
		}
	})
	t.Run("hyprctl missing", func(t *testing.T) {
		e := newHereEnv(t)
		t.Setenv("PATH", e.fakeBin) // no hyprctl anywhere
		h := hereOf(t, mustRun(t, e, "--here", "auto"))
		if h["source"] != "recent" {
			t.Fatalf("here = %v", h)
		}
	})
	t.Run("focused pid has no descendants", func(t *testing.T) {
		e := newHereEnv(t)
		e.fakeHyprctl(t, `{"address":"0xb","class":"firefox","pid":300}`, `[]`)
		e.addProc(t, 300, 1, "firefox", e.foo, "")
		m := mustRun(t, e, "--here", "auto")
		h := hereOf(t, m)
		if h["source"] != "recent" || h["project_key"] != recentKey(t, m) {
			t.Fatalf("here = %v", h)
		}
	})
}

func TestHereAutoWindowAddresses(t *testing.T) {
	e := newHereEnv(t)
	clients := `[
	 {"address":"0xf1","class":"ghostty","pid":401,"focusHistoryID":3},
	 {"address":"0xf2","class":"ghostty","pid":402,"focusHistoryID":1},
	 {"address":"0xba","class":"ghostty","pid":403,"focusHistoryID":2},
	 {"address":"0xbr","class":"firefox","pid":404,"focusHistoryID":0}]`
	e.fakeHyprctl(t, `{"address":"0xba","pid":403}`, strings.ReplaceAll(clients, "\n", " "))
	for pid, cwd := range map[int]string{401: e.foo, 402: e.wtFoo, 403: e.bar} {
		e.addProc(t, pid, 1, "ghostty", e.home, "")
		e.addProc(t, pid+10, pid, "bash", cwd, "")
	}
	e.addProc(t, 404, 1, "firefox", e.foo, "")
	m := mustRun(t, e, "--here", "auto")
	got := map[string]string{}
	for _, r := range m["where_left_off"].([]any) {
		row := r.(map[string]any)
		var ps []any
		if p, ok := row["project"]; ok {
			ps = []any{p}
		} else {
			ps = row["children"].([]any)
		}
		for _, p := range ps {
			pm := p.(map[string]any)
			w, ok := pm["window"].(string)
			if !ok {
				t.Fatalf("project %v has no window string", pm["project_key"])
			}
			got[pm["project_key"].(string)] = w
		}
	}
	if got[e.fooKey] != "0xf2" || got[e.barKey] != "0xba" {
		t.Fatalf("windows = %v (foo wants lowest focusHistoryID 0xf2, bar 0xba)", got)
	}
	if w, ok := got[e.quxKey]; !ok || w != "" {
		t.Fatalf("third project window = %q (present %v), want \"\"", w, ok)
	}
}
