package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/RidgetopAi/backstory/internal/store"
)

const affirmUsage = "usage: backstory affirm <handoff-id>"

// runAffirm is `backstory affirm <handoff-id>`: the human dismisses a
// handoff's "possibly stale" flag. It writes ONE human-declared confirm/
// affirm record with an informs edge into the handoff — the same record
// store.latestAffirmInforming already honours as the freshness boundary, so
// the flag clears until NEW evidence arrives after it. Like delete and edit
// it opens the store directly: the CLI is the only route to the human tier
// (an agent's MCP confirm always resolves to an agent identity), so an agent
// can never dismiss a flag on the human's behalf.
func runAffirm(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		_, _ = fmt.Fprintln(stderr, "backstory affirm:", affirmUsage)
		return 2
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory affirm:", err)
		return 1
	}
	st, err := openStore(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory affirm:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	h, found, err := st.FindRecordByIDPrefix(args[0])
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory affirm:", err)
		return 1
	}
	if !found {
		_, _ = fmt.Fprintf(stderr, "backstory affirm: no record with id %q (or the prefix is ambiguous)\n", args[0])
		return 1
	}
	if h.Kind != store.KindHandoff {
		_, _ = fmt.Fprintf(stderr, "backstory affirm: record %s is a %s, not a handoff\n", h.ID, h.Kind)
		return 1
	}
	if h.TombstonedAt != nil {
		_, _ = fmt.Fprintf(stderr, "backstory affirm: record %s is deleted\n", h.ID)
		return 1
	}

	id, err := st.Confirm(store.ConfirmParams{
		Identity:   humanIdentity,
		Action:     store.ConfirmAffirm,
		RecordID:   h.ID,
		ProjectKey: h.ProjectKey,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory affirm:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "affirmed %s (%s)\n", h.ID, id)
	return 0
}
