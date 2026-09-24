package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/RidgetopAi/backstory/internal/backfill/claude"
	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/mcp"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/socket"
	"github.com/RidgetopAi/backstory/internal/store"
)

// firstLineDeadlineEnvVar overrides socket.Server.FirstLineDeadline for this
// daemon process only — production never sets it, so the default (the
// socket package's FirstLineDeadline constant, 5s) always applies there. It
// exists purely so a hermetic test can spawn a real `backstory daemon`
// subprocess with a fast idle timeout instead of waiting out the production
// value (named config, per CONTRIBUTING.md, rather than a test build tag or
// a hardcoded short deadline).
const firstLineDeadlineEnvVar = "BACKSTORY_FIRST_LINE_DEADLINE"

// applyFirstLineDeadlineOverride reads firstLineDeadlineEnvVar and, if set,
// parses it as a time.Duration and applies it to srv.FirstLineDeadline. It
// is a no-op when the variable is unset, which is every production run.
func applyFirstLineDeadlineOverride(srv *socket.Server) error {
	raw := os.Getenv(firstLineDeadlineEnvVar)
	if raw == "" {
		return nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("parse %s=%q: %w", firstLineDeadlineEnvVar, raw, err)
	}
	srv.FirstLineDeadline = d
	return nil
}

// runDaemon opens the store, listens on the daemon socket, logs the
// identity of every connection, and exits on SIGTERM/SIGINT.
func runDaemon(_ []string, stdout, stderr io.Writer) int {
	logger := log.New(stdout, "", log.LstdFlags)

	sockPath, err := socketPath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory daemon:", err)
		return 1
	}
	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory daemon:", err)
		return 1
	}

	st, err := store.Open(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory daemon:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	go runClaudeBackfillOnce(st, logger)

	resolver := &ident.Resolver{
		ProcFS: ident.RealProcFS{},
		ProjectKey: func(cwd string) string {
			return project.Key(cwd, project.RealGit{})
		},
	}

	sessions := mcp.NewSessionRegistry()
	srv, err := socket.Listen(sockPath, resolver, func(id ident.Identity, conn net.Conn) {
		defer func() { _ = conn.Close() }()
		logIdentity(logger, id)
		mcp.ServeDaemonConn(id, conn, st, ident.RealProcFS{}, project.RealGit{}, logger, sessions)
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory daemon:", err)
		return 1
	}
	defer func() { _ = srv.Close() }()

	if err := applyFirstLineDeadlineOverride(srv); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory daemon:", err)
		return 1
	}

	logger.Printf("listening on %s (store %s)", sockPath, dbPath)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve() }()

	select {
	case <-ctx.Done():
		logger.Printf("signal received, shutting down")
		_ = srv.Close()
		<-serveErr
		return 0
	case err := <-serveErr:
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory daemon:", err)
			return 1
		}
		return 0
	}
}

// logIdentity prints the daemon's per-connection identity log line,
// appending reason=... only when Resolve set a non-empty Identity.Reason —
// a harness was found but its cwd could not be read (task f2718b5b), which
// otherwise looks identical in the log to "no harness found" at all.
func logIdentity(logger *log.Logger, id ident.Identity) {
	line := fmt.Sprintf("identity: kind=%s uid=%d pid=%d harness=%s harness_pid=%d cwd=%q project=%q",
		id.Kind, id.UID, id.PID, id.Harness, id.HarnessPID, id.CWD, id.ProjectKey)
	if id.Reason != "" {
		line += fmt.Sprintf(" reason=%q", id.Reason)
	}
	logger.Print(line)
}

// runClaudeBackfillOnce runs the Claude transcript importer once in the
// background so the daemon's own startup is never delayed by however many
// transcripts ~/.claude/projects holds (PLAN.md §Phase 3: "This Week"
// populated within two minutes of the unit starting, from transcripts
// alone). Root comes from claude.DefaultRoot() ($BACKSTORY_CLAUDE_ROOT,
// else ~/.claude/projects); a missing root or any other failure is logged
// and never stops the daemon — a fresh install with no ~/.claude yet must
// still come up clean.
func runClaudeBackfillOnce(st *store.Store, logger *log.Logger) {
	res, err := claude.Import(st, claude.Options{})
	if err != nil {
		logger.Printf("backfill claude: %v", err)
		return
	}
	logger.Printf("%s", res.String())
}

// socketPath is $XDG_RUNTIME_DIR/backstory/sock, falling back to
// ~/.local/state/backstory/sock when XDG_RUNTIME_DIR is unset.
func socketPath() (string, error) {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "backstory", "sock"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "state", "backstory", "sock"), nil
}

// captureOffPath is $XDG_RUNTIME_DIR/backstory/capture-off, falling back to
// ~/.local/state/backstory/capture-off when XDG_RUNTIME_DIR is unset — the
// same directory and fallback as socketPath, since it is the same
// "omarchy toggle"-style flag file precedent (AGENT-CONTRACT.md §User-only
// powers) checked from the same client processes that dial the socket.
// Only `backstory hook post-tool-use` honours it today (task 04b1cb40); the
// daemon-side write paths SCHEMA.md invariant 8 also names are pre-existing,
// unimplemented scope this task does not touch.
func captureOffPath() (string, error) {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "backstory", "capture-off"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "state", "backstory", "capture-off"), nil
}

// storePath is $XDG_DATA_HOME/backstory/backstory.db, falling back to
// ~/.local/share/backstory/backstory.db when XDG_DATA_HOME is unset.
func storePath() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "backstory", "backstory.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "backstory", "backstory.db"), nil
}
