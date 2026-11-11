package main

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/RidgetopAi/backstory/internal/store"
)

// TestRepoHomedHandoffCountsOnTheRepoRow is DONE WHEN clause 5 (decision
// dc0a4387): a handoff written by a session in a repo under a workspace
// counts on the repo's row — in `backstory projects` RECORDS and in the
// this-week grid's records_written — and not on the workspace row. The
// fixture stores it the pre-fix way (filed under the workspace key); the
// first Open (what the projects command does) migrates it onto the repo.
func TestRepoHomedHandoffCountsOnTheRepoRow(t *testing.T) {
	dataDir := t.TempDir()
	buildProjectsFixtureStore(t, dataDir)
	st, err := store.Open(filepath.Join(dataDir, "backstory", "backstory.db"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// s-git-2 is a repo session; the handoff is stored the pre-fix way.
	mustInsertThisWeekRecord(t, st, fixtureRecord{
		ID: "h-rehome", ProjectKey: projectsWSKey, SessionID: "s-git-2", TS: date(7, 10, 0),
		Kind: store.KindHandoff, Tier: store.TierAgentDeclared, Text: "stopped mid-feature",
	})
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runProjectsCLI(t, dataDir, "--json")
	if code != 0 {
		t.Fatalf("projects exit %d: %s", code, stderr)
	}
	var rows []struct {
		Key     string `json:"key"`
		Records int    `json:"records"`
	}
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		want := 0
		if r.Key == projectsGitKey {
			want = 4 // three notes plus the migrated handoff
		}
		if r.Records != want {
			t.Errorf("projects row %s RECORDS = %d, want %d", r.Key, r.Records, want)
		}
	}

	// The this-week grid: the handoff is dated inside the fixture week.
	out, stderr, code := runThisWeekCLI(t, dataDir, "--json")
	if code != 0 {
		t.Fatalf("this-week exit %d: %s", code, stderr)
	}
	var tw thisWeekOutputJSON
	if err := json.Unmarshal([]byte(out), &tw); err != nil {
		t.Fatal(err)
	}
	written := map[string]int{}
	for _, d := range tw.Week {
		written[d.DisplayName] += d.RecordsWritten
	}
	if written["projects"] != 0 {
		t.Errorf("workspace row records_written = %d, want 0", written["projects"])
	}
	if written["projects/omarcade"] < 1 {
		t.Errorf("repo row records_written = %d, want the handoff counted (>=1); grid: %v", written["projects/omarcade"], written)
	}
}
