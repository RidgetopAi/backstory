package claude

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RidgetopAi/backstory/internal/store"
)

// subagentGlob matches exactly <root>/<slug>/<sessionId>/subagents/agent-*.jsonl
// — four path segments below root, never more. The bug this file fixes
// (task 6047db51) was a walk that globbed only one level
// (<root>/*/*.jsonl), so a subagent transcript at this nested path was
// never even opened. This glob is bounded to that one known layout on
// purpose: it must never recurse to unbounded depth looking for more
// nested transcripts.
func subagentGlob(root string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(root, "*", "*", "subagents", "agent-*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// importSubagents imports every subagent transcript under root, attributing
// each to the parent session whose id is the subagents/ directory's parent
// directory name (task 6047db51 DONE WHEN clause 1) — found by looking up
// the already-processed backfill cursor for that same directory name's
// <slug>/<sessionId>.jsonl main transcript, so attribution never guesses.
// A subagent whose parent transcript has no cursor row (never imported, or
// absent from this root entirely) is skipped, not an error (clause 4):
// there is no session to attribute it to.
func importSubagents(st *store.Store, root string) (Result, error) {
	files, err := subagentGlob(root)
	if err != nil {
		return Result{}, fmt.Errorf("backfill/claude: glob subagents under %s: %w", root, err)
	}

	var res Result
	for _, f := range files {
		sessionDir := filepath.Dir(filepath.Dir(f)) // .../<slug>/<sessionId>
		slugDir := filepath.Dir(sessionDir)          // .../<slug>
		sessionDirName := filepath.Base(sessionDir)  // <sessionId>
		mainPath := filepath.Join(slugDir, sessionDirName+".jsonl")

		cursor, exists, err := st.GetBackfillCursor(Source, mainPath)
		if err != nil {
			return res, fmt.Errorf("backfill/claude: lookup parent cursor for %s: %w", f, err)
		}
		if !exists {
			continue
		}

		res.FilesScanned++
		agentID := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		stats, err := importSubagentFile(st, f, cursor.SessionID, agentID)
		if err != nil {
			return res, fmt.Errorf("backfill/claude: import subagent %s: %w", f, err)
		}
		res.EventsCreated += stats.events
		res.LinesSkipped += stats.skipped
	}
	return res, nil
}

// importSubagentFile imports every line of a subagent transcript that a
// saved cursor — keyed by the subagent file's own path, source Source
// (clause 3) — has not already consumed, appending tool.use/tool.result
// events to the parent session. It never mints a session of its own: a
// subagent shares its parent's session (AGENT-CONTRACT.md: "Subagents
// share the parent process and therefore the parent session"), so unlike
// importFile there is no session.start/session.end here. A file whose
// every line is blank or fails to parse (the degenerate "malformed
// agent-*.jsonl" of clause 4) contributes nothing and leaves its cursor
// untouched, exactly like importFile — the caller simply moves on to the
// next file in the walk.
func importSubagentFile(st *store.Store, path, parentSessionID, agentID string) (fileStats, error) {
	cursor, exists, err := st.GetBackfillCursor(Source, path)
	if err != nil {
		return fileStats{}, err
	}
	var offset int64
	if exists {
		offset = cursor.ByteOffset
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
		// Every line in this batch was blank or malformed: the same
		// "leave the cursor alone, retry these bytes next run" rule
		// importFile applies, and (for a wholly malformed file) the
		// mechanism by which clause 4's degenerate file is skipped
		// without touching any other file in the walk.
		return stats, nil
	}

	n, err := appendToolEvents(st, parentSessionID, agentID, parsedLines, validIdx)
	if err != nil {
		return fileStats{}, err
	}
	stats.events += n

	if err := st.SetBackfillCursor(Source, path, store.BackfillCursor{
		SessionID:  parentSessionID,
		LastUUID:   lastUUID,
		ByteOffset: lastEnd,
	}); err != nil {
		return fileStats{}, err
	}

	return stats, nil
}
