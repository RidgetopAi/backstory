package mcp

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/store"
)

// timelineScopeSession and timelineScopeProject are TimelineParams.Scope's
// accepted wire values (tools.go's frozen v0 schema enum). An empty scope
// defaults to project — the same "unscoped means my whole project" default
// `backstory timeline`'s CLI already has (cmd/backstory/timeline.go).
const (
	timelineScopeSession = "session"
	timelineScopeProject = "project"
)

// TimelineParams is timeline's argument shape, frozen at v0
// (tools.go's ToolsV0, testdata/tools-v0.json): scope, since, kind, limit.
// It deliberately has no Project or Session field even though a caller
// might expect one: scope=project always means the caller's OWN observed
// project and scope=session always means the caller's OWN observed
// session, never a declared one — the same never-list rule NoteParams
// applies to tier/session, applied here to timeline's own scope
// (AGENT-CONTRACT.md §The never-list, item 2; §Observed identity).
type TimelineParams struct {
	Scope string `json:"scope,omitempty"`
	Since string `json:"since,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// timelineRecordLineMaxRunes bounds the first line of a linked record's text
// inlined on a timeline event (task d0c8c84b); a longer line is cut with an
// ellipsis. The full text stays one recall away.
const timelineRecordLineMaxRunes = 200

// TimelineEventResult is one timeline_events row rendered for the wire: ID
// is always the event's rowid, so a caller can quote it back verbatim in a
// later note's `evidence` (AGENT-CONTRACT.md §timeline — "the observed
// truth an agent cites as evidence").
type TimelineEventResult struct {
	ID      int64           `json:"id"`
	TS      string          `json:"ts"`
	Kind    string          `json:"kind"`
	Source  string          `json:"source"`
	Payload json.RawMessage `json:"payload,omitempty"`
	// RecordID, RecordKind and RecordFirstLine are additive (task d0c8c84b):
	// present only on a tool.use event whose payload names a record_id (a
	// Backstory note call), joined from records at read time. RecordKind and
	// RecordFirstLine are omitted when the record is not in the caller's own
	// project, does not exist, or is tombstoned.
	RecordID        string `json:"record_id,omitempty"`
	RecordKind      string `json:"record_kind,omitempty"`
	RecordFirstLine string `json:"record_first_line,omitempty"`
}

// firstLine returns text's first non-blank line, cut to max runes.
func firstLine(text string, max int) string {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if r := []rune(l); len(r) > max {
			return string(r[:max]) + "…"
		}
		return l
	}
	return ""
}

// TimelineResult is timeline's return value: the caller's own project, the
// scope actually used, and the matching events, oldest to newest.
type TimelineResult struct {
	ProjectKey string                `json:"project_key"`
	Scope      string                `json:"scope"`
	TZ         string                `json:"tz"`
	Events     []TimelineEventResult `json:"events"`
}

// handleTimeline serves the timeline socket method: scope=session (the
// caller's own observed session alone) or scope=project (the default — the
// caller's own observed project, every session in it), filtered by since
// (RFC3339) and kind, capped at limit. It never reads a project or session
// off the request line — id.ProjectKey and sessionID are the daemon's own
// observations, the same rule handleRecall follows for its own project
// anchor (AGENT-CONTRACT.md §Observed identity).
func handleTimeline(st *store.Store, id ident.Identity, sessionID string, raw json.RawMessage) DaemonResponse {
	var p TimelineParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return errResponse("invalid-params", "invalid timeline params: "+err.Error())
		}
	}

	scope := p.Scope
	if scope == "" {
		scope = timelineScopeProject
	}
	if scope != timelineScopeSession && scope != timelineScopeProject {
		return errResponse("invalid-params", `invalid "scope": `+scope)
	}

	var since time.Time
	if p.Since != "" {
		t, err := time.Parse(time.RFC3339, p.Since)
		if err != nil {
			return errResponse("invalid-params", `invalid "since": `+err.Error())
		}
		since = t
	}

	var (
		events []store.TimelineEvent
		err    error
	)
	switch scope {
	case timelineScopeSession:
		events, err = st.EventsForSessionTimeline(sessionID, since, p.Kind, p.Limit)
	default:
		events, err = st.EventsForTimeline(id.ProjectKey, since, p.Kind, p.Limit)
	}
	if err != nil {
		return errResponse("internal", err.Error())
	}

	out := make([]TimelineEventResult, len(events))
	for i, e := range events {
		out[i] = TimelineEventResult{
			ID:      e.ID,
			TS:      e.TS.UTC().Format(time.RFC3339Nano),
			Kind:    e.Kind,
			Source:  e.Source,
			Payload: json.RawMessage(e.Payload),
		}
		if e.Kind == payload.KindToolUse {
			var tu payload.ToolUse
			if json.Unmarshal([]byte(e.Payload), &tu) == nil && tu.RecordID != "" {
				out[i].RecordID = tu.RecordID
				if rec, rerr := st.GetRecord(tu.RecordID); rerr == nil && rec.ProjectKey == id.ProjectKey && rec.TombstonedAt == nil {
					out[i].RecordKind = string(rec.Kind)
					out[i].RecordFirstLine = firstLine(rec.Text, timelineRecordLineMaxRunes)
				}
			}
		}
	}

	result := TimelineResult{
		ProjectKey: id.ProjectKey,
		Scope:      scope,
		TZ:         ResultTimeZone,
		Events:     out,
	}

	b, err := json.Marshal(result)
	if err != nil {
		return errResponse("internal", err.Error())
	}
	return DaemonResponse{Result: b}
}
