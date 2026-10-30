package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestProjectSessionLookupsSearchAnIndex pins the query shape that
// LastActivity and EventsForTimeline run once per project for this-week:
// a project's sessions, joined to their timeline events. Without an index
// on sessions(project_key) SQLite either scans every session or walks every
// timeline event through timeline_ts and checks each one's session, which
// made `this-week` take about 1.4 s on a store with 110k events.
func TestProjectSessionLookupsSearchAnIndex(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))

	queries := map[string]string{
		"sessions of a project": `SELECT started_at FROM sessions WHERE project_key = ?`,
		"events of a project": `SELECT e.ts FROM timeline_events e
			JOIN sessions s ON s.id = e.session_id
			WHERE s.project_key = ?`,
	}
	for name, q := range queries {
		plan := queryPlan(t, s, q, "p")
		if !strings.Contains(plan, "USING INDEX") || !strings.Contains(plan, "(project_key=?)") {
			t.Errorf("%s: plan does not search sessions by project_key:\n%s", name, plan)
		}
		if strings.Contains(plan, "SCAN") {
			t.Errorf("%s: plan scans a table:\n%s", name, plan)
		}
	}
}

// queryPlan returns EXPLAIN QUERY PLAN's detail lines for q, one per line.
func queryPlan(t *testing.T, s *Store, q string, args ...any) string {
	t.Helper()
	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var lines []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan plan row: %v", err)
		}
		lines = append(lines, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	return strings.Join(lines, "\n")
}
