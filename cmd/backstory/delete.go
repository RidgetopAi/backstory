package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/RidgetopAi/backstory/internal/store"
)

// humanIdentity is the store.Identity every `backstory delete` tombstone is
// attributed to: the CLI's direct, trusted path (internal/ident.KindHuman's
// doc comment) is the only route to store.IdentityHuman, since the socket
// resolver's SO_PEERCRED + /proc walk never produces it.
var humanIdentity = store.Identity{Kind: store.IdentityHuman, Actor: "human"}

// runDelete is the `backstory delete <id> [--yes]` subcommand. It opens the
// store directly (the same store.Open(storePath()) pattern runBackfillClaude
// uses) rather than dialing the daemon: TombstoneRecord is the sole
// human-only power a direct CLI process, not the socket API, ever reaches
// (AGENT-CONTRACT.md §User-only powers), and the DSN's WAL journaling plus
// busy_timeout (internal/store/store.go) make it safe to open alongside a
// live daemon.
func runDelete(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		_, _ = fmt.Fprintln(stderr, "backstory delete: usage: backstory delete <id> [--yes]")
		return 2
	}
	id := rest[0]

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory delete:", err)
		return 1
	}
	st, err := store.Open(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory delete:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	rec, err := st.GetRecord(id)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory delete:", err)
		return 1
	}

	if !*yes {
		if !isTerminal(stdin) {
			_, _ = fmt.Fprintln(stderr, "backstory delete: refusing to delete without --yes on a non-interactive session")
			return 1
		}
		if !confirmDelete(stdin, stdout, rec) {
			_, _ = fmt.Fprintln(stdout, "aborted")
			return 1
		}
	}

	if err := st.TombstoneRecord(id, humanIdentity); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory delete:", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "tombstoned", id)
	return 0
}

// confirmDelete prints rec's kind and first line and reads one y/N answer
// from stdin. Only a bare "y" or "Y" (surrounding whitespace trimmed)
// counts as consent; EOF, an empty line, or anything else refuses.
func confirmDelete(stdin io.Reader, stdout io.Writer, rec store.Record) bool {
	firstLine, _, _ := strings.Cut(rec.Text, "\n")
	_, _ = fmt.Fprintf(stdout, "%s: %s\ndelete? [y/N] ", rec.Kind, firstLine)
	scanner := bufio.NewScanner(stdin)
	if !scanner.Scan() {
		return false
	}
	answer := strings.TrimSpace(scanner.Text())
	return answer == "y" || answer == "Y"
}

// isTerminal reports whether r is the process's own connection to an
// interactive terminal. Only a real *os.File can be a terminal at all, and
// os.ModeCharDevice distinguishes one from a regular file or a pipe — the
// same test's helpers (runHookInDir and friends) always hand a subprocess's
// stdin an explicit bytes.Reader or OS pipe, never a bare terminal, so this
// never misreads a test as interactive.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
