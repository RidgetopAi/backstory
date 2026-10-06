package project

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RealGit shells out to the git binary. It is the production Git
// implementation; tests use a fake (key_test.go).
type RealGit struct{}

// Repo implements Git by running git rev-parse / git remote in cwd.
func (RealGit) Repo(cwd string) (Repo, bool) {
	common, ok := gitOutput(cwd, "rev-parse", "--git-common-dir")
	if !ok || common == "" {
		return Repo{}, false
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(cwd, common)
	}
	common = filepath.Clean(common)

	top, _ := gitOutput(cwd, "rev-parse", "--show-toplevel")
	remote := firstRemoteURL(cwd)

	return Repo{CommonDir: common, RemoteURL: remote, Toplevel: top}, true
}

// State implements Git by running git rev-parse/status in cwd. ok is false
// when cwd is not inside a git working tree, or either git invocation
// failed — the caller must never fall back to Uncommitted == 0 in that case.
func (RealGit) State(cwd string) (State, bool) {
	branch, ok := gitOutput(cwd, "rev-parse", "--abbrev-ref", "HEAD")
	if !ok {
		return State{}, false
	}
	status, ok := gitOutputRaw(cwd, "status", "--porcelain")
	if !ok {
		return State{}, false
	}
	var lines []string
	if status = strings.TrimRight(status, "\n"); status != "" {
		lines = strings.Split(status, "\n")
	}
	// A repo with no commit yet has no HEAD to resolve; that is an observed
	// "", not a failure.
	head, _ := gitOutput(cwd, "rev-parse", "--verify", "-q", "HEAD")
	paths := porcelainPaths(lines)
	top, _ := gitOutput(cwd, "rev-parse", "--show-toplevel")
	return State{Branch: branch, Uncommitted: len(lines), Head: head, Paths: paths, Hashes: pathHashes(top, paths)}, true
}

// Sentinels for a path whose working-tree content is not a readable file.
const (
	hashAbsent     = "absent"
	hashUnreadable = "unreadable"
)

// pathHashes maps each repo-relative path to a hash of its working-tree
// content. A deleted path hashes to hashAbsent; a directory (git lists a wholly
// untracked directory as "dir/") hashes every file beneath it. Only hashes are
// returned — file content never leaves this function. An empty top (the
// toplevel could not be resolved) yields nil: no fingerprint, path-set only.
func pathHashes(top string, paths []string) map[string]string {
	if top == "" {
		return nil
	}
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		// A rename line is "old -> new"; the new name is what exists on disk.
		name := p
		if i := strings.LastIndex(p, " -> "); i >= 0 {
			name = p[i+len(" -> "):]
		}
		out[p] = hashTreePath(filepath.Join(top, filepath.FromSlash(name)))
	}
	return out
}

func hashTreePath(abs string) string {
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return hashAbsent
		}
		return hashUnreadable
	}
	h := sha256.New()
	if info.IsDir() {
		err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(abs, path)
			_, _ = io.WriteString(h, rel+"\x00")
			sum, herr := hashFile(path)
			_, _ = io.WriteString(h, sum+"\x00")
			return herr
		})
	} else {
		var sum string
		sum, err = hashFile(abs)
		_, _ = io.WriteString(h, sum)
	}
	if err != nil {
		return hashUnreadable
	}
	return hex.EncodeToString(h.Sum(nil))
}

// hashFile hashes a regular file's bytes, or a symlink's target text.
func hashFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t, err := os.Readlink(path)
		return "link:" + t, err
	}
	f, err := os.Open(path) //nolint:gosec // path is under the session's own git working tree
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// porcelainPaths extracts the sorted, de-duplicated paths from `git status
// --porcelain` lines ("XY path", or "XY old -> new" for a rename).
func porcelainPaths(lines []string) []string {
	seen := map[string]bool{}
	var paths []string
	for _, l := range lines {
		if len(l) < 4 {
			continue
		}
		p := l[3:]
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return paths
}

// gitOutputRaw is gitOutput without trimming leading whitespace: a porcelain
// line's first column may be a space, which is part of the status code.
func gitOutputRaw(cwd string, args ...string) (string, bool) {
	cmd := exec.Command("git", args...) //nolint:gosec // args are a fixed set of literal git subcommands, never caller-controlled
	cmd.Dir = cwd
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", false
	}
	return out.String(), true
}

// firstRemoteURL returns the URL of the first remote `git remote` lists for
// cwd's repo, or "" if none is configured.
func firstRemoteURL(cwd string) string {
	out, ok := gitOutput(cwd, "remote")
	if !ok || out == "" {
		return ""
	}
	names := strings.Fields(out)
	if len(names) == 0 {
		return ""
	}
	url, ok := gitOutput(cwd, "remote", "get-url", names[0])
	if !ok {
		return ""
	}
	return url
}

// gitOutput runs git with args in cwd and returns its trimmed stdout. ok is
// false on any non-zero exit (typically: cwd is not a git working tree).
func gitOutput(cwd string, args ...string) (string, bool) {
	cmd := exec.Command("git", args...) //nolint:gosec // args are a fixed set of literal git subcommands, never caller-controlled
	cmd.Dir = cwd
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", false
	}
	return strings.TrimSpace(out.String()), true
}

// GitCommandTimeout bounds every git process the record-stamp path runs
// (HeadSHA, CommitsSince): a hung git (a wedged network mount, a lock)
// must never block a record write or a recall read (task 5615ddae).
const GitCommandTimeout = 3 * time.Second

// gitOutputBounded is gitOutput with GitCommandTimeout applied and the
// process's environment stripped of GIT_DIR / GIT_WORK_TREE / GIT_INDEX_FILE,
// which would otherwise redirect a command aimed at dir to some other repo
// when the daemon is launched from inside a git hook.
func gitOutputBounded(dir string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), GitCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // args are a fixed set of literal git subcommands plus a validated sha
	cmd.Dir = dir
	env := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_DIR=") || strings.HasPrefix(kv, "GIT_WORK_TREE=") || strings.HasPrefix(kv, "GIT_INDEX_FILE=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", false
	}
	return strings.TrimSpace(out.String()), true
}

// isFullSHA reports whether s is a 40- or 64-hex-digit object id.
func isFullSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// HeadSHA returns the full sha of HEAD in dir's repo. ok is false when dir
// is empty, not in a git repo, git is unavailable, or the repo has no
// commits yet — every one of which means "nothing to stamp", never an error.
func HeadSHA(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	sha, ok := gitOutputBounded(dir, "rev-parse", "--verify", "-q", "HEAD^{commit}")
	if !ok || !isFullSHA(sha) {
		return "", false
	}
	return sha, true
}

// CommitsSince returns `git rev-list --count <sha>..HEAD` in dir. ok is
// false with a non-empty reason when the count cannot be computed (sha not
// a valid id, dir gone or not a repo, sha unreachable from / unknown to the
// repo, git failed or timed out): the caller reports the sha without a
// count, never an error.
func CommitsSince(dir, sha string) (n int, reason string, ok bool) {
	if !isFullSHA(sha) {
		return 0, "stamped sha is malformed", false
	}
	if dir == "" {
		return 0, "project directory unknown", false
	}
	if _, ok := gitOutputBounded(dir, "rev-parse", "--git-dir"); !ok {
		return 0, "repo not available at " + dir, false
	}
	if _, ok := gitOutputBounded(dir, "cat-file", "-e", sha+"^{commit}"); !ok {
		return 0, "stamped commit no longer exists in the repo (history rewritten?)", false
	}
	// Unreachable-from-HEAD (rebased away but object still present) shows up
	// as a non-ancestor: merge-base --is-ancestor exits 1.
	if _, ok := gitOutputBounded(dir, "merge-base", "--is-ancestor", sha, "HEAD"); !ok {
		return 0, "stamped commit is not an ancestor of HEAD (history rewritten?)", false
	}
	out, ok := gitOutputBounded(dir, "rev-list", "--count", sha+"..HEAD")
	if !ok {
		return 0, "git rev-list failed", false
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		return 0, "git rev-list returned non-numeric output", false
	}
	return n, "", true
}
