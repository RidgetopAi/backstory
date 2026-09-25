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

// TestRenderWorkspaceKeyResumesFromLegacyPlainKeyHandoff is the punch's
// DONE WHEN clause 1 (task 50249f56): a handoff and a session seeded under
// the legacy plain key `<tmp>/projects` — exactly what a pre-workspace
// build wrote before the workspaces re-key (79f7b20e) minted the
// workspace: prefix, since no migration carries old rows across and
// records stay append-only — still surfaces in the SessionStart block's
// Resume slot when cwd `<tmp>/projects` is rendered with that directory
// configured as a workspace, resolving to project.Key's workspace:<dir>
// form.
func TestRenderWorkspaceKeyResumesFromLegacyPlainKeyHandoff(t *testing.T) {
	legacyKey := filepath.Join(t.TempDir(), "projects")

	s := newTestStore(t)
	if err := s.UpsertProject(store.Project{Key: legacyKey, Toplevel: legacyKey, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	sessionID, err := s.StartSession(store.StartSessionParams{
		Agent: "claude", CWD: legacyKey, ProjectKey: legacyKey, StartedAt: time.Now(), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	handoffID, err := s.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff,
		Text: "yesterday's handoff", SessionID: sessionID, ProjectKey: legacyKey,
	})
	if err != nil {
		t.Fatalf("InsertRecord handoff: %v", err)
	}

	// project.Key computes cwd's project key purely from cwd/git/workspace
	// config, never consulting the store: for cwd == legacyKey, not inside a
	// git working tree, configured as the (only) workspace dir, it returns
	// the workspace-prefixed key — regardless of what key the store's rows
	// happen to carry.
	notAGitRepo := fakeGitNoRepo{}
	projectKey := project.Key(legacyKey, notAGitRepo, []string{legacyKey})
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
