// Package block renders the Backstory SessionStart block: a header naming
// Backstory as the source, then five fixed slots, in order, each omitted
// when empty, under a user-set token budget (AGENT-CONTRACT.md §The
// SessionStart block). It reads the store; it never writes to it.
package block

import (
	"encoding/json"
	"fmt"
	"path/filepath"
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

// handoffRule is the sentence FinalLineFor appends to FinalLine: the handoff
// rule used to live only in the skill, so an agent that never loaded it ended
// its session without writing one (V1 review, tesla-gaps #6).
const handoffRule = "Before you stop: note handoff with next = the single next step"

// FinalLineFor is slot 5: FinalLine, then the handoff rule, naming the
// Resume handoff's id as supersedes when resumeID is not empty so the chain
// links without the agent having to look the id up.
func FinalLineFor(resumeID string) string {
	if resumeID == "" {
		return FinalLine + ". " + handoffRule + ", supersedes = nothing (no earlier handoff)"
	}
	return FinalLine + ". " + handoffRule + ", supersedes = " + resumeID
}

// EndsWithFinalLine reports whether out's last line is a FinalLineFor line
// (any resume id).
func EndsWithFinalLine(out string) bool {
	last := out[strings.LastIndex(out, "\n")+1:]
	return strings.HasPrefix(last, FinalLine+". "+handoffRule)
}

// maxFailureCmdRunes bounds how much of a failed command the Last failure
// line quotes.
const maxFailureCmdRunes = 80

// modeLine matches a "MODE: <word>" line inside a handoff's free text
// (AGENT-CONTRACT.md §The SessionStart block: "its MODE line if present").
// It captures the leading mode token only (letters, digits, "_", "/", "-"),
// so a real line like "MODE: debug/verify. extra words" still renders as
// "MODE: debug/verify" instead of being missed (task 91860fb0).
var modeLine = regexp.MustCompile(`(?m)^MODE:[ \t]*([\w/-]+)`)

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
	shellSessions, err := p.Store.ShellSessionIDs()
	if err != nil {
		return "", fmt.Errorf("block: shell sessions: %w", err)
	}
	failure := lastFailure(deltaEvents)
	repoLine, err := repoStateLine(p, handoff, hasHandoff, deltaEvents, shellSessions)
	if err != nil {
		return "", err
	}
	ledgerLine, err := ledgerLine(p)
	if err != nil {
		return "", err
	}
	slot2 := joinLines(repoLine, deltaSlot(deltaEvents, shellSessions, p.SessionID), failure.line(p.Now), ledgerLine)
	slot3 := coordinationSlot(liveSessions, allEvents, p.SessionID, p.ProcFS)
	slot4 := attentionSlot(draftCount, contradictionCount, len(staleReasons) > 0, failure.found)

	if slot1 == "" && slot2 == "" && slot3 == "" && slot4 == "" {
		return HeaderLine + "\n\n" + EmptyProjectLine, nil
	}

	budgetTokens, err := resolveBudget(p.Store, p.Harness)
	if err != nil {
		return "", fmt.Errorf("block: resolve budget: %w", err)
	}
	resumeID := ""
	if hasHandoff {
		resumeID = handoff.ID
	}
	return assemble(slot1, slot2, slot3, slot4, FinalLineFor(resumeID), budgetTokens), nil
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
	if rec.Next != "" {
		line += "\nNext: " + rec.Next
	}
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

// resolveResumeHandoff is decision f3fa04c7's RESUME rule, unified by task
// ed31b744: the newest non-tombstoned handoff that belongs to the caller's
// location (store.LatestHandoffAt — the one resolver recall, export,
// records and This Week's row share): the repo-key handoffs plus the
// workspace-homed ones whose writing session is labelled with that
// location; at the workspace root itself, exactly those labelled with the
// root. At the root with none, no handoff body but a one-line pointer
// (returned as the string) naming the projects that have one. With no
// workspace involved at all (cwd empty, or outside every configured
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

	h, found, err := st.LatestHandoffAt(cwd, git, workspaces)
	if err != nil {
		return store.Record{}, false, "", "", fmt.Errorf("block: location handoff: %w", err)
	}
	if home != projectKey {
		// Inside a specific repo under the workspace: no label shown, the
		// caller is already inside it.
		return h, found, "", "", nil
	}

	// At the workspace root itself — or a non-git direct child, which
	// shares the root's key (decision 7a2556b6): with no handoff of its own
	// it carries the root's.
	if !found {
		if root, ok := workspaceRootOf(cwd, home, workspaces); ok && root != filepath.Clean(cwd) {
			h, found, err = st.LatestHandoffAt(root, git, workspaces)
			if err != nil {
				return store.Record{}, false, "", "", fmt.Errorf("block: root handoff: %w", err)
			}
		}
	}
	if !found {
		others, err := st.HandoffsByWriter(home)
		if err != nil {
			return store.Record{}, false, "", "", fmt.Errorf("block: root handoffs: %w", err)
		}
		pointer, err := projectPointerLine(st, others, git, workspaces)
		return store.Record{}, false, "", pointer, err
	}
	label, err := st.HandoffLabel(h, git, workspaces)
	if err != nil {
		return store.Record{}, false, "", "", fmt.Errorf("block: resume handoff label: %w", err)
	}
	return h, true, label, "", nil
}

// workspaceRootOf is the configured workspace directory whose home key is
// home and that holds cwd (cwd itself, or a direct child of it).
func workspaceRootOf(cwd, home string, workspaces []string) (string, bool) {
	cwd = filepath.Clean(cwd)
	for _, w := range workspaces {
		w = filepath.Clean(w)
		if cwd != w && filepath.Dir(cwd) != w {
			continue
		}
		if h, ok := project.WorkspaceHome(w, []string{w}); ok && h == home {
			return w, true
		}
	}
	return "", false
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

// deltaSlot is the Delta line of slot 2: sessions and distinct files
// touched, from events since the handoff's event cursor (or every project
// event when there is no handoff). events is caller-filtered by
// store.EventsSinceID(handoff.EventCursor) — the membership boundary is the
// handoff's position in the sequence, never its ts: a backfilled event can
// carry a ts earlier than the handoff's even though it was appended after
// it (SCHEMA.md invariant 10, critic T1 on 7d3954f0).
//
// "files touched" counts CHANGED files only (payload.IsMutatingFileTool),
// never every tool_use that merely carries a path: a Read populates Path
// too (it is real history), but counting it here overstated the figure
// 3.9x on real history (task 393d174c) — the number that Brian recalls to
// check has to be a number he can check.
//
// The reading session (selfSessionID) is never one of the "N sessions": the
// reader is not news to itself, and its own session.start always lands after
// the handoff. The files phrase is omitted when 0 (agents that write through
// Bash never produce a mutating tool.use, so it read "0 files touched"
// whenever they did), and the line itself when there is nothing left to say.
//
// shellSessions are the sessions (store.ShellSessionIDs) a delta never counts
// in its "N sessions": a shell's captured commands are not agent work.
func DeltaSummary(events []store.TimelineEvent, shellSessions map[string]bool, selfSessionID string) string {
	return deltaSlot(events, shellSessions, selfSessionID)
}

func deltaSlot(events []store.TimelineEvent, shellSessions map[string]bool, selfSessionID string) string {
	sessions := map[string]bool{}
	files := map[string]bool{}
	for _, e := range events {
		if e.SessionID != "" && e.SessionID != selfSessionID && !shellSessions[e.SessionID] {
			sessions[e.SessionID] = true
		}
		if e.Kind != payload.KindToolUse {
			continue
		}
		var tu payload.ToolUse
		if json.Unmarshal([]byte(e.Payload), &tu) != nil {
			continue
		}
		if tu.Path != "" && payload.IsMutatingFileTool(tu.Name) {
			files[tu.Path] = true
		}
	}

	var parts []string
	if len(sessions) > 0 {
		parts = append(parts, fmt.Sprintf("%d sessions", len(sessions)))
	}
	if len(files) > 0 {
		parts = append(parts, fmt.Sprintf("%d files touched", len(files)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Delta: " + strings.Join(parts, ", ")
}

// joinLines joins the non-empty lines with newlines.
func joinLines(lines ...string) string {
	var kept []string
	for _, l := range lines {
		if l != "" {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

// failure is the newest uncleared failure since the handoff. exit is 0 when
// the failure was reported without an exit code (a tool.result with
// is_error and no exit).
type failure struct {
	found bool
	cmd   string
	exit  int
	ts    time.Time
}

// lastFailure finds the newest uncleared failure in events (already
// restricted to those since the handoff, ordered by id): a shell `command`
// event with a non-zero exit, or a tool.result that is_error or carries a
// non-zero exit (its command is read back from the matching tool.use). A
// later result for the same subject clears it: the same Bash command text, or
// the same tool and path, coming back as a non-error tool.result or a shell
// event with exit 0. A different command passing says nothing about the one
// that failed, so it clears nothing.
func lastFailure(events []store.TimelineEvent) failure {
	type subject struct{ key, cmd string }
	toolSubject := map[string]subject{}
	var open []failure
	var keys []string
	clear := func(key string) {
		kept, keptKeys := open[:0], keys[:0]
		for i, f := range open {
			if keys[i] != key {
				kept = append(kept, f)
				keptKeys = append(keptKeys, keys[i])
			}
		}
		open, keys = kept, keptKeys
	}
	for _, e := range events {
		switch e.Kind {
		case payload.KindToolUse:
			var tu payload.ToolUse
			if json.Unmarshal([]byte(e.Payload), &tu) == nil && tu.ToolUseID != "" {
				sub := subject{cmd: tu.Command, key: "cmd:" + tu.Command}
				if tu.Command == "" {
					sub.cmd = tu.Name
					sub.key = "tool:" + tu.Name + ":" + tu.Path
				}
				toolSubject[tu.ToolUseID] = sub
			}
		case payload.KindToolResult:
			var tr payload.ToolResult
			if json.Unmarshal([]byte(e.Payload), &tr) != nil {
				continue
			}
			sub, known := toolSubject[tr.ToolUseID]
			failed := tr.IsError || (tr.Exit != nil && *tr.Exit != 0)
			if !failed {
				if known {
					clear(sub.key)
				}
				continue
			}
			if !known {
				sub = subject{cmd: "(command not recorded)", key: "result:" + tr.ToolUseID}
			}
			clear(sub.key)
			exit := 0
			if tr.Exit != nil {
				exit = *tr.Exit
			}
			open = append(open, failure{found: true, cmd: sub.cmd, exit: exit, ts: e.TS})
			keys = append(keys, sub.key)
		case payload.KindShellCommand:
			var sc payload.ShellCommand
			if json.Unmarshal([]byte(e.Payload), &sc) != nil {
				continue
			}
			key := "cmd:" + sc.Cmd
			clear(key)
			if sc.Exit == 0 {
				continue
			}
			open = append(open, failure{found: true, cmd: sc.Cmd, exit: sc.Exit, ts: e.TS})
			keys = append(keys, key)
		}
	}
	if len(open) == 0 {
		return failure{}
	}
	return open[len(open)-1]
}

// line renders `Last failure: <cmd> exit N (<age>)` (`failed` when no exit
// was reported), or "" when none.
func (f failure) line(now time.Time) string {
	if !f.found {
		return ""
	}
	cmd := strings.Join(strings.Fields(f.cmd), " ")
	if r := []rune(cmd); len(r) > maxFailureCmdRunes {
		cmd = string(r[:maxFailureCmdRunes]) + "…"
	}
	outcome := "failed"
	if f.exit != 0 {
		outcome = fmt.Sprintf("exit %d", f.exit)
	}
	return fmt.Sprintf("Last failure: %s %s (%s)", cmd, outcome, formatAge(now.Sub(f.ts)))
}

// formatAge renders d as a coarse age: "just now", "5m ago", "3h ago", "2d ago".
func formatAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}

// repoStateLine is slot 2's observed repo state, `Repo: branch <b> · <n>
// uncommitted · <k> commits since the handoff`: the branch and uncommitted
// count come from the newest non-shell session.git_state since the handoff
// (the last session end; with could_not_observe there is no count to show),
// the commits from the handoff's git stamp against the repo as it is now.
// Each part is shown only when observed; "" when none is.
func repoStateLine(p Params, handoff store.Record, hasHandoff bool, events []store.TimelineEvent, shellSessions map[string]bool) (string, error) {
	var gs *payload.SessionGitState
	for _, e := range events {
		if e.Kind != payload.KindSessionGitState || shellSessions[e.SessionID] {
			continue
		}
		var v payload.SessionGitState
		if json.Unmarshal([]byte(e.Payload), &v) == nil {
			gs = &v
		}
	}
	var parts []string
	if gs != nil && !gs.CouldNotObserve {
		if gs.Branch != "" {
			parts = append(parts, "branch "+gs.Branch)
		}
		if gs.UncommittedCount != nil {
			parts = append(parts, fmt.Sprintf("%d uncommitted", *gs.UncommittedCount))
		}
	}
	if hasHandoff && handoff.GitHead != "" {
		top := p.CWD
		if top == "" {
			proj, found, err := p.Store.GetProject(handoff.ProjectKey)
			if err != nil {
				return "", fmt.Errorf("block: handoff project: %w", err)
			}
			if found {
				top = proj.Toplevel
			}
		}
		if n, _, ok := project.CommitsSince(top, handoff.GitHead); ok {
			word := "commits"
			if n == 1 {
				word = "commit"
			}
			parts = append(parts, fmt.Sprintf("%d %s since the handoff", n, word))
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	return "Repo: " + strings.Join(parts, " · "), nil
}

// ledgerLine is slot 2's `Ledger: N decisions · M outcomes · K claims
// (newest <age>) — recall for them`, counting this location's current
// records so the agent knows whether a recall call is worth making. "" when
// all three counts are 0.
func ledgerLine(p Params) (string, error) {
	var ls store.LocationScope
	var err error
	if p.CWD != "" && p.Git != nil {
		ls, err = p.Store.LocationScope(p.CWD, p.Git, p.WorkspaceDirs)
	} else {
		ls, err = p.Store.LocationScopeForKey(p.ProjectKey)
	}
	if err != nil {
		return "", fmt.Errorf("block: ledger scope: %w", err)
	}
	c, err := p.Store.LedgerCounts(ls, p.Now)
	if err != nil {
		return "", fmt.Errorf("block: ledger counts: %w", err)
	}
	if c.Total() == 0 {
		return "", nil
	}
	return fmt.Sprintf("Ledger: %d decisions · %d outcomes · %d claims (newest %s) — recall for them",
		c.Decisions, c.Outcomes, c.Claims, formatAge(p.Now.Sub(c.Newest))), nil
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
// handoff is possibly stale (decision bcc9fa54), and whether a command failed
// since the handoff. The possibly-stale clause
// is appended only when staleHandoff is true, so a project with no flagged
// handoff renders the exact same "Attention: N unconfirmed draft(s), M
// contradiction(s)" text this slot always has.
func attentionSlot(draftCount, contradictionCount int, staleHandoff, failedCommand bool) string {
	if draftCount == 0 && contradictionCount == 0 && !staleHandoff && !failedCommand {
		return ""
	}
	line := fmt.Sprintf("Attention: %d unconfirmed draft(s), %d contradiction(s)", draftCount, contradictionCount)
	if staleHandoff {
		line += ", 1 possibly-stale handoff"
	}
	if failedCommand {
		line += ", 1 command failed since the handoff (see Last failure)"
	}
	return line
}
