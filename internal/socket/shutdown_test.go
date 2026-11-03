package socket_test

import (
	"bufio"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/socket"
)

// TestCloseAndWaitReleaseIdleClient pins the daemon-stop fix: a client that
// stays connected and idle (handler blocked reading its next line) must not
// keep Close + Wait from returning past the shutdown grace, and the client
// must then see a closed connection rather than hang.
func TestCloseAndWaitReleaseIdleClient(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "sock")
	handlerDone := make(chan struct{})
	srv, err := socket.Listen(sockPath, &ident.Resolver{ProcFS: fakeProcFS{}}, func(_ ident.Identity, conn net.Conn) {
		defer close(handlerDone)
		br := bufio.NewReader(conn)
		for {
			if _, err := br.ReadString('\n'); err != nil {
				return
			}
		}
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv.ShutdownGrace = 200 * time.Millisecond
	go func() { _ = srv.Serve() }()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("{}\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // handler is now idle on its next line

	stopped := make(chan struct{})
	go func() { _ = srv.Close(); srv.Wait(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(srv.ShutdownGrace + time.Second):
		t.Fatal("Close+Wait did not return within the grace period plus 1s with an idle client connected")
	}
	select {
	case <-handlerDone:
	default:
		t.Fatal("Wait returned before the idle handler returned")
	}

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("client read succeeded, want EOF or closed-connection error")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("client read hung instead of seeing a closed connection: %v", err)
	}
}
