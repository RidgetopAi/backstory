package store

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
)

const labelsWorkspaceDir = "/tmp/fixture/projects"

var labelsWorkspaces = []string{labelsWorkspaceDir}

// labelsFakeGit is a canned dir -> Repo lookup for sessionLabels/
// LatestHandoffAt tests: only the repo dirs a test explicitly seeds report
// as git working trees, exactly like a real filesystem where every other
// dir under the workspace is a plain, non-git folder.
type labelsFakeGit map[string]project.Repo

func (f labelsFakeGit) Repo(dir string) (project.Repo, bool) { r, ok := f[dir]; return r, ok }
func (f labelsFakeGit) State(string) (project.State, bool)   { return project.State{}, false }

var labelsGit = labelsFakeGit{
	labelsWorkspaceDir + "/omarcade": {Toplevel: labelsWorkspaceDir + "/omarcade"},
	labelsWorkspaceDir + "/vidflow":  {Toplevel: labelsWorkspaceDir + "/vidflow"},
}

func mustAppendMutatingToolUse(t *testing.T, s *Store, sessionID, name, path string) {
	t.Helper()
	b, err := json.Marshal(payload.ToolUse{Name: name, Path: path})
	if err != nil {
		t.Fatalf("marshal tool.use payload: %v", err)
	}
	if _, err := s.AppendEvent(Event{TS: time.Now(), Kind: payload.KindToolUse, SessionID: sessionID, Source: "shell", Payload: string(b)}); err != nil {
		t.Fatalf("AppendEvent tool.use: %v", err)
	}
}

// TestSessionLabelsFromFileTouchingEvents is the punch's DONE WHEN clause 2
// (task 482b2320, decision f3fa04c7): a session started at the workspace
// root whose timeline events touch files in two repos under it has labels
// exactly {projects/omarcade, projects/vidflow} — derived from the touched
// file paths, never from about[] on any record.
//
// Mutation probe: derive labels from the handoff's about[] instead of
// events (the critic clause 6 mutation) -> RED (labels would include
// "projects/other", which about[] names but no event ever touched);
// restore -> GREEN.
func TestSessionLabelsFromFileTouchingEvents(t *testing.T) {
	s := mustOpen(t, tempDBPath(t))
	mustUpsertProject(t, s, "workspace:"+labelsWorkspaceDir)
	sessionID, err := s.StartSession(StartSessionParams{
		Agent: "claude", CWD: labelsWorkspaceDir, ProjectKey: "workspace:" + labelsWorkspaceDir,
		StartedAt: time.Now(), Origin: OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	mustAppendMutatingToolUse(t, s, sessionID, "Edit", labelsWorkspaceDir+"/omarcade/main.go")
	mustAppendMutatingToolUse(t, s, sessionID, "Write", labelsWorkspaceDir+"/vidflow/app.py")
	// A Read carries a path too but never changed anything: it must not
	// contribute a label (payload.IsMutatingFileTool's own rule, task
	// 393d174c, reused here).
	mustAppendMutatingToolUse(t, s, sessionID, "Read", labelsWorkspaceDir+"/elsewhere/README.md")

	// The handoff's about[] names a THIRD path this session never touched a
	// file under. If labels were ever derived from about[] instead of
	// events, "projects/other" would leak into the result.
	if _, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindHandoff, Text: "handoff text",
		About: []string{labelsWorkspaceDir + "/other/file.go"}, SessionID: sessionID,
		ProjectKey: "workspace:" + labelsWorkspaceDir,
	}); err != nil {
		t.Fatalf("InsertRecord handoff: %v", err)
	}

	sess, err := s.getSession(sessionID)
	if err != nil {
		t.Fatalf("getSession: %v", err)
	}
	labels, err := s.sessionLabels(sess, labelsGit, labelsWorkspaces)
	if err != nil {
		t.Fatalf("sessionLabels: %v", err)
	}

	want := []string{"projects/omarcade", "projects/vidflow"}
	if !reflect.DeepEqual(labels, want) {
		t.Fatalf("sessionLabels = %v, want exactly %v (never projects/other, never projects/elsewhere)", labels, want)
	}
}

// TestSessionLabelsFallsBackToOwnFolder is DONE WHEN clause 2's second half:
// a session with no file-touching events at all is labelled with its own
// folder.
func TestSessionLabelsFallsBackToOwnFolder(t *testing.T) {
	s := mustOpen(t, tempDBPath(t))
	mustUpsertProject(t, s, "workspace:"+labelsWorkspaceDir)
	sessionID, err := s.StartSession(StartSessionParams{
		Agent: "claude", CWD: labelsWorkspaceDir, ProjectKey: "workspace:" + labelsWorkspaceDir,
		StartedAt: time.Now(), Origin: OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	// A session.start event with no path at all: not a file-touching event.
	if _, err := s.AppendEvent(Event{TS: time.Now(), Kind: payload.KindSessionStart, SessionID: sessionID, Source: "shell", Payload: `{}`}); err != nil {
		t.Fatalf("AppendEvent session.start: %v", err)
	}

	sess, err := s.getSession(sessionID)
	if err != nil {
		t.Fatalf("getSession: %v", err)
	}
	labels, err := s.sessionLabels(sess, labelsGit, labelsWorkspaces)
	if err != nil {
		t.Fatalf("sessionLabels: %v", err)
	}
	if want := []string{"projects"}; !reflect.DeepEqual(labels, want) {
		t.Fatalf("sessionLabels (no file-touching events) = %v, want %v (its own folder)", labels, want)
	}
}

// TestLatestHandoffAtFiltersHomeByRepo is store.LatestHandoffAt's own unit
// coverage: two handoffs share one home (the workspace), each from a
// session labelled for a different repo; a label-scoped lookup returns the
// one whose session actually carries that label, even when it is NOT the
// newest of the two.
func TestLatestHandoffAtFiltersHomeByRepo(t *testing.T) {
	s := mustOpen(t, tempDBPath(t))
	home := "workspace:" + labelsWorkspaceDir
	mustUpsertProject(t, s, home)

	sessOmarcade, err := s.StartSession(StartSessionParams{
		Agent: "claude", CWD: labelsWorkspaceDir + "/omarcade", ProjectKey: home, StartedAt: time.Now(), Origin: OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession(omarcade): %v", err)
	}
	older, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindHandoff, Text: "omarcade handoff",
		SessionID: sessOmarcade, ProjectKey: home,
	})
	if err != nil {
		t.Fatalf("InsertRecord omarcade handoff: %v", err)
	}

	sessVidflow, err := s.StartSession(StartSessionParams{
		Agent: "claude", CWD: labelsWorkspaceDir + "/vidflow", ProjectKey: home, StartedAt: time.Now(), Origin: OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession(vidflow): %v", err)
	}
	if _, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindHandoff, Text: "vidflow handoff (newer)",
		SessionID: sessVidflow, ProjectKey: home,
	}); err != nil {
		t.Fatalf("InsertRecord vidflow handoff: %v", err)
	}

	got, ok, err := s.LatestHandoffAt(labelsWorkspaceDir+"/omarcade", labelsGit, labelsWorkspaces)
	if err != nil {
		t.Fatalf("LatestHandoffAt: %v", err)
	}
	if !ok {
		t.Fatal("LatestHandoffAt found = false, want true")
	}
	if got.ID != older {
		t.Errorf("LatestHandoffAt(home, projects/omarcade) = %s, want the OLDER omarcade handoff %s, not the newer vidflow one", got.ID, older)
	}
}

func tempDBPath(t *testing.T) string {
	t.Helper()
	return t.TempDir() + "/backstory.db"
}
