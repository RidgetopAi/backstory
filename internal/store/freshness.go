package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/RidgetopAi/backstory/internal/payload"
)

// FreshnessReasonKind names why HandoffFreshness flags a handoff possibly
// stale (decision bcc9fa54): positive evidence only, never elapsed time and
// never the absence of later activity.
type FreshnessReasonKind string

const (
	// FreshnessContradicted is reason (a): a `contradicts` edge targets the
	// handoff.
	FreshnessContradicted FreshnessReasonKind = "contradicted"
	// FreshnessLaterRecord is reason (b): a later decision/note/outcome
	// record in the same project shares an about[] path with the handoff.
	FreshnessLaterRecord FreshnessReasonKind = "later-record"
	// FreshnessLaterActivity is reason (c): a later timeline event touches a
	// path the handoff's about[] names.
	FreshnessLaterActivity FreshnessReasonKind = "later-activity"
)

// FreshnessReason is one reason HandoffFreshness flagged a handoff, with the
// evidence ids it cites: record ids for FreshnessContradicted and
// FreshnessLaterRecord, timeline event ids for FreshnessLaterActivity —
// never both on the same reason.
type FreshnessReason struct {
	Kind      FreshnessReasonKind
	RecordIDs []string
	EventIDs  []int64
}

// HandoffFreshness reports every reason h is possibly stale (decision
// bcc9fa54), in a fixed order (contradicted, later-record, later-activity):
// positive evidence only, arising after h's own event_cursor and after the
// latest `confirm affirm` record informing h, if any — never from elapsed
// time and never from the mere absence of later activity. A handoff with no
// about[] can only ever be flagged by FreshnessContradicted: the other two
// reasons require an about[] path in common, which is impossible against an
// empty set. A nil/empty return means h is fresh.
func (s *Store) HandoffFreshness(h Record) ([]FreshnessReason, error) {
	boundaryID := h.ID
	boundaryEventCursor := h.EventCursor
	if affirmID, affirmCursor, ok, err := s.latestAffirmInforming(h.ID); err != nil {
		return nil, err
	} else if ok {
		boundaryID = affirmID
		boundaryEventCursor = affirmCursor
	}

	var reasons []FreshnessReason

	contradictorIDs, err := s.recordsAfter(h.ID, boundaryID, EdgeContradicts)
	if err != nil {
		return nil, err
	}
	if len(contradictorIDs) > 0 {
		reasons = append(reasons, FreshnessReason{Kind: FreshnessContradicted, RecordIDs: contradictorIDs})
	}

	laterRecordIDs, err := s.laterRecordsSharingAbout(h.ProjectKey, boundaryID, h.About)
	if err != nil {
		return nil, err
	}
	if len(laterRecordIDs) > 0 {
		reasons = append(reasons, FreshnessReason{Kind: FreshnessLaterRecord, RecordIDs: laterRecordIDs})
	}

	laterEventIDs, err := s.laterEventsTouchingAbout(h.ProjectKey, boundaryEventCursor, h.About)
	if err != nil {
		return nil, err
	}
	if len(laterEventIDs) > 0 {
		reasons = append(reasons, FreshnessReason{Kind: FreshnessLaterActivity, EventIDs: laterEventIDs})
	}

	return reasons, nil
}

// latestAffirmInforming returns the most-recently-inserted `confirm` record
// with an `informs` edge into handoffID, and its own event_cursor, if that
// record is an affirm rather than a promote — the two confirm actions that
// both mint `informs` (store/confirm.go's confirmEdgeType). A promote
// always stamps its new record's promoter (the promoting session id); an
// affirm never does (store.Confirm's own switch), so `promoter IS NULL` is
// the disambiguator, not a second stored action column.
func (s *Store) latestAffirmInforming(handoffID string) (id string, eventCursor int64, ok bool, err error) {
	err = s.db.QueryRow(`
		SELECT c.id, c.event_cursor FROM edges e
		JOIN records c ON c.id = e.from_id
		WHERE e.to_id = ? AND e.type = ? AND c.kind = ? AND c.promoter IS NULL
		ORDER BY c.rowid DESC LIMIT 1`,
		handoffID, string(EdgeInforms), string(KindConfirm)).Scan(&id, &eventCursor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("store: latest affirm informing %s: %w", handoffID, err)
	}
	return id, eventCursor, true, nil
}

// recordsAfter returns, oldest first, the ids of records with an edgeType
// edge into targetID whose own insertion sequence is strictly after
// boundaryID's (rowid, never ts — SCHEMA.md invariant 10, the same rule
// every other sequence comparison in this package follows).
func (s *Store) recordsAfter(targetID, boundaryID string, edgeType EdgeType) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT c.id FROM edges e
		JOIN records c ON c.id = e.from_id
		WHERE e.to_id = ? AND e.type = ?
		AND c.rowid > (SELECT rowid FROM records WHERE id = ?)
		ORDER BY c.rowid ASC`,
		targetID, string(edgeType), boundaryID)
	if err != nil {
		return nil, fmt.Errorf("store: records after %s (%s) into %s: %w", boundaryID, edgeType, targetID, err)
	}
	return scanIDs(rows)
}

// laterRecordsSharingAbout returns, oldest first, the ids of non-tombstoned
// decision/note/outcome records in projectKey inserted strictly after
// boundaryID's sequence position (rowid) whose about[] shares at least one
// path with about.
func (s *Store) laterRecordsSharingAbout(projectKey, boundaryID string, about []string) ([]string, error) {
	if len(about) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT id, about FROM records
		WHERE project_key = ? AND kind IN (?, ?, ?) AND tombstoned_at IS NULL
		AND rowid > (SELECT rowid FROM records WHERE id = ?)
		ORDER BY rowid ASC`,
		projectKey, string(KindDecision), string(KindNote), string(KindOutcome), boundaryID)
	if err != nil {
		return nil, fmt.Errorf("store: later records sharing about for project %s: %w", projectKey, err)
	}
	defer func() { _ = rows.Close() }()

	wanted := map[string]bool{}
	for _, p := range about {
		wanted[p] = true
	}

	var out []string
	for rows.Next() {
		var id, aboutJSON string
		if err := rows.Scan(&id, &aboutJSON); err != nil {
			return nil, fmt.Errorf("store: scan later record sharing about: %w", err)
		}
		var paths []string
		if err := json.Unmarshal([]byte(aboutJSON), &paths); err != nil {
			return nil, fmt.Errorf("store: parse about for record %s: %w", id, err)
		}
		for _, p := range paths {
			if wanted[p] {
				out = append(out, id)
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: later records sharing about for project %s: %w", projectKey, err)
	}
	return out, nil
}

// laterEventsTouchingAbout returns, ascending by id, the ids of timeline
// events in projectKey after sinceEventCursor whose tool_use payload names a
// path in about — the same "changed a file" rule the SessionStart block's
// delta slot applies (payload.IsMutatingFileTool; task 393d174c: a Read
// carries a path too, but it observed the file, it did not change it).
func (s *Store) laterEventsTouchingAbout(projectKey string, sinceEventCursor int64, about []string) ([]int64, error) {
	if len(about) == 0 {
		return nil, nil
	}
	events, err := s.EventsSinceID(projectKey, sinceEventCursor)
	if err != nil {
		return nil, err
	}

	wanted := map[string]bool{}
	for _, p := range about {
		wanted[p] = true
	}

	var out []int64
	for _, e := range events {
		if e.Kind != payload.KindToolUse {
			continue
		}
		var tu payload.ToolUse
		if json.Unmarshal([]byte(e.Payload), &tu) != nil {
			continue
		}
		if tu.Path != "" && payload.IsMutatingFileTool(tu.Name) && wanted[tu.Path] {
			out = append(out, e.ID)
		}
	}
	return out, nil
}
