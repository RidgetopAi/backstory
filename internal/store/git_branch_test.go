package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func insertBranchHandoff(t *testing.T, s *Store, project, branch string) string {
	t.Helper()
	id, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindHandoff, Text: "h on " + branch,
		ProjectKey: project, GitBranch: branch,
	})
	if err != nil {
		t.Fatalf("InsertRecord: %v", err)
	}
	return id
}

// The branch is stamped at write, read back, and refused for UPDATE like git_head.
func TestGitBranchStampedAndImmutable(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	seedRemoteProject(t, s, "k", "")
	id := insertBranchHandoff(t, s, "k", "feature-a")
	rec, err := s.GetRecord(id)
	if err != nil || rec.GitBranch != "feature-a" {
		t.Fatalf("GetRecord = %+v, %v; want git_branch feature-a", rec, err)
	}
	for _, q := range []string{
		`UPDATE records SET git_branch = 'feature-b' WHERE id = ?`,
		`UPDATE records SET git_branch = NULL WHERE id = ?`,
	} {
		_, err := s.DB().Exec(q, id)
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: err = %v, want the append-only abort", q, err)
		}
	}
	unknown := insertBranchHandoff(t, s, "k", "")
	if _, err := s.DB().Exec(`UPDATE records SET git_branch = 'x' WHERE id = ?`, unknown); err == nil {
		t.Error("a NULL git_branch must not be settable afterwards either")
	}
	if rec, _ := s.GetRecord(unknown); rec.GitBranch != "" {
		t.Errorf("unknown branch read back as %q", rec.GitBranch)
	}
}

// The branch crosses sync import and survives backup/restore.
func TestGitBranchSyncAndBackupRoundTrip(t *testing.T) {
	a := mustOpen(t, filepath.Join(t.TempDir(), "a.db"))
	b := mustOpen(t, filepath.Join(t.TempDir(), "b.db"))
	seedRemoteProject(t, a, "/a/widgets", "https://github.com/acme/widgets")
	seedRemoteProject(t, b, "/b/widgets", "git@github.com:acme/widgets.git")
	withBranch := insertBranchHandoff(t, a, "/a/widgets", "feature-a")
	noBranch := insertBranchHandoff(t, a, "/a/widgets", "")

	changes, _, _, err := a.ChangesSince(0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var recs []SyncChange
	for _, c := range changes {
		if c.Kind == SyncKindRecord {
			recs = append(recs, c)
		}
	}
	if len(recs) != 2 {
		t.Fatalf("served %d records, want 2", len(recs))
	}
	human := Identity{Kind: IdentityHuman, Actor: humanActor}
	if _, err := b.ImportSyncChanges(human, SyncPeer{DeviceID: "dev-a"}, recs); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{withBranch: "feature-a", noBranch: ""} {
		got, err := b.GetRecord(id)
		if err != nil || got.GitBranch != want {
			t.Errorf("imported %s: git_branch = %q, %v; want %q", id[:8], got.GitBranch, err, want)
		}
	}

	dest := filepath.Join(t.TempDir(), "snap.db")
	if _, err := a.Backup(dest, false); err != nil {
		t.Fatal(err)
	}
	r := mustOpen(t, dest)
	if got, err := r.GetRecord(withBranch); err != nil || got.GitBranch != "feature-a" {
		t.Errorf("restored git_branch = %q, %v", got.GitBranch, err)
	}
}
