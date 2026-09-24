package main

import (
	"fmt"
	"os"

	"github.com/RidgetopAi/backstory/internal/project"
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
	return project.Key(cwd, project.RealGit{}), nil
}
