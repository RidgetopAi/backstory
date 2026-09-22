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

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/socket"
	"github.com/RidgetopAi/backstory/internal/store"
)

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

	resolver := &ident.Resolver{
		ProcFS: ident.RealProcFS{},
		ProjectKey: func(cwd string) string {
			return project.Key(cwd, project.RealGit{})
		},
	}

	srv, err := socket.Listen(sockPath, resolver, func(id ident.Identity, conn net.Conn) {
		defer func() { _ = conn.Close() }()
		logger.Printf("identity: kind=%s uid=%d pid=%d harness=%s harness_pid=%d cwd=%q project=%q",
			id.Kind, id.UID, id.PID, id.Harness, id.HarnessPID, id.CWD, id.ProjectKey)
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory daemon:", err)
		return 1
	}
	defer func() { _ = srv.Close() }()

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
