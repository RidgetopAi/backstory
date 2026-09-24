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

// State is a git working tree's observed state at a point in time: the
// current branch and the count of uncommitted/untracked files (`git status
// --porcelain`).
type State struct {
	Branch      string
	Uncommitted int
}

// Git resolves git facts for a working directory. Tests inject a fake; the
// production implementation is RealGit (git.go).
type Git interface {
	// Repo reports the git identity facts for cwd. ok is false when cwd is
	// not inside a git working tree at all.
	Repo(cwd string) (Repo, bool)
	// State reports cwd's current branch and uncommitted/untracked file
	// count. ok is false when cwd is not inside a git working tree, or a git
	// command itself failed — a caller must never fall back to Uncommitted
	// == 0 in that case (SCHEMA.md invariant 7: could-not-observe is a
	// value, never folded into a false/zero reading).
	State(cwd string) (State, bool)
}

// keySeparator joins CommonDir and RemoteURL in Key's output. It used to be
// a NUL byte (0x00): `status` returns project_key over JSON, where a byte
// below 0x20 comes back as a \u0000 escape that every agent renders
// literally instead of a real separator (critic T1 on 14704ebe, task
// e7951178). "|" is printable ASCII so it needs no JSON escaping, is
// illegal in a Windows path (defense in depth for cross-platform tooling),
// and does not occur in the ssh/https URL schemes git remotes use or in a
// *nix directory path in practice.
const keySeparator = "|"

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
		return repo.CommonDir + keySeparator + repo.RemoteURL
	}
	return repo.Toplevel
}
