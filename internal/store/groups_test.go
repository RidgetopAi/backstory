package store

import (
	"path/filepath"
	"testing"
	"time"
)

const (
	humanActor = "human"
	agentActor = "sess-agent-1"
)

func seedGroupTestProjects(t *testing.T, s *Store, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if err := s.UpsertProject(Project{Key: k, Toplevel: "/tmp/" + k, FirstSeen: time.Now()}); err != nil {
			t.Fatalf("UpsertProject(%s): %v", k, err)
		}
	}
}

// TestSetMoveClearListGroupsHumanIdentityRoundTrip is proof (1): set, move
// (re-set to another group), clear and list groups all round-trip under the
// human identity, and GroupOf reflects each state change.
func TestSetMoveClearListGroupsHumanIdentityRoundTrip(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	human := Identity{Kind: IdentityHuman, Actor: humanActor}

	seedGroupTestProjects(t, s, "proj-a", "proj-b")

	// Set.
	if err := s.SetProjectGroup(human, "work", "proj-a", nil); err != nil {
		t.Fatalf("SetProjectGroup(proj-a -> work): %v", err)
	}
	name, ok, err := s.GroupOf("proj-a", nil)
	if err != nil {
		t.Fatalf("GroupOf(proj-a): %v", err)
	}
	if !ok || name != "work" {
		t.Fatalf("GroupOf(proj-a) = (%q, %v), want (\"work\", true)", name, ok)
	}

	// A project never grouped returns none.
	_, ok, err = s.GroupOf("proj-b", nil)
	if err != nil {
		t.Fatalf("GroupOf(proj-b): %v", err)
	}
	if ok {
		t.Fatal("GroupOf(proj-b) = ok, want false (never grouped)")
	}

	// Move: re-set proj-a to a different group overwrites, not adds.
	if err := s.SetProjectGroup(human, "personal", "proj-a", nil); err != nil {
		t.Fatalf("SetProjectGroup(proj-a -> personal): %v", err)
	}
	name, ok, err = s.GroupOf("proj-a", nil)
	if err != nil {
		t.Fatalf("GroupOf(proj-a) after move: %v", err)
	}
	if !ok || name != "personal" {
		t.Fatalf("GroupOf(proj-a) after move = (%q, %v), want (\"personal\", true)", name, ok)
	}

	// List: proj-a in personal, proj-b in work.
	if err := s.SetProjectGroup(human, "work", "proj-b", nil); err != nil {
		t.Fatalf("SetProjectGroup(proj-b -> work): %v", err)
	}
	groups, err := s.ListGroups()
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("ListGroups returned %d rows, want 2: %+v", len(groups), groups)
	}
	byProject := map[string]string{}
	for _, g := range groups {
		byProject[g.ProjectKey] = g.GroupName
		if g.SetAt.IsZero() {
			t.Errorf("group %+v has zero SetAt", g)
		}
	}
	if byProject["proj-a"] != "personal" {
		t.Errorf("ListGroups: proj-a group = %q, want personal", byProject["proj-a"])
	}
	if byProject["proj-b"] != "work" {
		t.Errorf("ListGroups: proj-b group = %q, want work", byProject["proj-b"])
	}

	// Clear: proj-a drops out of ListGroups and GroupOf reports none.
	if err := s.ClearProjectGroup(human, "proj-a", nil); err != nil {
		t.Fatalf("ClearProjectGroup(proj-a): %v", err)
	}
	_, ok, err = s.GroupOf("proj-a", nil)
	if err != nil {
		t.Fatalf("GroupOf(proj-a) after clear: %v", err)
	}
	if ok {
		t.Fatal("GroupOf(proj-a) after clear = ok, want false")
	}
	groups, err = s.ListGroups()
	if err != nil {
		t.Fatalf("ListGroups after clear: %v", err)
	}
	if len(groups) != 1 || groups[0].ProjectKey != "proj-b" {
		t.Fatalf("ListGroups after clear = %+v, want only proj-b", groups)
	}

	// Clearing an already-ungrouped project is a no-op, not an error.
	if err := s.ClearProjectGroup(human, "proj-a", nil); err != nil {
		t.Fatalf("ClearProjectGroup(proj-a) second time: %v", err)
	}
}

// TestSetProjectGroupRejectsAgentIdentity is proof (2): a group write with
// an agent identity is rejected — ErrProjectGroupRequiresHuman, nothing
// written — and the table is left exactly as it was.
func TestSetProjectGroupRejectsAgentIdentity(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	agent := Identity{Kind: IdentityAgent, Actor: agentActor}

	seedGroupTestProjects(t, s, "proj-a")

	if err := s.SetProjectGroup(agent, "work", "proj-a", nil); err != ErrProjectGroupRequiresHuman {
		t.Fatalf("SetProjectGroup with agent identity: err = %v, want ErrProjectGroupRequiresHuman", err)
	}
	_, ok, err := s.GroupOf("proj-a", nil)
	if err != nil {
		t.Fatalf("GroupOf(proj-a): %v", err)
	}
	if ok {
		t.Fatal("SetProjectGroup with agent identity wrote a row, want nothing written")
	}
	groups, err := s.ListGroups()
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("ListGroups after rejected agent write = %+v, want empty", groups)
	}

	// An inference identity is rejected the same way.
	inference := Identity{Kind: IdentityInference, Actor: "daemon"}
	if err := s.SetProjectGroup(inference, "work", "proj-a", nil); err != ErrProjectGroupRequiresHuman {
		t.Fatalf("SetProjectGroup with inference identity: err = %v, want ErrProjectGroupRequiresHuman", err)
	}

	// ClearProjectGroup rejects the same way. Seed a group as human first so
	// there is something a rejected clear could wrongly remove.
	human := Identity{Kind: IdentityHuman, Actor: humanActor}
	if err := s.SetProjectGroup(human, "work", "proj-a", nil); err != nil {
		t.Fatalf("SetProjectGroup(human): %v", err)
	}
	if err := s.ClearProjectGroup(agent, "proj-a", nil); err != ErrProjectGroupRequiresHuman {
		t.Fatalf("ClearProjectGroup with agent identity: err = %v, want ErrProjectGroupRequiresHuman", err)
	}
	name, ok, err := s.GroupOf("proj-a", nil)
	if err != nil {
		t.Fatalf("GroupOf(proj-a) after rejected clear: %v", err)
	}
	if !ok || name != "work" {
		t.Fatalf("GroupOf(proj-a) after rejected agent clear = (%q, %v), want (\"work\", true) unchanged", name, ok)
	}
}

// TestMigrationV7AppliesOnV6FixtureWithExistingData is proof (3): migration
// 0007 applies cleanly on a store already at schema version 6 carrying
// existing projects and records, creates project_groups, and leaves the
// pre-existing data untouched.
func TestMigrationV7AppliesOnV6FixtureWithExistingData(t *testing.T) {
	rawPath := filepath.Join(t.TempDir(), "v6fixture.db")

	db := buildFixtureThroughVersion(t, rawPath, 6)
	nowNanos := time.Now().UTC().UnixNano()

	const projectKey = "proj-v6"
	if _, err := db.Exec(`INSERT INTO projects (key, toplevel, first_seen) VALUES (?, ?, ?)`,
		projectKey, "/tmp/"+projectKey, nowNanos); err != nil {
		t.Fatalf("seed v6 project: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO records (id, ts, kind, tier, text, project_key) VALUES (?, ?, ?, ?, ?, ?)`,
		"rec-v6", nowNanos, "note", "human-declared", "pre-migration record", projectKey); err != nil {
		t.Fatalf("seed v6 record: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v6 fixture: %v", err)
	}

	s := mustOpen(t, rawPath)

	v, err := s.Version()
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v != SchemaVersion {
		t.Errorf("Version() after migrating a v6 fixture = %d, want %d (SchemaVersion)", v, SchemaVersion)
	}
	if !tableExists(t, s, "project_groups") {
		t.Fatal("project_groups table not created by migrating a v6 fixture")
	}

	rec, err := s.GetRecord("rec-v6")
	if err != nil {
		t.Fatalf("GetRecord(rec-v6) after migrating: %v", err)
	}
	if rec.Text != "pre-migration record" {
		t.Errorf("pre-migration record text = %q, want unchanged", rec.Text)
	}

	// The new table works immediately after the migration that created it.
	human := Identity{Kind: IdentityHuman, Actor: humanActor}
	if err := s.SetProjectGroup(human, "work", projectKey, nil); err != nil {
		t.Fatalf("SetProjectGroup after migrating v6 fixture: %v", err)
	}
	name, ok, err := s.GroupOf(projectKey, nil)
	if err != nil {
		t.Fatalf("GroupOf after migrating v6 fixture: %v", err)
	}
	if !ok || name != "work" {
		t.Fatalf("GroupOf(%s) after migrating v6 fixture = (%q, %v), want (\"work\", true)", projectKey, name, ok)
	}
}
