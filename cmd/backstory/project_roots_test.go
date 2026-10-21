package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/project"
)

// weekProjectKeys lists every project_key a this-week --json output names in
// where_left_off, week and attention.
func weekProjectKeys(t *testing.T, raw string) map[string]bool {
	t.Helper()
	var out struct {
		Attention []struct {
			ProjectKey string `json:"project_key"`
		} `json:"attention"`
		WhereLeftOff []struct {
			Project *struct {
				ProjectKey string `json:"project_key"`
			} `json:"project"`
			Children []struct {
				ProjectKey string `json:"project_key"`
			} `json:"children"`
		} `json:"where_left_off"`
		Week []struct {
			ProjectKey string `json:"project_key"`
		} `json:"week"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	keys := map[string]bool{}
	for _, a := range out.Attention {
		if a.ProjectKey == "sync" { // environment state, not a project
			continue
		}
		keys[a.ProjectKey] = true
	}
	for _, r := range out.WhereLeftOff {
		if r.Project != nil {
			keys[r.Project.ProjectKey] = true
		}
		for _, c := range r.Children {
			keys[c.ProjectKey] = true
		}
	}
	for _, w := range out.Week {
		keys[w.ProjectKey] = true
	}
	return keys
}

func thisWeekText(t *testing.T, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run(append([]string{"this-week"}, args...), bytes.NewReader(nil), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	return out.String()
}

// The fixture's newest session is qux (outside the root); only foo is in it.
func TestThisWeekProjectRootsListsOnlyInScope(t *testing.T) {
	e := newHereEnv(t)
	t.Setenv("BACKSTORY_PROJECT_ROOTS", e.foo)

	_, raw := e.run(t)
	keys := weekProjectKeys(t, raw)
	if len(keys) != 1 || !keys[e.fooKey] {
		t.Fatalf("with roots, project keys = %v, want only %s", keys, e.fooKey)
	}
	text := thisWeekText(t)
	if !strings.Contains(text, "projects/foo") {
		t.Errorf("text lacks the in-scope project:\n%s", text)
	}
	for _, out := range []string{"projects/bar", "projects/qux", "acme-week"} {
		if strings.Contains(text, out) {
			t.Errorf("text lists out-of-scope %q:\n%s", out, text)
		}
	}
}

func TestThisWeekProjectRootsUnsetListsEverything(t *testing.T) {
	e := newHereEnv(t)
	_, raw := e.run(t)
	keys := weekProjectKeys(t, raw)
	for _, k := range []string{e.fooKey, e.barKey, e.quxKey} {
		if !keys[k] {
			t.Errorf("unset roots: %s missing from %v", k, keys)
		}
	}
	// A root covering everything changes nothing: scoping is the only delta.
	t.Setenv("BACKSTORY_PROJECT_ROOTS", "/")
	_, scoped := e.run(t)
	if scoped != raw {
		t.Errorf("roots=/ output differs from unset output")
	}
	text := thisWeekText(t)
	for _, in := range []string{"projects/foo", "projects/bar", "projects/qux"} {
		if !strings.Contains(text, in) {
			t.Errorf("text lacks %q:\n%s", in, text)
		}
	}
}

// The panel's current project (the `here` the bar widget reads) is the most
// recent in-scope project even though a newer out-of-scope one exists, for
// the `recent` fallback, a focused out-of-scope shell, and an out-of-scope
// --here DIR alike.
func TestHereNamesMostRecentInScopeProject(t *testing.T) {
	e := newHereEnv(t)
	t.Setenv("BACKSTORY_PROJECT_ROOTS", e.foo+string(os.PathListSeparator)+e.bar)

	e.fakeHyprctl(t, "FAIL", "")
	h := hereOf(t, mustRun(t, e, "--here", "auto"))
	if h["project_key"] != e.barKey || h["source"] != "recent" || h["display_name"] != "projects/bar" {
		t.Fatalf("recent here = %v, want bar (qux is newer but out of scope)", h)
	}

	e.fakeHyprctl(t, `{"address":"0xa","pid":100}`, `[]`)
	e.addProc(t, 100, 1, "ghostty", e.home, "")
	e.addProc(t, 101, 100, "bash", e.qux, "")
	h = hereOf(t, mustRun(t, e, "--here", "auto"))
	if h["project_key"] != e.barKey || h["source"] != "recent" {
		t.Fatalf("focused out-of-scope here = %v, want recent bar", h)
	}

	h = hereOf(t, mustRun(t, e, "--here", e.qux))
	if h["project_key"] != e.barKey || h["source"] != "recent" {
		t.Fatalf("--here out-of-scope DIR = %v, want recent bar", h)
	}
	h = hereOf(t, mustRun(t, e, "--here", e.foo))
	if h["project_key"] != e.fooKey || h["source"] != "arg" {
		t.Fatalf("--here in-scope DIR = %v, want foo", h)
	}
}

// status from a folder outside the roots exits 0 and says so; recall from the
// same folder still returns its own records.
func TestStatusAndRecallOutsideProjectRoots(t *testing.T) {
	bin := buildBackstory(t)
	harnessBin := buildHarnessClient(t, harnessName)
	_, runtimeDir, env := startTestDaemon(t, bin)
	sockPath := filepath.Join(runtimeDir, "backstory", "sock")
	root := t.TempDir()
	inside := filepath.Join(root, "repo")
	outside := t.TempDir()
	for _, d := range []string{inside, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil { //nolint:gosec // test dir
			t.Fatal(err)
		}
	}
	env = append(env, "BACKSTORY_PROJECT_ROOTS="+root)

	const noteText = "outside-roots note still recallable"
	if out, err := requestNoteAsHarness(t, harnessBin, sockPath, outside, "outside-roots-session",
		json.RawMessage(`{"kind":"note","text":"`+noteText+`"}`)); err != nil {
		t.Fatalf("note from outside folder: %v\n%s", err, out)
	}

	runIn := func(dir string, args ...string) (string, int) {
		cmd := exec.Command(bin, args...) //nolint:gosec // bin is this test's own built binary
		cmd.Env = env
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if ee, ok := err.(*exec.ExitError); ok {
			return string(out), ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), 0
	}

	out, code := runIn(outside, "status")
	if code != 0 || !strings.Contains(out, "outside BACKSTORY_PROJECT_ROOTS") {
		t.Fatalf("status outside roots = %q (exit %d), want exit 0 and the outside note", out, code)
	}
	if out, code := runIn(inside, "status"); code != 0 || strings.Contains(out, "outside BACKSTORY_PROJECT_ROOTS") {
		t.Fatalf("status inside roots = %q (exit %d), want no outside note", out, code)
	}

	key := project.Key(outside, project.RealGit{}, nil)
	rec, code := runIn(outside, "recall", "--project", key, "--json")
	if code != 0 || !strings.Contains(rec, noteText) {
		t.Fatalf("recall from outside folder = %q (exit %d), want its own note", rec, code)
	}
}
