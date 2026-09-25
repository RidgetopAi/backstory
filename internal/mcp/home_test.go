package mcp

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/RidgetopAi/backstory/internal/project"
)

// initGitRepo runs `git init` in dir so internal/project.RealGit resolves
// it as a real repo identity — the same helper cmd/backstory/group_test.go
// uses for the same purpose.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	cmd := exec.Command("git", "init", "-q", dir) //nolint:gosec // fixed literal git subcommand, dir is this test's own t.TempDir()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v\n%s", dir, err, out)
	}
}

// TestHandoffHomeIsWorkspaceForRepoUnderIt is the punch's DONE WHEN clause 1
// (task 482b2320, decision f3fa04c7): with <tmp>/projects configured as a
// workspace and <tmp>/projects/omarcade a git repo, a handoff noted from a
// session whose folder is <tmp>/projects/omarcade is stored with
// project_key workspace:<tmp>/projects — HOME, not the repo's own key —
// while every other record kind (exercised via TestNoteEndToEndInsertsAtIdentityTier
// elsewhere in this package) keeps the session's own key exactly as before.
//
// Mutation probe: make handoffHomeKey always report ok=false (the "home
// helper returns the session's own key" mutation task 482b2320's critic
// clause 6 names) -> RED (the stored project_key would be omarcade's own
// repo key, not the workspace key); restore -> GREEN.
func TestHandoffHomeIsWorkspaceForRepoUnderIt(t *testing.T) {
	tmp := t.TempDir()
	workspaceDir := filepath.Join(tmp, "projects")
	omarcadeDir := filepath.Join(workspaceDir, "omarcade")
	initGitRepo(t, omarcadeDir)
	workspaces := []string{workspaceDir}

	ownKey := project.Key(omarcadeDir, project.RealGit{}, []string{workspaceDir})
	wantHome := "workspace:" + filepath.Clean(workspaceDir)
	if ownKey == wantHome {
		t.Fatalf("test setup: omarcade's own key %q must differ from the workspace home %q", ownKey, wantHome)
	}

	st := mustOpenStore(t)
	sockPath := testDaemonWithWorkspaces(t, st, "claude", omarcadeDir, ownKey, workspaces)
	shim := dialShim(t, sockPath)

	raw, rerr := shim.CallTool(ToolNote, json.RawMessage(`{"kind":"handoff","text":"shipped the omarcade feature"}`))
	if rerr != nil {
		t.Fatalf("CallTool(note): %v", rerr)
	}
	var result NoteResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal NoteResult: %v", err)
	}
	rec, err := st.GetRecord(result.ID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if rec.ProjectKey != wantHome {
		t.Errorf("handoff project_key = %q, want the workspace home %q (not the repo's own key %q)", rec.ProjectKey, wantHome, ownKey)
	}
}

// TestHandoffOutsideAnyWorkspaceKeepsRepoKey is the punch's DONE WHEN clause
// 1's second half: a handoff noted from a git repo that lives outside every
// configured workspace dir is stored with that repo's own key, unchanged —
// HOME only ever re-homes a handoff into an ENCLOSING workspace, never
// invents one for a repo that has none.
func TestHandoffOutsideAnyWorkspaceKeepsRepoKey(t *testing.T) {
	tmp := t.TempDir()
	workspaceDir := filepath.Join(tmp, "projects")
	standaloneDir := filepath.Join(tmp, "standalone-repo")
	initGitRepo(t, standaloneDir)
	workspaces := []string{workspaceDir}

	ownKey := project.Key(standaloneDir, project.RealGit{}, []string{workspaceDir})

	st := mustOpenStore(t)
	sockPath := testDaemonWithWorkspaces(t, st, "claude", standaloneDir, ownKey, workspaces)
	shim := dialShim(t, sockPath)

	raw, rerr := shim.CallTool(ToolNote, json.RawMessage(`{"kind":"handoff","text":"shipped the standalone feature"}`))
	if rerr != nil {
		t.Fatalf("CallTool(note): %v", rerr)
	}
	var result NoteResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal NoteResult: %v", err)
	}
	rec, err := st.GetRecord(result.ID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if rec.ProjectKey != ownKey {
		t.Errorf("handoff project_key = %q, want the repo's own key %q unchanged (it lives outside every workspace)", rec.ProjectKey, ownKey)
	}
}

// TestNonHandoffRecordKeepsSessionOwnKeyUnderWorkspace is BUILD note 1's
// "Other record kinds unchanged": a decision noted from the SAME
// workspace-nested repo session is stored under the repo's OWN key, never
// re-homed — HOME applies to kind=handoff only.
func TestNonHandoffRecordKeepsSessionOwnKeyUnderWorkspace(t *testing.T) {
	tmp := t.TempDir()
	workspaceDir := filepath.Join(tmp, "projects")
	omarcadeDir := filepath.Join(workspaceDir, "omarcade")
	initGitRepo(t, omarcadeDir)
	workspaces := []string{workspaceDir}

	ownKey := project.Key(omarcadeDir, project.RealGit{}, []string{workspaceDir})

	st := mustOpenStore(t)
	sockPath := testDaemonWithWorkspaces(t, st, "claude", omarcadeDir, ownKey, workspaces)
	shim := dialShim(t, sockPath)

	raw, rerr := shim.CallTool(ToolNote, json.RawMessage(`{"kind":"decision","text":"chose the approach"}`))
	if rerr != nil {
		t.Fatalf("CallTool(note): %v", rerr)
	}
	var result NoteResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal NoteResult: %v", err)
	}
	rec, err := st.GetRecord(result.ID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if rec.ProjectKey != ownKey {
		t.Errorf("decision project_key = %q, want the repo's own key %q (only handoffs are re-homed)", rec.ProjectKey, ownKey)
	}
}
