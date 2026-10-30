package store

import (
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
)

// TestAgentActivityCountsEventsWithinTheWindowOnly pins how events decide a
// session's activity: a session that started before the window counts when an
// event at or before asOf lands inside it, an event after asOf never counts
// (nor hides the session's earlier in-window events: clock skew or a synced
// device can put a session's newest event after asOf), and the agent's latest
// activity is its newest such event.
func TestAgentActivityCountsEventsWithinTheWindowOnly(t *testing.T) {
	s := mustOpen(t, tempDBPath(t))
	key := "/srv/repo/.git"
	mustUpsertProject(t, s, key)
	now := time.Now().UTC().Truncate(time.Second)
	since := now.Add(-7 * 24 * time.Hour)

	session := func(agent string, started time.Time, events ...time.Time) string {
		t.Helper()
		id, err := s.StartSession(StartSessionParams{Agent: agent, CWD: "/srv/repo", ProjectKey: key, StartedAt: started, Origin: OriginLive})
		if err != nil {
			t.Fatalf("StartSession(%s): %v", agent, err)
		}
		for _, ts := range events {
			if _, err := s.AppendEvent(Event{TS: ts, Kind: payload.KindShellCommand, SessionID: id, Source: "shell", Payload: `{}`}); err != nil {
				t.Fatalf("AppendEvent: %v", err)
			}
		}
		return id
	}
	a := session("claude", now.Add(-10*24*time.Hour), now.Add(-24*time.Hour))
	b := session("codex", now.Add(-10*24*time.Hour), now.Add(time.Hour))
	c := session("claude", now.Add(-9*24*time.Hour), now.Add(-48*time.Hour), now.Add(-3*time.Hour))
	d := session("claude", now.Add(-8*24*time.Hour), now.Add(-2*time.Hour), now.Add(2*time.Hour))

	got, err := s.AgentActivityForSessions([]string{a, b, c, d}, since, now)
	if err != nil {
		t.Fatalf("AgentActivityForSessions: %v", err)
	}
	if len(got) != 1 || got[0].Agent != "claude" || got[0].SessionCount != 3 || !got[0].LastActivity.Equal(now.Add(-2*time.Hour)) {
		t.Errorf("AgentActivityForSessions = %+v; want only claude, 3 sessions, last activity 2h ago (%v)", got, now.Add(-2*time.Hour))
	}
}
