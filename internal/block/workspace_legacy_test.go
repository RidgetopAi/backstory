package block_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// TestRenderWorkspaceKeyResumesFromLegacyPlainKeyHandoff is task d65ef8ff's
// sweep proven end to end through the SessionStart block: a handoff and a
// session seeded under the legacy plain key `<tmp>/projects` — exactly what
// a pre-79f7b20e build wrote before project.Key started minting the
// workspace: prefix — is merged into that folder's canonical
// "workspace:"-prefixed key the moment a later Open configures it as a
// workspace dir (store.Open's own legacy-key sweep), and still surfaces in
// the SessionStart block's Resume slot when cwd `<tmp>/projects` is
// rendered under that canonical key.
func TestRenderWorkspaceKeyResumesFromLegacyPlainKeyHandoff(t *testing.T) {
	legacyKey := filepath.Join(t.TempDir(), "projects")
	dbPath := filepath.Join(t.TempDir(), "backstory.db")

	// Seed with no workspace dirs configured, exactly what a pre-79f7b20e
	// build's own Open (which never canonicalized a write at all) would have
	// left on disk: a session and a handoff filed under the plain path.
	seed, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatalf("store.Open (seed): %v", err)
	}
	if err := seed.UpsertProject(store.Project{Key: legacyKey, Toplevel: legacyKey, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	sessionID, err := seed.StartSession(store.StartSessionParams{
		Agent: "claude", CWD: legacyKey, ProjectKey: legacyKey, StartedAt: time.Now(), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	handoffID, err := seed.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff,
		Text: "yesterday's handoff", SessionID: sessionID, ProjectKey: legacyKey,
	})
	if err != nil {
		t.Fatalf("InsertRecord handoff: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	// Re-open with legacyKey now configured as a workspace dir — the
	// upgrade/config-change moment this punch's sweep runs at.
	notAGitRepo := fakeGitNoRepo{}
	workspaceDirs := []string{legacyKey}
	s, err := store.Open(dbPath, workspaceDirs, notAGitRepo)
	if err != nil {
		t.Fatalf("store.Open (post-sweep): %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// project.Key computes cwd's project key purely from cwd/git/workspace
	// config, never consulting the store: for cwd == legacyKey, not inside a
	// git working tree, configured as the (only) workspace dir, it returns
	// the workspace-prefixed key — the same key the sweep above just merged
	// legacyKey's rows into.
	projectKey := project.Key(legacyKey, notAGitRepo, workspaceDirs)
	if !project.IsWorkspaceKey(projectKey) {
		t.Fatalf("project.Key(%s, ..., [%s]) = %q, want a workspace: key", legacyKey, legacyKey, projectKey)
	}

	out, err := block.Render(block.Params{
		Store: s, ProcFS: fakeProcFS{}, ProjectKey: projectKey, SessionID: "some-other-session",
		Now: time.Now(),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	wantLine := "Resume: (id " + handoffID + ")"
	if !strings.Contains(out, wantLine) {
		t.Fatalf("Render(%s) = %q, want it to contain %q (the legacy-key handoff)", projectKey, out, wantLine)
	}
}

// fakeGitNoRepo reports every cwd as outside a git working tree — the
// project.Git shape needed to exercise project.Key's workspace branch.
type fakeGitNoRepo struct{}

func (fakeGitNoRepo) Repo(string) (project.Repo, bool)   { return project.Repo{}, false }
func (fakeGitNoRepo) State(string) (project.State, bool) { return project.State{}, false }
