package store

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
)

type repairGit map[string]project.Repo

func (g repairGit) Repo(cwd string) (project.Repo, bool) { r, ok := g[cwd]; return r, ok }
func (g repairGit) State(string) (project.State, bool)   { return project.State{}, false }

func TestUpsertProjectNeverClobbersRemote(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	const key = "/home/me/app/.git"
	put := func(p Project) Project {
		t.Helper()
		p.Key, p.Toplevel, p.FirstSeen = key, "/home/me/app", time.Now()
		if err := s.UpsertProject(p); err != nil {
			t.Fatalf("UpsertProject: %v", err)
		}
		got, ok, err := s.GetProject(key)
		if err != nil || !ok {
			t.Fatalf("GetProject: %v %v", ok, err)
		}
		return got
	}
	put(Project{GitCommonDir: key, RemoteURL: "https://github.com/x/app"})

	got := put(Project{}) // a live session start that observed no repo
	if got.RemoteURL != "https://github.com/x/app" || got.GitCommonDir != key {
		t.Fatalf("empty upsert clobbered: %+v", got)
	}
	if id, _ := s.SyncIdentityOf(key); id.ID != "github.com/x/app" {
		t.Fatalf("sync identity lost: %+v", id)
	}
	got = put(Project{RemoteURL: "https://github.com/x/renamed"})
	if got.RemoteURL != "https://github.com/x/renamed" || got.GitCommonDir != key {
		t.Fatalf("non-empty remote did not replace: %+v", got)
	}
}

func TestFillMissingRemotesRepairsNullRemotes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backstory.db")
	s := mustOpen(t, path)
	root := t.TempDir()
	keys := []string{root + "/live/.git", root + "/noremote/.git", root + "/gone/.git"}
	for _, k := range keys[:2] {
		if err := os.MkdirAll(k, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range keys {
		if err := s.UpsertProject(Project{Key: k, Toplevel: filepath.Dir(k), FirstSeen: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	git := repairGit{
		root + "/live":     {CommonDir: root + "/live/.git", RemoteURL: "git@github.com:x/live.git", Toplevel: root + "/live"},
		root + "/noremote": {CommonDir: root + "/noremote/.git", Toplevel: root + "/noremote"},
		// gone has vanished: git.Repo reports !ok.
	}
	remotes := func() map[string]string {
		s, err := Open(path, nil, git)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer func() { _ = s.Close() }()
		out := map[string]string{}
		for _, k := range keys {
			p, _, err := s.GetProject(k)
			if err != nil {
				t.Fatal(err)
			}
			out[k] = p.RemoteURL
		}
		if id, _ := s.SyncIdentityOf(root + "/live/.git"); id.ID != "github.com/x/live" {
			t.Fatalf("repaired project has no sync identity: %+v", id)
		}
		return out
	}
	first := remotes()
	if first[root+"/live/.git"] != "git@github.com:x/live.git" || first[root+"/noremote/.git"] != "" || first[root+"/gone/.git"] != "" {
		t.Fatalf("after repair: %v", first)
	}
	second := remotes()
	for k, v := range first {
		if second[k] != v {
			t.Fatalf("second repair changed %s: %q -> %q", k, v, second[k])
		}
	}
}

// countingGit wraps repairGit and counts Repo calls per cwd.
type countingGit struct {
	repairGit
	mu    sync.Mutex
	calls map[string]int
}

func (g *countingGit) Repo(cwd string) (project.Repo, bool) {
	g.mu.Lock()
	g.calls[cwd]++
	g.mu.Unlock()
	return g.repairGit.Repo(cwd)
}

func (g *countingGit) total() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, c := range g.calls {
		n += c
	}
	return n
}

func seedProjects(t *testing.T, path string, keys ...string) {
	t.Helper()
	s := mustOpen(t, path)
	for _, k := range keys {
		if err := s.UpsertProject(Project{Key: k, GitCommonDir: k, Toplevel: filepath.Dir(k), FirstSeen: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func openCounting(t *testing.T, path string, git project.Git) {
	t.Helper()
	s, err := Open(path, nil, git)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFillMissingRemotesDecidesEachProjectOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backstory.db")
	root := t.TempDir()
	none, live := root+"/none/.git", root+"/live/.git"
	for _, k := range []string{none, live} {
		if err := os.MkdirAll(k, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	seedProjects(t, path, none, live)
	git := &countingGit{calls: map[string]int{}, repairGit: repairGit{
		root + "/none": {CommonDir: none, Toplevel: root + "/none"},
		root + "/live": {CommonDir: live, RemoteURL: "git@github.com:x/live.git", Toplevel: root + "/live"},
	}}
	openCounting(t, path, git)
	openCounting(t, path, git)
	openCounting(t, path, git)
	if git.calls[root+"/none"] != 1 {
		t.Fatalf("remote-less project asked %d times over 3 Opens, want 1", git.calls[root+"/none"])
	}
	if git.calls[root+"/live"] != 1 {
		t.Fatalf("project with a remote asked %d times, want 1", git.calls[root+"/live"])
	}
}

func TestFillMissingRemotesSkipsVanishedCommonDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backstory.db")
	gone := t.TempDir() + "/gone/.git" // never created
	seedProjects(t, path, gone)
	git := &countingGit{calls: map[string]int{}, repairGit: repairGit{}}
	openCounting(t, path, git)
	openCounting(t, path, git)
	if n := git.total(); n != 0 {
		t.Fatalf("git ran %d times for a vanished common dir, want 0", n)
	}
}

func TestRemoteCheckedMarkerDoesNotFreezeRemoteURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backstory.db")
	key := t.TempDir() + "/app/.git"
	if err := os.MkdirAll(key, 0o750); err != nil {
		t.Fatal(err)
	}
	seedProjects(t, path, key)
	git := &countingGit{calls: map[string]int{}, repairGit: repairGit{
		filepath.Dir(key): {CommonDir: key, Toplevel: filepath.Dir(key)},
	}}
	openCounting(t, path, git) // checked, no remote: stamped

	s := mustOpen(t, path)
	defer func() { _ = s.Close() }()
	if err := s.UpsertProject(Project{Key: key, GitCommonDir: key, RemoteURL: "https://github.com/x/app", Toplevel: filepath.Dir(key)}); err != nil {
		t.Fatal(err)
	}
	p, _, err := s.GetProject(key)
	if err != nil || p.RemoteURL != "https://github.com/x/app" {
		t.Fatalf("a later observed remote was not recorded: %+v %v", p, err)
	}
	if id, _ := s.SyncIdentityOf(key); id.ID != "github.com/x/app" {
		t.Fatalf("sync identity not derived: %+v", id)
	}
}
