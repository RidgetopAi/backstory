package main

import (
	"fmt"
	"io"
	"net"

	"github.com/RidgetopAi/backstory/internal/mcp"
)

// runMCP is the `backstory mcp` subcommand: the stdio<->socket shim. It
// dials the daemon socket once, then speaks JSON-RPC 2.0 over stdin/stdout
// until stdin reaches EOF.
func runMCP(_ []string, stdin io.Reader, stdout, stderr io.Writer) int {
	sockPath, err := socketPath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory mcp:", err)
		return 1
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory mcp: dial daemon socket:", err)
		return 1
	}
	defer func() { _ = conn.Close() }()

	if err := mcp.NewServer(conn).Serve(stdin, stdout); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory mcp:", err)
		return 1
	}
	return 0
}
