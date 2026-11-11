package mcp

import (
	"bytes"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
	bsync "github.com/RidgetopAi/backstory/internal/sync"
)

// pullOnce runs one sync with client pulling from server over in-process pipes.
func pullOnce(t *testing.T, client, server *store.Store) {
	t.Helper()
	human := store.Identity{Kind: store.IdentityHuman, Actor: "human"}
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := bsync.Serve(server, human, c2sR, s2cW, bsync.Options{})
		_ = s2cW.Close()
		_ = c2sR.Close()
		done <- err
	}()
	var wire bytes.Buffer
	_, err := bsync.Run(client, human, bsync.Cursors{}, io.TeeReader(s2cR, &wire), c2sW, bsync.Options{})
	_ = c2sW.Close()
	_, _ = io.Copy(io.Discard, s2cR)
	<-done
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
}

// TestRepoHandoffsUnderWorkspaceSync is DONE WHEN clause 4 (decision dc0a4387):
// on two paired stores, a handoff written by a session in a repo under a
// workspace dir, and a migrated workspace-homed one, both arrive on the other
// store under the repo's sync identity; a workspace-root handoff never does.
//
// Mutation probe: restore the any-depth workspace rehoming in handoffHomeKey
// -> the written handoff is workspace-homed, never served, and this is RED.
func TestRepoHandoffsUnderWorkspaceSync(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "projects")
	repo := filepath.Join(root, "tide-notes")
	initGitRepo(t, repo)
	ws := []string{root}
	repoKey := project.Key(repo, project.RealGit{}, ws)
	wsKey := "workspace:" + root
	const remote = "git@github.com:ridgetop-e2e/tide-notes.git"
	const identity = "sync:github.com/ridgetop-e2e/tide-notes"

	dbPath := filepath.Join(tmp, "a.db")
	a, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	written, err := mustOpenStoreRecord(t, a, ws, repo)
	if err != nil {
		t.Fatal(err)
	}
	if written.ProjectKey != repoKey {
		t.Fatalf("written handoff project_key = %q, want the repo key %q", written.ProjectKey, repoKey)
	}
	rootHandoff, err := mustOpenStoreRecord(t, a, ws, root)
	if err != nil {
		t.Fatal(err)
	}
	if rootHandoff.ProjectKey != wsKey {
		t.Fatalf("root handoff project_key = %q, want %q", rootHandoff.ProjectKey, wsKey)
	}

	// A legacy workspace-homed handoff by a repo session, as stored before the
	// rule changed; the migration at Open moves it.
	sid, err := a.StartSession(store.StartSessionParams{Agent: "claude", CWD: repo, ProjectKey: repoKey, StartedAt: time.Now(), Origin: store.OriginLive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB().Exec(`INSERT INTO records (id, ts, kind, tier, text, about, session_id, project_key, evidence, event_cursor)
		VALUES ('legacy-h', ?, 'handoff', 'agent-declared', 'legacy handoff', '[]', ?, ?, '[]', 0)`, time.Now().UnixNano(), sid, wsKey); err != nil {
		t.Fatal(err)
	}
	if err := a.UpsertProject(store.Project{Key: repoKey, RemoteURL: remote, Toplevel: repo, FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if a, err = store.Open(dbPath, nil, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if m, _ := filepath.Glob(dbPath + ".pre-rehome-*"); len(m) != 1 {
		t.Fatalf("pre-rehome backups = %v, want one", m)
	}

	b, err := store.Open(filepath.Join(tmp, "b.db"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	pullOnce(t, b, a)

	for _, id := range []string{written.ID, "legacy-h"} {
		got, err := b.GetRecord(id)
		if err != nil || got.ProjectKey != identity {
			t.Errorf("record %s on the peer = %+v err=%v, want project %s", id, got, err, identity)
		}
	}
	if _, err := b.GetRecord(rootHandoff.ID); err == nil {
		t.Error("the workspace-root handoff reached the peer; it has no sync identity")
	}
}
