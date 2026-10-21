package sync

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// SSHCommandEnv overrides the transport's command prefix. The value is split
// on whitespace and run as `<prefix...> <host> backstory sync-serve`; tests
// point it at a script that runs the peer as a local subprocess.
const SSHCommandEnv = "BACKSTORY_SYNC_SSH"

// RemoteCommand is what runs on the peer.
var RemoteCommand = []string{"backstory", "sync-serve"}

// DefaultSSHCommand is the transport's default prefix. BatchMode makes ssh
// fail instead of prompting for a password, so a sync never hangs; the connect
// timeout bounds an unreachable host.
var DefaultSSHCommand = []string{"ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10"}

// FinishGrace is how long Finish waits for the peer process to exit after its
// stdin is closed before killing it.
const FinishGrace = 10 * time.Second

// stderrKeep bounds the peer's stderr we keep for classification.
const stderrKeep = 4096

// Transport starts the peer's sync-serve over ssh (or its override).
type Transport struct {
	// Command is the prefix before the host; empty means DefaultSSHCommand.
	Command []string
}

// TransportFromEnv builds the transport from BACKSTORY_SYNC_SSH.
func TransportFromEnv() Transport {
	return Transport{Command: strings.Fields(os.Getenv(SSHCommandEnv))}
}

// ValidHost reports whether host may be passed to ssh: non-empty, no
// whitespace, and not an option.
func ValidHost(host string) error {
	switch {
	case host == "":
		return errors.New("empty ssh host")
	case strings.HasPrefix(host, "-"):
		return fmt.Errorf("%q is not an ssh host", host)
	case strings.ContainsAny(host, " \t\r\n"):
		return fmt.Errorf("%q is not an ssh host", host)
	}
	return nil
}

// Session is one running peer process.
type Session struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr *tailBuffer
	once   sync.Once
	exit   Exit
}

// Reader is the peer's stdout.
func (s *Session) Reader() io.Reader { return s.stdout }

// Writer is the peer's stdin.
func (s *Session) Writer() io.Writer { return s.stdin }

// Exit is how the peer process ended.
type Exit struct {
	// Code is the exit code; -1 when the process was killed or never ran.
	Code   int
	Stderr string
}

// Open starts the peer process for host.
func (t Transport) Open(host string) (*Session, error) {
	if err := ValidHost(host); err != nil {
		return nil, err
	}
	argv := t.Command
	if len(argv) == 0 {
		argv = DefaultSSHCommand
	}
	args := append(append(append([]string{}, argv[1:]...), host), RemoteCommand...)
	cmd := exec.Command(argv[0], args...)
	tail := &tailBuffer{max: stderrKeep}
	cmd.Stderr = tail
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cannot run %s: %w", argv[0], err)
	}
	return &Session{cmd: cmd, stdin: stdin, stdout: stdout, stderr: tail}, nil
}

// Finish closes the peer's stdin, waits for it to exit (killing it after
// FinishGrace) and reports how it ended. It is safe to call more than once.
func (s *Session) Finish() Exit {
	s.once.Do(func() {
		_ = s.stdin.Close()
		done := make(chan error, 1)
		go func() { done <- s.cmd.Wait() }()
		var err error
		killed := false
		select {
		case err = <-done:
		case <-time.After(FinishGrace):
			_ = s.cmd.Process.Kill()
			killed = true
			err = <-done
		}
		s.exit = Exit{Code: 0, Stderr: strings.TrimSpace(s.stderr.String())}
		var ee *exec.ExitError
		switch {
		case killed:
			s.exit.Code = -1
		case errors.As(err, &ee):
			s.exit.Code = ee.ExitCode()
		case err != nil:
			s.exit.Code = -1
		}
	})
	return s.exit
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf bytes.Buffer
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf.Write(p)
	if over := t.buf.Len() - t.max; over > 0 {
		t.buf.Next(over)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String()
}

// connectionFailure matches what ssh prints when it never got a session.
var connectionFailure = regexp.MustCompile(`(?i)connection (refused|timed out|reset|closed)|timed out|could not resolve hostname|no route to host|network is unreachable|name or service not known|temporary failure in name resolution|host is down|operation timed out`)

// ClassifyExit turns a peer process's non-zero exit into a result and a
// one-line reason. unreachable is only a connection failure: ssh exit 255
// with a connection, timeout or DNS message. Everything else (auth refused,
// remote backstory missing or without sync-serve, any other exit) is error.
func ClassifyExit(e Exit) (result, detail string) {
	line := lastLine(e.Stderr)
	switch {
	case e.Code == 255 && connectionFailure.MatchString(e.Stderr):
		return "unreachable", orDefault(line, "connection failed")
	case strings.Contains(e.Stderr, "unknown command") || strings.Contains(e.Stderr, "unknown subcommand"):
		return "error", fmt.Sprintf("remote backstory has no sync-serve, so it has no sync (proto 0 vs %d)", ProtoVersion)
	case e.Code == 127 || strings.Contains(e.Stderr, "command not found") || strings.Contains(e.Stderr, "No such file or directory"):
		return "error", "remote has no backstory installed (command not found)"
	case e.Code == 255:
		return "error", orDefault(line, "ssh failed (exit 255)")
	case e.Code < 0:
		return "error", "peer process was killed"
	}
	if line != "" {
		return "error", fmt.Sprintf("peer exited %d: %s", e.Code, line)
	}
	return "error", fmt.Sprintf("peer exited %d", e.Code)
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(s)
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
