package store_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/recall"
	"github.com/RidgetopAi/backstory/internal/store"
	"github.com/RidgetopAi/backstory/internal/week"
)

// identityEnv is a hermetic HOME with real git: task 029485ae's fixture. It
// unsets GIT_DIR/GIT_WORK_TREE/GIT_INDEX_FILE so a `make check` run inside a
// git hook cannot redirect the fixture's git at some other repo, and it does
// not depend on BACKSTORY_WORKSPACE_DIRS: the workspace list is always passed
// explicitly.
type identityEnv struct {
	t      *testing.T
	home   string
	db     string
	wsDirs []string
	clock  time.Time
}

func newIdentityEnv(t *testing.T) *identityEnv {
	t.Helper()
	home := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	for _, v := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "BACKSTORY_WORKSPACE_DIRS"} {
		t.Setenv(v, "")
		_ = os.Unsetenv(v)
	}
	return &identityEnv{
		t: t, home: home, db: filepath.Join(home, "backstory.db"),
		wsDirs: []string{filepath.Join(home, "projects")},
		clock:  time.Now().UTC().Add(-time.Hour),
	}
}

func (e *identityEnv) git(dir string, args ...string) {
	e.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "init.defaultBranch=main"}, args...)...) //nolint:gosec // fixed git argv in a test fixture
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

func (e *identityEnv) initRepo(name string) string {
	e.t.Helper()
	dir := filepath.Join(e.home, "projects", name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		e.t.Fatal(err)
	}
	e.git(dir, "init")
	e.git(dir, "commit", "--allow-empty", "-m", "init")
	return dir
}

func (e *identityEnv) open() *store.Store {
	e.t.Helper()
	st, err := store.Open(e.db, e.wsDirs, project.RealGit{})
	if err != nil {
		e.t.Fatalf("Open: %v", err)
	}
	return st
}

// session plays what the daemon does for one session in cwd — open the
// store, resolve the key, upsert the project, start a session — then writes
// one record of kind/text when text != "". Returns the record id.
func (e *identityEnv) session(cwd string, kind store.RecordKind, text string) string {
	e.t.Helper()
	st := e.open()
	defer func() { _ = st.Close() }()
	git := project.RealGit{}
	key := project.Key(cwd, git, e.wsDirs)
	repo, _ := git.Repo(cwd)
	e.clock = e.clock.Add(time.Minute)
	if err := st.UpsertProject(store.Project{Key: key, GitCommonDir: repo.CommonDir, RemoteURL: repo.RemoteURL, Toplevel: repo.Toplevel, FirstSeen: e.clock}); err != nil {
		e.t.Fatalf("UpsertProject: %v", err)
	}
	sid, err := st.StartSession(store.StartSessionParams{
		Agent: "claude", CWD: cwd, ProjectKey: key, StartedAt: e.clock, Origin: store.OriginLive,
	})
	if err != nil {
		e.t.Fatalf("StartSession: %v", err)
	}
	if _, err := st.AppendEvent(store.Event{TS: e.clock, Kind: payload.KindSessionStart, SessionID: sid, Source: "daemon", Payload: "{}"}); err != nil {
		e.t.Fatalf("AppendEvent: %v", err)
	}
	if text == "" {
		return ""
	}
	id, err := st.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: kind, Text: text, SessionID: sid, ProjectKey: key,
	})
	if err != nil {
		e.t.Fatalf("InsertRecord: %v", err)
	}
	return id
}

func (e *identityEnv) recallIDs(cwd string) []string {
	e.t.Helper()
	st := e.open()
	defer func() { _ = st.Close() }()
	key := project.Key(cwd, project.RealGit{}, e.wsDirs)
	res, err := recall.Build(st, recall.ProjectAnchor(key), recall.AltitudeFull, 8000, e.wsDirs)
	if err != nil {
		e.t.Fatalf("recall.Build: %v", err)
	}
	var ids []string
	for _, it := range res.Items {
		ids = append(ids, it.ID)
	}
	return ids
}

func has(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func (e *identityEnv) projectRowsFor(toplevel string) int {
	e.t.Helper()
	st := e.open()
	defer func() { _ = st.Close() }()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM projects WHERE toplevel = ?`, toplevel).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// TestRepoKeepsOneIdentityAcrossRemoteAddChangeRemove is DONE WHEN clauses 1
// and 2 (task 029485ae): remote add, set-url and remove never split history.
func TestRepoKeepsOneIdentityAcrossRemoteAddChangeRemove(t *testing.T) {
	e := newIdentityEnv(t)
	foo := e.initRepo("foo")

	decision := e.session(foo, store.KindDecision, "use sqlite for the ledger")

	e.git(foo, "remote", "add", "origin", "https://example.invalid/foo.git")
	e.session(foo, "", "")
	if ids := e.recallIDs(foo); !has(ids, decision) {
		t.Fatalf("after remote add, recall = %v, want it to contain decision %s", ids, decision)
	}
	if n := e.projectRowsFor(foo); n != 1 {
		t.Fatalf("projects rows with toplevel %s = %d, want exactly 1", foo, n)
	}

	e.git(foo, "remote", "set-url", "origin", "https://example.invalid/foo2.git")
	note := e.session(foo, store.KindNote, "remote moved to foo2")
	ids := e.recallIDs(foo)
	if !has(ids, decision) || !has(ids, note) {
		t.Fatalf("after set-url, recall = %v, want decision %s and note %s", ids, decision, note)
	}

	e.git(foo, "remote", "remove", "origin")
	after := e.session(foo, store.KindNote, "remote removed")
	ids = e.recallIDs(foo)
	for _, want := range []string{decision, note, after} {
		if !has(ids, want) {
			t.Fatalf("after remote remove, recall = %v, missing %s", ids, want)
		}
	}
	if n := e.projectRowsFor(foo); n != 1 {
		t.Fatalf("projects rows with toplevel %s = %d, want exactly 1", foo, n)
	}

	st := e.open()
	defer func() { _ = st.Close() }()
	res, err := week.Build(week.Params{Store: st, Git: project.RealGit{}, WorkspaceDirs: e.wsDirs, Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("week.Build: %v", err)
	}
	rows := 0
	for _, r := range res.WhereLeftOff {
		if r.Project.ProjectKey != "" {
			rows++
		}
	}
	if rows != 1 {
		t.Fatalf("This Week where-you-left-off rows = %d (%+v), want 1", rows, res.WhereLeftOff)
	}
	sessions := 0
	for _, d := range res.Week {
		sessions += d.Sessions
	}
	if sessions != 4 {
		t.Fatalf("This Week sessions for foo = %d, want 4 (every session, across all remote states): %+v", sessions, res.Week)
	}
}

// TestExistingStoreWithBothLegacySpellingsMergesIdempotently is DONE WHEN
// clause 3: a store main's code wrote — rows under the toplevel key and rows
// under the common-dir|remote key — is merged by Open, recall sees both, and
// a re-open rewrites nothing.
func TestExistingStoreWithBothLegacySpellingsMergesIdempotently(t *testing.T) {
	e := newIdentityEnv(t)
	foo := e.initRepo("foo")
	common := filepath.Join(foo, ".git")
	oldTop := foo
	oldRemote := common + "|https://example.invalid/foo.git"

	seed, err := store.Open(e.db, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Seed exactly as main's code wrote them (raw keys, no canonicalisation).
	mk := func(key string, at time.Time, rec string) {
		if _, err := seed.DB().Exec(`INSERT INTO projects (key, git_common_dir, remote_url, toplevel, first_seen) VALUES (?, ?, NULL, ?, ?)`,
			key, common, foo, at.UnixNano()); err != nil {
			t.Fatal(err)
		}
		if _, err := seed.DB().Exec(`INSERT INTO sessions (id, agent, cwd, project_key, started_at, origin) VALUES (?, 'claude', ?, ?, ?, 'live')`,
			"s-"+rec, foo, key, at.UnixNano()); err != nil {
			t.Fatal(err)
		}
		if _, err := seed.DB().Exec(`INSERT INTO records (id, ts, kind, tier, text, about, session_id, project_key, evidence, event_cursor)
			VALUES (?, ?, 'decision', 'agent-declared', ?, '[]', ?, ?, '[]', 0)`, rec, at.UnixNano(), "text "+rec, "s-"+rec, key); err != nil {
			t.Fatal(err)
		}
	}
	t0 := time.Now().Add(-2 * time.Hour)
	mk(oldTop, t0, "rec-before-remote")
	mk(oldRemote, t0.Add(time.Hour), "rec-after-remote")
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	ids := e.recallIDs(foo)
	if !has(ids, "rec-before-remote") || !has(ids, "rec-after-remote") {
		t.Fatalf("recall after opening a two-spelling store = %v, want both records", ids)
	}
	st := e.open()
	var keys []string
	rows, err := st.DB().Query(`SELECT key FROM projects`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		keys = append(keys, k)
	}
	_ = rows.Close()
	if len(keys) != 1 || keys[0] != common {
		t.Fatalf("project keys = %v, want exactly [%s]", keys, common)
	}
	_ = st.Close()

	// Idempotent: re-opening rewrites nothing (no new backup, same content).
	before := dumpStore(t, e.db)
	backupsBefore, _ := filepath.Glob(e.db + ".pre-*")
	for i := 0; i < 2; i++ {
		_ = e.open().Close()
	}
	if after := dumpStore(t, e.db); after != before {
		t.Fatalf("re-open changed the store:\nbefore=%s\nafter=%s", before, after)
	}
	if backupsAfter, _ := filepath.Glob(e.db + ".pre-*"); len(backupsAfter) != len(backupsBefore) {
		t.Fatalf("re-open took another backup: %v -> %v", backupsBefore, backupsAfter)
	}
}

func dumpStore(t *testing.T, path string) string {
	t.Helper()
	st, err := store.Open(path, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var b strings.Builder
	for _, q := range []string{
		`SELECT key, IFNULL(git_common_dir,''), IFNULL(remote_url,''), toplevel, first_seen FROM projects ORDER BY key`,
		`SELECT id, IFNULL(project_key,'') FROM sessions ORDER BY id`,
		`SELECT id, IFNULL(project_key,'') FROM records ORDER BY id`,
	} {
		rows, err := st.DB().Query(q)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			_ = rows.Scan(ptrs...)
			for _, v := range vals {
				b.WriteString(strings.TrimSpace(anyString(v)) + "\x1f")
			}
			b.WriteString("\n")
		}
		_ = rows.Close()
	}
	return b.String()
}

func anyString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case int64:
		return time.Unix(0, x).UTC().Format(time.RFC3339Nano)
	}
	return ""
}
