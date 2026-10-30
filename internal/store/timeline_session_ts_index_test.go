package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSessionEventsInATimeWindowSearchByTime pins the per-session event
// reads this-week makes for every active project: events since the window
// start (EventsForTimeline) and the latest event at or before now
// (LastActivity, AgentActivityForSessions). With only timeline_session
// (session_id, id), SQLite read every event a session ever had and filtered
// ts afterwards. With timeline_session_ts (session_id, ts) it seeks the time
// range: this-week went from 256 ms to 198 ms on a store with 110k events.
func TestSessionEventsInATimeWindowSearchByTime(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))

	queries := map[string]string{
		"events since": `SELECT e.id FROM timeline_events e
			JOIN sessions s ON s.id = e.session_id
			WHERE s.project_key = ? AND e.ts >= ?`,
		"latest event at or before": `SELECT MAX(e.ts) FROM timeline_events e
			JOIN sessions s ON s.id = e.session_id
			WHERE s.project_key = ? AND e.ts <= ?`,
	}
	for name, q := range queries {
		plan := queryPlan(t, s, q, "p", 1)
		if !strings.Contains(plan, "(session_id=? AND ts") {
			t.Errorf("%s: plan does not seek events by session and time:\n%s", name, plan)
		}
	}
}
