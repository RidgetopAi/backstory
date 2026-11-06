package block_test

import (
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

// branchRepo is a real git checkout on feature-a plus a linked worktree on
// feature-b: one project key (<repo>/.git), two branches (task 64cd2b5e).
type branchRepo struct {
	t     *testing.T
	a, b  string // working directories
	key   string
	s     *store.Store
	headA string
	headB string
}

func branchGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "init.defaultBranch=main"}, args...)...) //nolint:gosec // fixed git argv in a test fixture
	cmd.Dir = dir
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_DIR=") && !strings.HasPrefix(kv, "GIT_WORK_TREE=") && !strings.HasPrefix(kv, "GIT_INDEX_FILE=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newBranchRepo(t *testing.T) *branchRepo {
	t.Helper()
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	a := filepath.Join(root, "repo")
	b := filepath.Join(root, "repo-b")
	if err := os.Mkdir(a, 0o750); err != nil {
		t.Fatal(err)
	}
	branchGit(t, a, "init", "-q")
	branchGit(t, a, "commit", "-q", "--allow-empty", "-m", "base")
	branchGit(t, a, "checkout", "-q", "-b", "feature-a")
	branchGit(t, a, "worktree", "add", "-q", "-b", "feature-b", b)
	key := project.Key(a, project.RealGit{}, nil)
	if keyB := project.Key(b, project.RealGit{}, nil); keyB != key {
		t.Fatalf("worktree key %q != checkout key %q", keyB, key)
	}
	s := newTestStore(t)
	if err := s.UpsertProject(store.Project{Key: key, Toplevel: a, FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return &branchRepo{t: t, a: a, b: b, key: key, s: s,
		headA: branchGit(t, a, "rev-parse", "HEAD"), headB: branchGit(t, b, "rev-parse", "HEAD")}
}

func (r *branchRepo) handoff(branch, text string) store.Record {
	r.t.Helper()
	id, err := r.s.InsertRecordWithEdges(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff, Text: text,
		ProjectKey: r.key, GitBranch: branch, GitHead: r.headA,
	}, nil)
	if err != nil {
		r.t.Fatalf("insert handoff: %v", err)
	}
	rec, err := r.s.GetRecord(id)
	if err != nil {
		r.t.Fatal(err)
	}
	return rec
}

func (r *branchRepo) render(cwd string) string {
	r.t.Helper()
	out, err := block.Render(block.Params{
		Store: r.s, ProcFS: fakeProcFS{}, ProjectKey: r.key, SessionID: "", Harness: "claude",
		CWD: cwd, Git: project.RealGit{},
	})
	if err != nil {
		r.t.Fatalf("Render: %v", err)
	}
	return out
}

func lastLine(out string) string { return out[strings.LastIndex(out, "\n")+1:] }

func TestResumeIsPerBranchAndOtherBranchIsAPointer(t *testing.T) {
	r := newBranchRepo(t)
	ha := r.handoff("feature-a", "Built the login form on feature-a.")
	hb := r.handoff("feature-b", "Wrote the export on feature-b.")

	outA := r.render(r.a)
	if !strings.Contains(outA, "Resume: (id "+ha.ID+")") || strings.Contains(outA, "Resume: (id "+hb.ID) {
		t.Errorf("feature-a block must resume its own handoff:\n%s", outA)
	}
	if !strings.Contains(outA, "Also on branch feature-b: handoff "+hb.ID[:8]) {
		t.Errorf("feature-a block missing the feature-b pointer:\n%s", outA)
	}
	if strings.Contains(lastLine(outA), hb.ID) || !strings.Contains(lastLine(outA), "supersedes = "+ha.ID) {
		t.Errorf("feature-a final line: %s", lastLine(outA))
	}

	outB := r.render(r.b)
	if !strings.Contains(outB, "Resume: (id "+hb.ID+")") || strings.Contains(outB, "Resume: (id "+ha.ID) {
		t.Errorf("feature-b block must resume its own handoff:\n%s", outB)
	}
	if !strings.Contains(outB, "Also on branch feature-a: handoff "+ha.ID[:8]) {
		t.Errorf("feature-b block missing the feature-a pointer:\n%s", outB)
	}
	if strings.Contains(lastLine(outB), ha.ID) || !strings.Contains(lastLine(outB), "supersedes = "+hb.ID) {
		t.Errorf("feature-b final line: %s", lastLine(outB))
	}
}

func TestResumeFallsBackToOtherBranchWithoutSuperseding(t *testing.T) {
	r := newBranchRepo(t)
	ha := r.handoff("feature-a", "Built the login form on feature-a.")

	out := r.render(r.b)
	if !strings.Contains(out, "Resume: (id "+ha.ID+", from branch feature-a; you are on feature-b)") {
		t.Errorf("fallback Resume must name both branches:\n%s", out)
	}
	if strings.Contains(lastLine(out), ha.ID) || strings.Contains(lastLine(out), "supersedes = "+ha.ID) {
		t.Errorf("final line must not supersede the other branch's handoff: %s", lastLine(out))
	}
	if !block.EndsWithFinalLine(out) {
		t.Errorf("block does not end with the final line:\n%s", out)
	}
	if strings.Contains(out, "Also on branch feature-a") {
		t.Errorf("the Resume handoff's own branch needs no pointer:\n%s", out)
	}

	// feature-b notes a handoff (its chain starts here, no supersedes); the
	// feature-a block still resumes the feature-a handoff.
	hb := r.handoff("feature-b", "Started export on feature-b.")
	outA := r.render(r.a)
	if !strings.Contains(outA, "Resume: (id "+ha.ID+")") {
		t.Errorf("feature-a must still resume its handoff:\n%s", outA)
	}
	if superseded, err := r.s.IsSuperseded(ha.ID); err != nil || superseded {
		t.Errorf("feature-a handoff superseded=%v err=%v", superseded, err)
	}
	if !strings.Contains(r.render(r.b), "Resume: (id "+hb.ID+")") {
		t.Errorf("feature-b must resume its new handoff")
	}
}

func TestResumeUnknownBranchKeepsRepoWideBehaviour(t *testing.T) {
	r := newBranchRepo(t)
	old := r.handoff("", "Pre-migration handoff, no branch.")

	for name, cwd := range map[string]string{"feature-a": r.a, "feature-b": r.b} {
		out := r.render(cwd)
		if !strings.Contains(out, "Resume: (id "+old.ID+")") || strings.Contains(out, "from branch") || strings.Contains(out, "Also on branch") {
			t.Errorf("NULL-branch handoff must resume plainly on %s:\n%s", name, out)
		}
		if !strings.Contains(lastLine(out), "supersedes = "+old.ID) {
			t.Errorf("NULL-branch handoff stays supersedable on %s: %s", name, lastLine(out))
		}
	}

	ha := r.handoff("feature-a", "On feature-a.")
	// Detached HEAD and a non-git folder: today's repo-wide newest, no branch wording.
	branchGit(t, r.b, "checkout", "-q", "--detach")
	nonGit := t.TempDir()
	for name, cwd := range map[string]string{"detached": r.b, "non-git": nonGit} {
		out := r.render(cwd)
		if !strings.Contains(out, "Resume: (id "+ha.ID+")") || strings.Contains(out, "branch") && !strings.Contains(out, "Repo:") {
			t.Errorf("%s reader must get repo-wide newest:\n%s", name, out)
		}
		if strings.Contains(out, "from branch") || strings.Contains(out, "Also on branch") || strings.Contains(out, "you are on") {
			t.Errorf("%s reader must see no branch wording:\n%s", name, out)
		}
		if !strings.Contains(lastLine(out), "supersedes = "+ha.ID) {
			t.Errorf("%s reader final line: %s", name, lastLine(out))
		}
	}
}

func TestCommitsSinceHandoffNeverCountsAnotherBranch(t *testing.T) {
	r := newBranchRepo(t)
	ha := r.handoff("feature-a", "On feature-a.")
	branchGit(t, r.b, "commit", "-q", "--allow-empty", "-m", "b1")
	branchGit(t, r.b, "commit", "-q", "--allow-empty", "-m", "b2")

	out := r.render(r.b)
	if strings.Contains(out, "since the handoff") {
		t.Errorf("feature-b commits must not count as since a feature-a handoff:\n%s", out)
	}
	if !strings.Contains(out, "handoff is from branch feature-a") {
		t.Errorf("Repo line should say the handoff is from another branch:\n%s", out)
	}

	branchGit(t, r.a, "commit", "-q", "--allow-empty", "-m", "a1")
	if outA := r.render(r.a); !strings.Contains(outA, "1 commit since the handoff") || !strings.Contains(outA, ha.ID) {
		t.Errorf("same-branch commit count lost:\n%s", outA)
	}
}

func TestFinalLineOtherBranchHasNoID(t *testing.T) {
	l := block.FinalLineOtherBranch("feature-a")
	if !strings.HasPrefix(l, block.FinalLine+". ") || !strings.Contains(l, "supersedes = nothing") || !strings.Contains(l, "feature-a") {
		t.Errorf("unexpected line: %s", l)
	}
}

// A handoff with no known branch matches any branch, so a newer one beats an
// older handoff written on the reader's own branch.
func TestResumeUnknownBranchCompetesWithOwnBranch(t *testing.T) {
	r := newBranchRepo(t)
	r.handoff("feature-b", "Older, feature-b.")
	unknown := r.handoff("", "Newer, no branch recorded.")
	out := r.render(r.b)
	if !strings.Contains(out, "Resume: (id "+unknown.ID+")") {
		t.Errorf("a newer NULL-branch handoff must be the Resume on feature-b:\n%s", out)
	}
}
