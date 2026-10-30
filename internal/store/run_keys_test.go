package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
)

// TestDuplicateRunKeysSeekOnlyTheGivenSessions: stampRunKeys runs on every
// event read, and grouping every session row by (agent,
// harness_session_id) each time cost about 25 ms of this-week. The query asks
// only about the given sessions and finds each one's twin through an index.
func TestDuplicateRunKeysSeekOnlyTheGivenSessions(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	plan := queryPlan(t, s, duplicateRunKeysQuery(2), "a", "b")
	if strings.Contains(plan, "SCAN") {
		t.Errorf("plan scans a table:\n%s", plan)
	}
	if !strings.Contains(plan, "(agent=? AND harness_session_id=?)") {
		t.Errorf("plan does not seek a twin by (agent, harness_session_id):\n%s", plan)
	}
}

// TestEventsCarryARunKeyOnlyForDuplicateRuns pins stampRunKeys through
// EventsForTimeline (task a757b754): an event's RunKey is
// "<agent>\x1f<harness_session_id>" when its session shares that pair with
// another session row, and "" otherwise, including for an empty harness id
// and for the same harness id under another agent.
func TestEventsCarryARunKeyOnlyForDuplicateRuns(t *testing.T) {
	s := mustOpen(t, tempDBPath(t))
	key := "/srv/repo/.git"
	mustUpsertProject(t, s, key)

	start := func(agent, harnessID string) string {
		t.Helper()
		id, err := s.StartSession(StartSessionParams{
			Agent: agent, HarnessSessionID: harnessID, CWD: "/srv/repo", ProjectKey: key, StartedAt: time.Now(), Origin: OriginLive,
		})
		if err != nil {
			t.Fatalf("StartSession(%s, %q): %v", agent, harnessID, err)
		}
		if _, err := s.AppendEvent(Event{TS: time.Now(), Kind: payload.KindShellCommand, SessionID: id, Source: "shell", Payload: `{}`}); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
		return id
	}
	dupA := start("claude", "run-1")
	dupB := start("claude", "run-1")
	unique := start("claude", "run-2")
	otherAgent := start("codex", "run-1")
	noHarnessID := start("claude", "")
	// StartSession stores "" as NULL, which never matches; a literal ''
	// (written by another path) must not make two sessions one run either.
	emptyA, emptyB := start("claude", "x"), start("claude", "x")
	if _, err := s.db.Exec(`UPDATE sessions SET harness_session_id = '' WHERE id IN (?, ?)`, emptyA, emptyB); err != nil {
		t.Fatalf("set empty harness ids: %v", err)
	}

	events, err := s.EventsForTimeline(key, time.Now().Add(-time.Hour), "", 0)
	if err != nil {
		t.Fatalf("EventsForTimeline: %v", err)
	}
	got := map[string]string{}
	for _, e := range events {
		got[e.SessionID] = e.RunKey
	}
	want := map[string]string{
		dupA: "claude\x1frun-1", dupB: "claude\x1frun-1",
		unique: "", otherAgent: "", noHarnessID: "", emptyA: "", emptyB: "",
	}
	for id, w := range want {
		if g, ok := got[id]; !ok || g != w {
			t.Errorf("RunKey of session %s = %q (seen %v); want %q", id, g, ok, w)
		}
	}
}
