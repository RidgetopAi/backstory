package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
//
// workspaces is the caller's already-resolved workspace directory list
// (task 482b2320, decision f3fa04c7's clause 7): the store package never
// resolves workspace dirs itself (no project.DefaultWorkspaceDirs call, no
// env read) so it behaves identically regardless of the calling process's
// environment. nil is safe — homeMembership then finds no session whose
// folder resolves inside a workspace, matching the pre-workspace scope.
func (s *Store) HandoffFreshness(h Record, workspaces []string) ([]FreshnessReason, error) {
	boundaryID := h.ID
	boundaryEventCursor := h.EventCursor
	if affirmID, affirmCursor, ok, err := s.latestAffirmInforming(h.ID); err != nil {
		return nil, err
	} else if ok {
		boundaryID = affirmID
		boundaryEventCursor = affirmCursor
	}

	memberKeys, sessionIDs, err := s.homeMembership(h.ProjectKey, workspaces)
	if err != nil {
		return nil, err
	}

	var reasons []FreshnessReason

	contradictorIDs, err := s.recordsAfter(h.ID, boundaryID, EdgeContradicts)
	if err != nil {
		return nil, err
	}
	if len(contradictorIDs) > 0 {
		reasons = append(reasons, FreshnessReason{Kind: FreshnessContradicted, RecordIDs: contradictorIDs})
	}

	laterRecordIDs, err := s.laterRecordsSharingAbout(memberKeys, boundaryID, h.About)
	if err != nil {
		return nil, err
	}
	if len(laterRecordIDs) > 0 {
		reasons = append(reasons, FreshnessReason{Kind: FreshnessLaterRecord, RecordIDs: laterRecordIDs})
	}

	laterEventIDs, err := s.laterEventsTouchingAbout(sessionIDs, boundaryEventCursor, h.About)
	if err != nil {
		return nil, err
	}
	if len(laterEventIDs) > 0 {
		reasons = append(reasons, FreshnessReason{Kind: FreshnessLaterActivity, EventIDs: laterEventIDs})
	}

	return reasons, nil
}

// homeMembership resolves handoffProjectKey's HOME scope (decision
// f3fa04c7): every session whose own folder resolves to handoffProjectKey
// (sessionsForHome) — which, for a workspace-homed handoff, spans every
// repo under that workspace, not just handoffProjectKey's own literal value
// — plus the distinct project_key values those sessions themselves carry
// (always including handoffProjectKey itself, so a handoff filed at a plain
// repo key with no workspace at all, or a home with no session literally at
// its own root, still matches its own records). Replaces the old
// "handoff's own project_key" scope HandoffFreshness's later-record/
// later-activity checks used before this punch.
func (s *Store) homeMembership(handoffProjectKey string, workspaces []string) (memberKeys []string, sessionIDs []string, err error) {
	sessions, err := s.sessionsForHome(handoffProjectKey, workspaces)
	if err != nil {
		return nil, nil, fmt.Errorf("store: home membership for %s: %w", handoffProjectKey, err)
	}

	seen := map[string]bool{handoffProjectKey: true}
	memberKeys = []string{handoffProjectKey}
	for _, sess := range sessions {
		sessionIDs = append(sessionIDs, sess.ID)
		if sess.ProjectKey != "" && !seen[sess.ProjectKey] {
			seen[sess.ProjectKey] = true
			memberKeys = append(memberKeys, sess.ProjectKey)
		}
	}
	return memberKeys, sessionIDs, nil
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
// decision/note/outcome records whose project_key is one of memberKeys,
// inserted strictly after boundaryID's sequence position (rowid), whose
// about[] shares at least one path with about. memberKeys is
// HandoffFreshness's home-scoped membership (homeMembership): for a
// workspace-homed handoff this can span every repo under that workspace,
// not just the handoff's own project_key (decision f3fa04c7). Every value
// in memberKeys is already canonical (task d65ef8ff): it is either the
// caller's own handoffProjectKey, which InsertRecordWithEdges canonicalized
// at write time, or a session's own project_key, which StartSession
// canonicalized the same way — so no further alias expansion is needed
// here.
func (s *Store) laterRecordsSharingAbout(memberKeys []string, boundaryID string, about []string) ([]string, error) {
	if len(about) == 0 || len(memberKeys) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(memberKeys))
	args := make([]any, 0, len(memberKeys)+4)
	for i, k := range memberKeys {
		placeholders[i] = "?"
		args = append(args, k)
	}
	args = append(args, string(KindDecision), string(KindNote), string(KindOutcome), boundaryID)

	//nolint:gosec // the concatenated part is only "?" placeholders (one per member key), every value is still bound as a query arg below
	query := `SELECT id, about FROM records
		WHERE project_key IN (` + strings.Join(placeholders, ",") + `) AND kind IN (?, ?, ?) AND tombstoned_at IS NULL
		AND rowid > (SELECT rowid FROM records WHERE id = ?)
		ORDER BY rowid ASC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: later records sharing about for %v: %w", memberKeys, err)
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
		return nil, fmt.Errorf("store: later records sharing about for %v: %w", memberKeys, err)
	}
	return out, nil
}

// laterEventsTouchingAbout returns, ascending by id, the ids of timeline
// events belonging to one of sessionIDs after sinceEventCursor whose
// tool_use payload names a path in about — the same "changed a file" rule
// the SessionStart block's delta slot applies (payload.IsMutatingFileTool;
// task 393d174c: a Read carries a path too, but it observed the file, it
// did not change it). sessionIDs is HandoffFreshness's home-scoped
// membership (homeMembership): a workspace-homed handoff's later activity
// can come from any session under that workspace, not just sessions
// sharing the handoff's own project_key (decision f3fa04c7).
func (s *Store) laterEventsTouchingAbout(sessionIDs []string, sinceEventCursor int64, about []string) ([]int64, error) {
	if len(about) == 0 || len(sessionIDs) == 0 {
		return nil, nil
	}
	events, err := s.eventsSinceIDForSessions(sessionIDs, sinceEventCursor)
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
