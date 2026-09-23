// Package block renders the Backstory SessionStart block: a header naming
// Backstory as the source, then five fixed slots, in order, each omitted
// when empty, under a user-set token budget (AGENT-CONTRACT.md §The
// SessionStart block). It reads the store; it never writes to it.
package block

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/store"
)

// HeaderLine opens every rendered block, populated or empty-state alike: it
// names Backstory as the block's source and tells the agent the block is
// already loaded, so the agent can obey AGENT-CONTRACT.md §The SessionStart
// block's "do not re-fetch it" rule instead of proposing a recall call to
// fetch content it is already holding (task 6ae45e80, decision 1e53165a —
// measured on Brian's desktop 2026-09-23: an agent that received the block
// with no header could not tell it came from Backstory and offered to
// re-fetch it with recall). It counts against the token budget like every
// slot, but is the last thing the budget cutter drops, never the first.
const HeaderLine = "Backstory (already loaded this session — no need to call recall to fetch it again):"

// EmptyProjectLine is the body Render returns, after HeaderLine, for a
// project with no records and no events — an honest empty state, never
// invented prose (AGENT-CONTRACT.md §The SessionStart block, PLAN.md §Phase
// 2).
const EmptyProjectLine = "backstory: no history yet for this project."

// FinalLine is slot 5, verbatim. It survives every budget cut except the
// most extreme one: a budget too small even for HeaderLine plus FinalLine
// together, where FinalLine is dropped so HeaderLine — never dropped —
// still fits (task 6ae45e80's DONE WHEN clause 2).
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
	allEvents, err := p.Store.EventsSinceID(p.ProjectKey, 0)
	if err != nil {
		return "", fmt.Errorf("block: events: %w", err)
	}
	var deltaCursor int64
	if hasHandoff {
		deltaCursor = handoff.EventCursor
	}
	deltaEvents, err := p.Store.EventsSinceID(p.ProjectKey, deltaCursor)
	if err != nil {
		return "", fmt.Errorf("block: delta events: %w", err)
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
	slot2 := deltaSlot(deltaEvents)
	slot3 := coordinationSlot(liveSessions, allEvents, p.SessionID, p.ProcFS)
	slot4 := attentionSlot(draftCount, contradictionCount)

	if slot1 == "" && slot2 == "" && slot3 == "" && slot4 == "" {
		return HeaderLine + "\n\n" + EmptyProjectLine, nil
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

// deltaSlot is slot 2: sessions, distinct files touched, and last exit
// codes, from events since the handoff's event cursor (or every project
// event when there is no handoff). events is caller-filtered by
// store.EventsSinceID(handoff.EventCursor) — the membership boundary is the
// handoff's position in the sequence, never its ts: a backfilled event can
// carry a ts earlier than the handoff's even though it was appended after
// it (SCHEMA.md invariant 10, critic T1 on 7d3954f0). events is already
// ordered by id ascending, so "last exit codes" reflects true sequence.
//
// "files touched" counts CHANGED files only (payload.IsMutatingFileTool),
// never every tool_use that merely carries a path: a Read populates Path
// too (it is real history), but counting it here overstated the figure
// 3.9x on real history (task 393d174c) — the number that Brian recalls to
// check has to be a number he can check.
// DeltaSummary is deltaSlot's compressed "N sessions, M files touched, last
// exit codes" rendering, exported for recall's own recent-timeline summary
// (internal/mcp/recall.go) so the one definition of "what a delta looks
// like" stays here rather than growing a second copy.
func DeltaSummary(events []store.TimelineEvent) string {
	return deltaSlot(events)
}

func deltaSlot(events []store.TimelineEvent) string {
	if len(events) == 0 {
		return ""
	}

	sessions := map[string]bool{}
	files := map[string]bool{}
	var exitCodes []int
	for _, e := range events {
		if e.SessionID != "" {
			sessions[e.SessionID] = true
		}
		switch e.Kind {
		case payload.KindToolUse:
			var tu payload.ToolUse
			if json.Unmarshal([]byte(e.Payload), &tu) != nil {
				continue
			}
			if tu.Path != "" && payload.IsMutatingFileTool(tu.Name) {
				files[tu.Path] = true
			}
		case payload.KindToolResult:
			var tr payload.ToolResult
			if json.Unmarshal([]byte(e.Payload), &tr) != nil {
				continue
			}
			if tr.Exit != nil {
				exitCodes = append(exitCodes, *tr.Exit)
			}
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
// git branch from its session.start events. events is ordered by id
// ascending, so a later match simply overwrites an earlier one.
func latestBranchPerSession(events []store.TimelineEvent) map[string]string {
	out := map[string]string{}
	for _, e := range events {
		if e.SessionID == "" || e.Kind != payload.KindSessionStart {
			continue
		}
		var ss payload.SessionStart
		if json.Unmarshal([]byte(e.Payload), &ss) != nil {
			continue
		}
		if ss.GitBranch != "" {
			out[e.SessionID] = ss.GitBranch
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
