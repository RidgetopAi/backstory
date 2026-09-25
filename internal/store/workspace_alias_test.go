package store

import (
	"path/filepath"
	"testing"
)

// legacyHandoffFixture seeds st with one handoff record and one session
// under legacyKey directly — the shape a pre-workspace build wrote before
// the re-key (79f7b20e) minted the workspace: prefix, since no migration
// carries old rows across (records stay append-only). Returns the
// handoff's id.
func legacyHandoffFixture(t *testing.T, s *Store, legacyKey string) string {
	t.Helper()
	mustUpsertProject(t, s, legacyKey)
	sessionID := mustStartSessionInProject(t, s, legacyKey)
	id, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindHandoff,
		Text: "legacy handoff", SessionID: sessionID, ProjectKey: legacyKey,
	})
	if err != nil {
		t.Fatalf("InsertRecord legacy handoff: %v", err)
	}
	return id
}

// TestWorkspaceKeyReadAliasesLegacyPlainKey is the punch's DONE WHEN clause
// 1/2 groundwork at the store level: a handoff and a session seeded under
// the legacy plain key (exactly what a pre-workspace build would have
// written) are found by every project-scoped read this punch's helper
// covers, when queried by the workspace-prefixed key the same directory
// gets today.
func TestWorkspaceKeyReadAliasesLegacyPlainKey(t *testing.T) {
	legacyKey := filepath.Join(t.TempDir(), "projects")
	wsKey := "workspace:" + legacyKey

	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	handoffID := legacyHandoffFixture(t, s, legacyKey)

	t.Run("LatestRecord", func(t *testing.T) {
		rec, ok, err := s.LatestRecord(wsKey, KindHandoff)
		if err != nil {
			t.Fatalf("LatestRecord: %v", err)
		}
		if !ok {
			t.Fatal("LatestRecord found = false, want true (legacy-key handoff)")
		}
		if rec.ID != handoffID {
			t.Errorf("LatestRecord.ID = %q, want %q", rec.ID, handoffID)
		}
	})

	t.Run("RecordsForProjectAll", func(t *testing.T) {
		recs, err := s.RecordsForProjectAll(wsKey, 100)
		if err != nil {
			t.Fatalf("RecordsForProjectAll: %v", err)
		}
		if !containsRecordID(recs, handoffID) {
			t.Errorf("RecordsForProjectAll(%s) = %v, want it to include %s", wsKey, recIDs(recs), handoffID)
		}
	})

	t.Run("LiveSessionsInProject", func(t *testing.T) {
		sessions, err := s.LiveSessionsInProject(wsKey)
		if err != nil {
			t.Fatalf("LiveSessionsInProject: %v", err)
		}
		if len(sessions) != 1 {
			t.Fatalf("LiveSessionsInProject(%s) = %d sessions, want 1", wsKey, len(sessions))
		}
	})

	t.Run("SearchRecordsInProject", func(t *testing.T) {
		results, err := s.SearchRecordsInProject(wsKey, "legacy", 10)
		if err != nil {
			t.Fatalf("SearchRecordsInProject: %v", err)
		}
		if len(results) != 1 || results[0].ID != handoffID {
			t.Fatalf("SearchRecordsInProject(%s) = %v, want exactly [%s]", wsKey, results, handoffID)
		}
	})
}

// TestRepoKeyNeverAliased is the punch's DONE WHEN clause 3: a record under
// a plain key belonging to a DIFFERENT directory (a git repo's own key, or
// any bare cwd fallback key) never shows up for a repo project's own read,
// and never shows up for workspace:<tmp>/projects when <tmp>/other is not
// that workspace dir — the alias only ever pulls in the exact legacy form
// of the key it was asked for, never every plain key in the store.
func TestRepoKeyNeverAliased(t *testing.T) {
	tmp := t.TempDir()
	workspaceDir := filepath.Join(tmp, "projects")
	otherDir := filepath.Join(tmp, "other")
	wsKey := "workspace:" + workspaceDir

	s := mustOpen(t, filepath.Join(tmp, "backstory.db"))
	mustUpsertProject(t, s, otherDir)
	otherSession := mustStartSessionInProject(t, s, otherDir)
	otherID, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindHandoff,
		Text: "other repo handoff", SessionID: otherSession, ProjectKey: otherDir,
	})
	if err != nil {
		t.Fatalf("InsertRecord other: %v", err)
	}

	t.Run("absent from the workspace's own read", func(t *testing.T) {
		recs, err := s.RecordsForProjectAll(wsKey, 100)
		if err != nil {
			t.Fatalf("RecordsForProjectAll(%s): %v", wsKey, err)
		}
		if containsRecordID(recs, otherID) {
			t.Fatalf("RecordsForProjectAll(%s) = %v, must not include the other repo's record %s", wsKey, recIDs(recs), otherID)
		}
	})

	t.Run("a repo key is never itself aliased away from its own read", func(t *testing.T) {
		// otherDir is a plain (non-workspace) key — project.IsWorkspaceKey is
		// false for it — so reading it must return exactly its own record,
		// nothing more, nothing less: no alias ever applies to a repo key.
		recs, err := s.RecordsForProjectAll(otherDir, 100)
		if err != nil {
			t.Fatalf("RecordsForProjectAll(%s): %v", otherDir, err)
		}
		if len(recs) != 1 || recs[0].ID != otherID {
			t.Fatalf("RecordsForProjectAll(%s) = %v, want exactly [%s]", otherDir, recIDs(recs), otherID)
		}
	})

	t.Run("LatestRecord for the workspace does not see it either", func(t *testing.T) {
		_, ok, err := s.LatestRecord(wsKey, KindHandoff)
		if err != nil {
			t.Fatalf("LatestRecord(%s): %v", wsKey, err)
		}
		if ok {
			t.Fatalf("LatestRecord(%s) found = true, want false: the other repo's key must never alias into the workspace's read", wsKey)
		}
	})
}

// TestProjectKeyAliasesGitRepoKeyUnchanged asserts projectKeyAliases itself
// returns exactly the key it was given for anything that is not a
// workspace key (a git repo's CommonDir|RemoteURL key, or a bare cwd
// fallback key) — the DONE WHEN clause 3 assertion at the helper's own
// level, independent of any query.
func TestProjectKeyAliasesGitRepoKeyUnchanged(t *testing.T) {
	for _, key := range []string{
		"/home/brian/repo|https://example.com/repo.git",
		"/home/brian/some/toplevel",
		"",
	} {
		got := projectKeyAliases(key)
		if len(got) != 1 || got[0] != key {
			t.Errorf("projectKeyAliases(%q) = %v, want [%q] (a repo/bare key is never aliased)", key, got, key)
		}
	}
}

func containsRecordID(recs []Record, id string) bool {
	for _, r := range recs {
		if r.ID == id {
			return true
		}
	}
	return false
}

func recIDs(recs []Record) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.ID
	}
	return out
}
