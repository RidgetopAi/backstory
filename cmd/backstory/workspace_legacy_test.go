package main

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/RidgetopAi/backstory/internal/store"
)

// TestRecallSurfacesLegacyWorkspaceKeyHandoffAndThisWeekNeverShowsThePlainKey
// is task d65ef8ff's sweep proven through two CLI commands sharing one
// store: a handoff and a session seeded under the legacy plain key
// `<tmp>/projects` — exactly what a pre-79f7b20e build wrote before
// project.Key started minting the workspace: prefix — is merged into that
// folder's canonical "workspace:"-prefixed key by the next Open that
// configures the dir as a workspace (store.Open's own sweep), so `backstory
// recall` run for the workspace (--project workspace:<tmp>/projects, the
// key project.Key now computes for that cwd) still returns it. Before this
// punch, `backstory this-week --json` had the opposite bug (this test's own
// prior form): the legacy session's stored project_key looked like an
// ordinary repo key, so This Week's partitionActiveProjectKeys treated the
// WORKSPACE FOLDER ITSELF as a project row (violating decision bcc9fa54, "a
// workspace is not a project") — the sweep closes that leak, so the plain
// key must never appear anywhere in this-week's own output either.
func TestRecallSurfacesLegacyWorkspaceKeyHandoffAndThisWeekNeverShowsThePlainKey(t *testing.T) {
	dataDir := t.TempDir()
	legacyKey := fixtureWorkspaceDir // runThisWeekCLI pins BACKSTORY_WORKSPACE_DIRS to this, so this-week's own sweep must catch exactly this dir.
	wsKey := "workspace:" + legacyKey
	const handoffID = "handoff-legacy-0001"

	// Both runRecallCLI and runThisWeekCLI need to see legacyKey configured
	// as a workspace dir; runThisWeekCLI pins it to fixtureWorkspaceDir
	// itself (t.Setenv here is redundant for that half, but required for
	// runRecallCLI, which never touches the var).
	t.Setenv("BACKSTORY_WORKSPACE_DIRS", legacyKey)

	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	// Seed with no workspace dirs configured, exactly what a pre-79f7b20e
	// build's own Open (which never canonicalized a write at all) would have
	// left on disk.
	st, err := store.Open(dbPath, nil, nil)
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

	t.Run("this-week never shows the plain legacy key", func(t *testing.T) {
		stdout, stderr, code := runThisWeekCLI(t, dataDir, "--json")
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
		}
		var out thisWeekOutputJSON
		if err := json.Unmarshal([]byte(stdout), &out); err != nil {
			t.Fatalf("decode --json output: %v\n%s", err, stdout)
		}
		for _, row := range out.WhereLeftOff {
			if row.Project != nil && row.Project.ProjectKey == legacyKey {
				t.Fatalf("where_left_off has a standalone project row keyed on the plain legacy key %s: %+v", legacyKey, row.Project)
			}
			for _, c := range row.Children {
				if c.ProjectKey == legacyKey {
					t.Fatalf("where_left_off group %q has a child row keyed on the plain legacy key %s: %+v", row.Group, legacyKey, c)
				}
			}
		}
		for _, d := range out.Week {
			if d.ProjectKey == legacyKey {
				t.Fatalf("week has a day row keyed on the plain legacy key %s: %+v", legacyKey, d)
			}
		}
	})
}
