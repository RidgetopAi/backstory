package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/week"
)

const dismissUsage = "usage: backstory dismiss <event-id>"

// runDismiss is `backstory dismiss <event-id>`: the human hides an
// uncommitted-at-session-end item (the panel's Needs you Dismiss) until a
// newer session in that working tree ends dirty. <event-id> is the item's
// one evidence id, the session.git_state event this-week cites; it must be
// that item's evidence NOW (not superseded by a later session end, and not
// already cleared because the tree is clean), which is checked against the
// same week.Build this-week renders. A clean session end still clears the
// item as always, and a newer dirty one shows it again.
//
// Like affirm it opens the store directly and writes as humanIdentity: the
// CLI is the only route to it, no MCP tool or socket method reaches
// store.DismissGitState, and that refuses any identity but the human's.
// Unlike affirm's record it never syncs: it is a local setting, because its
// evidence is a timeline event and timeline events never leave this device,
// so the item it hides exists only here.
func runDismiss(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		_, _ = fmt.Fprintln(stderr, "backstory dismiss:", dismissUsage)
		return 2
	}
	eventID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || eventID <= 0 {
		_, _ = fmt.Fprintf(stderr, "backstory dismiss: %q is not an event id (the item's evidence id, a number)\n", args[0])
		return 2
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory dismiss:", err)
		return 1
	}
	st, err := openStore(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory dismiss:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	ev, found, err := st.GetEvent(eventID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory dismiss:", err)
		return 1
	}
	if !found {
		_, _ = fmt.Fprintf(stderr, "backstory dismiss: no event with id %d\n", eventID)
		return 1
	}
	if ev.Kind != payload.KindSessionGitState {
		_, _ = fmt.Fprintf(stderr, "backstory dismiss: event %d is a %s, not a %s\n", eventID, ev.Kind, payload.KindSessionGitState)
		return 1
	}
	if dismissed, err := st.GitStateDismissed(ev); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory dismiss:", err)
		return 1
	} else if dismissed {
		_, _ = fmt.Fprintf(stderr, "backstory dismiss: event %d is already dismissed\n", eventID)
		return 1
	}

	result, err := week.Build(thisWeekParams(st, project.NewCachedGit(project.RealGit{})))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory dismiss:", err)
		return 1
	}
	want := strconv.FormatInt(eventID, 10)
	var item *week.AttentionItem
	for i, it := range result.Attention {
		if it.Kind == week.AttentionUncommittedAtSessionEnd && len(it.EvidenceIDs) == 1 && it.EvidenceIDs[0] == want {
			item = &result.Attention[i]
			break
		}
	}
	if item == nil {
		_, _ = fmt.Fprintf(stderr, "backstory dismiss: event %d is not what an uncommitted-changes item shows now (a later session in that folder replaced it, or the folder is clean now)\n", eventID)
		return 1
	}

	if err := st.DismissGitState(humanIdentity, ev); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory dismiss:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "dismissed %q; it shows again when a session there next ends with uncommitted changes\n", item.Reason)
	return 0
}
