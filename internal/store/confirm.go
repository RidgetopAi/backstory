package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// ConfirmAction is confirm's action parameter (AGENT-CONTRACT.md §confirm):
// promote a draft, flag a contradiction with evidence, mark supersession, or
// affirm a target is still true. Frozen at v0 plus the additive `affirm`
// value (decision bcc9fa54): the tool's own enum grows the same way.
type ConfirmAction string

const (
	ConfirmPromote    ConfirmAction = "promote"
	ConfirmContradict ConfirmAction = "contradict"
	ConfirmSupersede  ConfirmAction = "supersede"
	ConfirmAffirm     ConfirmAction = "affirm"
)

// confirmEdgeType maps a ConfirmAction to the edge type Confirm mints from
// the new kind=confirm record to its target (WHAT TO BUILD, task
// dcd72714): contradict mints `contradicts`, supersede mints `supersedes`;
// promote and affirm both mint `informs` — neither the never-list nor the
// edge enum names a dedicated "promotes" or "affirms" type, and adding one
// would make the edges enum change non-additive, so both reuse the same
// general "relates to" edge `note`'s own `links` field already established.
var confirmEdgeType = map[ConfirmAction]EdgeType{
	ConfirmPromote:    EdgeInforms,
	ConfirmContradict: EdgeContradicts,
	ConfirmSupersede:  EdgeSupersedes,
	ConfirmAffirm:     EdgeInforms,
}

// ConfirmEdgeType exposes confirmEdgeType so a caller (the mcp package,
// reporting what it wrote) does not have to re-derive the action -> edge
// mapping itself. It panics on an action Confirm itself would reject, since
// a caller is expected to have already gone through a successful Confirm.
func ConfirmEdgeType(a ConfirmAction) EdgeType {
	t, ok := confirmEdgeType[a]
	if !ok {
		panic(fmt.Sprintf("store: ConfirmEdgeType: unknown action %q", a))
	}
	return t
}

// ConfirmParams is the input to Confirm.
type ConfirmParams struct {
	Identity Identity
	Action   ConfirmAction
	// RecordID is the target every action names: the draft to promote, the
	// record being contradicted, the record being superseded, or the record
	// being affirmed still true.
	RecordID   string
	Text       string
	Evidence   []int64
	SessionID  string
	ProjectKey string
}

// Errors Confirm returns for its action-specific validation. Each is checked
// BEFORE InsertRecordWithEdges runs, so a rejected confirm writes nothing —
// not the record, not the edge (mirrors note's own contract, records.go).
var (
	// ErrPromoteNotADraft is returned when promote's target is not
	// tier=inferred: "promote a draft" has nothing to promote otherwise.
	ErrPromoteNotADraft = errors.New("store: promote requires the target record to be tier=inferred")
	// ErrPromoteSameSession is returned when the promoting session is the
	// same session the target draft was inferred from (SCHEMA.md invariant
	// 3: "a draft inferred from session S cannot be promoted by session S").
	ErrPromoteSameSession = errors.New("store: a draft cannot be promoted by the session that inferred it")
	// ErrContradictRequiresEvidence is returned when contradict names no
	// evidence at all (AGENT-CONTRACT.md §Outcomes: "contradiction only on
	// positive evidence").
	ErrContradictRequiresEvidence = errors.New("store: contradict requires at least one evidence id that exists in timeline_events for this project")
)

// UnknownEvidenceError is returned by Confirm's contradict action when an
// evidence id does not exist in timeline_events for the caller's project —
// including an id that exists but belongs to a different project. No record
// and no edge are inserted when this error is returned.
type UnknownEvidenceError struct {
	ID int64
}

func (e *UnknownEvidenceError) Error() string {
	return fmt.Sprintf("store: unknown evidence id %d for this project", e.ID)
}

// CrossProjectRecordError is returned by Confirm's supersede action when the
// target record does not belong to the caller's own observed project.
type CrossProjectRecordError struct {
	RecordID string
}

func (e *CrossProjectRecordError) Error() string {
	return fmt.Sprintf("store: record %s is not in the caller's observed project", e.RecordID)
}

// Confirm appends a kind=confirm record and, in the same transaction as
// InsertRecordWithEdges, the one edge the action mints to RecordID. Every
// action-specific check below runs before that insert, so a rejected
// confirm writes nothing at all.
//
//   - promote: RecordID must be tier=inferred and not inferred from the
//     caller's own SessionID; the new record's tier still comes from
//     p.Identity the same way every record's does (identity.go), which is
//     what caps a promotion at the promoter's own tier — an agent identity
//     can never mint human-declared, promotion included. The promoter is
//     recorded on the new confirm record's Promoter column.
//   - contradict: requires >=1 evidence id, each of which must exist in
//     timeline_events for p.ProjectKey (contradiction on positive evidence
//     only — AGENT-CONTRACT.md §Outcomes).
//   - supersede: RecordID must belong to p.ProjectKey (the new record
//     always does, by construction).
//   - affirm: no extra validation — any target may be affirmed still true.
func (s *Store) Confirm(p ConfirmParams) (string, error) {
	edgeType, ok := confirmEdgeType[p.Action]
	if !ok {
		return "", fmt.Errorf("store: unknown confirm action %q", p.Action)
	}

	// promote and supersede need the target record's own fields (Tier,
	// SessionID, ProjectKey) to validate against, so they fetch it here.
	// contradict and affirm need nothing from the target beyond "it
	// exists", which InsertRecordWithEdges's own atomic edge-target check
	// already establishes inside the same transaction as the record
	// insert — fetching it again here first would only shadow that check
	// with a second, redundant one outside the transaction, the exact
	// split this package's history (critic T1 on 14704ebe, task e7951178)
	// exists to avoid.
	var target Record
	if p.Action == ConfirmPromote || p.Action == ConfirmSupersede {
		var err error
		target, err = s.GetRecord(p.RecordID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", &UnknownEdgeTargetError{Field: "record_id", ID: p.RecordID}
			}
			return "", err
		}
	}

	var promoter string
	switch p.Action {
	case ConfirmPromote:
		if target.Tier != TierInferred {
			return "", ErrPromoteNotADraft
		}
		if p.SessionID != "" && target.SessionID == p.SessionID {
			return "", ErrPromoteSameSession
		}
		promoter = p.SessionID
	case ConfirmContradict:
		if len(p.Evidence) == 0 {
			return "", ErrContradictRequiresEvidence
		}
		for _, evID := range p.Evidence {
			exists, err := s.eventExistsInProject(evID, p.ProjectKey)
			if err != nil {
				return "", err
			}
			if !exists {
				return "", &UnknownEvidenceError{ID: evID}
			}
		}
	case ConfirmSupersede:
		if !projectKeyMatches(target.ProjectKey, p.ProjectKey) {
			return "", &CrossProjectRecordError{RecordID: p.RecordID}
		}
	case ConfirmAffirm:
		// No extra validation: affirm means "still true as of now" and
		// applies to any target the caller can name.
	}

	return s.InsertRecordWithEdges(InsertRecordParams{
		Identity:   p.Identity,
		Kind:       KindConfirm,
		Text:       p.Text,
		SessionID:  p.SessionID,
		ProjectKey: p.ProjectKey,
		Evidence:   p.Evidence,
		Promoter:   promoter,
	}, []EdgeSpec{{
		OtherID:    p.RecordID,
		Type:       edgeType,
		DeclaredBy: p.SessionID,
		Field:      "record_id",
	}})
}

// eventExistsInProject reports whether eventID names a timeline_events row
// belonging (via its session) to projectKey — the same join EventsSinceID
// uses, since timeline_events carries no project_key column of its own. An
// event with no session (session_id NULL) belongs to no project and never
// matches, the same rule EventsSinceID applies.
func (s *Store) eventExistsInProject(eventID int64, projectKey string) (bool, error) {
	k1, k2 := projectKeyIN(projectKey)
	var exists int
	err := s.db.QueryRow(`SELECT 1 FROM timeline_events e
		JOIN sessions sess ON sess.id = e.session_id
		WHERE e.id = ? AND sess.project_key IN (?, ?) LIMIT 1`, eventID, k1, k2).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: event %d exists in project %s: %w", eventID, projectKey, err)
	}
	return true, nil
}
