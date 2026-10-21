package main

import (
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/socket"
)

// TestShutdownServerWaitsForInFlightHandler pins the daemon shutdown
// contract: runDaemon defers shutdownServer ahead of the store's Close, so it
// must not return while a connection handler is still running.
func TestShutdownServerWaitsForInFlightHandler(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	sockPath := filepath.Join(t.TempDir(), "sock")
	srv, err := socket.Listen(sockPath, &ident.Resolver{ProcFS: nextProcFS{}}, func(ident.Identity, net.Conn) {
		close(started)
		<-release
	})
	if err != nil {
		t.Fatalf("socket.Listen: %v", err)
	}
	go func() { _ = srv.Serve() }()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("{}\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never started")
	}

	done := make(chan struct{})
	go func() { shutdownServer(srv); close(done) }()
	select {
	case <-done:
		t.Fatal("shutdownServer returned while a handler was still running")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdownServer did not return after the handler finished")
	}
}
