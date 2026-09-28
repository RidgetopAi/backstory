// Package pi imports Pi's own transcripts
// (~/.pi/agent/sessions/--<cwd-slug>--/<iso-ts>_<uuid>.jsonl) into
// Backstory's store as backfilled sessions and timeline events (PLAN.md
// §Phase 3, decision 3e14db82 "Pi + local models in v1"). Unlike
// internal/backfill/claude's transcript, a Pi transcript is a TREE
// (id/parentId links, branches on edit-and-resend or regenerate), so this
// importer walks it (tree.go's walkOrder) instead of trusting file order.
package pi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

const (
	// Source is store.Event.Source and the first key of a backfill_cursors
	// row for every event and cursor this importer writes.
	Source = "pi"
	// EventSource is the timeline_events.source value for everything this
	// importer writes (SCHEMA.md enumerates it as `backfill`, matching
	// internal/backfill/claude.EventSource — the importer identity ("pi")
	// is the cursor key only, never the event source).
	EventSource = "backfill"
	// Agent is store.Session.Agent for every session this importer creates.
	Agent = "pi"

	EventSessionStart = payload.KindSessionStart
	EventToolUse      = payload.KindToolUse
	EventToolResult   = payload.KindToolResult
	EventSessionEnd   = payload.KindSessionEnd

	// PromptExcerptMaxRunes truncates the first user message stored on a
	// session's session.start event, same rationale and value as
	// internal/backfill/claude.PromptExcerptMaxRunes.
	PromptExcerptMaxRunes = 4000
)

// rootEnvVar is BACKSTORY_PI_ROOT: the daemon's override for the transcript
// root, checked before the ~/.pi/agent/sessions default. `backstory backfill
// pi` honours the same variable when --root is not given, matching
// internal/backfill/claude's BACKSTORY_CLAUDE_ROOT.
const rootEnvVar = "BACKSTORY_PI_ROOT"

// DefaultRoot resolves the transcript root when Options.Root is empty:
// $BACKSTORY_PI_ROOT if set, else ~/.pi/agent/sessions.
func DefaultRoot() (string, error) {
	if v := os.Getenv(rootEnvVar); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("backfill/pi: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".pi", "agent", "sessions"), nil
}

// Options configures Import.
type Options struct {
	// Root is the sessions/ directory containing one subdirectory per
	// cwd-slug, each holding that directory's *.jsonl transcripts. Empty
	// uses DefaultRoot().
	Root string
	// Git resolves project identity from a resolved cwd (AGENT-CONTRACT.md
	// §Project = git repository identity). Nil uses project.RealGit{}.
	Git project.Git
	// Workspaces lists the parent-of-many-repos dirs a resolved cwd is
	// checked against before falling back to a repo/path key (decision
	// bcc9fa54). Nil uses project.DefaultWorkspaceDirs().
	Workspaces []string
}

// Result is Import's one-line summary: `backstory backfill pi` prints it.
type Result struct {
	FilesScanned    int
	SessionsCreated int
	EventsCreated   int
	LinesSkipped    int
}

// String renders Result as a one-line summary, matching
// internal/backfill/claude.Result.String's shape.
func (r Result) String() string {
	return fmt.Sprintf("backfill pi: %d file(s), %d session(s), %d event(s), %d line(s) skipped",
		r.FilesScanned, r.SessionsCreated, r.EventsCreated, r.LinesSkipped)
}

// Import scans opts.Root (or its default) for *.jsonl transcripts and
// imports each one into st. It is safe to call repeatedly: a file already
// imported (a backfill_cursors row exists for it) contributes nothing on a
// rerun, and a root that does not exist is simply zero files, never an
// error — a machine with no ~/.pi installed must not fail the daemon's
// on-start run.
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
		// failed backfill (same rule as the daemon and the Claude importer).
		if ws, err := project.DefaultWorkspaceDirs(); err == nil {
			workspaces = ws
		}
	}

	files, err := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
	if err != nil {
		return Result{}, fmt.Errorf("backfill/pi: glob %s: %w", root, err)
	}
	sort.Strings(files)

	var res Result
	for _, f := range files {
		res.FilesScanned++
		stats, err := importFile(st, git, workspaces, f)
		if err != nil {
			return res, fmt.Errorf("backfill/pi: import %s: %w", f, err)
		}
		if stats.sessionCreated {
			res.SessionsCreated++
		}
		res.EventsCreated += stats.events
		res.LinesSkipped += stats.skipped
	}
	return res, nil
}

type fileStats struct {
	sessionCreated bool
	events         int
	skipped        int
}

// importFile imports path once: a saved backfill_cursors row for it means
// this exact file was already fully imported, and the whole file is
// skipped with zero new sessions/events (DONE WHEN clause 4). This differs
// from internal/backfill/claude's byte-offset resumption on purpose — a
// byte range read from the middle of a growing file has no way to know
// where a newly appended line's parentId places it in the tree, so a
// partial re-read could never be trusted to walk correctly; only a whole
// fresh read can.
func importFile(st *store.Store, git project.Git, workspaces []string, path string) (fileStats, error) {
	_, exists, err := st.GetBackfillCursor(Source, path)
	if err != nil {
		return fileStats{}, err
	}
	if exists {
		return fileStats{}, nil
	}

	raw, err := os.ReadFile(path) //nolint:gosec // path is produced by filepath.Glob over the configured backfill root, never caller input
	if err != nil {
		return fileStats{}, err
	}

	var stats fileStats
	var lines []treeLine
	for _, chunk := range bytes.Split(raw, []byte("\n")) {
		if len(bytes.TrimSpace(chunk)) == 0 {
			continue
		}
		l, perr := parseLine(chunk)
		if perr != nil || l.ID == "" {
			stats.skipped++
			continue
		}
		lines = append(lines, l)
	}
	if len(lines) == 0 {
		// Nothing usable at all: leave no cursor, so a later run (once the
		// file is complete/fixed) retries these same bytes rather than
		// silently losing them.
		return stats, nil
	}

	order, unreachable := walkOrder(lines)
	stats.skipped += unreachable
	if len(order) == 0 {
		// No type=="session" line reachable: cwd cannot be determined, and
		// it must never be guessed from the directory slug (DONE WHEN
		// clause 2) — skip the whole file, cursor left unset so a later,
		// complete write of it is retried.
		stats.skipped += len(lines)
		return stats, nil
	}
	root := order[0]
	if root.CWD == "" {
		stats.skipped += len(lines)
		return stats, nil
	}
	// Structural node types (model_change, thinking_level_change, a
	// system-role message) reach the walk but never produce a session/event
	// value of their own — counted skipped for the same reason Claude's
	// importer skips every line whose Type isn't user/assistant.
	for _, l := range order[1:] {
		switch {
		case l.Type == "message" && (l.Role == "user" || l.Role == "assistant"):
		case l.Type == "toolResult":
		default:
			stats.skipped++
		}
	}

	// A run captured live already minted a session for this exact
	// transcript (mirrors internal/backfill/claude's dedup, task 25b74537's
	// clause 2) — attach to it instead of minting a second, backfilled
	// session for the same run. Pi has no live capture path wired in yet,
	// so this is always a miss today; it costs nothing to hold the
	// invariant now rather than relearn it once one lands.
	sessionID := ""
	if live, ok, err := st.LiveSessionByHarnessSessionID(root.ID); err != nil {
		return fileStats{}, err
	} else if ok {
		sessionID = live.ID
	} else {
		sessionID, err = createSession(st, git, workspaces, root)
		if err != nil {
			return fileStats{}, err
		}
		stats.sessionCreated = true
	}

	if _, err := st.AppendEvent(store.Event{
		TS:        root.Timestamp,
		Kind:      EventSessionStart,
		SessionID: sessionID,
		Source:    EventSource,
		Payload:   sessionStartPayload(order),
	}); err != nil {
		return fileStats{}, err
	}
	stats.events++

	n, err := appendToolEvents(st, sessionID, order[1:])
	if err != nil {
		return fileStats{}, err
	}
	stats.events += n

	// A session whose origin is 'live' has its end-of-life owned exclusively
	// by the daemon (mirrors internal/backfill/claude's rule, task
	// 25b74537): this importer must never end one, or re-timestamp its
	// ending, once one exists.
	origin, err := st.SessionOrigin(sessionID)
	if err != nil {
		return fileStats{}, err
	}
	if origin != store.OriginLive {
		lastTS := order[len(order)-1].Timestamp
		endPayload, err := json.Marshal(payload.SessionEnd{Reason: "eof"})
		if err != nil {
			return fileStats{}, err
		}
		if _, err := st.AppendEvent(store.Event{
			TS: lastTS, Kind: EventSessionEnd, SessionID: sessionID,
			Source: EventSource, Payload: string(endPayload),
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
		ByteOffset: int64(len(raw)),
	}); err != nil {
		return fileStats{}, err
	}

	return stats, nil
}

// createSession mints a file's backfilled session. root.CWD (the type=="session"
// line's own cwd field) is the ONLY source of cwd this importer ever reads —
// never the directory slug (--<cwd-slug>--), which is lossy in exactly the
// same way Claude's slug is (DONE WHEN clause 2).
func createSession(st *store.Store, git project.Git, workspaces []string, root treeLine) (string, error) {
	projectKey := project.Key(root.CWD, git, workspaces)

	proj := store.Project{Key: projectKey, Toplevel: root.CWD, FirstSeen: time.Now()}
	if repo, ok := git.Repo(root.CWD); ok {
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
		HarnessSessionID: root.ID,
		CWD:              root.CWD,
		ProjectKey:       projectKey,
		StartedAt:        root.Timestamp,
		Origin:           store.OriginBackfilled,
	})
	// pid is left nil: a backfilled session was never a live process
	// (SCHEMA.md "pid NULL when backfilled").
}

// sessionStartPayload builds the session.start event payload: the first
// user message's text, in walk order (never file order, so a shuffled copy
// of the same tree picks the same message — DONE WHEN clause 1).
func sessionStartPayload(order []treeLine) string {
	var prompt string
	for _, l := range order {
		if l.Type == "message" && l.Role == "user" && l.Text != "" {
			prompt = l.Text
			break
		}
	}
	b, _ := json.Marshal(payload.SessionStart{Prompt: truncateRunes(prompt, PromptExcerptMaxRunes)})
	return string(b)
}

// appendToolEvents emits one tool.use per toolCall block and one
// tool.result per toolResult line, walking lines in tree order (walkOrder's
// output, never file order). A toolResult is paired with its toolCall
// exclusively by id (block.ID / line.ToolCallID) — DONE WHEN clause 3 and
// clause 5's mutation target: pairing by line order instead would silently
// mismatch a result to the wrong call the moment two calls' results don't
// come back in the order they were issued, which a branched tree (or even
// a single async provider) makes routine.
//
// A block/line whose id was already captured live is skipped rather than
// appended a second time, mirroring internal/backfill/claude.appendToolEvents:
// live capture runs ahead of backfill by construction.
func appendToolEvents(st *store.Store, sessionID string, lines []treeLine) (int, error) {
	n := 0
	for _, l := range lines {
		switch {
		case l.Type == "message" && l.Role == "assistant":
			blocks, err := contentBlocks(l.Content)
			if err != nil {
				continue
			}
			for _, b := range blocks {
				if b.Type != "toolCall" {
					continue
				}
				if b.ID != "" {
					dup, err := st.HasEventWithToolUseID(EventToolUse, b.ID)
					if err != nil {
						return n, err
					}
					if dup {
						continue
					}
				}
				payloadBytes, err := json.Marshal(toolUsePayload(b.ID, b.Name))
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
			}
		case l.Type == "toolResult":
			if l.ToolCallID == "" {
				continue
			}
			dup, err := st.HasEventWithToolUseID(EventToolResult, l.ToolCallID)
			if err != nil {
				return n, err
			}
			if dup {
				continue
			}
			payloadBytes, err := json.Marshal(payload.ToolResult{
				ToolUseID: l.ToolCallID, IsError: l.IsError, Content: blockText(l.Content),
			})
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

// toolUsePayload builds one toolCall block's payload. Path/Command are left
// unset: unlike Claude's tool_use.input (a documented, stable field set —
// file_path, command), Pi's toolCall.arguments shape is per-tool and not
// part of this punch's field list; guessing a field name here risks
// silently wrong data once a real transcript shows up. This importer is
// provider-agnostic by construction (name/arguments are read the same way
// regardless of which provider or model produced the call, so a
// local-llama transcript imports identically to a hosted one — DONE WHEN
// clause 3).
func toolUsePayload(id, name string) payload.ToolUse {
	return payload.ToolUse{ToolUseID: id, Name: name}
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
