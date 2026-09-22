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

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

const (
	// Source is store.Event.Source and the first key of a backfill_cursors
	// row for every event and cursor this importer writes.
	Source = "claude"
	// Agent is store.Session.Agent for every session this importer creates.
	Agent = "claude"

	EventSessionStart = "session.start"
	EventToolUse      = "tool.use"
	EventToolResult   = "tool.result"
	EventSessionEnd   = "session.end"

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

	files, err := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
	if err != nil {
		return Result{}, fmt.Errorf("backfill/claude: glob %s: %w", root, err)
	}
	sort.Strings(files)

	var res Result
	for _, f := range files {
		res.FilesScanned++
		stats, err := importFile(st, git, f)
		if err != nil {
			return res, fmt.Errorf("backfill/claude: import %s: %w", f, err)
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

// importFile imports every line of path that a saved cursor has not
// already consumed. On a file's first import it also mints the session
// (cwd/branch/version/project resolved from the batch, session.start and
// session.end emitted); a later run against the same file only appends
// tool.use/tool.result events for whatever lines were appended since.
func importFile(st *store.Store, git project.Git, path string) (fileStats, error) {
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
		sessionID, err = createSession(st, git, path, parsedLines, firstTS)
		if err != nil {
			return fileStats{}, err
		}
		stats.sessionCreated = true

		if _, err := st.AppendEvent(store.Event{
			TS:        firstTS,
			Kind:      EventSessionStart,
			SessionID: sessionID,
			Source:    Source,
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

	if !exists {
		if _, err := st.AppendEvent(store.Event{
			TS:        lastTS,
			Kind:      EventSessionEnd,
			SessionID: sessionID,
			Source:    Source,
			Payload:   `{"reason":"eof"}`,
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

// createSession resolves cwd/branch/version/project identity from a file's
// batch of parsed lines and mints its backfilled session.
func createSession(st *store.Store, git project.Git, path string, lines []transcriptLine, firstTS time.Time) (string, error) {
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
	projectKey := project.Key(resolvedCWD, git)

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

	b, _ := json.Marshal(struct {
		Prompt    string `json:"prompt"`
		Version   string `json:"version,omitempty"`
		GitBranch string `json:"git_branch,omitempty"`
	}{Prompt: truncateRunes(prompt, PromptExcerptMaxRunes), Version: version, GitBranch: gitBranch})
	return string(b)
}

// appendToolEvents emits one tool.use per tool_use block and one
// tool.result per tool_result block, walking validIdx (already in file
// order) so rowid order matches file order regardless of each line's own
// timestamp (SCHEMA.md invariant 10).
func appendToolEvents(st *store.Store, sessionID string, lines []transcriptLine, validIdx []int) (int, error) {
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
				payload, err := json.Marshal(struct {
					ToolUseID string `json:"tool_use_id"`
					Name      string `json:"name"`
					Detail    string `json:"detail,omitempty"`
				}{ToolUseID: b.ID, Name: b.Name, Detail: toolDetail(b.Name, b.Input)})
				if err != nil {
					return n, err
				}
				if _, err := st.AppendEvent(store.Event{
					TS: l.Timestamp, Kind: EventToolUse, SessionID: sessionID,
					Source: Source, Payload: string(payload),
				}); err != nil {
					return n, err
				}
				n++
			case "tool_result":
				payload, err := json.Marshal(struct {
					ToolUseID string `json:"tool_use_id"`
					IsError   bool   `json:"is_error,omitempty"`
					Content   string `json:"content"`
				}{ToolUseID: b.ToolUseID, IsError: b.IsError, Content: blockText(b.Content)})
				if err != nil {
					return n, err
				}
				if _, err := st.AppendEvent(store.Event{
					TS: l.Timestamp, Kind: EventToolResult, SessionID: sessionID,
					Source: Source, Payload: string(payload),
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
