package socket_test

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/socket"
)

// fakeProcFS is a fully synthetic /proc tree, keyed on the dialing test
// process's own real pid (SO_PEERCRED can't be faked without root, so the
// connecting side is genuinely this test binary; only what the tree says
// about that pid's ancestry is fake).
type fakeProcFS struct {
	status map[int]ident.Status
	cwd    map[int]string
}

func (f fakeProcFS) Status(pid int) (ident.Status, error) {
	st, ok := f.status[pid]
	if !ok {
		return ident.Status{}, fmt.Errorf("fakeProcFS: no status for pid %d", pid)
	}
	return st, nil
}

func (f fakeProcFS) Cwd(pid int) (string, error) {
	cwd, ok := f.cwd[pid]
	if !ok {
		return "", fmt.Errorf("fakeProcFS: no cwd for pid %d", pid)
	}
	return cwd, nil
}

func (f fakeProcFS) Cmdline(int) ([]string, error) { return nil, nil }

func TestListenCreatesSocketMode0600InDirMode0700(t *testing.T) {
	root := t.TempDir()
	sockPath := filepath.Join(root, "run", "backstory", "sock")

	resolver := &ident.Resolver{ProcFS: fakeProcFS{}}
	srv, err := socket.Listen(sockPath, resolver, func(ident.Identity, net.Conn) {})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = srv.Close() }()

	dirInfo, err := os.Stat(filepath.Dir(sockPath))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("dir mode = %04o, want 0700", got)
	}

	sockInfo, err := os.Stat(sockPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if got := sockInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("socket mode = %04o, want 0600", got)
	}
}

// TestForgedRequestFieldsNeverBecomeIdentity is the punch's acceptance
// clause 3: a client that sends session:"forged" and actor:"root" (and,
// for good measure, forged kind/uid/pid fields) still gets an Identity
// derived only from peer creds + the fake ProcFS. The forged values may
// appear in Declared and nowhere else.
func TestForgedRequestFieldsNeverBecomeIdentity(t *testing.T) {
	root := t.TempDir()
	sockPath := filepath.Join(root, "run", "sock")

	selfPID := os.Getpid()
	procfs := fakeProcFS{
		status: map[int]ident.Status{
			selfPID: {PPid: 800, Name: "shim"},
			800:     {PPid: 700, Name: "claude"},
			700:     {PPid: 600, Name: "tmux"},
			600:     {PPid: 1, Name: "alacritty"},
		},
		cwd: map[int]string{800: "/home/brian/proj"},
	}
	resolver := &ident.Resolver{
		ProcFS:     procfs,
		ProjectKey: func(cwd string) string { return "key:" + cwd },
	}

	received := make(chan ident.Identity, 1)
	srv, err := socket.Listen(sockPath, resolver, func(id ident.Identity, conn net.Conn) {
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

	req := `{"session":"forged","actor":"root","kind":"human","uid":0,"pid":1,"harness":"evil"}` + "\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var id ident.Identity
	select {
	case id = <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("handler was never called")
	}

	if id.Kind != ident.KindAgent {
		t.Errorf("Kind = %v, want KindAgent (forged 'kind':'human' must not apply)", id.Kind)
	}
	if id.UID != os.Getuid() {
		t.Errorf("UID = %d, want %d (real SO_PEERCRED uid; forged 'uid':0 must not apply)", id.UID, os.Getuid())
	}
	if id.PID != selfPID {
		t.Errorf("PID = %d, want %d (real SO_PEERCRED pid; forged 'pid':1 must not apply)", id.PID, selfPID)
	}
	if id.Harness != "claude" {
		t.Errorf("Harness = %q, want claude (forged 'harness':'evil' must not apply)", id.Harness)
	}
	if id.HarnessPID != 800 {
		t.Errorf("HarnessPID = %d, want 800", id.HarnessPID)
	}
	if id.CWD != "/home/brian/proj" {
		t.Errorf("CWD = %q, want /home/brian/proj", id.CWD)
	}
	if id.ProjectKey != "key:/home/brian/proj" {
		t.Errorf("ProjectKey = %q, want key:/home/brian/proj", id.ProjectKey)
	}

	if got := id.Declared["session"]; got != "forged" {
		t.Errorf("Declared[session] = %q, want forged", got)
	}
	if got := id.Declared["actor"]; got != "root" {
		t.Errorf("Declared[actor] = %q, want root", got)
	}
	// "kind", "uid", "pid", "harness" are not in DeclaredFields at all: they
	// must not leak into Declared either.
	for _, k := range []string{"kind", "uid", "pid", "harness"} {
		if _, ok := id.Declared[k]; ok {
			t.Errorf("Declared[%q] present, want absent (only session/actor are declared join keys)", k)
		}
	}
}

// TestHandlerSeesFullRequestLine makes sure the handler's own conn reader
// gets the request line intact, even though socket already peeked at it to
// harvest Declared fields.
func TestHandlerSeesFullRequestLine(t *testing.T) {
	root := t.TempDir()
	sockPath := filepath.Join(root, "sock")

	resolver := &ident.Resolver{ProcFS: fakeProcFS{}}

	readLine := make(chan string, 1)
	srv, err := socket.Listen(sockPath, resolver, func(_ ident.Identity, conn net.Conn) {
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 256)
		n, _ := conn.Read(buf)
		readLine <- string(buf[:n])
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

	const want = `{"method":"status"}` + "\n"
	if _, err := conn.Write([]byte(want)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	select {
	case got := <-readLine:
		if got != want {
			t.Errorf("handler read %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler never read anything")
	}
}
