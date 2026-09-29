package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/store"
)

// gitStampEnvModes are the invoking-environment conditions every git-stamp
// test runs under (task 5615ddae DONE WHEN 5): "clean", and "hostile" —
// GIT_DIR / GIT_WORK_TREE pointing at an unrelated decoy repo and a HOME
// whose .gitconfig would break any commit that did not override it.
var gitStampEnvModes = []string{"clean", "hostile"}

// applyGitStampEnv sets (hostile) or unsets (clean) the ambient git
// environment for the duration of t. The tests' own git invocations never
// depend on it (see gitIn); this is what the daemon under test inherits.
func applyGitStampEnv(t *testing.T, mode string) {
	t.Helper()
	for _, k := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if mode != "hostile" {
		return
	}
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"),
		[]byte("[commit]\n\tgpgsign = true\n[gpg]\n\tprogram = /nonexistent-gpg\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	decoy := t.TempDir()
	gitIn(t, decoy, "init", "-q")
	gitIn(t, decoy, "commit", "-q", "--allow-empty", "-m", "decoy")
	t.Setenv("GIT_DIR", filepath.Join(decoy, ".git"))
	t.Setenv("GIT_WORK_TREE", decoy)
}

// gitIn runs git in dir under a fully controlled environment (own identity,
// no global/system config, no inherited GIT_* redirection) and returns its
// trimmed stdout.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	env := []string{}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newTempRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "c1")
	return dir
}

func findItem(t *testing.T, res RecallResult, id string) RecallItem {
	t.Helper()
	for _, it := range res.Items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("recall did not return record %s", id)
	return RecallItem{}
}

func TestGitStampWriteAndRecall(t *testing.T) {
	for _, mode := range gitStampEnvModes {
		t.Run(mode, func(t *testing.T) {
			applyGitStampEnv(t, mode)
			repo := newTempRepo(t)
			head := gitIn(t, repo, "rev-parse", "HEAD")

			st := mustOpenStore(t)
			shim := dialShim(t, testDaemon(t, st, "claude", repo, "git-proj"))

			id := noteText(t, shim, "decision", "stamped at c1")
			rec, err := st.GetRecord(id)
			if err != nil {
				t.Fatal(err)
			}
			if rec.GitHead != head {
				t.Fatalf("stored git_head = %q, want full HEAD %q", rec.GitHead, head)
			}

			// (2) no further commits: at current HEAD, commits_since = 0.
			it := findItem(t, callRecall(t, shim, nil), id)
			if it.GitHead != head || it.GitShort != head[:7] {
				t.Errorf("git_head/short = %q/%q, want %q/%q", it.GitHead, it.GitShort, head, head[:7])
			}
			if it.CommitsSince == nil || *it.CommitsSince != 0 {
				t.Fatalf("commits_since = %v, want 0", it.CommitsSince)
			}
			if !strings.Contains(it.GitStatus, "at current HEAD") {
				t.Errorf("git_status = %q, want it to read at current HEAD", it.GitStatus)
			}

			// (2) two further commits.
			gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "c2")
			gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "c3")
			res := callRecall(t, shim, nil)
			it = findItem(t, res, id)
			if it.CommitsSince == nil || *it.CommitsSince != 2 {
				t.Fatalf("commits_since = %v, want 2", it.CommitsSince)
			}
			want := "written at " + head[:7] + "; repo now 2 commits later"
			if it.GitStatus != want {
				t.Errorf("git_status = %q, want %q", it.GitStatus, want)
			}
		})
	}
}

func TestGitStampNonGitDirWritesNull(t *testing.T) {
	for _, mode := range gitStampEnvModes {
		t.Run(mode, func(t *testing.T) {
			applyGitStampEnv(t, mode)
			dir := t.TempDir()
			st := mustOpenStore(t)
			shim := dialShim(t, testDaemon(t, st, "claude", dir, "plain-proj"))

			id := noteText(t, shim, "note", "no repo here")
			rec, err := st.GetRecord(id)
			if err != nil {
				t.Fatal(err)
			}
			if rec.GitHead != "" {
				t.Errorf("git_head = %q, want NULL for a non-git directory", rec.GitHead)
			}
			it := findItem(t, callRecall(t, shim, nil), id)
			if it.GitHead != "" || it.CommitsSince != nil || it.GitStatus != "" {
				t.Errorf("unstamped record carries git fields: %+v", it)
			}
		})
	}
}

func TestGitStampUnreachableShaStillRecalls(t *testing.T) {
	cases := map[string]func(t *testing.T, repo string){
		// The whole repo replaced: the stamped commit object is gone.
		"new-root-repo": func(t *testing.T, repo string) {
			if err := os.RemoveAll(filepath.Join(repo, ".git")); err != nil {
				t.Fatal(err)
			}
			gitIn(t, repo, "init", "-q")
			gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "new root")
		},
		// History rewritten onto an orphan root: the object survives but is
		// no longer an ancestor of HEAD.
		"orphan-root": func(t *testing.T, repo string) {
			gitIn(t, repo, "checkout", "-q", "--orphan", "rewritten")
			gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "new root")
		},
	}
	for name, rewrite := range cases {
		for _, mode := range gitStampEnvModes {
			t.Run(name+"/"+mode, func(t *testing.T) {
				applyGitStampEnv(t, mode)
				repo := newTempRepo(t)
				head := gitIn(t, repo, "rev-parse", "HEAD")
				st := mustOpenStore(t)
				shim := dialShim(t, testDaemon(t, st, "claude", repo, "rewritten-proj"))
				id := noteText(t, shim, "outcome", "written before the rewrite")

				rewrite(t, repo)

				it := findItem(t, callRecall(t, shim, nil), id)
				if it.GitHead != head {
					t.Errorf("git_head = %q, want %q", it.GitHead, head)
				}
				if it.CommitsSince != nil {
					t.Errorf("commits_since = %d, want absent for an unreachable sha", *it.CommitsSince)
				}
				if it.GitNote == "" {
					t.Error("git_note empty, want a reason")
				}
				if !strings.Contains(it.GitStatus, head[:7]) {
					t.Errorf("git_status = %q, want it to name %s", it.GitStatus, head[:7])
				}
			})
		}
	}
}

func TestGitStampRecordsRoundTripsThroughStore(t *testing.T) {
	st := mustOpenStore(t)
	if err := st.UpsertProject(store.Project{Key: "k", Toplevel: "/x"}); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	id, err := st.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityHuman}, Kind: store.KindNote, Text: "t", ProjectKey: "k", GitHead: sha,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := st.GetRecord(id)
	if err != nil || rec.GitHead != sha {
		t.Fatalf("GetRecord = %+v, %v; want git_head %s", rec, err, sha)
	}
}
