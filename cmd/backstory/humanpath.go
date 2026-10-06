package main

import (
	"flag"
	"fmt"
	"os"

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
