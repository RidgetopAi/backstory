package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/RidgetopAi/backstory/internal/store"
)

const (
	projectsGitKey   = fixtureWorkspaceDir + "/omarcade/.git"
	projectsWSKey    = "workspace:" + fixtureWorkspaceDir
	projectsEmptyKey = "/home/brian/bin"
)

// buildProjectsFixtureStore seeds a git project (2 sessions, 3 records), a
// workspace: project (1 session, newer than the git one's) and a project
// with no sessions.
func buildProjectsFixtureStore(t *testing.T, dataDir string) {
	t.Helper()
	st, err := store.Open(filepath.Join(dataDir, "backstory", "backstory.db"), nil, nil)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()
	for _, p := range []store.Project{
		{Key: projectsGitKey, Toplevel: fixtureWorkspaceDir + "/omarcade", FirstSeen: date(1, 9, 0)},
		{Key: projectsWSKey, Toplevel: fixtureWorkspaceDir, FirstSeen: date(1, 9, 0)},
		{Key: projectsEmptyKey, Toplevel: projectsEmptyKey, FirstSeen: date(1, 9, 0)},
	} {
		if err := st.UpsertProject(p); err != nil {
			t.Fatalf("UpsertProject(%s): %v", p.Key, err)
		}
	}
	for _, s := range []struct {
		id, key string
		day     int
	}{{"s-git-1", projectsGitKey, 2}, {"s-git-2", projectsGitKey, 3}, {"s-ws-1", projectsWSKey, 4}} {
		if _, err := st.StartSession(store.StartSessionParams{
			ID: s.id, Agent: "claude", CWD: fixtureWorkspaceDir, ProjectKey: s.key,
			StartedAt: date(s.day, 9, 0), Origin: store.OriginLive,
		}); err != nil {
			t.Fatalf("StartSession(%s): %v", s.id, err)
		}
	}
	for _, text := range []string{"one", "two", "three"} {
		if _, err := st.InsertRecord(store.InsertRecordParams{
			Identity: store.Identity{Kind: store.IdentityAgent, Actor: "seed"}, Kind: store.KindNote,
			Text: text, ProjectKey: projectsGitKey,
		}); err != nil {
			t.Fatalf("InsertRecord: %v", err)
		}
	}
}

func runProjectsCLI(t *testing.T, dataDir string, args ...string) (string, string, int) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", dataDir)
	t.Setenv("BACKSTORY_WORKSPACE_DIRS", fixtureWorkspaceDir)
	t.Setenv("HOME", "/home/brian")
	var out, errBuf bytes.Buffer
	code := run(append([]string{"projects"}, args...), bytes.NewReader(nil), &out, &errBuf)
	return out.String(), errBuf.String(), code
}

func TestProjectsTableRows(t *testing.T) {
	dataDir := t.TempDir()
	buildProjectsFixtureStore(t, dataDir)
	stdout, stderr, code := runProjectsCLI(t, dataDir)
	if code != 0 {
		t.Fatalf("exit %d (stderr: %s)", code, stderr)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 4 { // header + three rows
		t.Fatalf("want header + 3 rows, got:\n%s", stdout)
	}
	want := []struct {
		label, key, last string
		sessions, recs   string
	}{
		{"projects", projectsWSKey, "2026-01-04 09:00", "1", "0"},
		{"projects/omarcade", projectsGitKey, "2026-01-03 09:00", "2", "3"},
		{"bin", projectsEmptyKey, "never", "0", "0"},
	}
	for i, w := range want {
		f := strings.Fields(lines[i+1])
		if len(f) < 5 {
			t.Fatalf("row %d too short: %q", i, lines[i+1])
		}
		if f[1] != w.key || f[len(f)-1] != w.recs || f[len(f)-2] != w.sessions {
			t.Errorf("row %d = %q, want key %s sessions %s records %s", i, lines[i+1], w.key, w.sessions, w.recs)
		}
		if !strings.Contains(lines[i+1], w.last) {
			t.Errorf("row %d = %q, want last activity %q", i, lines[i+1], w.last)
		}
		if !strings.HasPrefix(lines[i+1], w.label) {
			t.Errorf("row %d = %q, want label %q", i, lines[i+1], w.label)
		}
	}
}

func TestProjectsJSON(t *testing.T) {
	dataDir := t.TempDir()
	buildProjectsFixtureStore(t, dataDir)
	stdout, stderr, code := runProjectsCLI(t, dataDir, "--json")
	if code != 0 {
		t.Fatalf("exit %d (stderr: %s)", code, stderr)
	}
	var got []struct {
		Key          string  `json:"key"`
		Label        string  `json:"label"`
		LastActivity *string `json:"last_activity"`
		Sessions     int     `json:"sessions"`
		Records      int     `json:"records"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("decode %q: %v", stdout, err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 objects, got %+v", got)
	}
	ws, git, empty := got[0], got[1], got[2]
	if ws.Key != projectsWSKey || ws.Label != "projects" || ws.Sessions != 1 || ws.Records != 0 ||
		ws.LastActivity == nil || *ws.LastActivity != "2026-01-04T09:00:00Z" {
		t.Errorf("workspace row = %+v", ws)
	}
	if git.Key != projectsGitKey || git.Label != "projects/omarcade" || git.Sessions != 2 || git.Records != 3 ||
		git.LastActivity == nil || *git.LastActivity != "2026-01-03T09:00:00Z" {
		t.Errorf("git row = %+v", git)
	}
	if empty.Key != projectsEmptyKey || empty.Label != "bin" || empty.Sessions != 0 || empty.Records != 0 || empty.LastActivity != nil {
		t.Errorf("empty row = %+v", empty)
	}
	if !strings.Contains(stdout, `"last_activity":null`) {
		t.Errorf("last_activity not a JSON null: %s", stdout)
	}
}

func TestProjectsIsReadOnly(t *testing.T) {
	dataDir := t.TempDir()
	buildProjectsFixtureStore(t, dataDir)
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	counts := func() map[string]int {
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		out := map[string]int{}
		for _, tbl := range []string{"records", "sessions", "projects"} {
			var n int
			if err := db.QueryRow("SELECT COUNT(*) FROM " + tbl).Scan(&n); err != nil {
				t.Fatal(err)
			}
			out[tbl] = n
		}
		return out
	}
	before := counts()
	for _, args := range [][]string{{}, {"--json"}} {
		if _, stderr, code := runProjectsCLI(t, dataDir, args...); code != 0 {
			t.Fatalf("exit %d (stderr: %s)", code, stderr)
		}
	}
	after := counts()
	for tbl, n := range before {
		if after[tbl] != n {
			t.Errorf("%s rows: before %d, after %d", tbl, n, after[tbl])
		}
	}
	if before["records"] != 3 || before["sessions"] != 3 || before["projects"] != 3 {
		t.Errorf("unexpected fixture counts %v", before)
	}
}

func TestProjectsUsageAndReadme(t *testing.T) {
	if !strings.Contains(usage, "\n  projects ") {
		t.Errorf("usage does not list projects:\n%s", usage)
	}
	var purgeLine string
	for _, l := range strings.Split(usage, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "purge ") {
			purgeLine = l
		}
	}
	if !strings.Contains(purgeLine, "backstory projects") {
		t.Errorf("purge usage line does not mention `backstory projects`: %q", purgeLine)
	}
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "`backstory projects") {
		t.Error("README.md command list does not contain `backstory projects`")
	}
}

// TestProjectsFullyPurgedProjectShowsNever: a project purged in full stays
// listed with 0 sessions, 0 records and last activity never / null.
func TestProjectsFullyPurgedProjectShowsNever(t *testing.T) {
	dataDir := t.TempDir()
	buildProjectsFixtureStore(t, dataDir)
	st, err := store.Open(filepath.Join(dataDir, "backstory", "backstory.db"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PurgeSessions(store.PurgeScope{ProjectKey: projectsGitKey}, store.Identity{Kind: store.IdentityHuman}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	stdout, stderr, code := runProjectsCLI(t, dataDir)
	if code != 0 {
		t.Fatalf("exit %d (stderr: %s)", code, stderr)
	}
	var row string
	for _, l := range strings.Split(stdout, "\n") {
		if strings.Contains(l, projectsGitKey) {
			row = l
		}
	}
	f := strings.Fields(row)
	if row == "" || !strings.Contains(row, "never") || f[len(f)-2] != "0" || f[len(f)-1] != "0" {
		t.Fatalf("purged row = %q, want never / 0 sessions / 0 records in:\n%s", row, stdout)
	}

	jout, _, _ := runProjectsCLI(t, dataDir, "--json")
	var got []struct {
		Key          string  `json:"key"`
		LastActivity *string `json:"last_activity"`
		Sessions     int     `json:"sessions"`
		Records      int     `json:"records"`
	}
	if err := json.Unmarshal([]byte(jout), &got); err != nil {
		t.Fatal(err)
	}
	for _, g := range got {
		if g.Key == projectsGitKey && (g.LastActivity != nil || g.Sessions != 0 || g.Records != 0) {
			t.Errorf("json row = %+v, want null last_activity, 0 sessions", g)
		}
	}
}
