package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"

	"github.com/RidgetopAi/backstory/internal/mcp"
)

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

	project := result.ProjectKey
	if project == "" {
		project = "(none)"
	}
	captureState := "on"
	if !result.CaptureOn {
		captureState = "off"
	}
	_, _ = fmt.Fprintf(stdout, "project: %s\ncapture: %s\nbudget remaining: %d\n",
		project, captureState, result.RemainingBudget)
	return 0
}
