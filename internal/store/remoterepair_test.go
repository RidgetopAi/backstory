package store

import (
	"path/filepath"
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
	keys := []string{"/r/live/.git", "/r/noremote/.git", "/r/gone/.git"}
	for _, k := range keys {
		if err := s.UpsertProject(Project{Key: k, Toplevel: filepath.Dir(k), FirstSeen: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	git := repairGit{
		"/r/live":     {CommonDir: "/r/live/.git", RemoteURL: "git@github.com:x/live.git", Toplevel: "/r/live"},
		"/r/noremote": {CommonDir: "/r/noremote/.git", Toplevel: "/r/noremote"},
		// /r/gone has vanished: git.Repo reports !ok.
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
		if id, _ := s.SyncIdentityOf("/r/live/.git"); id.ID != "github.com/x/live" {
			t.Fatalf("repaired project has no sync identity: %+v", id)
		}
		return out
	}
	first := remotes()
	if first["/r/live/.git"] != "git@github.com:x/live.git" || first["/r/noremote/.git"] != "" || first["/r/gone/.git"] != "" {
		t.Fatalf("after repair: %v", first)
	}
	second := remotes()
	for k, v := range first {
		if second[k] != v {
			t.Fatalf("second repair changed %s: %q -> %q", k, v, second[k])
		}
	}
}
