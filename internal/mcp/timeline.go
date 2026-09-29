package mcp

import (
	"encoding/json"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
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
