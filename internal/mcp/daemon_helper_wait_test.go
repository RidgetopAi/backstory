package mcp

import (
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
)

type fakeCleanuper struct {
	t        *testing.T
	cleanups []func()
}

func (f *fakeCleanuper) Helper()                           {}
func (f *fakeCleanuper) Fatalf(format string, args ...any) { f.t.Fatalf(format, args...) }
func (f *fakeCleanuper) Cleanup(fn func())                 { f.cleanups = append(f.cleanups, fn) }

// TestServeTestDaemonCleanupWaitsForInFlightHandler pins that the shared
// helper's Cleanup does not return while a handler is still running.
func TestServeTestDaemonCleanupWaitsForInFlightHandler(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	fc := &fakeCleanuper{t: t}
	sockPath := filepath.Join(t.TempDir(), "sock")
	serveTestDaemon(fc, sockPath, &ident.Resolver{ProcFS: fakeProcFS{}}, func(ident.Identity, net.Conn) {
		close(started)
		<-release
	})

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

	cleaned := make(chan struct{})
	go func() {
		for _, fn := range fc.cleanups {
			fn()
		}
		close(cleaned)
	}()
	select {
	case <-cleaned:
		t.Fatal("cleanup returned while a handler was still blocked")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-cleaned:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not return after the handler was released")
	}
}
