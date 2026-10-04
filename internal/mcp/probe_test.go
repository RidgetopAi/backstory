package mcp

import (
	"bufio"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/store"
)

const probeWait = 5 * time.Second

func sessionRowCount(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

// serveProbeConn runs ServeDaemonConn for an unidentified-harness peer over
// an in-memory pipe, returning the client end and a channel closed when the
// daemon side has fully returned (so counts are read after all its writes).
func serveProbeConn(t *testing.T, st *store.Store) (net.Conn, <-chan struct{}) {
	t.Helper()
	client, server := net.Pipe()
	id := ident.Identity{Kind: ident.KindAgent, PID: 4242, CWD: "/home/probe/proj", ProjectKey: "/home/probe/proj"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = server.Close() }()
		ServeDaemonConn(id, server, st, fakeProcFS{}, fakeGit{}, nil, NewSessionRegistry(), captureNeverOff, nil)
	}()
	return client, done
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(probeWait):
		t.Fatal("ServeDaemonConn did not return")
	}
}

// A liveness probe (connect, close, no request line) must mint no session.
func TestConnectionClosedBeforeFirstLineMintsNoSession(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "backstory.db"), nil, nil)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	before := sessionRowCount(t, st)
	client, done := serveProbeConn(t, st)
	_ = client.Close()
	waitDone(t, done)
	if after := sessionRowCount(t, st); after != before {
		t.Fatalf("sessions rows = %d after a probe connection, want %d", after, before)
	}
}

// Control: a client that sends one request still gets exactly one session.
func TestConnectionWithOneRequestMintsExactlyOneSession(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "backstory.db"), nil, nil)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	before := sessionRowCount(t, st)
	client, done := serveProbeConn(t, st)
	b, _ := json.Marshal(DaemonRequest{Method: daemonMethodStatus})
	if _, err := client.Write(append(b, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := bufio.NewReader(client).ReadBytes('\n'); err != nil {
		t.Fatalf("read response: %v", err)
	}
	_ = client.Close()
	waitDone(t, done)
	if after := sessionRowCount(t, st); after != before+1 {
		t.Fatalf("sessions rows = %d, want %d", after, before+1)
	}
}
