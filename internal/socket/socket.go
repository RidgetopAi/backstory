// Package socket listens on a unix socket and dispatches each accepted
// connection to a Handler with an Identity resolved purely from SO_PEERCRED
// plus /proc ancestry. A caller's own request never influences that
// Identity (AGENT-CONTRACT.md §Observed identity — never declared); at most
// a request's recognized join-key fields land in Identity.Declared.
package socket

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
)

// DirMode and FileMode are the required permissions for the socket's parent
// directory and the socket file itself.
const (
	DirMode  os.FileMode = 0o700
	FileMode os.FileMode = 0o600
)

// DeclaredFields lists the request's top-level string fields that land in
// Identity.Declared as join keys. Named config, not a literal check buried
// in handle: AGENT-CONTRACT.md §Observed identity calls out "session" (a
// harness's session_id, a join key) and "actor" (a subagent's declared
// label) by name. Neither ever becomes part of the observed identity.
var DeclaredFields = []string{"session", "actor"}

// Handler is called once per accepted connection, after Identity has been
// resolved from SO_PEERCRED plus /proc ancestry and Declared has been filled
// from the connection's first request line. conn is still positioned at the
// start of that same first line, so the handler's own line-delimited JSON
// loop sees every byte the client sent, including the line socket already
// peeked at.
type Handler func(id ident.Identity, conn net.Conn)

// Server listens on a unix socket and dispatches accepted connections.
type Server struct {
	ln       net.Listener
	resolver *ident.Resolver
	handler  Handler
}

// Listen creates (or replaces) a unix socket at path, mode FileMode inside a
// mode DirMode parent directory, and returns a Server ready to Serve.
func Listen(path string, resolver *ident.Resolver, handler Handler) (*Server, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, DirMode); err != nil {
		return nil, fmt.Errorf("socket: create dir %s: %w", dir, err)
	}
	if err := os.Chmod(dir, DirMode); err != nil {
		return nil, fmt.Errorf("socket: chmod dir %s: %w", dir, err)
	}
	// A stale socket file left by a crashed daemon blocks bind; remove it.
	// The daemon owns its $XDG_RUNTIME_DIR exclusively, so a second live
	// daemon racing this Listen is out of scope for this punch.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("socket: remove stale socket %s: %w", path, err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("socket: listen %s: %w", path, err)
	}
	if err := os.Chmod(path, FileMode); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("socket: chmod %s: %w", path, err)
	}
	return &Server{ln: ln, resolver: resolver, handler: handler}, nil
}

// Addr returns the socket's filesystem path.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Close stops accepting connections and removes the socket file.
func (s *Server) Close() error {
	if err := s.ln.Close(); err != nil {
		return fmt.Errorf("socket: close: %w", err)
	}
	return nil
}

// Serve accepts connections until the listener is closed, dispatching each
// to the Server's Handler in its own goroutine. It returns nil when Close
// caused the shutdown, and the Accept error otherwise.
func (s *Server) Serve() error {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("socket: accept: %w", err)
		}
		go s.handle(conn)
	}
}

// handle resolves the connecting peer's Identity and hands (Identity, conn)
// to the Server's Handler.
func (s *Server) handle(conn net.Conn) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		return
	}
	uid, pid, err := peerCreds(uc)
	if err != nil {
		_ = conn.Close()
		return
	}
	id := s.resolver.Resolve(ident.PeerCreds{UID: uid, PID: pid})

	if err := conn.SetReadDeadline(time.Now().Add(FirstLineDeadline)); err != nil {
		_ = conn.Close()
		return
	}
	br := bufio.NewReader(conn)
	line, cutOff := readFirstLine(br)
	if cutOff != "" {
		log.Printf("socket: closing connection from pid %d: %s", pid, cutOff)
		_ = conn.Close()
		return
	}
	// Handler manages its own read timing from here; the first-line deadline
	// must not linger and cut off a slower, legitimate later read.
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return
	}
	id.Declared = declaredFields(line)

	s.handler(id, &prefixConn{Conn: conn, r: io.MultiReader(bytes.NewReader(line), br)})
}

// readFirstLine reads a single '\n'-terminated line from br, one buffered
// read at a time, so that a line's length is checked against MaxLineLength
// as soon as it grows past the cap rather than after it has already been
// read into memory in full. It returns a non-empty cutOff reason instead of
// a line when the peer's first-line deadline expires or the cap is
// exceeded; both cases leave line nil so the caller never invokes the
// Handler with a partial or oversized line. A peer that closes the
// connection before sending '\n' is not a cutOff: its partial line (best
// effort) is returned so the Handler still sees whatever bytes arrived.
func readFirstLine(br *bufio.Reader) (line []byte, cutOff string) {
	for {
		chunk, err := br.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > MaxLineLength {
			return nil, fmt.Sprintf("first line exceeded %d bytes without '\\n'", MaxLineLength)
		}
		if err == nil {
			return line, ""
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, fmt.Sprintf("no complete first line within %s", FirstLineDeadline)
		}
		return line, "" // premature close or other read error: best-effort partial line
	}
}

// declaredFields extracts DeclaredFields' string values from a request
// line's top-level JSON object. A line that isn't a JSON object, or is
// missing a field, simply omits that field — it never falls back to reading
// anything else as identity.
func declaredFields(line []byte) map[string]string {
	declared := map[string]string{}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(line), &raw); err != nil {
		return declared
	}
	for _, key := range DeclaredFields {
		v, ok := raw[key]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			continue
		}
		declared[key] = s
	}
	return declared
}

// prefixConn re-delivers the bytes handle already consumed while peeking the
// first request line, so the Handler's own reader sees the full stream from
// the beginning.
type prefixConn struct {
	net.Conn
	r io.Reader
}

func (c *prefixConn) Read(p []byte) (int, error) { return c.r.Read(p) }
