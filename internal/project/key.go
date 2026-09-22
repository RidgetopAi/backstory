// Package project computes Backstory's project identity: a git repository,
// not a cwd string (AGENT-CONTRACT.md §Project = git repository identity).
package project

// Repo is the git identity facts for a working directory.
type Repo struct {
	// CommonDir is `git rev-parse --git-common-dir`, worktree-safe: a
	// worktree and its main checkout share this path.
	CommonDir string
	// RemoteURL is the first configured remote's URL, or "" if the repo has
	// none configured.
	RemoteURL string
	// Toplevel is `git rev-parse --show-toplevel`, the fallback identity
	// when the repo has no remote.
	Toplevel string
}

// Git resolves git facts for a working directory. Tests inject a fake; the
// production implementation is RealGit (git.go).
type Git interface {
	// Repo reports the git identity facts for cwd. ok is false when cwd is
	// not inside a git working tree at all.
	Repo(cwd string) (Repo, bool)
}

// Key computes cwd's project key:
//   - inside a git working tree with a configured remote: the common dir
//     plus the first remote URL, so a worktree and its main checkout (same
//     common dir, same remote) collapse to the same key;
//   - inside a git working tree with no remote: the toplevel path;
//   - not inside a git working tree at all: cwd itself.
//
// cwd stays a fact on the session/event; Key is what project-scoped records
// carry (AGENT-CONTRACT.md §Project = git repository identity).
func Key(cwd string, git Git) string {
	repo, ok := git.Repo(cwd)
	if !ok {
		return cwd
	}
	if repo.RemoteURL != "" {
		return repo.CommonDir + "\x00" + repo.RemoteURL
	}
	return repo.Toplevel
}
