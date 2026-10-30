package store

import (
	"path/filepath"
	"regexp"
	"testing"
)

// TestAgentActivitySeeksEachSessionsLatestEvent: AgentActivityForSessions
// needs only each session's latest event at or before asOf, but it read every
// event of every given session up to asOf (almost all of them) and took the
// maximum afterwards. Asked per session (a correlated MAX), that is one seek
// in timeline_session_ts: for this week's 74 sessions on a real store,
// 6.9 ms -> 0.14 ms, about 35 ms of this-week across its calls.
func TestAgentActivitySeeksEachSessionsLatestEvent(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	q, args := agentActivityQuery([]string{"a", "b"}, []string{"claude"}, 10, 1)
	plan := queryPlan(t, s, q, args...)
	perSession := regexp.MustCompile(`CORRELATED SCALAR SUBQUERY \d+\n.*SEARCH e USING COVERING INDEX timeline_session_ts \(session_id=\? AND ts<\?\)`)
	if !perSession.MatchString(plan) {
		t.Errorf("plan does not seek each session's latest event with a correlated MAX:\n%s", plan)
	}
}
