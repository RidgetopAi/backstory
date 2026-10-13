package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/RidgetopAi/backstory/internal/mcp"
	"github.com/RidgetopAi/backstory/internal/project"
)

// noSessionEnv, when set, makes the SessionStart hook ask the daemon not to
// record a session. `backstory status` never records one regardless.
const noSessionEnv = "BACKSTORY_NO_SESSION"

// runStatus is the `backstory status` subcommand: it connects to the daemon,
// calls the status tool, and prints the caller's project, capture state and
// budget. Before this there was no supported health check at all, which is
// exactly what made a live daemon look dead while diagnosing task 40008eea's
// broken-pipe bug — this fold-in gives an operator a direct way to ask the
// daemon "are you actually there" without going through a harness.
func runStatus(_ []string, stdout, stderr io.Writer) int {
	sockPath, err := socketPath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory status:", err)
		return 1
	}

	var dialErr error
	srv := mcp.NewServer(func() (net.Conn, error) {
		conn, dErr := net.Dial("unix", sockPath)
		dialErr = dErr
		return conn, dErr
	})
	defer func() { _ = srv.Close() }()
	// A read-only CLI call never mints a session; noSessionEnv is kept for
	// callers that set it and is now redundant here.
	srv.WithoutSession()

	raw, rerr := srv.CallTool(mcp.ToolStatus, nil)
	if rerr != nil {
		if dialErr != nil {
			_, _ = fmt.Fprintf(stderr, "backstory status: daemon unreachable at %s: %v\n", sockPath, dialErr)
		} else {
			_, _ = fmt.Fprintln(stderr, "backstory status:", rerr)
		}
		return 1
	}

	var result mcp.StatusResult
	if err := json.Unmarshal(raw, &result); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory status: decode result:", err)
		return 1
	}

	projectName := result.ProjectKey
	if projectName == "" {
		projectName = "(none)"
	}
	captureState := "on"
	if !result.CaptureOn {
		captureState = "off"
	}
	_, _ = fmt.Fprintf(stdout, "project: %s\ncapture: %s\nbudget remaining: %d\n",
		projectName, captureState, result.RemainingBudget)
	if cwd, err := os.Getwd(); err == nil {
		if note := outsideProjectRootsNote(cwd, project.DefaultProjectRoots()); note != "" {
			_, _ = fmt.Fprintln(stdout, note)
		}
	}
	return 0
}

// outsideProjectRootsNote is the status line for a folder that
// BACKSTORY_PROJECT_ROOTS scopes out of the panel and This Week: "" when no
// roots are set or the folder's repo (or the folder itself, outside git) is
// in scope. It is a display-side note only: capture and recall for the
// folder work as always.
func outsideProjectRootsNote(cwd string, roots []string) string {
	if len(roots) == 0 {
		return ""
	}
	dir := cwd
	if repo, ok := (project.RealGit{}).Repo(cwd); ok && repo.Toplevel != "" {
		dir = repo.Toplevel
	}
	if project.InRoots(roots, dir) {
		return ""
	}
	return fmt.Sprintf("scope: %s is outside %s; it is not shown as a project in the panel or This Week (capture and recall still work here)",
		dir, project.ProjectRootsEnvVar)
}
