package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
)

// SessionWorkEvidence is what one session's own timeline and ledger show
// about whether it changed anything and whether it wrote a handoff — the
// observed counts the Stop hook's one-time nudge is built from. It is read
// only: nothing here writes, and nothing here authors a handoff.
type SessionWorkEvidence struct {
	// EditedFiles counts the files the session changed: distinct paths among
	// its mutating tool.use events (payload.IsMutatingFileTool), plus one for
	// each such event that carries no path.
	EditedFiles int
	// FailedCommands counts the session's tool.result events that report a
	// failure: is_error set, or a real non-zero exit. An unobserved exit
	// (nil) is never read as a failure.
	FailedCommands int
	// HandoffWritten is true when a live (non-tombstoned) handoff record
	// carries this session's id.
	HandoffWritten bool
}

// SessionWorkEvidence reads sessionID's evidence. Events are scoped by
// session id alone, never by project, so a sibling session's edits never
// count toward this one.
func (s *Store) SessionWorkEvidence(sessionID string) (SessionWorkEvidence, error) {
	var ev SessionWorkEvidence

	uses, err := s.EventsForSessionTimeline(sessionID, time.Time{}, payload.KindToolUse, 0)
	if err != nil {
		return ev, err
	}
	paths := map[string]bool{}
	for _, e := range uses {
		var tu payload.ToolUse
		if json.Unmarshal([]byte(e.Payload), &tu) != nil || !payload.IsMutatingFileTool(tu.Name) {
			continue
		}
		if tu.Path == "" {
			ev.EditedFiles++
		} else if !paths[tu.Path] {
			paths[tu.Path] = true
			ev.EditedFiles++
		}
	}

	results, err := s.EventsForSessionTimeline(sessionID, time.Time{}, payload.KindToolResult, 0)
	if err != nil {
		return ev, err
	}
	for _, e := range results {
		var tr payload.ToolResult
		if json.Unmarshal([]byte(e.Payload), &tr) != nil {
			continue
		}
		if tr.IsError || (tr.Exit != nil && *tr.Exit != 0) {
			ev.FailedCommands++
		}
	}

	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM records
		WHERE session_id = ? AND kind = ? AND tombstoned_at IS NULL`,
		sessionID, string(KindHandoff)).Scan(&n); err != nil {
		return ev, fmt.Errorf("store: count handoffs for session %s: %w", sessionID, err)
	}
	ev.HandoffWritten = n > 0
	return ev, nil
}
