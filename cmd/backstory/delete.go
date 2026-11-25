package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/RidgetopAi/backstory/internal/store"
)

// humanIdentity is the store.Identity every `backstory delete` tombstone is
// attributed to: the CLI's direct, trusted path (internal/ident.KindHuman's
// doc comment) is the only route to store.IdentityHuman, since the socket
// resolver's SO_PEERCRED + /proc walk never produces it. "Human" here means
// "came through the CLI", which an agent's own shell can also run; delete
// and purge additionally pass eraseConfirmation (humanconfirm.go), which
// asks on the terminal inside an agent session.
var humanIdentity = store.Identity{Kind: store.IdentityHuman, Actor: "human"}

// runDelete is the `backstory delete <id> [--yes]` subcommand. It opens the
// store directly (the same store.Open(storePath()) pattern runBackfillClaude
// uses) rather than dialing the daemon: TombstoneRecord is the sole
// human-only power a direct CLI process, not the socket API, ever reaches
// (AGENT-CONTRACT.md §User-only powers), and the DSN's WAL journaling plus
// busy_timeout (internal/store/store.go) make it safe to open alongside a
// live daemon. Whether it may go ahead, and where it asks, is
// eraseConfirmation's call: inside an agent session --yes is refused and
// the question goes to the controlling terminal.
func runDelete(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	id, yes, err := parseDeleteArgs(args)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory delete:", err)
		return 2
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory delete:", err)
		return 1
	}
	st, err := openStore(dbPath)
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

	ask, ok, closeTTY := eraseConfirmation("delete", yes, os.Getenv, stdin, stdout, stderr)
	defer closeTTY()
	if !ok {
		return 1
	}
	if ask != nil {
		firstLine, _, _ := strings.Cut(rec.Text, "\n")
		if !ask.yesNo(fmt.Sprintf("%s: %s\ndelete? [y/N] ", rec.Kind, firstLine)) {
			_, _ = fmt.Fprintln(stdout, "aborted")
			return 1
		}
	}

	if err := st.TombstoneRecord(id, humanIdentity); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory delete:", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "tombstoned", id)
	printAlsoForgotten(st, stdout, stderr)
	return 0
}

// parseDeleteArgs parses `delete <id> [--yes]`. The standard library's
// flag.FlagSet stops parsing flags at the first non-flag argument, which
// would misparse the documented "<id> [--yes]" order (id first) as an
// unknown extra positional argument, so this walks args itself and accepts
// --yes in either position relative to id.
func parseDeleteArgs(args []string) (id string, yes bool, err error) {
	for _, a := range args {
		switch {
		case a == "--yes" || a == "-yes":
			yes = true
		case strings.HasPrefix(a, "-"):
			return "", false, fmt.Errorf("unknown flag %q; usage: backstory delete <id> [--yes]", a)
		case id != "":
			return "", false, fmt.Errorf("usage: backstory delete <id> [--yes]")
		default:
			id = a
		}
	}
	if id == "" {
		return "", false, fmt.Errorf("usage: backstory delete <id> [--yes]")
	}
	return id, yes, nil
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
