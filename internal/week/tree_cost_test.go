package week

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
)

// countingGit is twoTreeGit, counting every question asked of it. Each Repo
// call on project.RealGit is 3-4 git processes.
type countingGit struct{ calls int }

func (g *countingGit) Repo(dir string) (project.Repo, bool) {
	g.calls++
	return twoTreeGit{}.Repo(dir)
}

func (g *countingGit) State(dir string) (project.State, bool) {
	g.calls++
	return twoTreeGit{}.State(dir)
}

// buildGitCalls builds this week after n sessions, half in each tree of one
// repo, each ending dirty, with a plain (uncached) counting Git, and returns
// how many git questions Build asked and its uncommitted items.
func buildGitCalls(t *testing.T, n int) (int, []AttentionItem) {
	t.Helper()
	st := openTestStore(t)
	upsertProject(t, st, labelRepoKey, vidflowMain)
	for i := 0; i < n; i++ {
		tree := vidflowMain
		if i%2 == 1 {
			tree = vidflowWT
		}
		sid := startSession(t, st, fmt.Sprintf("sess-cost-%d", i), labelRepoKey, tree, fixtureNow.Add(-3*time.Hour).Add(time.Duration(i)*time.Second))
		appendGitState(t, st, sid, `{"branch":"main","uncommitted_count":1}`)
	}
	g := &countingGit{}
	items := uncommittedItemsWith(t, st, g)
	return g.calls, items
}

// week.Build resolves every session's working tree (uncommitted items are per
// tree); that must cost O(distinct folders), not O(sessions), whatever Git the
// caller hands it — a plain RealGit is 3-4 git processes per question. Pinned
// as a count: the same number of git questions after 300 sessions as after 4.
func TestBuildGitCostIsPerTreeNotPerSession(t *testing.T) {
	few, fewItems := buildGitCalls(t, 4)
	many, manyItems := buildGitCalls(t, 300)
	for _, items := range [][]AttentionItem{fewItems, manyItems} {
		if len(items) != 2 {
			t.Fatalf("uncommitted items = %+v, want one per tree (2)", items)
		}
		for _, it := range items {
			if !strings.Contains(it.Reason, "1 uncommitted") {
				t.Errorf("Reason = %q, want 1 uncommitted", it.Reason)
			}
		}
	}
	const ceiling = 10
	if many != few || many > ceiling {
		t.Fatalf("git questions: %d for 4 sessions, %d for 300 (want equal, at most %d) — Build pays per session, not per tree", few, many, ceiling)
	}
	t.Logf("git questions: %d for 4 sessions, %d for 300", few, many)
}
