package recall

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

func gitInDir(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // test helper; args are literals from this file
	cmd.Dir = dir
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestWorkspaceHomedHandoffReportsGitDrift: a handoff noted from repo R under
// workspace W is filed under W's key (a plain folder, not a repo); two
// commits in R later, recall counts them against R — the handoff session's
// observed location — and never reports "repo not available" for W.
func TestWorkspaceHomedHandoffReportsGitDrift(t *testing.T) {
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(ws, "r")
	if err := os.Mkdir(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	gitInDir(t, repo, "init", "-q")
	gitInDir(t, repo, "commit", "-q", "--allow-empty", "-m", "c1")
	head := gitInDir(t, repo, "rev-parse", "HEAD")

	st := newTestStore(t)
	home := "workspace:" + ws
	if err := st.UpsertProject(store.Project{Key: home, Toplevel: ws, FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	repoKey := project.Key(repo, project.RealGit{}, []string{ws})
	if err := st.UpsertProject(store.Project{Key: repoKey, Toplevel: repo, FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	pid := 1
	sid, err := st.StartSession(store.StartSessionParams{
		Agent: "claude", CWD: repo, ProjectKey: repoKey, PID: &pid, StartedAt: time.Now(), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff, Text: "h",
		SessionID: sid, ProjectKey: home, GitHead: head,
	}); err != nil {
		t.Fatal(err)
	}
	gitInDir(t, repo, "commit", "-q", "--allow-empty", "-m", "c2")
	gitInDir(t, repo, "commit", "-q", "--allow-empty", "-m", "c3")

	res, err := Build(st, ProjectAnchor(home), AltitudeFull, testBudget, []string{ws})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].Git == nil {
		t.Fatalf("items = %+v, want one with a git stamp", res.Items)
	}
	g := res.Items[0].Git
	if g.CommitsSince == nil || *g.CommitsSince != 2 {
		t.Fatalf("CommitsSince = %v (reason %q), want 2", g.CommitsSince, g.Reason)
	}
	if strings.Contains(res.Rendered, "repo not available") {
		t.Fatalf("rendered text reports repo not available:\n%s", res.Rendered)
	}
}
