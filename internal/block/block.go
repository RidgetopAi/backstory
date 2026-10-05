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
	"github.com/RidgetopAi/backstory/internal/project"
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
//
// Its wording agrees with the skill's own "if a SessionStart block is
// present, do not re-fetch" rule (internal/skill/SKILL.md line 2) rather
// than contradicting it (task b172e778, real use 2026-09-28: the block's
// own final line used to invite a recall call unconditionally, while the
// skill said not to re-fetch when a block is present — an agent reading
// only the block's own last line had no reason not to call recall anyway).
const FinalLine = "call recall only if you need more than this block"

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
	// CWD is the calling session's own folder — decision f3fa04c7's RESUME
	// rule needs it to tell "inside a repo under a workspace" apart from
	// "at the workspace root itself" apart from "no workspace involved at
	// all". Empty disables all three home-scoped Resume behaviors, falling
	// back to the pre-f3fa04c7 "newest handoff for ProjectKey" exactly —
	// existing callers that never set it keep their old behavior unchanged.
	CWD string
	// Git resolves CWD's (and a home-scoped handoff candidate's own
	// session's) repo identity for label purposes (project.Label). Only
	// read when CWD is non-empty; a caller that sets CWD must also set Git.
	Git project.Git
	// WorkspaceDirs is the caller's already-resolved workspace directory
	// list (task 482b2320, decision f3fa04c7's clause 7): block never
	// resolves workspace dirs itself (no project.DefaultWorkspaceDirs call,
	// no env read), so its Resume behavior depends only on what the caller
	// passes, never on the process environment. nil disables every
	// home-scoped Resume behavior the same way an empty CWD does.
	WorkspaceDirs []string
	// Now is the reference time for expiry checks (slot 4) and the delta
	// cutoff (slot 2). Zero means time.Now().
	Now time.Time
}

// Render builds the SessionStart block for p.ProjectKey.
func Render(p Params) (string, error) {
	if p.Now.IsZero() {
		p.Now = time.Now()
	}

	handoff, hasHandoff, resumeLabel, pointer, err := resolveResumeHandoff(p.Store, p.ProjectKey, p.CWD, p.Git, p.WorkspaceDirs)
	if err != nil {
		return "", fmt.Errorf("block: resolve resume handoff: %w", err)
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
	var staleReasons []store.FreshnessReason
	if hasHandoff {
		staleReasons, err = p.Store.HandoffFreshness(handoff, p.WorkspaceDirs)
		if err != nil {
			return "", fmt.Errorf("block: handoff freshness: %w", err)
		}
	}

	author := ""
	if hasHandoff {
		author, err = p.Store.SessionAgent(handoff.SessionID)
		if err != nil {
			return "", fmt.Errorf("block: resume handoff author: %w", err)
		}
	}
	slot1 := resumeSlot(handoff, hasHandoff, staleReasons, resumeLabel, handoffAuthorClause(author, p.Harness))
	if !hasHandoff {
		slot1 = pointer
	}
	slot2 := deltaSlot(deltaEvents)
	slot3 := coordinationSlot(liveSessions, allEvents, p.SessionID, p.ProcFS)
	slot4 := attentionSlot(draftCount, contradictionCount, len(staleReasons) > 0)

	if slot1 == "" && slot2 == "" && slot3 == "" && slot4 == "" {
		return HeaderLine + "\n\n" + EmptyProjectLine, nil
	}

	budgetTokens, err := resolveBudget(p.Store, p.Harness)
	if err != nil {
		return "", fmt.Errorf("block: resolve budget: %w", err)
	}
	return assemble(slot1, slot2, slot3, slot4, budgetTokens), nil
}

// resumeSlot is slot 1: the latest handoff's record id, its text, plus its
// MODE line when the text carries one. The id is rendered in full (never
// truncated to a short form) because note's supersedes matches a record id
// exactly (store.InsertRecordWithEdges), and it comes right after the
// "Resume:" label, in a fixed "(id ...)" shape, so an agent writing the next
// handoff can lift it verbatim into supersedes without needing the id
// spelled out in prose (task 56317fe7 — measured on Brian's desktop: a
// handoff's text named its predecessor in prose because the block carried
// no id at all, so the chain never linked).
func resumeSlot(rec store.Record, ok bool, staleReasons []store.FreshnessReason, label, authorClause string) string {
	if !ok {
		return ""
	}
	line := "Resume: (id " + rec.ID + ") " + authorClause + rec.Text
	if m := modeLine.FindStringSubmatch(rec.Text); m != nil {
		line += "\nMODE: " + m[1]
	}
	if label != "" {
		line += "\nLocation: " + label
	}
	if marker := staleMarker(staleReasons); marker != "" {
		line += "\n" + marker
	}
	return line
}

// handoffAuthorClause names the harness that wrote the Resume handoff, so a
// different harness reading it does not claim the work as its own (task
// b4829f9a: Codex said "we fixed" about a handoff Claude wrote). It follows
// the fixed "(id ...)" part, never altering it. The clause is empty — the
// line renders exactly as before — when the author is unknown (only the
// record's observed session agent is used, never guessed) or is the reading
// harness itself, for whom "we" is accurate.
func handoffAuthorClause(author, reader string) string {
	if author == "" || author == reader {
		return ""
	}
	return "written by " + author + ": "
}

// maxPointerProjects caps how many projects the workspace-root pointer line
// names before it collapses the rest into "+N more".
const maxPointerProjects = 3

// resolveResumeHandoff is decision f3fa04c7's RESUME rule, read side
// narrowed by decision 7a2556b6: inside a repo under a workspace, the
// newest non-tombstoned handoff in the home whose session's labels include
// that repo (no label shown — the caller is already inside it); at the
// workspace root itself, the newest handoff WRITTEN FROM the root (its
// session's own project_key is the home), with its session's label on the
// Resume line — and when none exists, no handoff body but a one-line
// pointer (returned as the string) naming the projects that have one; with
// no workspace involved at all (cwd empty, or outside every configured
// workspace), the pre-f3fa04c7 behavior — the newest handoff for
// projectKey, unchanged, no label.
func resolveResumeHandoff(st *store.Store, projectKey, cwd string, git project.Git, workspaces []string) (store.Record, bool, string, string, error) {
	if cwd == "" {
		h, ok, err := st.LatestRecord(projectKey, store.KindHandoff)
		return h, ok, "", "", err
	}
	home, ok := project.WorkspaceHome(cwd, workspaces)
	if !ok {
		h, ok, err := st.LatestRecord(projectKey, store.KindHandoff)
		return h, ok, "", "", err
	}

	if home != projectKey {
		// Inside a specific repo under the workspace: filter the home's
		// handoffs down to the one whose own session actually touched this
		// repo.
		label := project.Label(cwd, git, workspaces)
		h, found, err := st.HandoffForLabel(home, label, git, workspaces)
		return h, found, "", "", err
	}

	// At the workspace root itself: only handoffs written from the root.
	h, found, others, err := st.RootHandoffs(home)
	if err != nil {
		return store.Record{}, false, "", "", fmt.Errorf("block: root handoffs: %w", err)
	}
	if !found {
		pointer, err := projectPointerLine(st, others, git, workspaces)
		return store.Record{}, false, "", pointer, err
	}
	label, err := st.HandoffLabel(h, git, workspaces)
	if err != nil {
		return store.Record{}, false, "", "", fmt.Errorf("block: resume handoff label: %w", err)
	}
	return h, true, label, "", nil
}

// projectPointerLine renders the one-line stand-in for a root resume with no
// root-written handoff: each project (by label) with its latest handoff id,
// capped at maxPointerProjects then "+N more". Empty when there are none.
func projectPointerLine(st *store.Store, others []store.Record, git project.Git, workspaces []string) (string, error) {
	if len(others) == 0 {
		return "", nil
	}
	shown := others
	if len(shown) > maxPointerProjects {
		shown = shown[:maxPointerProjects]
	}
	parts := make([]string, 0, len(shown))
	for _, h := range shown {
		label, err := st.HandoffLabel(h, git, workspaces)
		if err != nil {
			return "", fmt.Errorf("block: pointer handoff label: %w", err)
		}
		if label == "" {
			label = "(unlabelled)"
		}
		parts = append(parts, label+" (id "+h.ID+")")
	}
	line := "Handoffs exist in projects under this workspace (none written here): " + strings.Join(parts, "; ")
	if extra := len(others) - len(shown); extra > 0 {
		line += fmt.Sprintf("; +%d more", extra)
	}
	return line, nil
}

// staleMarker renders the Resume slot's possibly-stale marker (decision
// bcc9fa54, AGENT-CONTRACT.md §The SessionStart block): a short clause per
// store.FreshnessReason plus the evidence ids it cites, e.g. "⚠ possibly
// stale: 3 later edits to files it names (ids 12,13,14)". Empty when reasons
// is empty — a fresh (or non-handoff-shaped) resume slot renders exactly as
// it did before this marker existed.
func staleMarker(reasons []store.FreshnessReason) string {
	if len(reasons) == 0 {
		return ""
	}
	clauses := make([]string, len(reasons))
	for i, r := range reasons {
		clauses[i] = staleReasonClause(r)
	}
	return "⚠ possibly stale: " + strings.Join(clauses, "; ")
}

func staleReasonClause(r store.FreshnessReason) string {
	ids := make([]string, 0, len(r.RecordIDs)+len(r.EventIDs))
	ids = append(ids, r.RecordIDs...)
	for _, id := range r.EventIDs {
		ids = append(ids, strconv.FormatInt(id, 10))
	}
	idList := strings.Join(ids, ",")
	switch r.Kind {
	case store.FreshnessContradicted:
		return fmt.Sprintf("contradicted (ids %s)", idList)
	case store.FreshnessLaterRecord:
		return fmt.Sprintf("%d later record(s) share a path it names (ids %s)", len(r.RecordIDs), idList)
	case store.FreshnessLaterActivity:
		return fmt.Sprintf("%d later edit(s) to files it names (ids %s)", len(r.EventIDs), idList)
	default:
		return fmt.Sprintf("%s (ids %s)", r.Kind, idList)
	}
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

// attentionSlot is slot 4: unconfirmed inferred drafts, contradictions
// flagged against this project's records, and whether the Resume slot's
// handoff is possibly stale (decision bcc9fa54). The possibly-stale clause
// is appended only when staleHandoff is true, so a project with no flagged
// handoff renders the exact same "Attention: N unconfirmed draft(s), M
// contradiction(s)" text this slot always has.
func attentionSlot(draftCount, contradictionCount int, staleHandoff bool) string {
	if draftCount == 0 && contradictionCount == 0 && !staleHandoff {
		return ""
	}
	line := fmt.Sprintf("Attention: %d unconfirmed draft(s), %d contradiction(s)", draftCount, contradictionCount)
	if staleHandoff {
		line += ", 1 possibly-stale handoff"
	}
	return line
}
