package week

import (
	"path/filepath"
	"strings"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// RealWork is the named config for what counts as real agent work (task
// ade3a4c3, rule R1): a project or home label earns a This Week row only
// with a non-tombstoned record in the window or a known-harness session
// with at least one of these event kinds. session.start/end/git_state and
// shell `command` events never count on their own.
var RealWork = store.RealWorkCriteria{
	EventKinds: []string{payload.KindToolUse, payload.KindToolResult},
}

// LocationRules is the ONE named config for locations that never earn a
// This Week row, week entry or attention item (rule R2). Read-side only:
// timeline, recall and records still see everything.
type LocationRules struct {
	// Home is the user's $HOME. Empty disables the three Home rules below.
	Home string
	// ExcludeHome excludes Home itself.
	ExcludeHome bool
	// ExcludeOutsideHome excludes any path not under Home (/root, /tmp, /).
	ExcludeOutsideHome bool
	// ExcludeHomeDotDirs excludes anything under a dot-directory directly
	// inside Home (~/.claude, ~/.codex, ~/.local, ~/.config, ...).
	ExcludeHomeDotDirs bool
	// ExcludedRoots are extra directory trees never shown ($TMPDIR,
	// $XDG_RUNTIME_DIR, os.TempDir()); callers resolve them from the
	// environment, this package never reads it.
	ExcludedRoots []string
}

// DefaultLocationRules is the shipped rule set for home and the
// caller-resolved extra roots.
func DefaultLocationRules(home string, roots ...string) LocationRules {
	return LocationRules{
		Home:               home,
		ExcludeHome:        true,
		ExcludeOutsideHome: true,
		ExcludeHomeDotDirs: true,
		ExcludedRoots:      roots,
	}
}

// within reports whether path is dir or lies under it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Excluded reports whether dir is a location This Week never shows. A
// non-absolute or empty dir is never excluded (nothing to judge it by).
func (r LocationRules) Excluded(dir string) bool {
	if dir == "" || !filepath.IsAbs(dir) {
		return false
	}
	dir = filepath.Clean(dir)
	for _, root := range r.ExcludedRoots {
		if root != "" && filepath.IsAbs(root) && within(dir, filepath.Clean(root)) {
			return true
		}
	}
	if r.Home == "" {
		return false
	}
	home := filepath.Clean(r.Home)
	if dir == home {
		return r.ExcludeHome
	}
	if !within(dir, home) {
		return r.ExcludeOutsideHome
	}
	if r.ExcludeHomeDotDirs {
		rel, _ := filepath.Rel(home, dir)
		if strings.HasPrefix(strings.SplitN(rel, string(filepath.Separator), 2)[0], ".") {
			return true
		}
	}
	return false
}

// LocationScope is the ONE row-scope function (task a9a784ec): exactly the
// sessions and records This Week attributes to the row whose summary cwd is
// dir — built from the same label attribution (store.sessionLabels) the
// per-label rows, handoff resolution and week bars use. The human
// `records --location` and `purge --location` both resolve through it.
func LocationScope(st *store.Store, git project.Git, workspaces []string, dir string) (store.LocationScope, error) {
	return st.LocationScope(dir, git, workspaces)
}
