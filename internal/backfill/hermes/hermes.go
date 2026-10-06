// Package hermes imports Hermes Agent's own session database
// ($HERMES_HOME/state.db, default ~/.hermes/state.db; source hermes_state.py)
// into Backstory's store as backfilled sessions and timeline events
// (decision 3e14db82: Hermes in v1), mirroring internal/backfill/codex.
//
// Unlike the Claude and Codex importers, which tail append-only transcript
// files, Hermes keeps its own sessions in a SQLite database that its own
// process may be writing to at any moment. This importer therefore opens
// state.db read-only (mode=ro) and never writes to it: a backfill that could
// corrupt or lock the agent's own live database would be worse than no
// backfill at all.
package hermes

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/RidgetopAi/backstory/internal/backfill/capture"
	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver
)

const (
	// Source is store.Event.Source and the first key of a backfill_cursors
	// row for every event and cursor this importer writes.
	Source = "hermes"
	// EventSource is the timeline_events.source value for everything this
	// importer writes: SCHEMA.md enumerates it as `backfill` and retention
	// keys on it. The importer identity ("hermes") is the cursor key only,
	// never the event source.
	EventSource = "backfill"
	// Agent is store.Session.Agent for every session this importer creates.
	Agent = "hermes"

	EventSessionStart = payload.KindSessionStart
	EventToolUse      = payload.KindToolUse
	EventToolResult   = payload.KindToolResult
	EventSessionEnd   = payload.KindSessionEnd

	// PromptExcerptMaxRunes truncates the first user message stored on a
	// session's session.start event, the same cap internal/backfill/claude
	// and internal/backfill/codex apply.
	PromptExcerptMaxRunes = 4000

	// endedSentinel is BackfillCursor.LastUUID's value once a Hermes
	// session's session.end has been emitted — LastUUID carries a message
	// uuid for the Claude/Codex importers, but Hermes messages are
	// integer-keyed, so this importer repurposes the field as a one-shot
	// "already ended" flag instead: sessions.ended_at can only go from unset
	// to set, never back, so a rerun must never re-emit session.end once
	// this is recorded.
	endedSentinel = "ended"
)

// homeEnvVar is HERMES_HOME: Hermes Agent's own data directory, the same
// variable Hermes itself honours for state.db's location.
const homeEnvVar = "HERMES_HOME"

// DefaultPath resolves state.db's path when Options.Path is empty:
// $HERMES_HOME/state.db if set, else ~/.hermes/state.db.
func DefaultPath() (string, error) {
	if v := os.Getenv(homeEnvVar); v != "" {
		return filepath.Join(v, "state.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("backfill/hermes: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".hermes", "state.db"), nil
}

// Options configures Import.
type Options struct {
	// Path is state.db's location. Empty uses DefaultPath().
	Path string
	// Git resolves project identity from a resolved cwd (AGENT-CONTRACT.md
	// §Project = git repository identity). Nil uses project.RealGit{}.
	Git project.Git
	// Workspaces lists the parent-of-many-repos dirs a resolved cwd is
	// checked against before falling back to a repo/path key (decision
	// bcc9fa54). Nil uses project.DefaultWorkspaceDirs().
	Workspaces []string
	// Warn receives one message per session row skipped because its
	// started_at/ended_at cannot be parsed. Nil logs via the standard
	// logger. A bad row never aborts the import.
	Warn func(msg string)
	// CaptureOff reports whether capture is paused (SCHEMA.md invariant 8).
	// While it is, Import imports nothing; a session that started inside a
	// recorded pause window is never imported. Nil means no flag to consult.
	CaptureOff capture.OffFunc
}

// Result is Import's one-line summary: `backstory backfill hermes` prints
// it, and the daemon logs it after its on-start run.
type Result struct {
	SessionsCreated int
	EventsCreated   int
	MessagesSkipped int
}

// String renders Result as the one-line summary the CLI and daemon log.
func (r Result) String() string {
	return fmt.Sprintf("backfill hermes: %d session(s), %d event(s), %d message(s) skipped",
		r.SessionsCreated, r.EventsCreated, r.MessagesSkipped)
}

// readOnlyDSN builds a modernc.org/sqlite connection string that opens path
// strictly read-only: mode=ro at the SQLite VFS layer (the driver refuses
// any write, including the hot-journal/WAL bookkeeping a normal connection
// performs) plus PRAGMA query_only as defense in depth, and a busy_timeout
// so a query that lands mid-write on Hermes's own connection retries for a
// bit instead of failing immediately — the wait is entirely on this side;
// it never holds a lock that could make Hermes's own writer wait on us.
func readOnlyDSN(path string) string {
	q := url.Values{}
	q.Add("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	q.Add("_pragma", "busy_timeout(2000)")
	return "file:" + path + "?" + q.Encode()
}

// Import opens opts.Path (or its default) read-only and imports every
// session it holds into st, paired tool_calls/tool messages into
// tool.use/tool.result events. It is safe to call repeatedly: a session
// already fully imported (its session.end already emitted) contributes
// nothing further on a rerun, and a path that does not exist is simply zero
// sessions, never an error — a machine with no Hermes Agent installed must
// not fail the daemon's on-start run.
//
// Every session with no parent_session_id is imported before any session
// that names one, so a subagent's parent session always already exists by
// the time its own import looks it up (mirrors internal/backfill/codex).
func Import(st *store.Store, opts Options) (Result, error) {
	path := opts.Path
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return Result{}, err
		}
		path = p
	}
	if paused, err := capture.Off(opts.CaptureOff); err != nil {
		return Result{}, err
	} else if paused {
		return Result{}, nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return Result{}, nil
		}
		return Result{}, fmt.Errorf("backfill/hermes: stat %s: %w", path, err)
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

	db, err := sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		return Result{}, fmt.Errorf("backfill/hermes: open %s: %w", path, err)
	}
	defer func() { _ = db.Close() }()

	warn := opts.Warn
	if warn == nil {
		warn = func(msg string) { log.Print(msg) }
	}

	sessions, err := readSessions(db, warn)
	if err != nil {
		return Result{}, fmt.Errorf("backfill/hermes: read sessions: %w", err)
	}

	var mains, subs []hermesSession
	for _, hs := range sessions {
		if hs.ParentSessionID != "" {
			subs = append(subs, hs)
		} else {
			mains = append(mains, hs)
		}
	}

	var res Result
	for _, hs := range mains {
		if err := importSession(st, db, git, workspaces, path, hs, "", &res); err != nil {
			return res, fmt.Errorf("backfill/hermes: import session %s: %w", hs.ID, err)
		}
	}
	for _, hs := range subs {
		parentSessionID := ""
		if parent, ok, err := st.SessionByHarnessSessionID(hs.ParentSessionID); err != nil {
			return res, fmt.Errorf("backfill/hermes: lookup parent for %s: %w", hs.ID, err)
		} else if ok {
			parentSessionID = parent.ID
		}
		if err := importSession(st, db, git, workspaces, path, hs, parentSessionID, &res); err != nil {
			return res, fmt.Errorf("backfill/hermes: import session %s: %w", hs.ID, err)
		}
	}

	return res, nil
}

// hermesSession is one row of state.db's sessions table, the 0.19.0 columns
// this importer needs (id, parent_session_id, started_at, ended_at, cwd,
// git_branch, git_repo_root). source, model, title, message_count and
// tool_call_count exist on the real table but nothing here reads them.
type hermesSession struct {
	ID              string
	ParentSessionID string
	StartedAt       time.Time
	EndedAt         *time.Time
	CWD             string
	GitBranch       string
	GitRepoRoot     string
}

// readSessions loads every session row. A row whose started_at or ended_at
// cannot be parsed is skipped with one warning naming its id; it never
// aborts the rest of the import.
func readSessions(db *sql.DB, warn func(string)) ([]hermesSession, error) {
	rows, err := db.Query(`SELECT id, parent_session_id, started_at, ended_at, cwd, git_branch, git_repo_root
		FROM sessions ORDER BY started_at ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []hermesSession
	for rows.Next() {
		var (
			hs                                     hermesSession
			parentSessionID, cwd, branch, repoRoot sql.NullString
			startedAt                              string
			endedAt                                sql.NullString
		)
		if err := rows.Scan(&hs.ID, &parentSessionID, &startedAt, &endedAt, &cwd, &branch, &repoRoot); err != nil {
			return nil, fmt.Errorf("scan session row: %w", err)
		}
		hs.ParentSessionID = parentSessionID.String
		hs.CWD = cwd.String
		hs.GitBranch = branch.String
		hs.GitRepoRoot = repoRoot.String
		ts, err := parseTimestamp(startedAt)
		if err != nil {
			warn(fmt.Sprintf("backfill hermes: skipping session %s: started_at: %v", hs.ID, err))
			continue
		}
		hs.StartedAt = ts
		if endedAt.Valid && endedAt.String != "" {
			t, err := parseTimestamp(endedAt.String)
			if err != nil {
				warn(fmt.Sprintf("backfill hermes: skipping session %s: ended_at: %v", hs.ID, err))
				continue
			}
			hs.EndedAt = &t
		}
		out = append(out, hs)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// hermesMessage is one row of state.db's messages table, the 0.19.0 columns
// this importer needs. finish_reason and compacted exist on the real table
// but nothing here reads them.
type hermesMessage struct {
	ID         int64
	Role       string
	Content    string
	ToolCallID string
	ToolCalls  string
	Timestamp  time.Time
	Active     bool
}

// readMessages returns sessionID's messages with id > afterID, in id order
// (id order is the story order backfill_cursors resumes by, regardless of
// each row's own timestamp — the same invariant SCHEMA.md holds file-backed
// importers to).
func readMessages(db *sql.DB, sessionID string, afterID int64) ([]hermesMessage, error) {
	rows, err := db.Query(`SELECT id, role, content, tool_call_id, tool_calls, timestamp, active
		FROM messages WHERE session_id = ? AND id > ? ORDER BY id ASC`, sessionID, afterID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []hermesMessage
	for rows.Next() {
		var (
			m                            hermesMessage
			content, toolCallID, toolCls sql.NullString
			ts                           string
			active                       int64
		)
		if err := rows.Scan(&m.ID, &m.Role, &content, &toolCallID, &toolCls, &ts, &active); err != nil {
			return nil, fmt.Errorf("scan message row: %w", err)
		}
		m.Content = content.String
		m.ToolCallID = toolCallID.String
		m.ToolCalls = toolCls.String
		m.Active = active != 0
		parsed, err := parseTimestamp(ts)
		if err != nil {
			return nil, fmt.Errorf("message %d timestamp: %w", m.ID, err)
		}
		m.Timestamp = parsed
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// timestampLayouts are the encodings state.db's started_at/ended_at/
// timestamp columns may use: SQLAlchemy's default sqlite DateTime rendering
// (space-separated, microseconds) and plain RFC3339, in case a future
// Hermes version switches to it.
var timestampLayouts = []string{
	"2006-01-02 15:04:05.999999",
	"2006-01-02 15:04:05",
	time.RFC3339Nano,
	time.RFC3339,
}

// parseTimestamp accepts the layouts above and numeric epoch seconds
// (integer, fractional or scientific notation: Hermes stores REAL columns,
// which the sql scan renders like "1.7909040867422266e+09") as UTC,
// rounded to the millisecond.
func parseTimestamp(s string) (time.Time, error) {
	if f, err := strconv.ParseFloat(s, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
		ms := math.Round(f * 1000)
		if math.Abs(ms) < math.MaxInt64/2 {
			return time.UnixMilli(int64(ms)).UTC(), nil
		}
	}
	for _, layout := range timestampLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable timestamp %q", s)
}

// importSession imports one Hermes session's new messages into st. On a
// session's first import it mints the backfilled session (linked to
// parentSessionID when non-empty), emits session.start, and processes every
// active message the session holds so far; a later run only processes
// messages appended since (backfill_cursors, keyed on path+session id since
// one state.db holds many sessions, unlike the Claude/Codex importers' one
// cursor per file). session.end is emitted at most once, the first run that
// observes sessions.ended_at set.
func importSession(st *store.Store, db *sql.DB, git project.Git, workspaces []string, dbPath string, hs hermesSession, parentSessionID string, res *Result) error {
	cursorPath := dbPath + "#" + hs.ID
	cursor, exists, err := st.GetBackfillCursor(Source, cursorPath)
	if err != nil {
		return err
	}
	sessionID := cursor.SessionID
	if exists {
		// A purged session (backstory purge) is never re-imported.
		if purged, err := st.SessionPurged(sessionID); err != nil {
			return err
		} else if purged {
			return nil
		}
	}
	lastMsgID := cursor.ByteOffset
	ended := cursor.LastUUID == endedSentinel

	msgs, err := readMessages(db, hs.ID, lastMsgID)
	if err != nil {
		return err
	}

	active := make([]hermesMessage, 0, len(msgs))
	for _, m := range msgs {
		if !m.Active {
			res.MessagesSkipped++
			continue
		}
		active = append(active, m)
	}

	// A message with no usable timestamp takes the last observed one (the
	// session's own start before the first valid message); never a zero time.
	if !capture.ValidTS(hs.StartedAt) {
		for _, m := range active {
			if capture.ValidTS(m.Timestamp) {
				hs.StartedAt = m.Timestamp
				break
			}
		}
		if !capture.ValidTS(hs.StartedAt) && !exists {
			// No usable time anywhere: nothing to anchor a new session on.
			return nil
		}
	}
	prev := hs.StartedAt
	for i := range active {
		if capture.ValidTS(active[i].Timestamp) {
			prev = active[i].Timestamp
		} else if capture.ValidTS(prev) {
			active[i].Timestamp = prev
		}
	}
	// Anything still untimed (an existing session whose start is itself
	// unusable and which has no earlier valid message in this batch) is
	// dropped rather than written with an invented time.
	timed := active[:0]
	for _, m := range active {
		if capture.ValidTS(m.Timestamp) {
			timed = append(timed, m)
		}
	}
	active = timed
	endedAt := hs.EndedAt
	if endedAt != nil && !capture.ValidTS(*endedAt) {
		// ended_at present but unusable: end at the session's last observed
		// event time, or leave the end out entirely when there is none.
		endedAt = nil
		if capture.ValidTS(prev) {
			endedAt = &prev
		}
	}

	// attachedLive: this Hermes session is still being captured live (origin
	// 'live', same harness session id) — attach to it instead of minting a
	// second, backfilled session for the same run, exactly like the Claude
	// and Pi importers. The live session already carries its own
	// session.start, and its end-of-life belongs to the daemon alone.
	attachedLive := false
	if !exists {
		// Started during a capture pause: never imported (invariant 8).
		if paused, err := capture.StartedInPause(st, hs.StartedAt); err != nil {
			return err
		} else if paused {
			return nil
		}
		if live, ok, err := st.LiveSessionByHarnessSessionID(hs.ID); err != nil {
			return err
		} else if ok {
			sessionID = live.ID
			attachedLive = true
		}
	}

	if !exists && !attachedLive {
		sessionID, err = createSession(st, git, workspaces, hs, parentSessionID)
		if err != nil {
			return err
		}
		res.SessionsCreated++

		if _, err := st.AppendEvent(store.Event{
			TS:        hs.StartedAt,
			Kind:      EventSessionStart,
			SessionID: sessionID,
			Source:    EventSource,
			Payload:   sessionStartPayload(hs, active),
		}); err != nil {
			return err
		}
		res.EventsCreated++
	}

	n, err := appendToolEvents(st, sessionID, active)
	if err != nil {
		return err
	}
	res.EventsCreated += n

	if len(msgs) > 0 {
		lastMsgID = msgs[len(msgs)-1].ID
	}

	// Re-check origin every run: a cursor attached to a live session must
	// never end it either, and a session the daemon ended is already ended.
	origin, err := st.SessionOrigin(sessionID)
	if err != nil {
		return err
	}
	if !ended && endedAt != nil && origin != store.OriginLive {
		endPayload, _ := json.Marshal(payload.SessionEnd{Reason: "eof"})
		if _, err := st.AppendEvent(store.Event{
			TS:        *endedAt,
			Kind:      EventSessionEnd,
			SessionID: sessionID,
			Source:    EventSource,
			Payload:   string(endPayload),
		}); err != nil {
			return err
		}
		res.EventsCreated++

		if err := st.EndSession(sessionID, *endedAt, "backfill"); err != nil {
			return err
		}
		ended = true
	}

	lastUUID := ""
	if ended {
		lastUUID = endedSentinel
	}
	return st.SetBackfillCursor(Source, cursorPath, store.BackfillCursor{
		SessionID:  sessionID,
		LastUUID:   lastUUID,
		ByteOffset: lastMsgID,
	})
}

// createSession resolves cwd/project identity from hs and mints its
// backfilled session, linked to parentSessionID when non-empty. cwd falls
// back to git_repo_root when Hermes recorded no cwd at all (project key is
// "from cwd / git_repo_root via project.Key" per this importer's punch).
func createSession(st *store.Store, git project.Git, workspaces []string, hs hermesSession, parentSessionID string) (string, error) {
	cwd := hs.CWD
	if cwd == "" {
		cwd = hs.GitRepoRoot
	}
	projectKey := project.Key(cwd, git, workspaces)

	proj := store.Project{Key: projectKey, Toplevel: cwd, FirstSeen: time.Now()}
	if repo, ok := git.Repo(cwd); ok {
		proj.GitCommonDir = repo.CommonDir
		proj.RemoteURL = repo.RemoteURL
		if repo.Toplevel != "" {
			proj.Toplevel = repo.Toplevel
		}
	} else if hs.GitRepoRoot != "" {
		proj.Toplevel = hs.GitRepoRoot
	}
	if err := st.UpsertProject(proj); err != nil {
		return "", err
	}

	return st.StartSession(store.StartSessionParams{
		Agent:            Agent,
		HarnessSessionID: hs.ID,
		CWD:              cwd,
		ProjectKey:       projectKey,
		StartedAt:        hs.StartedAt,
		Origin:           store.OriginBackfilled,
		ParentSessionID:  parentSessionID,
	})
	// pid is left nil: a backfilled session was never a live process
	// (SCHEMA.md sessions.pid "NULL when backfilled").
}

// sessionStartPayload builds the session.start event payload from the
// session's git_branch and its first active user message's content.
func sessionStartPayload(hs hermesSession, active []hermesMessage) string {
	var prompt string
	for _, m := range active {
		if m.Role == "user" && m.Content != "" {
			prompt = m.Content
			break
		}
	}
	b, _ := json.Marshal(payload.SessionStart{
		Prompt:    truncateRunes(prompt, PromptExcerptMaxRunes),
		GitBranch: hs.GitBranch,
	})
	return string(b)
}

// toolCallJSON is one entry of an assistant message's tool_calls JSON array.
// Arguments is left raw: only the tool-specific extraction in tools.go
// looks inside it, and only for the couple of field names it recognizes.
type toolCallJSON struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// appendToolEvents emits one tool.use per assistant tool_calls entry and one
// tool.result per role=tool message, paired by tool_call_id — read straight
// off each row, never inferred from position, so calls and results that
// complete out of order still attribute correctly. HasEventWithToolUseID
// guards against re-emitting a tool event a previous run (or a live capture
// of the same call id) already recorded.
func appendToolEvents(st *store.Store, sessionID string, active []hermesMessage) (int, error) {
	n := 0
	for _, m := range active {
		switch {
		case m.Role == "assistant" && m.ToolCalls != "":
			var calls []toolCallJSON
			if err := json.Unmarshal([]byte(m.ToolCalls), &calls); err != nil {
				continue
			}
			for _, call := range calls {
				if call.ID != "" {
					dup, err := st.HasEventWithToolUseID(EventToolUse, call.ID)
					if err != nil {
						return n, err
					}
					if dup {
						continue
					}
				}
				payloadBytes, err := json.Marshal(toolUsePayload(call))
				if err != nil {
					return n, err
				}
				if _, err := st.AppendEvent(store.Event{
					TS: m.Timestamp, Kind: EventToolUse, SessionID: sessionID,
					Source: EventSource, Payload: string(payloadBytes),
				}); err != nil {
					return n, err
				}
				n++
			}
		case m.Role == "tool" && m.ToolCallID != "":
			tr := payload.ToolResult{
				ToolUseID: m.ToolCallID,
				Content:   store.ToolOutputExcerpt(m.Content),
			}
			found, err := st.ReconcileToolResult(tr)
			if err != nil {
				return n, err
			}
			if found {
				continue
			}
			payloadBytes, err := json.Marshal(tr)
			if err != nil {
				return n, err
			}
			if _, err := st.AppendEvent(store.Event{
				TS: m.Timestamp, Kind: EventToolResult, SessionID: sessionID,
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
