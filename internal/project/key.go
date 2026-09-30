// Package project computes Backstory's project identity: a git repository,
// not a cwd string (AGENT-CONTRACT.md §Project = git repository identity).
package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// KeySeparator joined CommonDir and RemoteURL in Key's output before task
// 029485ae dropped the remote from the key; it is kept because store's
// migration and canonicalizeProjectKey must still recognise that old
// spelling. It used to be a NUL byte (0x00): `status` returns project_key over JSON, where a byte
// below 0x20 comes back as a \u0000 escape that every agent renders
// literally instead of a real separator (critic T1 on 14704ebe, task
// e7951178). "|" is printable ASCII so it needs no JSON escaping, is
// illegal in a Windows path (defense in depth for cross-platform tooling),
// and does not occur in the ssh/https URL schemes git remotes use or in a
// *nix directory path in practice.
const KeySeparator = "|"

// LegacyRemoteKeyCommonDir reports the common dir a pre-029485ae
// "<common-dir>|<remote>" key names, and whether key has that shape.
func LegacyRemoteKeyCommonDir(key string) (string, bool) {
	if IsWorkspaceKey(key) {
		return "", false
	}
	dir, _, ok := strings.Cut(key, KeySeparator)
	if !ok || dir == "" {
		return "", false
	}
	return dir, true
}

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
//   - inside a git working tree: the canonical git common dir ALONE (so a
//     worktree and its main checkout collapse to the same key, and two
//     clones of one remote at different paths stay two projects). The
//     remote is deliberately NOT part of the key: `git remote add`,
//     `set-url` and removal must never change a repo's identity (task
//     029485ae). This applies even when the repo lives under a workspace
//     dir (decision bcc9fa54): a real repo is always a project, never
//     swallowed into its parent workspace.
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
	return repo.CommonDir
}

// WorkspaceHome reports the workspace key that covers cwd — cwd itself, or
// a descendant of it AT ANY DEPTH, inside one of workspaces (decision
// f3fa04c7's HOME rule) — the containment check a kind=handoff record's
// project_key and every home-scoped read (freshness, labels, resume) apply.
// This is deliberately broader than workspaceOf's "workspace dir or a
// DIRECT child" (Key's own frozen rule, decision bcc9fa54): a repo three
// levels under a workspace dir is still "in the workspace" for HOME
// purposes even though Key() gives that repo its own key whenever
// git.Repo succeeds for it — WorkspaceHome never consults git at all, so it
// answers the same regardless of whether cwd is itself a git repo.
func WorkspaceHome(cwd string, workspaces []string) (string, bool) {
	cwd = filepath.Clean(cwd)
	for _, w := range workspaces {
		w = filepath.Clean(w)
		if cwd == w {
			return workspaceKeyPrefix + w, true
		}
		rel, err := filepath.Rel(w, cwd)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return workspaceKeyPrefix + w, true
	}
	return "", false
}

// WorkspaceRelativeName is dir's human-facing display label (decision
// f3fa04c7's LABELS rule): "<workspace-basename>/<relative-path>" when dir
// is inside (or is) one of workspaces — e.g. "projects/omarcade" for dir
// ".../projects/omarcade" and workspace ".../projects" — else dir's own
// basename, the pre-existing display-name form (week.displayName's old
// filepath.Base(toplevel) fallback), so applying this everywhere that used
// to call filepath.Base is a strict superset: a dir outside every
// configured workspace renders exactly as it always did.
func WorkspaceRelativeName(dir string, workspaces []string) string {
	dir = filepath.Clean(dir)
	for _, w := range workspaces {
		w = filepath.Clean(w)
		if dir == w {
			return filepath.Base(w)
		}
		rel, err := filepath.Rel(w, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return filepath.Base(w) + "/" + rel
	}
	return filepath.Base(dir)
}

// Label is dir's work-location label (decision f3fa04c7's LABELS rule): the
// git repo containing dir, resolved to its toplevel and made
// workspace-relative (WorkspaceRelativeName), when dir is inside a git
// working tree; else dir itself, made workspace-relative. Never consults
// about[] or any record text — LABELS is derived purely from where a path
// observably lives on disk.
func Label(dir string, git Git, workspaces []string) string {
	if repo, ok := git.Repo(dir); ok {
		return WorkspaceRelativeName(repo.Toplevel, workspaces)
	}
	return WorkspaceRelativeName(dir, workspaces)
}

// LabelForPath is Label applied to the directory containing path — LABELS'
// per-touched-file resolution (decision f3fa04c7): a session's work
// locations are derived from the file paths its file-touching timeline
// events carry, each resolved to the repo/folder that contains it.
func LabelForPath(path string, git Git, workspaces []string) string {
	return Label(filepath.Dir(path), git, workspaces)
}

// IsWorkspaceKey reports whether key is a workspace identity (Key's
// workspaceKeyPrefix result) rather than a real repo key — the check This
// Week's project list (internal/week) and any other caller enumerating
// project keys applies so a workspace never renders as a project (decision
// bcc9fa54, AGENT-CONTRACT.md §Project = git repository identity).
func IsWorkspaceKey(key string) bool {
	return strings.HasPrefix(key, workspaceKeyPrefix)
}

// WorkspaceDirOf reports the directory a workspace-prefixed key names — key
// with workspaceKeyPrefix stripped — and whether key is a workspace key at
// all. internal/mcp's home-project bookkeeping uses this to recover the
// directory a handoff's (always workspace-prefixed) home key names.
func WorkspaceDirOf(key string) (string, bool) {
	if !IsWorkspaceKey(key) {
		return "", false
	}
	return strings.TrimPrefix(key, workspaceKeyPrefix), true
}
