package block_test

import (
	"path/filepath"
	"strings"
	"testing"

	claudebackfill "github.com/RidgetopAi/backstory/internal/backfill/claude"
	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/project"
)

// fakeGit never matches any cwd, so project.Key falls back to the cwd
// itself — this test's fixture project directory is not a real git repo.
type fakeGit struct{}

func (fakeGit) Repo(string) (project.Repo, bool) { return project.Repo{}, false }

// State is unused by this test but required to satisfy project.Git.
func (fakeGit) State(string) (project.State, bool) { return project.State{}, false }

// TestRenderBackfilledDeltaCountsDistinctFilePathsNotZero is the punch's
// clause 2 (task 8ba5487a), end-to-end: import a fixture Claude transcript
// through the real backfill importer, then render the block for that
// project. Before the shared payload package, the backfill wrote a tool
// path under `detail` while the block's delta slot read `path` — two
// independent structs disagreeing — so the Delta slot reported "0 files
// touched" no matter how many Edit/Write lines the transcript carried. The
// fixture (testdata/backfilldelta) edits the same path twice (tu1, tu2 both
// /home/fixture/blockproj/a.go) plus one Write to a different path
// (b.go) and one Bash call: 2 distinct files, never 3, never 0. A Bash
// tool_result in a Claude transcript never carries an exit code, so the
// delta must not report any "Last failure" either.
func TestRenderBackfilledDeltaCountsDistinctFilePathsNotZero(t *testing.T) {
	s := newTestStore(t)
	root := filepath.Join("testdata", "backfilldelta", "projects")

	res, err := claudebackfill.Import(s, claudebackfill.Options{Root: root, Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.SessionsCreated != 1 {
		t.Fatalf("SessionsCreated = %d, want 1", res.SessionsCreated)
	}

	const projectKey = "/home/fixture/blockproj" // fakeGit{} never matches: project.Key falls back to the fixture's cwd

	out, err := block.Render(block.Params{
		Store: s, ProcFS: fakeProcFS{}, ProjectKey: projectKey, SessionID: "self", Harness: "claude",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if !strings.Contains(out, "Delta:") {
		t.Fatalf("delta slot missing entirely; got:\n%s", out)
	}
	if !strings.Contains(out, "2 files touched") {
		t.Errorf("delta slot did not report 2 distinct file paths (a.go edited twice counts once, plus b.go); got:\n%s", out)
	}
	if strings.Contains(out, "Last failure") {
		t.Errorf("delta slot reported exit codes from a backfilled Claude transcript, which never carries one (do NOT invent 0); got:\n%s", out)
	}
}
