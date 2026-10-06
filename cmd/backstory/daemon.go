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
	"strconv"
	"syscall"
	"time"

	"github.com/RidgetopAi/backstory/internal/backfill/claude"
	"github.com/RidgetopAi/backstory/internal/backfill/codex"
	"github.com/RidgetopAi/backstory/internal/backfill/hermes"
	"github.com/RidgetopAi/backstory/internal/backfill/pi"
	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/mcp"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/socket"
	"github.com/RidgetopAi/backstory/internal/store"
	omausage "github.com/RidgetopAi/backstory/internal/usage"
)

// procFSForDaemon is defined per build tag: daemon_procfs_release.go
// (default) always returns ident.RealProcFS{}; daemon_procfs_backstorytest.go
// (built with -tags backstorytest, cmd/backstory tests only) additionally
// honors BACKSTORY_TEST_FAKE_ANCESTRY. See that file's doc comment for why
// the override must never compile into a release binary (task fe7aee40).

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

// usageIntervalEnvVar overrides how often the daemon rewrites its Omarchy
// usage record (a time.Duration string); unset means omausage.DefaultInterval.
const usageIntervalEnvVar = "BACKSTORY_USAGE_INTERVAL"

// usageInterval reads usageIntervalEnvVar, defaulting when unset.
func usageInterval() (time.Duration, error) {
	raw := os.Getenv(usageIntervalEnvVar)
	if raw == "" {
		return omausage.DefaultInterval, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("parse %s=%q: want a positive duration", usageIntervalEnvVar, raw)
	}
	return d, nil
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

	// No resolvable home dir means no DEFAULT workspace, not a daemon that
	// refuses to start (decision bcc9fa54). Resolved before Open so Open's
	// own sweep (task d65ef8ff) sees the same dirs the resolver below does.
	workspaceDirs, err := project.DefaultWorkspaceDirs()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory daemon: no default workspace dirs:", err)
		workspaceDirs = nil
	}

	st, err := store.Open(dbPath, workspaceDirs, project.RealGit{})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory daemon:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	if n, err := mcp.EndOrphanedSessions(st, project.RealGit{}, logger, captureOff); err != nil {
		logger.Printf("end sessions left live by a previous run: %v", err)
	} else if n > 0 {
		logger.Printf("ended %d session(s) left live by a previous run", n)
	}

	go runClaudeBackfillOnce(st, logger)
	go runCodexBackfillOnce(st, logger)
	go runHermesBackfillOnce(st, logger)
	go runPiBackfillOnce(st, logger)

	procfs, err := procFSForDaemon()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory daemon:", err)
		return 1
	}

	resolver := &ident.Resolver{
		ProcFS: procfs,
		ProjectKey: func(cwd string) string {
			return project.Key(cwd, project.RealGit{}, workspaceDirs)
		},
	}

	sessions := mcp.NewSessionRegistry()
	srv, err := socket.Listen(sockPath, resolver, func(id ident.Identity, conn net.Conn) {
		defer func() { _ = conn.Close() }()
		logIdentity(logger, id)
		mcp.ServeDaemonConn(id, conn, st, procfs, project.RealGit{}, logger, sessions, captureOff, workspaceDirs)
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

	usageIval, err := usageInterval()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory daemon:", err)
		return 1
	}

	logger.Printf("listening on %s (store %s)", sockPath, dbPath)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	go omausage.Run(ctx, omausage.Config{
		Store:      st,
		Interval:   usageIval,
		CaptureOff: captureOff,
		Logger:     logger,
	})

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
	res, err := claude.Import(st, claude.Options{CaptureOff: captureOff})
	if err != nil {
		logger.Printf("backfill claude: %v", err)
		return
	}
	logger.Printf("%s", res.String())
}

// runCodexBackfillOnce is runClaudeBackfillOnce's Codex counterpart
// (decision 3e14db82, task e9cb97dd): it runs once in the background so the
// daemon's own startup is never delayed by however many rollouts
// ~/.codex/sessions holds. Root comes from codex.DefaultRoot()
// ($BACKSTORY_CODEX_ROOT, else ~/.codex/sessions); a missing root or any
// other failure is logged and never stops the daemon — a machine with no
// Codex CLI installed must still come up clean.
func runCodexBackfillOnce(st *store.Store, logger *log.Logger) {
	res, err := codex.Import(st, codex.Options{CaptureOff: captureOff})
	if err != nil {
		logger.Printf("backfill codex: %v", err)
		return
	}
	logger.Printf("%s", res.String())
}

// runHermesBackfillOnce is runClaudeBackfillOnce's Hermes counterpart
// (decision 3e14db82, task bf335dd4): it runs once in the background so the
// daemon's own startup is never delayed by however large Hermes Agent's own
// state.db has grown. Path comes from hermes.DefaultPath()
// ($HERMES_HOME/state.db, else ~/.hermes/state.db); a missing file or any
// other failure is logged and never stops the daemon — a machine with no
// Hermes Agent installed must still come up clean.
func runHermesBackfillOnce(st *store.Store, logger *log.Logger) {
	res, err := hermes.Import(st, hermes.Options{CaptureOff: captureOff})
	if err != nil {
		logger.Printf("backfill hermes: %v", err)
		return
	}
	logger.Printf("%s", res.String())
}

// runPiBackfillOnce is runClaudeBackfillOnce's counterpart for Pi
// transcripts (decision 3e14db82 "Pi + local models in v1"): same
// background-goroutine, never-block-startup, never-fail-the-daemon rules.
// pi.Import already treats a missing ~/.pi/agent/sessions as zero files
// (filepath.Glob over an absent root), so there is no separate existence
// check to make here — a machine with no Pi installed comes up exactly as
// clean as one with no Claude Code installed.
func runPiBackfillOnce(st *store.Store, logger *log.Logger) {
	res, err := pi.Import(st, pi.Options{CaptureOff: captureOff})
	if err != nil {
		logger.Printf("backfill pi: %v", err)
		return
	}
	logger.Printf("%s", res.String())
}

// socketPath is $XDG_RUNTIME_DIR/backstory/sock, falling back to
// ~/.local/state/backstory/sock when XDG_RUNTIME_DIR is unset.
// runtimeRoot is the parent of the per-user runtime dirs (<root>/<uid>) that
// systemd creates. A package-level variable so tests can point it at a temp
// dir.
var runtimeRoot = "/run/user"

// runtimeDir resolves the directory holding the daemon socket and capture-off
// flag: $XDG_RUNTIME_DIR when set; otherwise <runtimeRoot>/<uid> if it exists
// as a directory owned by the current uid (Codex launches MCP servers with a
// scrubbed environment lacking XDG_RUNTIME_DIR, yet the systemd --user daemon
// listens there); otherwise "" so callers use the ~/.local/state fallback.
func runtimeDir() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir
	}
	uid := os.Getuid()
	dir := filepath.Join(runtimeRoot, strconv.Itoa(uid))
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return ""
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != uid {
		return ""
	}
	return dir
}

func socketPath() (string, error) {
	if dir := runtimeDir(); dir != "" {
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
// `backstory hook post-tool-use` honours it client-side (task 04b1cb40,
// before ever dialing the daemon); `backstory capture off|on` writes and
// removes it; the daemon itself honours it on the note and status write
// paths (task fd620482, SCHEMA.md invariant 8) via the captureOff callback
// runDaemon passes to mcp.ServeDaemonConn below.
func captureOffPath() (string, error) {
	if dir := runtimeDir(); dir != "" {
		return filepath.Join(dir, "backstory", "capture-off"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "state", "backstory", "capture-off"), nil
}

// openStore opens the store at dbPath with the configured workspace dirs and
// real git identity resolution (task d65ef8ff): every `backstory` subcommand
// funnels through this ONE call so Open's sweep and write-time canonicalizer
// always see the same workspace dirs project.Key itself resolves against.
func openStore(dbPath string) (*store.Store, error) {
	return store.Open(dbPath, resolveWorkspaceDirs(), project.RealGit{})
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
