// Package block renders the Backstory SessionStart block: five fixed
// slots, in order, each omitted when empty, under a user-set token budget
// (AGENT-CONTRACT.md §The SessionStart block). It reads the store; it never
// writes to it.
package block

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/store"
)

// EmptyProjectLine is the exact and only line Render returns for a project
// with no records and no events — an honest empty state, never invented
// prose (AGENT-CONTRACT.md §The SessionStart block, PLAN.md §Phase 2).
const EmptyProjectLine = "backstory: no history yet for this project."

// FinalLine is slot 5, verbatim, always present, never truncated.
const FinalLine = "ask backstory for more"

// maxLastExitCodes bounds how many exit codes slot 2 lists, oldest kept
// dropped first, so the delta stays a compressed summary rather than a full
// log.
const maxLastExitCodes = 5

// modeLine matches a "MODE: <word>" line inside a handoff's free text
// (AGENT-CONTRACT.md §The SessionStart block: "its MODE line if present").
var modeLine = regexp.MustCompile(`(?m)^MODE:\s*(\S+)\s*$`)

// Params is Render's input: everything it needs to read from the store and
// /proc for one project, plus the caller's own session (excluded from slot
// 3's coordination list) and harness (for the per-harness budget override).
type Params struct {
	Store      *store.Store
	ProcFS     ident.ProcFS
	ProjectKey string
	SessionID  string
	Harness    string
	// Now is the reference time for expiry checks (slot 4) and the delta
	// cutoff (slot 2). Zero means time.Now().
	Now time.Time
}

// Render builds the SessionStart block for p.ProjectKey.
func Render(p Params) (string, error) {
	if p.Now.IsZero() {
		p.Now = time.Now()
	}

	handoff, hasHandoff, err := p.Store.LatestRecord(p.ProjectKey, store.KindHandoff)
	if err != nil {
		return "", fmt.Errorf("block: latest handoff: %w", err)
	}
	events, err := p.Store.EventsSinceID(p.ProjectKey, 0)
	if err != nil {
		return "", fmt.Errorf("block: events: %w", err)
	}
	liveSessions, err := p.Store.LiveSessionsInProject(p.ProjectKey)
	if err != nil {
		return "", fmt.Errorf("block: live sessions: %w", err)
	}
	draftCount, err := p.Store.UnconfirmedDraftCount(p.ProjectKey, p.Now)
	if err != nil {
		return "", fmt.Errorf("block: unconfirmed draft count: %w", err)
	}
	contradictionCount, err := p.Store.ContradictionCount(p.ProjectKey)
	if err != nil {
		return "", fmt.Errorf("block: contradiction count: %w", err)
	}

	slot1 := resumeSlot(handoff, hasHandoff)
	slot2 := deltaSlot(events, handoff, hasHandoff)
	slot3 := coordinationSlot(liveSessions, events, p.SessionID, p.ProcFS)
	slot4 := attentionSlot(draftCount, contradictionCount)

	if slot1 == "" && slot2 == "" && slot3 == "" && slot4 == "" {
		return EmptyProjectLine, nil
	}

	budgetTokens, err := resolveBudget(p.Store, p.Harness)
	if err != nil {
		return "", fmt.Errorf("block: resolve budget: %w", err)
	}
	return assemble(slot1, slot2, slot3, slot4, budgetTokens), nil
}

// resumeSlot is slot 1: the latest handoff's text, plus its MODE line when
// the text carries one.
func resumeSlot(rec store.Record, ok bool) string {
	if !ok {
		return ""
	}
	line := "Resume: " + rec.Text
	if m := modeLine.FindStringSubmatch(rec.Text); m != nil {
		line += "\nMODE: " + m[1]
	}
	return line
}

// eventPayload is the subset of a timeline event's JSON payload the delta
// and coordination slots read. All fields are optional: SCHEMA.md leaves
// timeline_events.kind and payload shape open in v0 ("Event kinds enum...
// TBD"), so an event that carries none of these simply contributes nothing
// beyond its session to the count.
type eventPayload struct {
	Path   string `json:"path,omitempty"`
	Exit   *int   `json:"exit,omitempty"`
	Branch string `json:"branch,omitempty"`
}

// deltaSlot is slot 2: sessions, distinct files touched, and last exit
// codes, from events strictly after the handoff (or every project event
// when there is no handoff). The membership boundary is the handoff's ts —
// the only link between the independently-sequenced records and
// timeline_events tables — but events is already ordered by id ascending
// (SCHEMA.md invariant 10), and that order is preserved through the filter,
// so "last exit codes" reflects true sequence, not a lying backfilled ts.
func deltaSlot(events []store.TimelineEvent, handoff store.Record, hasHandoff bool) string {
	var since []store.TimelineEvent
	for _, e := range events {
		if hasHandoff && e.TS.Before(handoff.TS) {
			continue
		}
		since = append(since, e)
	}
	if len(since) == 0 {
		return ""
	}

	sessions := map[string]bool{}
	files := map[string]bool{}
	var exitCodes []int
	for _, e := range since {
		if e.SessionID != "" {
			sessions[e.SessionID] = true
		}
		var pl eventPayload
		if json.Unmarshal([]byte(e.Payload), &pl) != nil {
			continue
		}
		if pl.Path != "" {
			files[pl.Path] = true
		}
		if pl.Exit != nil {
			exitCodes = append(exitCodes, *pl.Exit)
		}
	}

	line := fmt.Sprintf("Delta: %d sessions, %d files touched", len(sessions), len(files))
	if len(exitCodes) > 0 {
		if len(exitCodes) > maxLastExitCodes {
			exitCodes = exitCodes[len(exitCodes)-maxLastExitCodes:]
		}
		codes := make([]string, len(exitCodes))
		for i, c := range exitCodes {
			codes[i] = strconv.Itoa(c)
		}
		line += ", last exit codes: " + strings.Join(codes, ", ")
	}
	return line
}

// coordinationSlot is slot 3: other live sessions in the project, excluding
// the caller's own, with a dead-pid check against ProcFS and a branch when
// one was observed for that session in the timeline.
func coordinationSlot(sessions []store.Session, events []store.TimelineEvent, selfSessionID string, procfs ident.ProcFS) string {
	branches := latestBranchPerSession(events)

	var lines []string
	for _, s := range sessions {
		if s.ID == selfSessionID {
			continue
		}
		if s.Origin != store.OriginLive {
			continue
		}
		if !pidAlive(procfs, s.PID) {
			continue
		}
		line := fmt.Sprintf("- %s in %s", s.Agent, s.CWD)
		if branch, ok := branches[s.ID]; ok && branch != "" {
			line += fmt.Sprintf(" (branch %s)", branch)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return "Coordination:\n" + strings.Join(lines, "\n")
}

// latestBranchPerSession maps a session id to the most recently observed
// branch name from its events. events is ordered by id ascending, so a
// later match simply overwrites an earlier one.
func latestBranchPerSession(events []store.TimelineEvent) map[string]string {
	out := map[string]string{}
	for _, e := range events {
		if e.SessionID == "" {
			continue
		}
		var pl eventPayload
		if json.Unmarshal([]byte(e.Payload), &pl) != nil {
			continue
		}
		if pl.Branch != "" {
			out[e.SessionID] = pl.Branch
		}
	}
	return out
}

// pidAlive reports whether pid is a live process, using the same ProcFS
// abstraction the identity resolver uses (AGENT-CONTRACT.md §Observed
// identity) rather than reading /proc directly: ProcFS.Status returns an
// error for a pid with no /proc entry.
func pidAlive(procfs ident.ProcFS, pid *int) bool {
	if procfs == nil || pid == nil || *pid == 0 {
		return false
	}
	_, err := procfs.Status(*pid)
	return err == nil
}

// attentionSlot is slot 4: unconfirmed inferred drafts plus contradictions
// flagged against this project's records.
func attentionSlot(draftCount, contradictionCount int) string {
	if draftCount == 0 && contradictionCount == 0 {
		return ""
	}
	return fmt.Sprintf("Attention: %d unconfirmed draft(s), %d contradiction(s)", draftCount, contradictionCount)
}
