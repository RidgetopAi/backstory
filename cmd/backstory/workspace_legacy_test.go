package main

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/RidgetopAi/backstory/internal/store"
)

// TestRecallAndThisWeekSurfaceLegacyWorkspaceKeyHandoff is the punch's DONE
// WHEN clause 2 (task 50249f56): a handoff and a session seeded under the
// legacy plain key `<tmp>/projects` — exactly what a pre-workspace build
// wrote before the workspaces re-key (79f7b20e) minted the workspace:
// prefix, since no migration carries old rows across and records stay
// append-only — is still returned by `backstory recall` run for the
// workspace (--project workspace:<tmp>/projects, the key project.Key now
// computes for that cwd) and still carries into `backstory this-week
// --json`'s project row for the same store.
func TestRecallAndThisWeekSurfaceLegacyWorkspaceKeyHandoff(t *testing.T) {
	dataDir := t.TempDir()
	legacyKey := filepath.Join(t.TempDir(), "projects")
	wsKey := "workspace:" + legacyKey
	const handoffID = "handoff-legacy-0001"

	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.UpsertProject(store.Project{Key: legacyKey, Toplevel: legacyKey, FirstSeen: date(3, 9, 0)}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	sessID, err := st.StartSession(store.StartSessionParams{
		ID: "sess-legacy-1", Agent: "claude", CWD: legacyKey, ProjectKey: legacyKey,
		StartedAt: date(3, 9, 0), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	mustAppendEvent(t, st, store.Event{TS: date(3, 9, 0), Kind: "session.start", SessionID: sessID, Source: "shell", Payload: `{}`})
	mustInsertThisWeekRecord(t, st, fixtureRecord{
		ID: handoffID, ProjectKey: legacyKey, SessionID: sessID, TS: date(3, 9, 10),
		Kind: store.KindHandoff, Tier: store.TierAgentDeclared, Text: "yesterday's handoff, before the swap",
	})
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	t.Run("recall run for the workspace returns the legacy-key handoff", func(t *testing.T) {
		stdout, stderr, code := runRecallCLI(t, dataDir, "--project", wsKey, "--json")
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
		}
		var out recallOutputJSON
		if err := json.Unmarshal([]byte(stdout), &out); err != nil {
			t.Fatalf("decode --json output: %v\n%s", err, stdout)
		}
		var found bool
		for _, item := range out.Items {
			if item.ID == handoffID {
				found = true
			}
		}
		if !found {
			t.Fatalf("backstory recall --project %s --json items = %+v, want it to list %s", wsKey, out.Items, handoffID)
		}
	})

	t.Run("this-week's project row carries the legacy-key handoff", func(t *testing.T) {
		stdout, stderr, code := runThisWeekCLI(t, dataDir, "--json")
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
		}
		var out thisWeekOutputJSON
		if err := json.Unmarshal([]byte(stdout), &out); err != nil {
			t.Fatalf("decode --json output: %v\n%s", err, stdout)
		}
		var row *projectSummaryJSON
		for i := range out.WhereLeftOff {
			if p := out.WhereLeftOff[i].Project; p != nil && p.ProjectKey == legacyKey {
				row = p
			}
		}
		if row == nil {
			t.Fatalf("backstory this-week --json where_left_off = %+v, want a row for %s", out.WhereLeftOff, legacyKey)
		}
		if row.HandoffID != handoffID {
			t.Fatalf("this-week project row for %s: HandoffID = %q, want %q", legacyKey, row.HandoffID, handoffID)
		}
	})
}
