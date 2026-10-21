package socket_test

import (
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/socket"
)

// TestWaitCoversConnectionsAcceptedBeforeTheirHandlerRuns pins the contract
// that fixes the internal/mcp cleanup flake: a connection accepted before
// Close is counted before its goroutine starts, so Wait cannot return while
// its Handler has yet to run (the peer's first line has not even arrived).
func TestWaitCoversConnectionsAcceptedBeforeTheirHandlerRuns(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "sock")
	var handled atomic.Bool
	srv, err := socket.Listen(sockPath, &ident.Resolver{ProcFS: fakeProcFS{}}, func(ident.Identity, net.Conn) {
		handled.Store(true)
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	served := make(chan struct{})
	go func() { _ = srv.Serve(); close(served) }()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	// Let Serve accept the connection; its handle is now parked on the first line.
	time.Sleep(100 * time.Millisecond)

	_ = srv.Close()
	<-served
	waited := make(chan struct{})
	go func() { srv.Wait(); close(waited) }()

	select {
	case <-waited:
		t.Fatal("Wait returned while an accepted connection's handler had not run")
	case <-time.After(150 * time.Millisecond):
	}
	if _, err := conn.Write([]byte("{}\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait never returned after the handler could finish")
	}
	if !handled.Load() {
		t.Error("Wait returned before the handler ran")
	}
}
