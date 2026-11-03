package week

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// wsDesk is Brian's desk: a workspace of non-git folders, a scratchpad and
// Claude's memory dir outside the rows, and a fixed reference week.
type wsDesk struct {
	st                                  *store.Store
	home, ws, pad, mem                  string
	subA, subB, subC, foo               string
	rules                               LocationRules
	git                                 repoGit
	dayA, dayB, dayRoot, dayC, dayEdits time.Time
}

func (d *wsDesk) work(t *testing.T, id, cwd string, at time.Time, tools ...[2]string) {
	t.Helper()
	sid := startAgentSession(t, d.st, id, "claude", "workspace:"+d.ws, cwd, at)
	for i, tool := range tools {
		payload := fmt.Sprintf(`{"name":%q}`, tool[0])
		if tool[1] != "" {
			payload = fmt.Sprintf(`{"name":%q,"path":%q}`, tool[0], tool[1])
		}
		appendEvent(t, d.st, store.Event{TS: at.Add(time.Duration(i+1) * time.Minute), Kind: "tool.use", SessionID: sid, Source: "shell", Payload: payload})
	}
}

func newWSDesk(t *testing.T) *wsDesk {
	t.Helper()
	scratch := t.TempDir()
	d := &wsDesk{st: openTestStore(t), home: t.TempDir()}
	d.ws = filepath.Join(d.home, "projects")
	d.subA = filepath.Join(d.ws, "freax")
	d.subB = filepath.Join(d.ws, "personal")
	d.subC = filepath.Join(d.ws, "livestream")
	d.foo = filepath.Join(d.ws, "foo")
	d.pad = filepath.Join(scratch, "claude-1000", "scratchpad", "s.md")
	d.mem = filepath.Join(d.home, ".claude", "projects", "X", "memory", "m.md")
	d.rules = DefaultLocationRules(d.home, scratch)
	d.git = repoGit{root: filepath.Join(d.home, "no-repo")}
	d.dayA = fixtureNow.AddDate(0, 0, -5)
	d.dayB = fixtureNow.AddDate(0, 0, -4)
	d.dayRoot = fixtureNow.AddDate(0, 0, -3)
	d.dayC = fixtureNow.AddDate(0, 0, -2)
	d.dayEdits = fixtureNow.AddDate(0, 0, -1)

	// A: research only, one Write into the scratchpad.
	d.work(t, "a", d.subA, d.dayA, [2]string{"WebFetch", ""}, [2]string{"Bash", ""}, [2]string{"Write", d.pad})
	// B: Bash and a note, no edits.
	d.work(t, "b", d.subB, d.dayB, [2]string{"Bash", ""}, [2]string{"mcp__backstory__note", ""})
	// Root: Bash plus a Write into an excluded dir.
	d.work(t, "root", d.ws, d.dayRoot, [2]string{"Bash", ""}, [2]string{"Write", d.mem})
	// C: edits a file inside C.
	d.work(t, "c", d.subC, d.dayC, [2]string{"Edit", filepath.Join(d.subC, "main.go")})
	// A root session that edits foo/x.go: edit-derived label foo wins.
	d.work(t, "root-edits-foo", d.ws, d.dayEdits, [2]string{"Bash", ""}, [2]string{"Edit", filepath.Join(d.foo, "x.go")})
	return d
}

func (d *wsDesk) build(t *testing.T) Result {
	t.Helper()
	res, err := Build(Params{Store: d.st, Git: d.git, WorkspaceDirs: []string{d.ws}, Locations: d.rules, Now: fixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return res
}

func weekSessions(res Result, name string) (days, sessions int) {
	for _, w := range res.Week {
		if w.DisplayName == name && w.Sessions > 0 {
			days++
			sessions += w.Sessions
		}
	}
	return days, sessions
}

func rowNames(res Result) map[string]bool {
	got := map[string]bool{}
	for _, row := range res.WhereLeftOff {
		if row.Group == "" {
			got[row.Project.DisplayName] = true
		}
		for _, c := range row.Children {
			got[c.DisplayName] = true
		}
	}
	return got
}

// A session in a non-git workspace folder earns its folder's row and week
// bar from its cwd even when it edits nothing, or only excluded places.
func TestBuildWorkspaceSessionsLabelledByCWD(t *testing.T) {
	d := newWSDesk(t)
	res := d.build(t)
	rows := rowNames(res)
	// The root counts two sessions on two days: the Bash-only root session
	// and the root session that edited foo (a session always counts for the
	// folder it started in, decision 1e53165a).
	for name, want := range map[string]int{"projects/freax": 1, "projects/personal": 1, "projects": 2, "projects/livestream": 1} {
		if !rows[name] {
			t.Errorf("where_left_off lacks %q: have %v", name, rows)
		}
		if days, n := weekSessions(res, name); days != want || n != want {
			t.Errorf("week bars for %q: %d days, %d sessions, want %d and %d", name, days, n, want, want)
		}
	}
	for name := range rows {
		for _, bad := range []string{"scratchpad", "memory"} {
			if name == bad {
				t.Errorf("excluded location %q became a row", name)
			}
		}
	}
	// A bar for a research-only session counts the session, not files.
	for _, w := range res.Week {
		if (w.DisplayName == "projects/freax" || w.DisplayName == "projects/personal") && w.FilesTouched != 0 {
			t.Errorf("%s files_touched = %d, want 0", w.DisplayName, w.FilesTouched)
		}
	}
}

// A root session editing foo/x.go is foo's AND the root's (it started there,
// decision 1e53165a): the edit-derived label no longer displaces the cwd one.
func TestBuildWorkspaceEditLabelBeatsCWD(t *testing.T) {
	d := newWSDesk(t)
	res := d.build(t)
	if !rowNames(res)["projects/foo"] {
		t.Fatalf("no projects/foo row: %v", rowNames(res))
	}
	if _, n := weekSessions(res, "projects/foo"); n != 1 {
		t.Errorf("projects/foo sessions = %d, want 1", n)
	}
	// The editing session also counts toward the root, on the day it ran.
	var rootEditDay bool
	for _, w := range res.Week {
		if w.DisplayName == "projects" && w.Day.Equal(d.dayEdits.UTC().Truncate(24*time.Hour)) && w.Sessions == 1 {
			rootEditDay = true
		}
	}
	if !rootEditDay {
		t.Errorf("no root bar with 1 session on the day of the foo edit")
	}
	if days, n := weekSessions(res, "projects"); days != 2 || n != 2 {
		t.Errorf("root bars: %d days, %d sessions, want 2 and 2", days, n)
	}
}

// Decision 1e53165a clause 3: a session that started in projects/personal but
// edited only outside the workspace (and in a git repo inside it) still shows
// under personal.
func TestBuildSessionShowsUnderItsStartFolderWhenItEditedElsewhere(t *testing.T) {
	d := newWSDesk(t)
	d.work(t, "away", d.subB, d.dayC, [2]string{"Write", filepath.Join(d.home, ".local", "bin", "cam-watch")}, [2]string{"Edit", filepath.Join(d.foo, "y.go")})
	res := d.build(t)
	if days, n := weekSessions(res, "projects/personal"); days != 2 || n != 2 {
		t.Errorf("personal bars: %d days, %d sessions, want 2 and 2", days, n)
	}
	if _, n := weekSessions(res, "projects/foo"); n != 2 {
		t.Errorf("foo sessions = %d, want 2 (the edit-only session and the away one)", n)
	}
}
