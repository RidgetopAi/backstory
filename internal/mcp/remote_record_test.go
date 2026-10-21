package mcp

import (
	"path/filepath"
	"testing"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

type remoteGit map[string]project.Repo

func (g remoteGit) Repo(cwd string) (project.Repo, bool) { r, ok := g[cwd]; return r, ok }
func (g remoteGit) State(string) (project.State, bool)   { return project.State{}, false }

// A live session start records the repo's remote (and keeps it across a
// second start), so the project has a sync identity.
func TestStartSessionRecordsRemoteURL(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "backstory.db"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	const key = "/home/me/app/.git"
	git := remoteGit{"/home/me/app/sub": {CommonDir: key, RemoteURL: "https://github.com/x/app", Toplevel: "/home/me/app"}}
	id := ident.Identity{ProjectKey: key, CWD: "/home/me/app/sub", Harness: ident.HarnessUnknown}

	for i := 0; i < 2; i++ {
		if _, err := startSession(st, git, id); err != nil {
			t.Fatalf("startSession: %v", err)
		}
		p, ok, err := st.GetProject(key)
		if err != nil || !ok {
			t.Fatalf("GetProject: %v %v", ok, err)
		}
		if p.RemoteURL != "https://github.com/x/app" || p.GitCommonDir != key {
			t.Fatalf("start %d: project = %+v", i, p)
		}
		if sid, _ := st.SyncIdentityOf(key); sid.ID != "github.com/x/app" {
			t.Fatalf("start %d: sync identity = %+v", i, sid)
		}
	}
}
