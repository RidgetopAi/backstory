package socket_test

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/socket"
)

// TestIdleClientNeverReachesHandler is the punch's acceptance clause 1's
// idle-client case: a peer that connects and sends nothing is disconnected
// after socket.FirstLineDeadline, and the Handler is never invoked. RED if
// the read deadline is removed (the goroutine, and this test, would hang).
func TestIdleClientNeverReachesHandler(t *testing.T) {
	root := t.TempDir()
	sockPath := filepath.Join(root, "sock")

	handlerCalled := make(chan struct{}, 1)
	srv, err := socket.Listen(sockPath, &ident.Resolver{ProcFS: fakeProcFS{}}, func(ident.Identity, net.Conn) {
		handlerCalled <- struct{}{}
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = srv.Close() }()
	go func() { _ = srv.Serve() }()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Give plenty of slack over FirstLineDeadline: this asserts disconnect
	// eventually happens and the handler never ran, not exact timing.
	deadline := time.Now().Add(socket.FirstLineDeadline + 5*time.Second)
	if err := conn.SetReadDeadline(deadline); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}

	buf := make([]byte, 1)
	n, readErr := conn.Read(buf)
	if readErr == nil {
		t.Fatalf("Read: got %d bytes, want the server to close the connection", n)
	}
	if readErr != nil && !isEOFOrClosed(readErr) {
		t.Fatalf("Read: got %v, want EOF/closed (server disconnect), not our own test deadline", readErr)
	}

	select {
	case <-handlerCalled:
		t.Fatal("handler was invoked for an idle client that never sent a line")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestOverLongLineDisconnectsBeforeCap is the punch's acceptance clause 1's
// long-line case: a client that streams a line longer than
// socket.MaxLineLength without '\n' is disconnected before the cap is
// exceeded by more than one buffered read, and the Handler is never called.
// RED if the line-size cap is removed: with no cap, this specific client
// (which writes its whole over-long line up front and then stops, without
// ever sending '\n') is only ever disconnected once socket.FirstLineDeadline
// itself expires, not promptly — so this test's tight deadline-independent
// bound catches that regression instead of quietly passing for the wrong
// reason (the idle-client test above already covers the deadline itself).
func TestOverLongLineDisconnectsBeforeCap(t *testing.T) {
	if socket.FirstLineDeadline < 3*time.Second {
		t.Fatalf("FirstLineDeadline = %s, too short for this test's cap-vs-deadline margin", socket.FirstLineDeadline)
	}

	root := t.TempDir()
	sockPath := filepath.Join(root, "sock")

	handlerCalled := make(chan struct{}, 1)
	srv, err := socket.Listen(sockPath, &ident.Resolver{ProcFS: fakeProcFS{}}, func(ident.Identity, net.Conn) {
		handlerCalled <- struct{}{}
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = srv.Close() }()
	go func() { _ = srv.Serve() }()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetDeadline(time.Now().Add(socket.FirstLineDeadline + 5*time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}

	disconnected := make(chan struct{})
	go func() {
		// One line, well past MaxLineLength, with no '\n' anywhere in it,
		// followed by a read: whichever notices the server closed first.
		payload := []byte(strings.Repeat("x", socket.MaxLineLength+256*1024))
		if _, err := conn.Write(payload); err != nil {
			close(disconnected)
			return
		}
		buf := make([]byte, 1)
		if _, err := conn.Read(buf); err != nil {
			close(disconnected)
		}
	}()

	// A cap enforced mid-stream must close the connection promptly, well
	// under FirstLineDeadline — not merely "eventually" via the deadline.
	const capBudget = 2 * time.Second
	select {
	case <-disconnected:
	case <-time.After(capBudget):
		t.Fatalf("server did not disconnect the over-long line within %s of the cap (%d bytes); "+
			"it looks like only the %s deadline would have caught this, not MaxLineLength",
			capBudget, socket.MaxLineLength, socket.FirstLineDeadline)
	}

	select {
	case <-handlerCalled:
		t.Fatal("handler was invoked for a line exceeding MaxLineLength")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestWellFormedLineReachesHandler is the control: a line under both the
// deadline and the size cap reaches the Handler unchanged.
func TestWellFormedLineReachesHandler(t *testing.T) {
	root := t.TempDir()
	sockPath := filepath.Join(root, "sock")

	received := make(chan ident.Identity, 1)
	srv, err := socket.Listen(sockPath, &ident.Resolver{ProcFS: fakeProcFS{}}, func(id ident.Identity, conn net.Conn) {
		defer func() { _ = conn.Close() }()
		received <- id
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = srv.Close() }()
	go func() { _ = srv.Serve() }()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write([]byte(`{"method":"status"}` + "\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("handler was never called for a well-formed line")
	}
}

func isEOFOrClosed(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "closed") ||
		strings.Contains(msg, "reset") ||
		strings.Contains(msg, "broken pipe")
}
