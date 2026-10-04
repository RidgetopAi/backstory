package block_test

import (
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// resumeHomeFakeGit reports a git repo, toplevel == the dir itself, for
// every dir a test seeds — enough to exercise project.Label's repo branch
// without shelling out to a real git binary.
type resumeHomeFakeGit map[string]bool

func (f resumeHomeFakeGit) Repo(dir string) (project.Repo, bool) {
	if f[dir] {
		return project.Repo{Toplevel: dir}, true
	}
	return project.Repo{}, false
}
func (f resumeHomeFakeGit) State(string) (project.State, bool) { return project.State{}, false }

// TestRenderResumeFiltersHomeByRepoAndNamesLabelAtWorkspaceRoot is the
// punch's DONE WHEN clause 3 (task 482b2320, decision f3fa04c7): the home
// holds handoff H1 (labelled projects/omarcade) and a NEWER handoff H2
// (labelled projects/vidflow). The SessionStart block for cwd
// <workspace>/omarcade shows Resume: (id H1) — the OLDER handoff, because
// it is the one whose own session actually touched omarcade, even though
// H2 is newer; for cwd <workspace> itself it shows Resume: (id H2) — the
// home's newest handoff, unfiltered — and names its label, projects/vidflow,
// on the Resume line.
func TestRenderResumeFiltersHomeByRepoAndNamesLabelAtWorkspaceRoot(t *testing.T) {
	workspaceDir := "/home/fixture/projects"
	omarcadeDir := workspaceDir + "/omarcade"
	vidflowDir := workspaceDir + "/vidflow"
	home := "workspace:" + workspaceDir

	s := newTestStore(t)
	mustUpsertProject(t, s, home)
	mustUpsertProject(t, s, omarcadeDir)
	mustUpsertProject(t, s, vidflowDir)

	sessOmarcade, err := s.StartSession(store.StartSessionParams{
		Agent: "claude", CWD: omarcadeDir, ProjectKey: omarcadeDir, StartedAt: time.Now(), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession(omarcade): %v", err)
	}
	h1, err := s.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff,
		Text: "shipped the omarcade feature", SessionID: sessOmarcade, ProjectKey: home,
	})
	if err != nil {
		t.Fatalf("InsertRecord H1: %v", err)
	}

	sessVidflow, err := s.StartSession(store.StartSessionParams{
		Agent: "claude", CWD: vidflowDir, ProjectKey: vidflowDir, StartedAt: time.Now(), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession(vidflow): %v", err)
	}
	h2, err := s.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff,
		Text: "shipped the vidflow feature", SessionID: sessVidflow, ProjectKey: home,
	})
	if err != nil {
		t.Fatalf("InsertRecord H2: %v", err)
	}

	workspaces := []string{workspaceDir}
	git := resumeHomeFakeGit{omarcadeDir: true, vidflowDir: true}

	t.Run("inside omarcade resumes H1, the repo's own handoff, not the newer H2", func(t *testing.T) {
		out, err := block.Render(block.Params{
			Store: s, ProcFS: fakeProcFS{}, ProjectKey: omarcadeDir, SessionID: "caller-session",
			CWD: omarcadeDir, Git: git, WorkspaceDirs: workspaces, Now: time.Now(),
		})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if !strings.Contains(out, "Resume: (id "+h1+")") {
			t.Fatalf("Render(omarcade) = %q, want it to contain Resume: (id %s) (H1, not H2)", out, h1)
		}
		if strings.Contains(out, "Resume: (id "+h2+")") {
			t.Fatalf("Render(omarcade) = %q, must not resume H2 (vidflow's own handoff)", out)
		}
	})

	t.Run("at the workspace root serves no project-written handoff, only a pointer", func(t *testing.T) {
		out, err := block.Render(block.Params{
			Store: s, ProcFS: fakeProcFS{}, ProjectKey: home, SessionID: "caller-session",
			CWD: workspaceDir, Git: git, WorkspaceDirs: workspaces, Now: time.Now(),
		})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if strings.Contains(out, "Resume: (id") || strings.Contains(out, "shipped the") {
			t.Fatalf("Render(workspace root) = %q, must carry no handoff body", out)
		}
		if !strings.Contains(out, "projects/vidflow (id "+h2+")") || !strings.Contains(out, "projects/omarcade (id "+h1+")") {
			t.Fatalf("Render(workspace root) = %q, want pointer naming both projects and ids", out)
		}
	})
}
