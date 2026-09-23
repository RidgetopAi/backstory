package main

import (
	"fmt"
	"io"
	"net"

	"github.com/RidgetopAi/backstory/internal/mcp"
)

// runMCP is the `backstory mcp` subcommand: the stdio<->socket shim. A
// harness launches this process at session start and the first tool call
// can arrive minutes later, so it must NOT dial the daemon socket at
// startup: an idle dialed connection sits past the daemon's
// FirstLineDeadline and is dead by the time it's finally used (task
// 40008eea). Instead it hands mcp.NewServer a Dialer that dials on the
// shim's first daemon-backed tool call and speaks JSON-RPC 2.0 over
// stdin/stdout until stdin reaches EOF.
func runMCP(_ []string, stdin io.Reader, stdout, stderr io.Writer) int {
	sockPath, err := socketPath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory mcp:", err)
		return 1
	}

	srv := mcp.NewServer(func() (net.Conn, error) { return net.Dial("unix", sockPath) })
	defer func() { _ = srv.Close() }()

	if err := srv.Serve(stdin, stdout); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory mcp:", err)
		return 1
	}
	return 0
}
