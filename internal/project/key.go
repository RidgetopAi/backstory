// Package project computes Backstory's project identity: a git repository,
// not a cwd string (AGENT-CONTRACT.md §Project = git repository identity).
package project

import (
	"fmt"
	"os"
	"path/filepath"
)

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

// keySeparator joins CommonDir and RemoteURL in Key's output. It used to be
// a NUL byte (0x00): `status` returns project_key over JSON, where a byte
// below 0x20 comes back as a \u0000 escape that every agent renders
// literally instead of a real separator (critic T1 on 14704ebe, task
// e7951178). "|" is printable ASCII so it needs no JSON escaping, is
// illegal in a Windows path (defense in depth for cross-platform tooling),
// and does not occur in the ssh/https URL schemes git remotes use or in a
// *nix directory path in practice.
const keySeparator = "|"

// workspaceKeyPrefix marks a Key result as a workspace identity rather than
// a repo key (decision bcc9fa54): a folder that holds many repos (e.g.
// ~/projects) must not become a project of its own, and must not be
// mistaken for one downstream.
const workspaceKeyPrefix = "workspace:"

// workspaceDirsEnvVar overrides DefaultWorkspaceDirs' default
// (os.PathListSeparator-joined, same shape as PATH). Production never sets
// it; it exists so a deployment can point Backstory at a non-default
// parent-of-many-repos folder without a code change.
const workspaceDirsEnvVar = "BACKSTORY_WORKSPACE_DIRS"

// DefaultWorkspaceDirs resolves the configured workspace directories:
// $BACKSTORY_WORKSPACE_DIRS (os.PathListSeparator-joined) if set, else
// [$HOME/projects].
func DefaultWorkspaceDirs() ([]string, error) {
	if v := os.Getenv(workspaceDirsEnvVar); v != "" {
		return filepath.SplitList(v), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("project: resolve home dir: %w", err)
	}
	return []string{filepath.Join(home, "projects")}, nil
}

// workspaceOf reports whether cwd is itself one of workspaces, or a direct
// child of one, and if so which workspace dir matched. It does not consult
// git: the caller only calls it once git.Repo has already said cwd is not
// inside a working tree, or is about to override that with the workspace
// rule (a cwd inside a git repo under a workspace dir is still that repo,
// per decision bcc9fa54).
func workspaceOf(cwd string, workspaces []string) (string, bool) {
	cwd = filepath.Clean(cwd)
	parent := filepath.Dir(cwd)
	for _, w := range workspaces {
		w = filepath.Clean(w)
		if cwd == w || parent == w {
			return w, true
		}
	}
	return "", false
}

// Key computes cwd's project key:
//   - inside a git working tree: the common dir plus the first remote URL
//     when the repo has one configured (so a worktree and its main
//     checkout, which share common dir and remote, collapse to the same
//     key), else the toplevel path. This applies even when the repo lives
//     under a workspace dir (decision bcc9fa54): a real repo is always a
//     project, never swallowed into its parent workspace.
//   - not inside a git working tree, and cwd is a workspace dir or a
//     direct, non-git child of one: workspaceKeyPrefix + that workspace
//     dir. workspaces is normally DefaultWorkspaceDirs(), list-valued and
//     read from configuration, never hard-coded here.
//   - neither of the above: cwd itself.
//
// cwd stays a fact on the session/event; Key is what project-scoped records
// carry (AGENT-CONTRACT.md §Project = git repository identity).
func Key(cwd string, git Git, workspaces []string) string {
	repo, ok := git.Repo(cwd)
	if !ok {
		if ws, ok := workspaceOf(cwd, workspaces); ok {
			return workspaceKeyPrefix + ws
		}
		return cwd
	}
	if repo.RemoteURL != "" {
		return repo.CommonDir + keySeparator + repo.RemoteURL
	}
	return repo.Toplevel
}
