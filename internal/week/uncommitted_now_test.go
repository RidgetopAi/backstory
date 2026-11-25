package week

import (
	"strconv"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// nowGit is twoTreeGit whose State answers from now: a tree mapped to a count
// is observed with that many uncommitted changes; any other folder is
// could-not-observe. asked records every folder State was asked about.
type nowGit struct {
	now   map[string]int
	asked []string
}

func (g *nowGit) Repo(dir string) (project.Repo, bool) { return twoTreeGit{}.Repo(dir) }

func (g *nowGit) State(dir string) (project.State, bool) {
	g.asked = append(g.asked, dir)
	n, ok := g.now[dir]
	if !ok {
		return project.State{}, false
	}
	return project.State{Branch: "master", Uncommitted: n}, true
}

// dirtyMain records one session in vidflowMain that ended with 19 uncommitted
// changes, and returns its git-state event.
func dirtyMain(t *testing.T, st *store.Store, id string, at time.Duration) store.TimelineEvent {
	t.Helper()
	sid := startSession(t, st, id, labelRepoKey, vidflowMain, fixtureNow.Add(-at))
	evID := appendGitState(t, st, sid, `{"branch":"master","uncommitted_count":19}`)
	return store.TimelineEvent{ID: evID, Event: store.Event{SessionID: sid}}
}

const nineteenAtEnd = "projects/vidflow: session ended with 19 uncommitted change(s) on branch master"

// Brian's desk, 2026-11-25: the session ended dirty, he committed everything
// from the row's Terminal, and the row stayed. Observed clean now → no item;
// still dirty now → the item, worded for the session end; could-not-observe
// now (the folder is gone) → the item, never read as clean.
func TestUncommittedReobservesTheLocalTreeNow(t *testing.T) {
	cases := []struct {
		name     string
		now      map[string]int
		wantItem bool
	}{
		{"clean now", map[string]int{vidflowMain: 0}, false},
		{"dirty now", map[string]int{vidflowMain: 3}, true},
		{"could not observe now", map[string]int{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t)
			upsertProject(t, st, labelRepoKey, vidflowMain)
			ev := dirtyMain(t, st, "sess-vid", 2*time.Hour)
			g := &nowGit{now: tc.now}
			items := uncommittedItemsWith(t, st, g)
			if len(g.asked) != 1 || g.asked[0] != vidflowMain {
				t.Fatalf("State asked about %q, want exactly the one dirty tree %s", g.asked, vidflowMain)
			}
			if !tc.wantItem {
				if len(items) != 0 {
					t.Fatalf("items = %+v, want none: the tree is clean now", items)
				}
				return
			}
			if len(items) != 1 || items[0].Reason != nineteenAtEnd || items[0].CWD != vidflowMain ||
				items[0].EvidenceIDs[0] != strconv.FormatInt(ev.ID, 10) {
				t.Fatalf("items = %+v, want one item %q at %s citing %d", items, nineteenAtEnd, vidflowMain, ev.ID)
			}
		})
	}
}

// A paired device's session (or one with no absolute cwd) has no local tree
// to ask: its item stays and State is never asked about "" (which would
// observe whatever folder this process runs in).
func TestUncommittedPeerSlotIsNotReobserved(t *testing.T) {
	st := openTestStore(t)
	upsertProject(t, st, labelRepoKey, vidflowMain)
	peer := startSession(t, st, "sess-peer", labelRepoKey, vidflowMain, fixtureNow.Add(-2*time.Hour))
	if _, err := st.DB().Exec(`UPDATE sessions SET origin_device = 'laptop-device-id' WHERE id = ?`, peer); err != nil {
		t.Fatalf("mark peer: %v", err)
	}
	appendGitState(t, st, peer, `{"branch":"master","uncommitted_count":3}`)
	g := &nowGit{now: map[string]int{"": 0, vidflowMain: 0}}
	items := uncommittedItemsWith(t, st, g)
	if len(items) != 1 || items[0].CWD != "" {
		t.Fatalf("items = %+v, want the peer's one item with no cwd", items)
	}
	if len(g.asked) != 0 {
		t.Fatalf("State asked about %q for a peer's session, want never", g.asked)
	}
}

// Dismiss (option a): the item is hidden while its evidence event is the
// tree's latest; a newer dirty session end in that tree shows it again; a
// clean one clears it as always.
func TestUncommittedDismissHidesUntilTheNextDirtyEnd(t *testing.T) {
	st := openTestStore(t)
	upsertProject(t, st, labelRepoKey, vidflowMain)
	first := dirtyMain(t, st, "sess-1", 3*time.Hour)
	g := &nowGit{now: map[string]int{vidflowMain: 19}}
	if items := uncommittedItemsWith(t, st, g); len(items) != 1 {
		t.Fatalf("before dismiss: %+v, want 1 item", items)
	}
	if err := st.DismissGitState(store.Identity{Kind: store.IdentityHuman, Actor: "human"}, first); err != nil {
		t.Fatalf("DismissGitState: %v", err)
	}
	g.asked = nil
	if items := uncommittedItemsWith(t, st, g); len(items) != 0 {
		t.Fatalf("after dismiss: %+v, want none", items)
	}
	if len(g.asked) != 0 {
		t.Fatalf("a dismissed item still asked git about %q", g.asked)
	}

	second := dirtyMain(t, st, "sess-2", 2*time.Hour)
	items := uncommittedItemsWith(t, st, g)
	if len(items) != 1 || items[0].EvidenceIDs[0] != strconv.FormatInt(second.ID, 10) {
		t.Fatalf("after a newer dirty end: %+v, want the item back citing %d", items, second.ID)
	}

	clean := startSession(t, st, "sess-3", labelRepoKey, vidflowMain, fixtureNow.Add(-1*time.Hour))
	appendGitState(t, st, clean, `{"branch":"master","uncommitted_count":0}`)
	if items := uncommittedItemsWith(t, st, g); len(items) != 0 {
		t.Fatalf("after a clean end: %+v, want none", items)
	}
}

// A dismissal is for ONE event of ONE session. The same id under another
// session (a rowid a purge handed out again) is not hidden by it.
func TestUncommittedDismissIsKeyedToTheEventsSession(t *testing.T) {
	st := openTestStore(t)
	upsertProject(t, st, labelRepoKey, vidflowMain)
	ev := dirtyMain(t, st, "sess-1", 2*time.Hour)
	stale := store.TimelineEvent{ID: ev.ID, Event: store.Event{SessionID: "sess-purged-long-ago"}}
	if err := st.DismissGitState(store.Identity{Kind: store.IdentityHuman, Actor: "human"}, stale); err != nil {
		t.Fatalf("DismissGitState: %v", err)
	}
	if items := uncommittedItemsWith(t, st, &nowGit{now: map[string]int{vidflowMain: 19}}); len(items) != 1 {
		t.Fatalf("items = %+v, want the item: the dismissal names another session", items)
	}
}
