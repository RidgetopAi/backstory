package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// plainDir makes a non-git dir outside the fixture's workspace dirs.
func (e *identityEnv) plainDir(name string) string {
	e.t.Helper()
	dir := filepath.Join(e.home, "plain", name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		e.t.Fatal(err)
	}
	return dir
}

// startLong plays the daemon: a long-lived Store with a project row and
// session under key, and a first note under it.
func (e *identityEnv) startLong(st *store.Store, dir, key string) string {
	e.t.Helper()
	if err := st.UpsertProject(store.Project{Key: key, Toplevel: dir, FirstSeen: e.clock}); err != nil {
		e.t.Fatalf("UpsertProject: %v", err)
	}
	sid, err := st.StartSession(store.StartSessionParams{Agent: "claude", CWD: dir, ProjectKey: key, StartedAt: e.clock, Origin: store.OriginLive})
	if err != nil {
		e.t.Fatalf("StartSession: %v", err)
	}
	e.note(st, sid, key, "before")
	return sid
}

func (e *identityEnv) note(st *store.Store, sid, key, text string) string {
	e.t.Helper()
	id, err := st.InsertRecordWithEdges(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindNote, Text: text, SessionID: sid, ProjectKey: key,
	}, nil)
	if err != nil {
		e.t.Fatalf("note %q under %q: %v", text, key, err)
	}
	return id
}

func (e *identityEnv) projectKeys(st *store.Store) []string {
	e.t.Helper()
	rows, err := st.DB().Query(`SELECT key FROM projects ORDER BY key`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			e.t.Fatal(err)
		}
		keys = append(keys, k)
	}
	return keys
}

// gitInit turns dir into a repo and returns its canonical (common dir) key.
func (e *identityEnv) gitInit(dir string) string {
	e.t.Helper()
	e.git(dir, "init")
	e.git(dir, "commit", "--allow-empty", "-m", "init")
	return project.Key(dir, project.RealGit{}, e.wsDirs)
}

// TestLongLivedStoreNoteSurvivesOtherStoresRepoIdentityMerge: DONE WHEN (1).
func TestLongLivedStoreNoteSurvivesOtherStoresRepoIdentityMerge(t *testing.T) {
	e := newIdentityEnv(t)
	dir := e.plainDir("notgit")
	a := e.open()
	defer func() { _ = a.Close() }()
	sid := e.startLong(a, dir, dir)

	canon := e.gitInit(dir)
	if canon != filepath.Join(dir, ".git") {
		t.Fatalf("canonical key = %q, want <dir>/.git", canon)
	}
	b := e.open() // the CLI: its Open runs the merge
	_ = b.Close()

	id := e.note(a, sid, dir, "after")
	rec, err := a.GetRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ProjectKey != canon {
		t.Errorf("record project_key = %q, want %q", rec.ProjectKey, canon)
	}
}

// TestLongLivedStoreNoteSurvivesOtherStoresWorkspaceSweep: DONE WHEN (2).
func TestLongLivedStoreNoteSurvivesOtherStoresWorkspaceSweep(t *testing.T) {
	e := newIdentityEnv(t)
	dir := e.plainDir("ws")
	a, err := store.Open(e.db, nil, project.RealGit{}) // old config: dir is a plain key
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	sid := e.startLong(a, dir, dir)

	e.wsDirs = append(e.wsDirs, dir)
	canon := project.Key(dir, project.RealGit{}, e.wsDirs)
	if !project.IsWorkspaceKey(canon) {
		t.Fatalf("key %q is not a workspace key", canon)
	}
	b := e.open() // sweeps dir -> canon
	_ = b.Close()

	id := e.note(a, sid, dir, "after")
	rec, err := a.GetRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ProjectKey != canon {
		t.Errorf("record project_key = %q, want %q", rec.ProjectKey, canon)
	}
}

// TestLongLivedStoreUpsertAndStartSessionDoNotRecreateLegacyRow: DONE WHEN (3).
func TestLongLivedStoreUpsertAndStartSessionDoNotRecreateLegacyRow(t *testing.T) {
	e := newIdentityEnv(t)
	dir := e.plainDir("notgit")
	a := e.open()
	defer func() { _ = a.Close() }()
	e.startLong(a, dir, dir)
	canon := e.gitInit(dir)
	_ = e.open().Close()

	if err := a.UpsertProject(store.Project{Key: dir, Toplevel: dir, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if _, err := a.StartSession(store.StartSessionParams{Agent: "claude", CWD: dir, ProjectKey: dir, StartedAt: time.Now(), Origin: store.OriginLive}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if keys := e.projectKeys(a); len(keys) != 1 || keys[0] != canon {
		t.Errorf("projects = %v, want only %q", keys, canon)
	}
}

// TestAliasIgnoredWhileLegacyRowLive: a re-registered plain spelling is not
// redirected by a stale alias.
func TestAliasIgnoredWhileLegacyRowLive(t *testing.T) {
	e := newIdentityEnv(t)
	dir := e.plainDir("notgit")
	a := e.open()
	defer func() { _ = a.Close() }()
	e.startLong(a, dir, dir)
	canon := e.gitInit(dir)
	_ = e.open().Close()
	if got := a.CanonicalProjectKey(dir); got != canon {
		t.Fatalf("CanonicalProjectKey = %q, want %q", got, canon)
	}
	if _, err := a.DB().Exec(`INSERT INTO projects (key, toplevel, first_seen) VALUES (?, ?, 1)`, dir, dir); err != nil {
		t.Fatal(err)
	}
	if got := a.CanonicalProjectKey(dir); got != dir {
		t.Errorf("CanonicalProjectKey with live row = %q, want %q", got, dir)
	}
}

// TestInsertForeignKeyFailureNamesKeyAndSession: DONE WHEN (5).
func TestInsertForeignKeyFailureNamesKeyAndSession(t *testing.T) {
	e := newIdentityEnv(t)
	st := e.open()
	defer func() { _ = st.Close() }()
	_, err := st.InsertRecordWithEdges(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindNote, Text: "x",
		SessionID: "sess-missing-123", ProjectKey: "/no/such/project",
	}, nil)
	if err == nil {
		t.Fatal("expected a foreign key error")
	}
	for _, want := range []string{"/no/such/project", "sess-missing-123", "FOREIGN KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}
