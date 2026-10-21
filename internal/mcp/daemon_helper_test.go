package mcp

import (
	"net"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/socket"
)

// testCleanuper is the slice of *testing.T serveTestDaemon needs; a fake lets
// daemon_helper_test.go run the registered cleanup on demand.
type testCleanuper interface {
	Helper()
	Fatalf(format string, args ...any)
	Cleanup(func())
}

// serveTestDaemon is the one place internal/mcp tests start a daemon socket.
// serve is the per-connection body (normally ServeDaemonConn). The socket
// package counts each connection before its handler can run, and Cleanup
// closes the listener then waits for every handler, so no handler outlives
// the test and writes into the store directory after the store closed
// (cleanups run LIFO: this one runs before the store's Close and RemoveAll).
func serveTestDaemon(t testCleanuper, sockPath string, resolver *ident.Resolver, serve func(ident.Identity, net.Conn)) {
	t.Helper()
	srv, err := socket.Listen(sockPath, resolver, func(id ident.Identity, conn net.Conn) {
		defer func() { _ = conn.Close() }()
		serve(id, conn)
	})
	if err != nil {
		t.Fatalf("socket.Listen: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close(); srv.Wait() })
	go func() { _ = srv.Serve() }()
}
