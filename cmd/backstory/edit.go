package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/RidgetopAi/backstory/internal/store"
)

const editUsage = "usage: backstory edit <id> [--file PATH | --stdin]"

// defaultEditor is the last-resort editor when neither $VISUAL nor $EDITOR
// is set.
const defaultEditor = "vi"

// maxSupersedeChain bounds the walk from a superseded record to its current
// head, so a corrupt cyclic chain can never loop forever.
const maxSupersedeChain = 1000

// runEdit is `backstory edit <id> [--file PATH | --stdin]`: the human
// corrects a saved record. Records are append-only (SCHEMA.md invariant 1),
// so an edit writes ONE new human-declared record — same kind, project_key
// and about — plus a supersedes edge new→old, in one transaction; the old
// row is never touched (decision 02c511b3 D2). Like delete, it opens the
// store directly and attributes the write to humanIdentity.
func runEdit(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	idArg, file, useStdin, err := parseEditArgs(args)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory edit:", err)
		return 2
	}
	interactive := isTerminal(stdin)
	if file == "" && !useStdin && !interactive {
		_, _ = fmt.Fprintln(stderr, "backstory edit: no text source on a non-interactive session;", editUsage)
		return 2
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory edit:", err)
		return 1
	}
	st, err := openStore(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory edit:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	old, found, err := st.FindRecordByIDPrefix(idArg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory edit:", err)
		return 1
	}
	if !found {
		_, _ = fmt.Fprintf(stderr, "backstory edit: no record with id %q (or the prefix is ambiguous)\n", idArg)
		return 1
	}
	if old.TombstonedAt != nil {
		_, _ = fmt.Fprintf(stderr, "backstory edit: record %s is deleted; it cannot be edited\n", old.ID)
		return 1
	}
	head, superseded, err := supersedeHead(st, old.ID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory edit:", err)
		return 1
	}
	if superseded {
		_, _ = fmt.Fprintf(stderr, "backstory edit: record %s is already superseded; edit the current record %s instead\n", old.ID, head)
		return 1
	}

	var text string
	switch {
	case file != "":
		b, err := os.ReadFile(file) //nolint:gosec // the human named this path on their own command line
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory edit:", err)
			return 1
		}
		text = string(b)
	case useStdin:
		b, err := io.ReadAll(stdin)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory edit:", err)
			return 1
		}
		text = string(b)
	default:
		text, err = editInEditor(old.Text, stdout, stderr)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory edit:", err)
			return 1
		}
	}

	text = strings.TrimRight(text, "\r\n")
	if strings.TrimSpace(text) == "" || text == strings.TrimRight(old.Text, "\r\n") {
		_, _ = fmt.Fprintln(stdout, "no change")
		return 0
	}

	newID, err := st.InsertRecordWithEdges(store.InsertRecordParams{
		Identity:   humanIdentity,
		Kind:       old.Kind,
		Text:       text,
		About:      old.About,
		ProjectKey: old.ProjectKey,
	}, []store.EdgeSpec{{
		OtherID:    old.ID,
		Type:       store.EdgeSupersedes,
		DeclaredBy: humanIdentity.Actor,
		Field:      "supersedes",
	}})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory edit:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "edited %s → %s\n", old.ID, newID)
	return 0
}

// parseEditArgs parses `edit <id> [--file PATH | --stdin]`, accepting the
// flags before or after id (see parseDeleteArgs for why flag.FlagSet is not
// used).
func parseEditArgs(args []string) (id, file string, useStdin bool, err error) {
	bad := fmt.Errorf("%s", editUsage)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--stdin" || a == "-stdin":
			useStdin = true
		case a == "--file" || a == "-file":
			i++
			if i >= len(args) {
				return "", "", false, bad
			}
			file = args[i]
		case strings.HasPrefix(a, "--file="):
			file = strings.TrimPrefix(a, "--file=")
		case strings.HasPrefix(a, "-"):
			return "", "", false, fmt.Errorf("unknown flag %q; %s", a, editUsage)
		case id != "":
			return "", "", false, bad
		default:
			id = a
		}
	}
	if id == "" || (file != "" && useStdin) {
		return "", "", false, bad
	}
	return id, file, useStdin, nil
}

// supersedeHead follows supersedes edges from id to the current head of its
// chain. superseded is false when nothing supersedes id.
func supersedeHead(st *store.Store, id string) (head string, superseded bool, err error) {
	cur := id
	for range maxSupersedeChain {
		edges, err := st.EdgesTouching(cur)
		if err != nil {
			return "", false, err
		}
		next := ""
		for _, e := range edges {
			if e.Type == store.EdgeSupersedes && e.ToID == cur {
				next = e.FromID
				break
			}
		}
		if next == "" {
			return cur, cur != id, nil
		}
		cur = next
	}
	return "", false, fmt.Errorf("supersedes chain from %s exceeds %d records", id, maxSupersedeChain)
}

// editorCommand picks $VISUAL, then $EDITOR, then defaultEditor.
func editorCommand() []string {
	for _, v := range []string{"VISUAL", "EDITOR"} {
		if f := strings.Fields(os.Getenv(v)); len(f) > 0 {
			return f
		}
	}
	return []string{defaultEditor}
}

// editInEditor writes current to a 0600 temp file, runs the editor on it
// attached to the terminal, and returns the file's contents. The temp file
// is removed afterwards.
func editInEditor(current string, stdout, stderr io.Writer) (string, error) {
	f, err := os.CreateTemp("", "backstory-edit-*.txt") // CreateTemp makes the file 0600
	if err != nil {
		return "", err
	}
	path := f.Name()
	defer func() { _ = os.Remove(path) }()
	if _, err := f.WriteString(current + "\n"); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}

	argv := editorCommand()
	cmd := exec.Command(argv[0], append(argv[1:], path)...) //nolint:gosec // the human's own $VISUAL/$EDITOR is the point
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("editor %s exited with an error: %w", argv[0], err)
		}
		return "", fmt.Errorf("run editor %s: %w", argv[0], err)
	}
	b, err := os.ReadFile(path) //nolint:gosec // path is the temp file this function created
	if err != nil {
		return "", err
	}
	return string(b), nil
}
