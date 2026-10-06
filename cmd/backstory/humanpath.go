package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// resolveHumanProjectKey resolves the project key for a human-path,
// read-only command (recall, timeline): projectFlag verbatim when given
// (--project), else the git repo of the current directory — PLAN.md §Phase
// 4 CLI's "default project = the repo of the current directory", the same
// resolution --here makes explicit. Both commands open the store directly
// and never dial the daemon socket (decision d9d456e7): unlike the daemon's
// own SO_PEERCRED + /proc identity resolution, there is no live connection
// here to observe a project from, so the human path computes it the same
// way the daemon does (internal/project.Key off the caller's own cwd) but
// lets a human name a different one explicitly.
func resolveHumanProjectKey(projectFlag string) (string, error) {
	if projectFlag != "" {
		return projectFlag, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current directory: %w", err)
	}
	// Same workspace rule as the daemon (decision bcc9fa54); no resolvable
	// home dir just means no default workspace.
	workspaces, _ := project.DefaultWorkspaceDirs()
	return project.Key(cwd, project.RealGit{}, workspaces), nil
}

// resolveHumanScope is resolveHumanProjectKey plus the location scope the
// command's records are read through (task ed31b744): --project KEY reads
// the key's records and those its sessions wrote (a repo session's handoff
// is filed under the workspace key); with no flag, the current directory's
// location, so --here sees the workspace-homed handoffs of exactly this
// repo.
func resolveHumanScope(st *store.Store, projectFlag string) (string, store.LocationScope, error) {
	if projectFlag != "" {
		ls, err := st.LocationScopeForKey(projectFlag)
		return projectFlag, ls, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", store.LocationScope{}, fmt.Errorf("resolve current directory: %w", err)
	}
	workspaces, _ := project.DefaultWorkspaceDirs()
	ls, err := st.LocationScope(cwd, project.RealGit{}, workspaces)
	return project.Key(cwd, project.RealGit{}, workspaces), ls, err
}

// locationGiven reports whether --location was passed at all, even empty: an
// empty value must be refused, never silently widen to the whole project
// key (task a9a784ec).
func locationGiven(fs *flag.FlagSet) bool {
	given := false
	fs.Visit(func(f *flag.Flag) { given = given || f.Name == "location" })
	return given
}

// resolveProjectRef resolves a human-supplied --project value to the project
// key it names: a known key as-is, else a directory (resolved through the
// same location resolver as `records --here`, project.Key) whose key is a
// known project. A value that resolves to no project is an error naming
// everything tried, never a silent empty scope (task 149d6cd4).
func resolveProjectRef(st *store.Store, ref string) (string, error) {
	if _, ok, err := st.GetProject(ref); err != nil {
		return "", err
	} else if ok {
		return ref, nil
	}
	dir := ref
	if abs, err := filepath.Abs(ref); err == nil {
		if info, serr := os.Stat(abs); serr == nil && info.IsDir() {
			dir = abs
		}
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("--project %q is not a known project key and not an existing directory", ref)
	}
	workspaces, _ := project.DefaultWorkspaceDirs()
	key := project.Key(filepath.Clean(dir), project.RealGit{}, workspaces)
	if _, ok, err := st.GetProject(key); err != nil {
		return "", err
	} else if !ok {
		return "", fmt.Errorf("--project %q resolves to no project (tried as a project key, then as a directory -> key %q)", ref, key)
	}
	return key, nil
}
