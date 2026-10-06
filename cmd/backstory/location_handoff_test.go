package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// handoffFixture is a workspace W with git repos W/demo and W/other, one
// handoff per repo filed under the workspace key from a session in that repo
// — the shape `note` writes (decision f3fa04c7) and every bare-repo-key
// reader missed (task ed31b744).
type handoffFixture struct {
	ws, demo, other string
	demoKey         string
	demoH, otherH   string
}

func newHandoffFixture(t *testing.T) *handoffFixture {
	t.Helper()
	home := t.TempDir()
	f := &handoffFixture{ws: filepath.Join(home, "projects")}
	f.demo, f.other = filepath.Join(f.ws, "demo"), filepath.Join(f.ws, "other")
	for _, d := range []string{f.demo, f.other} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "-C", d, "init", "-q").CombinedOutput(); err != nil { //nolint:gosec // fixed args
			t.Fatalf("git init: %v\n%s", err, out)
		}
	}
	dataDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", dataDir)
	t.Setenv("BACKSTORY_WORKSPACE_DIRS", f.ws)

	workspaces := []string{f.ws}
	st, err := store.Open(filepath.Join(dataDir, "backstory", "backstory.db"), workspaces, project.RealGit{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	wsKey := "workspace:" + f.ws
	f.demoKey = project.Key(f.demo, project.RealGit{}, workspaces)
	otherKey := project.Key(f.other, project.RealGit{}, workspaces)
	for _, k := range []string{wsKey, f.demoKey, otherKey} {
		if err := st.UpsertProject(store.Project{Key: k, Toplevel: k, FirstSeen: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	handoff := func(cwd, key, text string) string {
		sid, err := st.StartSession(store.StartSessionParams{Agent: "claude", CWD: cwd, ProjectKey: key, StartedAt: time.Now(), Origin: store.OriginLive})
		if err != nil {
			t.Fatal(err)
		}
		id, err := st.InsertRecord(store.InsertRecordParams{
			Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff,
			Text: text, SessionID: sid, ProjectKey: wsKey,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	f.demoH = handoff(f.demo, f.demoKey, "DEMO-HANDOFF state")
	f.otherH = handoff(f.other, otherKey, "OTHER-HANDOFF state")
	return f
}

func (f *handoffFixture) resume(t *testing.T, dir string) string {
	t.Helper()
	dataDir := os.Getenv("XDG_DATA_HOME")
	st, err := store.Open(filepath.Join(dataDir, "backstory", "backstory.db"), []string{f.ws}, project.RealGit{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	out, err := block.Render(block.Params{
		Store: st, ProjectKey: project.Key(dir, project.RealGit{}, []string{f.ws}), SessionID: "caller",
		CWD: dir, Git: project.RealGit{}, WorkspaceDirs: []string{f.ws}, Now: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func runCLI(t *testing.T, cwd string, args ...string) (string, int) {
	t.Helper()
	t.Chdir(cwd)
	var out, errb bytes.Buffer
	code := run(args, bytes.NewReader(nil), &out, &errb)
	if code != 0 {
		t.Logf("%v stderr: %s", args, errb.String())
	}
	return out.String(), code
}

// Task ed31b744 DONE WHEN 1: `records --here` and `export` in W/demo see the
// workspace-homed handoff noted from W/demo, and not W/other's; the block's
// Resume there is the same record.
func TestRecordsHereAndExportSeeWorkspaceHomedHandoff(t *testing.T) {
	f := newHandoffFixture(t)

	out, code := runCLI(t, f.demo, "records", "--here", "--kind", "handoff", "--json")
	if code != 0 {
		t.Fatalf("records --here exit %d", code)
	}
	var rec recordsOutputJSON
	if err := json.Unmarshal([]byte(out), &rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.Records) != 1 || rec.Records[0].ID != f.demoH {
		t.Errorf("records --here in W/demo = %+v, want only %s", rec.Records, f.demoH)
	}

	out, code = runCLI(t, f.demo, "export")
	if code != 0 || !strings.Contains(out, "DEMO-HANDOFF") || strings.Contains(out, "OTHER-HANDOFF") || strings.Contains(out, "no handoff yet") {
		t.Errorf("export in W/demo (exit %d) = %q, want demo's handoff only", code, out)
	}

	if blk := f.resume(t, f.demo); !strings.Contains(blk, "Resume: (id "+f.demoH+")") {
		t.Errorf("block in W/demo = %q, want Resume %s", blk, f.demoH)
	}
}

// Task ed31b744 DONE WHEN 3: `purge --project <W/demo key> --yes` tombstones
// W/demo's workspace-homed handoff, the next block in W/demo has no Resume
// for it, and W/other's handoff survives.
func TestPurgeProjectTombstonesWorkspaceHomedHandoff(t *testing.T) {
	f := newHandoffFixture(t)

	out, code := runCLI(t, f.demo, "purge", "--project", f.demoKey, "--yes")
	if code != 0 {
		t.Fatalf("purge exit %d: %s", code, out)
	}
	if blk := f.resume(t, f.demo); strings.Contains(blk, f.demoH) || strings.Contains(blk, "DEMO-HANDOFF") {
		t.Errorf("block in W/demo after purge = %q, still resumes the purged handoff", blk)
	}
	if blk := f.resume(t, f.other); !strings.Contains(blk, "Resume: (id "+f.otherH+")") {
		t.Errorf("block in W/other after purging W/demo = %q, want its own handoff %s", blk, f.otherH)
	}
}
