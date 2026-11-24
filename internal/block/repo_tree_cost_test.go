package block_test

import (
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
)

// countingGit is twoTreeGit, counting every question asked of it. Each Repo
// call on project.RealGit is 3-4 git processes, so this count is the cost
// the SessionStart hook (10 s timeout) pays.
type countingGit struct{ repo, state int }

func (g *countingGit) Repo(dir string) (project.Repo, bool) {
	g.repo++
	return twoTreeGit{}.Repo(dir)
}

func (g *countingGit) State(dir string) (project.State, bool) {
	g.state++
	return twoTreeGit{}.State(dir)
}

// renderGitCalls renders the block for a reader in readerCWD after n prior
// sessions, all in sessionCWD and each with a session-end git state, no
// handoff (cursor 0: every one of them is "since the handoff"), and returns
// how many git questions the render asked and its Repo line.
func renderGitCalls(t *testing.T, n int, sessionCWD, readerCWD string) (int, string) {
	t.Helper()
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	base := time.Now().Add(-48 * time.Hour)
	for i := 0; i < n; i++ {
		id := mustStartSession(t, s, "claude", sessionCWD, 1000+i)
		c := i % 3
		mustAppendEvent(t, s, id, payload.KindSessionGitState, base.Add(time.Duration(i)*time.Minute), payload.SessionGitState{Branch: "main", UncommittedCount: &c})
	}
	self := mustStartSession(t, s, "claude", readerCWD, 99999)
	g := &countingGit{}
	out, err := block.Render(block.Params{
		Store: s, ProcFS: fakeProcFS{}, ProjectKey: testProjectKey, SessionID: self, Harness: "claude",
		CWD: readerCWD, Git: g, Now: time.Now(),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return g.repo + g.state, lineWith(out, "Repo:")
}

// The Repo line's per-tree rule (only the reader's own working tree counts)
// must cost O(distinct trees), not O(sessions): on RealGit every Repo
// question is 3-4 git processes, and 300 sessions in one folder took the
// render from 67 ms to 2.3 s. Pinned as a COUNT (wall time is flaky): the
// render asks exactly as many git questions after 300 sessions as after 3,
// for a reader in the sessions' own tree (newest-first can stop at once) and
// in another tree of the same repo (every session must be resolved, so only
// the per-render cache keeps it flat).
func TestRepoLineGitCostIsPerTreeNotPerSession(t *testing.T) {
	for _, tc := range []struct {
		name, sessionCWD, readerCWD, wantLine string
	}{
		{"reader in the sessions' tree", treeMain, treeMain + "/cmd", "branch main · 2 uncommitted"},
		{"reader in another tree", treeMain, treeWT, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			few, fewLine := renderGitCalls(t, 3, tc.sessionCWD, tc.readerCWD)
			many, manyLine := renderGitCalls(t, 300, tc.sessionCWD, tc.readerCWD)
			// Per-tree correctness is unchanged: the newest session (i=299,
			// 299%3 == 2 uncommitted; i=2 for n=3, also 2) in the reader's own
			// tree, or no observed state at all from another tree.
			for _, l := range []string{fewLine, manyLine} {
				if tc.wantLine == "" {
					if strings.Contains(l, "uncommitted") {
						t.Errorf("Repo line %q: another tree's state leaked into the reader's", l)
					}
				} else if !strings.Contains(l, tc.wantLine) {
					t.Errorf("Repo line %q, want it to contain %q", l, tc.wantLine)
				}
			}
			const ceiling = 8
			if many != few || many > ceiling {
				t.Fatalf("git questions: %d for 3 sessions, %d for 300 (want equal, at most %d) — the Repo line pays per session, not per tree", few, many, ceiling)
			}
			t.Logf("git questions: %d for 3 sessions, %d for 300", few, many)
		})
	}
}
