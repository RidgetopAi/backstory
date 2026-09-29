// Package codex imports Codex CLI's own rollout transcripts
// (~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl) into Backstory's
// store as backfilled sessions and timeline events (decision 3e14db82:
// Codex in v1), mirroring internal/backfill/claude.
package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

const (
	// Source is store.Event.Source and the first key of a backfill_cursors
	// row for every event and cursor this importer writes.
	Source = "codex"
	// EventSource is the timeline_events.source value for everything this
	// importer writes: SCHEMA.md enumerates it as `backfill` and retention
	// keys on it. The importer identity ("codex") is the cursor key only,
	// never the event source.
	EventSource = "backfill"
	// Agent is store.Session.Agent for every session this importer creates.
	Agent = "codex"

	EventSessionStart = payload.KindSessionStart
	EventToolUse      = payload.KindToolUse
	EventToolResult   = payload.KindToolResult
	EventSessionEnd   = payload.KindSessionEnd

	// PromptExcerptMaxRunes truncates the first user message stored on a
	// session's session.start event, the same cap internal/backfill/claude
	// applies: the transcript's own prompt can be arbitrarily long, and the
	// event only needs enough to identify the session.
	PromptExcerptMaxRunes = 4000
)

// rootEnvVar is BACKSTORY_CODEX_ROOT: the daemon's override for the rollout
// root, checked before the ~/.codex/sessions default. `backstory backfill
// codex` honours the same variable when --root is not given, so the CLI and
// the daemon's on-start run agree (mirrors internal/backfill/claude's
// BACKSTORY_CLAUDE_ROOT).
const rootEnvVar = "BACKSTORY_CODEX_ROOT"

// DefaultRoot resolves the rollout root when Options.Root is empty:
// $BACKSTORY_CODEX_ROOT if set, else ~/.codex/sessions.
func DefaultRoot() (string, error) {
	if v := os.Getenv(rootEnvVar); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("backfill/codex: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".codex", "sessions"), nil
}

// Options configures Import.
type Options struct {
	// Root is the sessions/ directory containing YYYY/MM/DD subdirectories
	// of rollout-*.jsonl files. Empty uses DefaultRoot().
	Root string
	// Git resolves project identity from a resolved cwd (AGENT-CONTRACT.md
	// §Project = git repository identity). Nil uses project.RealGit{}.
	Git project.Git
	// Workspaces lists the parent-of-many-repos dirs a resolved cwd is
	// checked against before falling back to a repo/path key (decision
	// bcc9fa54). Nil uses project.DefaultWorkspaceDirs().
	Workspaces []string
}

// Result is Import's one-line summary: `backstory backfill codex` prints
// it, and the daemon logs it after its on-start run.
type Result struct {
	FilesScanned    int
	SessionsCreated int
	EventsCreated   int
	LinesSkipped    int
	// PartialFiles counts rollouts whose final line was truncated (no
	// trailing newline) on this run — the harness may still be mid-write on
	// them (DONE WHEN clause 3). Every complete line in such a file still
	// imports; the run itself never fails over it.
	PartialFiles int
}

// String renders Result as the one-line summary DONE WHEN clause 6 wants.
func (r Result) String() string {
	return fmt.Sprintf("backfill codex: %d file(s), %d session(s), %d event(s), %d line(s) skipped, %d file(s) partial",
		r.FilesScanned, r.SessionsCreated, r.EventsCreated, r.LinesSkipped, r.PartialFiles)
}

// record folds one file's stats into the running Result.
func (r *Result) record(stats fileStats) {
	if stats.sessionCreated {
		r.SessionsCreated++
	}
	r.EventsCreated += stats.events
	r.LinesSkipped += stats.skipped
	if stats.partial {
		r.PartialFiles++
	}
}

// Import scans opts.Root (or its default) for rollout-*.jsonl transcripts
// and imports every new line into st. It is safe to call repeatedly: a file
// already fully imported contributes nothing on a rerun (internal/store's
// backfill_cursors), and a root that does not exist is simply zero files,
// never an error — a fresh install with no ~/.codex yet must not fail the
// daemon's on-start run.
//
// Every top-level rollout (no parent_thread_id) is imported before any
// subagent rollout, regardless of filename order, so a subagent's parent
// session always already exists by the time its own import looks it up
// (task e9cb97dd DONE WHEN clause 1).
func Import(st *store.Store, opts Options) (Result, error) {
	root := opts.Root
	if root == "" {
		r, err := DefaultRoot()
		if err != nil {
			return Result{}, err
		}
		root = r
	}
	git := opts.Git
	if git == nil {
		git = project.RealGit{}
	}
	workspaces := opts.Workspaces
	if workspaces == nil {
		// An unresolvable home dir means no default workspace, never a
		// failed backfill (same rule as the daemon).
		if ws, err := project.DefaultWorkspaceDirs(); err == nil {
			workspaces = ws
		}
	}

	files, err := walkRollouts(root)
	if err != nil {
		return Result{}, fmt.Errorf("backfill/codex: walk %s: %w", root, err)
	}

	type classified struct {
		path string
		meta sessionMetaPayload
	}
	var mainFiles, subFiles []classified
	for _, f := range files {
		meta, ok, err := peekSessionMeta(f)
		if err != nil {
			return Result{}, fmt.Errorf("backfill/codex: peek %s: %w", f, err)
		}
		if ok && meta.ParentThreadID != "" {
			subFiles = append(subFiles, classified{path: f, meta: meta})
		} else {
			mainFiles = append(mainFiles, classified{path: f, meta: meta})
		}
	}

	var res Result
	for _, c := range mainFiles {
		res.FilesScanned++
		stats, err := importFile(st, git, workspaces, c.path, "")
		if err != nil {
			return res, fmt.Errorf("backfill/codex: import %s: %w", c.path, err)
		}
		res.record(stats)
	}
	for _, c := range subFiles {
		res.FilesScanned++
		parentSessionID := ""
		if parent, ok, err := st.SessionByHarnessSessionID(c.meta.ParentThreadID); err != nil {
			return res, fmt.Errorf("backfill/codex: lookup parent for %s: %w", c.path, err)
		} else if ok {
			parentSessionID = parent.ID
		}
		stats, err := importFile(st, git, workspaces, c.path, parentSessionID)
		if err != nil {
			return res, fmt.Errorf("backfill/codex: import %s: %w", c.path, err)
		}
		res.record(stats)
	}

	return res, nil
}

type fileStats struct {
	sessionCreated bool
	events         int
	skipped        int
	partial        bool
}

// importFile imports every line of path that a saved cursor has not already
// consumed. On a file's first import it also mints the session (cwd/version
// resolved from the batch, session.start and session.end emitted, linked to
// parentSessionID when non-empty); a later run against the same file only
// appends tool.use/tool.result events for whatever complete lines were
// appended since, and re-emits session.end at the batch's new last line —
// unlike internal/backfill/claude, a Codex session is never 'live' (there is
// no live Codex capture yet to hand off to), so every rerun over new lines
// safely owns its own session.end.
func importFile(st *store.Store, git project.Git, workspaces []string, path, parentSessionID string) (fileStats, error) {
	cursor, exists, err := st.GetBackfillCursor(Source, path)
	if err != nil {
		return fileStats{}, err
	}
	var offset int64
	sessionID := ""
	if exists {
		offset = cursor.ByteOffset
		sessionID = cursor.SessionID
	}

	raws, partial, err := readLinesFrom(path, offset)
	if err != nil {
		return fileStats{}, err
	}
	stats := fileStats{partial: partial}
	if len(raws) == 0 {
		return stats, nil
	}

	lastEnd := offset
	parsedLines := make([]rolloutLine, 0, len(raws))
	var validIdx []int

	for _, rl := range raws {
		lastEnd = rl.End
		if len(bytes.TrimSpace(rl.Text)) == 0 {
			continue
		}
		line, perr := parseLine(rl.Text)
		if perr != nil {
			stats.skipped++
			continue
		}
		idx := len(parsedLines)
		parsedLines = append(parsedLines, line)
		if line.Type == "response_item" {
			validIdx = append(validIdx, idx)
		} else {
			stats.skipped++
		}
	}

	if len(parsedLines) == 0 {
		// Every line in this batch was blank or malformed: nothing to
		// anchor a new session on, and nothing to append to an existing
		// one. The cursor is deliberately left untouched so the next run
		// retries the same bytes rather than silently losing them.
		return stats, nil
	}

	firstTS := parsedLines[0].Timestamp
	lastTS := parsedLines[len(parsedLines)-1].Timestamp

	if !exists {
		sessionID, err = createSession(st, git, workspaces, parsedLines, firstTS, parentSessionID)
		if err != nil {
			return fileStats{}, err
		}
		stats.sessionCreated = true

		if _, err := st.AppendEvent(store.Event{
			TS:        firstTS,
			Kind:      EventSessionStart,
			SessionID: sessionID,
			Source:    EventSource,
			Payload:   sessionStartPayload(parsedLines),
		}); err != nil {
			return fileStats{}, err
		}
		stats.events++
	}

	n, err := appendToolEvents(st, sessionID, parsedLines, validIdx)
	if err != nil {
		return fileStats{}, err
	}
	stats.events += n

	// Every run that processes new lines re-mints session.end as the
	// session's current last event (append-only: an earlier run's
	// session.end is never deleted or rewritten) and moves sessions.ended_at
	// to this batch's last line, mirroring internal/backfill/claude's own
	// rule for a still-growing transcript.
	sessionEndPayload, _ := json.Marshal(payload.SessionEnd{Reason: "eof"})
	if _, err := st.AppendEvent(store.Event{
		TS:        lastTS,
		Kind:      EventSessionEnd,
		SessionID: sessionID,
		Source:    EventSource,
		Payload:   string(sessionEndPayload),
	}); err != nil {
		return fileStats{}, err
	}
	stats.events++

	if err := st.EndSession(sessionID, lastTS, "backfill"); err != nil {
		return fileStats{}, err
	}

	if err := st.SetBackfillCursor(Source, path, store.BackfillCursor{
		SessionID:  sessionID,
		ByteOffset: lastEnd,
	}); err != nil {
		return fileStats{}, err
	}

	return stats, nil
}

// createSession resolves cwd/version/harness-session identity from a file's
// batch of parsed lines and mints its backfilled session, linked to
// parentSessionID when non-empty (task e9cb97dd).
func createSession(st *store.Store, git project.Git, workspaces []string, lines []rolloutLine, firstTS time.Time, parentSessionID string) (string, error) {
	var cwd, version, harnessSessionID string
	for _, l := range lines {
		switch l.Type {
		case "session_meta":
			meta, err := parseSessionMeta(l.Payload)
			if err != nil {
				continue
			}
			if cwd == "" && meta.CWD != "" {
				cwd = meta.CWD
			}
			if version == "" && meta.CLIVersion != "" {
				version = meta.CLIVersion
			}
			if harnessSessionID == "" && meta.ID != "" {
				harnessSessionID = meta.ID
			}
		case "turn_context":
			tc, err := parseTurnContext(l.Payload)
			if err != nil {
				continue
			}
			if cwd == "" && tc.CWD != "" {
				cwd = tc.CWD
			}
		}
	}

	projectKey := project.Key(cwd, git, workspaces)

	proj := store.Project{Key: projectKey, Toplevel: cwd, FirstSeen: time.Now()}
	if repo, ok := git.Repo(cwd); ok {
		proj.GitCommonDir = repo.CommonDir
		proj.RemoteURL = repo.RemoteURL
		if repo.Toplevel != "" {
			proj.Toplevel = repo.Toplevel
		}
	}
	if err := st.UpsertProject(proj); err != nil {
		return "", err
	}

	return st.StartSession(store.StartSessionParams{
		Agent:            Agent,
		HarnessSessionID: harnessSessionID,
		CWD:              cwd,
		ProjectKey:       projectKey,
		StartedAt:        firstTS,
		Origin:           store.OriginBackfilled,
		ParentSessionID:  parentSessionID,
	})
	// pid is left nil: a backfilled session was never a live process
	// (SCHEMA.md sessions.pid "NULL when backfilled").
}

// sessionStartPayload builds the session.start event payload: the first
// user message's text, plus the cli_version session_meta carried.
func sessionStartPayload(lines []rolloutLine) string {
	var prompt, version string
	for _, l := range lines {
		switch l.Type {
		case "session_meta":
			if version != "" {
				continue
			}
			meta, err := parseSessionMeta(l.Payload)
			if err == nil && meta.CLIVersion != "" {
				version = meta.CLIVersion
			}
		case "response_item":
			if prompt != "" {
				continue
			}
			item, err := parseResponseItem(l.Payload)
			if err != nil || item.Type != "message" || item.Role != "user" {
				continue
			}
			if text := contentText(item.Content); text != "" {
				prompt = text
			}
		}
	}

	b, _ := json.Marshal(payload.SessionStart{
		Prompt:  truncateRunes(prompt, PromptExcerptMaxRunes),
		Version: version,
	})
	return string(b)
}

// appendToolEvents emits one tool.use per function_call/custom_tool_call
// item and one tool.result per function_call_output/custom_tool_call_output
// item, walking validIdx (already in file order) so rowid order matches
// file order regardless of each line's own timestamp (SCHEMA.md invariant
// 10). Every pairing is keyed on the item's own call_id field — read
// straight off each line, never inferred from its position among the
// batch's calls or outputs — so calls and outputs that complete out of
// order (a later call's output returning before an earlier call's) still
// attribute correctly (DONE WHEN clause 5).
func appendToolEvents(st *store.Store, sessionID string, lines []rolloutLine, validIdx []int) (int, error) {
	n := 0
	for _, idx := range validIdx {
		l := lines[idx]
		item, err := parseResponseItem(l.Payload)
		if err != nil {
			continue
		}
		switch item.Type {
		case "function_call", "custom_tool_call":
			if item.CallID != "" {
				dup, err := st.HasEventWithToolUseID(EventToolUse, item.CallID)
				if err != nil {
					return n, err
				}
				if dup {
					continue
				}
			}
			payloadBytes, err := json.Marshal(toolUsePayload(item))
			if err != nil {
				return n, err
			}
			if _, err := st.AppendEvent(store.Event{
				TS: l.Timestamp, Kind: EventToolUse, SessionID: sessionID,
				Source: EventSource, Payload: string(payloadBytes),
			}); err != nil {
				return n, err
			}
			n++
		case "function_call_output", "custom_tool_call_output":
			tr := payload.ToolResult{
				ToolUseID: item.CallID,
				Content:   store.ToolOutputExcerpt(item.Output),
			}
			if item.CallID != "" {
				found, err := st.ReconcileToolResult(tr)
				if err != nil {
					return n, err
				}
				if found {
					continue
				}
			}
			payloadBytes, err := json.Marshal(tr)
			if err != nil {
				return n, err
			}
			if _, err := st.AppendEvent(store.Event{
				TS: l.Timestamp, Kind: EventToolResult, SessionID: sessionID,
				Source: EventSource, Payload: string(payloadBytes),
			}); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

// truncateRunes cuts s to at most max runes, appending an ellipsis when it
// does. Rune-safe so a multibyte character is never split.
func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}
