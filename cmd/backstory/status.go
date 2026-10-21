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
	if result.DeviceID != "" {
		_, _ = fmt.Fprintf(stdout, "device: %s (%s)\n", result.DeviceName, shortDeviceID(result.DeviceID))
	}
	printSyncLines(stdout, stderr)
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

// shortDeviceID is the first 8 characters of a device id, for display.
func shortDeviceID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// printSyncLines prints one `sync:` line per paired device: its name, when it
// last synced ok and the result of the latest attempt. A store that cannot be
// read is reported on stderr and does not fail status.
func printSyncLines(stdout, stderr io.Writer) {
	dbPath, err := storePath()
	if err != nil {
		return
	}
	if _, err := os.Stat(dbPath); err != nil {
		return
	}
	st, err := openStore(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory status: sync:", err)
		return
	}
	defer func() { _ = st.Close() }()
	rows, err := st.SyncStatus()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory status: sync:", err)
		return
	}
	for _, r := range rows {
		lastOK := "never"
		if r.HasOK {
			lastOK = r.LastOK.Local().Format("2006-01-02 15:04")
		}
		last := "no attempt yet"
		if r.HasAttempt {
			last = r.Last.Result
			if r.Last.Detail != "" {
				last += " (" + r.Last.Detail + ")"
			}
		}
		if !r.Peer.Enabled {
			last = "removed"
		}
		_, _ = fmt.Fprintf(stdout, "sync: %s: last ok %s, last result %s\n", r.Peer.Label(), lastOK, last)
	}
}
