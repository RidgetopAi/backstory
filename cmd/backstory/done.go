package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/RidgetopAi/backstory/internal/store"
)

const doneUsage = "usage: backstory done <handoff-id>"

// runDone is `backstory done <handoff-id>`: the human marks a handoff
// finished (nothing to resume). It writes ONE human-declared confirm record
// with an informs edge into the handoff and deletes nothing; undo is
// `backstory delete <done-record-id>`. Like affirm it opens the store
// directly, so an agent can never mark a handoff done on the human's behalf.
func runDone(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		_, _ = fmt.Fprintln(stderr, "backstory done:", doneUsage)
		return 2
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory done:", err)
		return 1
	}
	st, err := openStore(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory done:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	h, found, err := st.FindRecordByIDPrefix(args[0])
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory done:", err)
		return 1
	}
	if !found {
		_, _ = fmt.Fprintf(stderr, "backstory done: no record with id %q (or the prefix is ambiguous)\n", args[0])
		return 1
	}
	if h.Kind != store.KindHandoff {
		_, _ = fmt.Fprintf(stderr, "backstory done: record %s is a %s, not a handoff\n", h.ID, h.Kind)
		return 1
	}
	if h.TombstonedAt != nil {
		_, _ = fmt.Fprintf(stderr, "backstory done: record %s is deleted\n", h.ID)
		return 1
	}
	if at, done, err := st.HandoffDone(h.ID); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory done:", err)
		return 1
	} else if done {
		_, _ = fmt.Fprintf(stderr, "backstory done: handoff %s is already marked done (%s)\n", h.ID, at.UTC().Format("2006-01-02"))
		return 1
	}

	id, err := st.Confirm(store.ConfirmParams{
		Identity:   humanIdentity,
		Action:     store.ConfirmDone,
		RecordID:   h.ID,
		ProjectKey: h.ProjectKey,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory done:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "marked %s done (%s); undo with: backstory delete %s\n", h.ID, id, id)
	return 0
}
