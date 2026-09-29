// Package claude imports Claude Code's own transcripts
// (~/.claude/projects/<slug>/<sessionId>.jsonl) into Backstory's store as
// backfilled sessions and timeline events (PLAN.md §Phase 3).
package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

const (
	// Source is store.Event.Source and the first key of a backfill_cursors
	// row for every event and cursor this importer writes.
	Source = "claude"
	// EventSource is the timeline_events.source value for everything this importer
	// writes: SCHEMA.md enumerates it as `backfill` and retention keys on it. The
	// importer identity ("claude") is the cursor key only, never the event source.
	EventSource = "backfill"
	// Agent is store.Session.Agent for every session this importer creates.
	Agent = "claude"

	EventSessionStart = payload.KindSessionStart
	EventToolUse      = payload.KindToolUse
	EventToolResult   = payload.KindToolResult
	EventSessionEnd   = payload.KindSessionEnd

	// PromptExcerptMaxRunes truncates the first non-isMeta user prompt
	// stored on a session's session.start event. The transcript's own
	// prompt can be arbitrarily long; the event only needs enough to
	// identify the session, not the whole turn.
	PromptExcerptMaxRunes = 4000
)

// rootEnvVar is BACKSTORY_CLAUDE_ROOT (PLAN.md §Phase 3): the daemon's
// override for the transcript root, checked before the ~/.claude/projects
// default. `backstory backfill claude` honours the same variable when
// --root is not given, so the CLI and the daemon's on-start run agree.
const rootEnvVar = "BACKSTORY_CLAUDE_ROOT"

// DefaultRoot resolves the transcript root when Options.Root is empty:
// $BACKSTORY_CLAUDE_ROOT if set, else ~/.claude/projects.
func DefaultRoot() (string, error) {
	if v := os.Getenv(rootEnvVar); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("backfill/claude: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// Options configures Import.
type Options struct {
	// Root is the projects/ directory containing one subdirectory per
	// slug, each holding that project's *.jsonl transcripts. Empty uses
	// DefaultRoot().
	Root string
	// Git resolves project identity from a resolved cwd (AGENT-CONTRACT.md
	// §Project = git repository identity). Nil uses project.RealGit{}.
	Git project.Git
	// Workspaces lists the parent-of-many-repos dirs a resolved cwd is
	// checked against before falling back to a repo/path key (decision
	// bcc9fa54). Nil uses project.DefaultWorkspaceDirs().
	Workspaces []string
}

// Result is Import's one-line summary: `backstory backfill claude` prints
// it, and the daemon logs it after its on-start run.
type Result struct {
	FilesScanned    int
	SessionsCreated int
	EventsCreated   int
	LinesSkipped    int
}

// String renders Result as the one-line summary DONE WHEN clause 5 wants.
func (r Result) String() string {
	return fmt.Sprintf("backfill claude: %d file(s), %d session(s), %d event(s), %d line(s) skipped",
		r.FilesScanned, r.SessionsCreated, r.EventsCreated, r.LinesSkipped)
}

// Import scans opts.Root (or its default) for *.jsonl transcripts and
// imports every new line into st. It is safe to call repeatedly: a file
// already fully imported contributes nothing on a rerun (internal/store's
// backfill_cursors), and a root that does not exist is simply zero files,
// never an error — a fresh Omarchy install with no ~/.claude yet must not
// fail the daemon's on-start run.
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

	files, err := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
	if err != nil {
		return Result{}, fmt.Errorf("backfill/claude: glob %s: %w", root, err)
	}
	sort.Strings(files)

	var res Result
	for _, f := range files {
		res.FilesScanned++
		stats, err := importFile(st, git, workspaces, f)
		if err != nil {
			return res, fmt.Errorf("backfill/claude: import %s: %w", f, err)
		}
		if stats.sessionCreated {
			res.SessionsCreated++
		}
		res.EventsCreated += stats.events
		res.LinesSkipped += stats.skipped
	}

	subRes, err := importSubagents(st, root)
	if err != nil {
		return res, err
	}
	res.FilesScanned += subRes.FilesScanned
	res.EventsCreated += subRes.EventsCreated
	res.LinesSkipped += subRes.LinesSkipped

	return res, nil
}

type fileStats struct {
	sessionCreated bool
	events         int
	skipped        int
}

// importFile imports every line of path that a saved cursor has not
// already consumed. On a file's first import it also mints the session
// (cwd/branch/version/project resolved from the batch, session.start and
// session.end emitted); a later run against the same file only appends
// tool.use/tool.result events for whatever lines were appended since.
func importFile(st *store.Store, git project.Git, workspaces []string, path string) (fileStats, error) {
	cursor, exists, err := st.GetBackfillCursor(Source, path)
	if err != nil {
		return fileStats{}, err
	}
	var offset int64
	sessionID := ""
	if exists {
		offset = cursor.ByteOffset
		sessionID = cursor.SessionID
		// A purged session (backstory purge) is never re-imported, however
		// much its transcript has grown since.
		if purged, err := st.SessionPurged(sessionID); err != nil {
			return fileStats{}, err
		} else if purged {
			return fileStats{}, nil
		}
	}

	raws, err := readLinesFrom(path, offset)
	if err != nil {
		return fileStats{}, err
	}
	if len(raws) == 0 {
		return fileStats{}, nil
	}

	var stats fileStats
	lastUUID := cursor.LastUUID
	lastEnd := offset
	parsedLines := make([]transcriptLine, 0, len(raws))
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
		if line.UUID != "" {
			lastUUID = line.UUID
		}
		idx := len(parsedLines)
		parsedLines = append(parsedLines, line)
		if line.Type == "user" || line.Type == "assistant" {
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
		// A transcript never imported whose session was purged (a live
		// session the human erased) must not come back either.
		if purged, err := st.HarnessSessionPurged(firstHarnessSessionID(parsedLines)); err != nil {
			return fileStats{}, err
		} else if purged {
			return fileStats{}, nil
		}
		// A run captured live already minted a session for this exact
		// transcript (internal/mcp's startSession, keyed on the harness's
		// own declared session id — the same field this transcript's
		// sessionId carries) — attach to it instead of minting a second,
		// backfilled-origin session for the same run (task 25b74537's
		// clause 2: the daemon-side dedup task 32c6900d left unbuilt).
		if live, ok, err := st.LiveSessionByHarnessSessionID(firstHarnessSessionID(parsedLines)); err != nil {
			return fileStats{}, err
		} else if ok {
			sessionID = live.ID
		} else {
			sessionID, err = createSession(st, git, workspaces, path, parsedLines, firstTS)
			if err != nil {
				return fileStats{}, err
			}
			stats.sessionCreated = true
		}

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

	n, err := appendToolEvents(st, sessionID, "", parsedLines, validIdx)
	if err != nil {
		return fileStats{}, err
	}
	stats.events += n

	// A session whose origin is 'live' has its end-of-life owned exclusively
	// by the daemon (internal/mcp's SessionRegistry sweep, task 25b74537):
	// backfill must never append its own session.end or call EndSession for
	// one, on this run or any later rerun over the same still-growing
	// transcript — doing so would end (or re-timestamp the ending of) a run
	// the daemon may still consider live, and would double-count it in the
	// SessionStart block's delta the moment the daemon's own sweep later
	// disagrees. The cursor still advances either way, so a rerun never
	// reprocesses these bytes.
	origin, err := st.SessionOrigin(sessionID)
	if err != nil {
		return fileStats{}, err
	}
	if origin != store.OriginLive {
		// Every run that processes new lines re-mints session.end as the
		// session's current last event (append-only: an earlier run's
		// session.end is never deleted or rewritten, so a session that has
		// been backfilled twice has two session.end events, the later one
		// last by rowid) and moves sessions.ended_at to this batch's last
		// line — the transcript may still be growing, and each run's
		// session.end/ended_at reflects what had been written as of that run.
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
	}

	if err := st.SetBackfillCursor(Source, path, store.BackfillCursor{
		SessionID:  sessionID,
		LastUUID:   lastUUID,
		ByteOffset: lastEnd,
	}); err != nil {
		return fileStats{}, err
	}

	return stats, nil
}

// firstHarnessSessionID returns the first non-empty sessionId field among
// lines — the transcript's own harness-declared session id, which becomes
// harness_session_id on the session this file's import creates or attaches
// to, exactly like internal/mcp's startSession does for a live connection's
// declared "session" join key (socket.DeclaredFields). Empty when no line
// carries one, in which case a live-session match is never attempted (an
// empty harness_session_id would otherwise ambiguously match every live
// session with no declared id of its own).
func firstHarnessSessionID(lines []transcriptLine) string {
	for _, l := range lines {
		if l.SessionID != "" {
			return l.SessionID
		}
	}
	return ""
}

// createSession resolves cwd/branch/version/project identity from a file's
// batch of parsed lines and mints its backfilled session.
func createSession(st *store.Store, git project.Git, workspaces []string, path string, lines []transcriptLine, firstTS time.Time) (string, error) {
	var cwd, gitBranch, version, harnessSessionID string
	for _, l := range lines {
		if cwd == "" && l.CWD != "" {
			cwd = l.CWD
		}
		if gitBranch == "" && l.GitBranch != "" {
			gitBranch = l.GitBranch
		}
		if version == "" && l.Version != "" {
			version = l.Version
		}
		if harnessSessionID == "" && l.SessionID != "" {
			harnessSessionID = l.SessionID
		}
	}

	// slug → cwd → repo key (AGENT-CONTRACT.md §Project = git repository
	// identity; PLAN.md §Phase 3): the cwd field on the first line that
	// carries one wins, because the slug is lossy (a '-' in a real path is
	// indistinguishable from the slug's directory separator). Only a file
	// with no cwd on any line falls back to the slug.
	resolvedCWD := cwd
	if resolvedCWD == "" {
		resolvedCWD = slugToPath(filepath.Base(filepath.Dir(path)))
	}
	projectKey := project.Key(resolvedCWD, git, workspaces)

	proj := store.Project{Key: projectKey, Toplevel: resolvedCWD, FirstSeen: time.Now()}
	if repo, ok := git.Repo(resolvedCWD); ok {
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
		CWD:              resolvedCWD,
		ProjectKey:       projectKey,
		StartedAt:        firstTS,
		Origin:           store.OriginBackfilled,
	})
	// pid is left nil: a backfilled session was never a live process
	// (SCHEMA.md sessions.pid "NULL when backfilled").
}

// slugToPath reverses Claude Code's lossy slug encoding (cwd's '/' -> '-')
// back to a path. Used only when no line in the file carries a cwd.
func slugToPath(slug string) string {
	return strings.ReplaceAll(slug, "-", "/")
}

// sessionStartPayload builds the session.start event payload: the first
// non-isMeta user prompt (isMeta lines are harness-injected, never a human
// prompt), plus version/gitBranch when known.
func sessionStartPayload(lines []transcriptLine) string {
	var prompt, version, gitBranch string
	for _, l := range lines {
		if version == "" && l.Version != "" {
			version = l.Version
		}
		if gitBranch == "" && l.GitBranch != "" {
			gitBranch = l.GitBranch
		}
		if prompt != "" || l.Type != "user" || l.IsMeta || l.Message == nil {
			continue
		}
		blocks, err := contentBlocks(l.Message.Content)
		if err != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				prompt = b.Text
				break
			}
		}
	}

	b, _ := json.Marshal(payload.SessionStart{
		Prompt:    truncateRunes(prompt, PromptExcerptMaxRunes),
		Version:   version,
		GitBranch: gitBranch,
	})
	return string(b)
}

// appendToolEvents emits one tool.use per tool_use block and one
// tool.result per tool_result block, walking validIdx (already in file
// order) so rowid order matches file order regardless of each line's own
// timestamp (SCHEMA.md invariant 10). agentID is "" for the main-thread
// transcript, or a subagent transcript's own agent-<hex> identifier
// (task 6047db51) — carried on every payload emitted here so a subagent's
// events are attributable without a second query.
//
// A block whose tool_use_id was already captured live (cmd/backstory's
// `hook post-tool-use`, internal/mcp's handlePostToolUse) is skipped rather
// than appended a second time (task 04b1cb40's DONE WHEN clause 3): live
// capture runs ahead of backfill by construction — the daemon backfills a
// transcript once, on start or a later restart, well after PostToolUse
// already recorded the same tool call — so by the time this importer sees
// the line, a duplicate here is never legitimate history, only replay.
func appendToolEvents(st *store.Store, sessionID, agentID string, lines []transcriptLine, validIdx []int) (int, error) {
	n := 0
	for _, idx := range validIdx {
		l := lines[idx]
		if l.Message == nil {
			continue
		}
		blocks, err := contentBlocks(l.Message.Content)
		if err != nil {
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case "tool_use":
				if b.ID != "" {
					dup, err := st.HasEventWithToolUseID(EventToolUse, b.ID)
					if err != nil {
						return n, err
					}
					if dup {
						continue
					}
				}
				payloadBytes, err := json.Marshal(toolUsePayload(b.ID, b.Name, b.Input, agentID))
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
			case "tool_result":
				text := blockText(b.Content)
				tr := payload.ToolResult{
					ToolUseID: b.ToolUseID, IsError: b.IsError, Content: store.ToolOutputExcerpt(text),
					Exit: payload.ParseExitCodeLine(text), AgentID: agentID,
				}
				if b.ToolUseID != "" {
					// A live-captured tool.result for this id (content-less) is
					// ENRICHED with the transcript's outcome, never skipped
					// (task c9ab6d28).
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
