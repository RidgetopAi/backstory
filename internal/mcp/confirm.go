package mcp

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/RidgetopAi/backstory/internal/store"
)

// ConfirmParams is confirm's argument shape (AGENT-CONTRACT.md §confirm,
// tools.go's frozen v0 schema). Like NoteParams, it deliberately has no
// Tier or Session field: json.Unmarshal drops anything sent under those
// keys, never read (AGENT-CONTRACT.md §The never-list, item 2).
type ConfirmParams struct {
	RecordID string  `json:"record_id"`
	Action   string  `json:"action"`
	Evidence []int64 `json:"evidence,omitempty"`
	Text     string  `json:"text,omitempty"`
}

// ConfirmResult is confirm's return value: the new confirm record's id and
// tier, plus the edge it minted to RecordID — EdgeType and TargetID
// together name that edge, the way DONE WHEN clause 1 requires the result
// text to.
type ConfirmResult struct {
	ID       string `json:"id"`
	Tier     string `json:"tier"`
	EdgeType string `json:"edge_type"`
	TargetID string `json:"target_id"`
}

// confirmActions is the set of actions the confirm tool accepts (tools.go's
// enum): promote|contradict|supersede|affirm.
var confirmActions = map[string]store.ConfirmAction{
	string(store.ConfirmPromote):    store.ConfirmPromote,
	string(store.ConfirmContradict): store.ConfirmContradict,
	string(store.ConfirmSupersede):  store.ConfirmSupersede,
	string(store.ConfirmAffirm):     store.ConfirmAffirm,
}

// handleConfirm validates a confirm request and, if valid, calls
// store.Confirm. Like handleNote, the returned tier comes back from a
// GetRecord read-after-write rather than the daemon's own belief about what
// Confirm assigned.
func handleConfirm(st *store.Store, identity store.Identity, sessionID, projectKey string, raw json.RawMessage) DaemonResponse {
	var p ConfirmParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return errResponse("invalid-params", "invalid confirm params: "+err.Error())
	}
	if p.RecordID == "" {
		return errResponse("invalid-params", `missing required field "record_id"`)
	}
	if p.Action == "" {
		return errResponse("invalid-params", `missing required field "action"`)
	}
	action, ok := confirmActions[p.Action]
	if !ok {
		return errResponse("invalid-params", fmt.Sprintf("unknown action %q", p.Action))
	}

	id, err := st.Confirm(store.ConfirmParams{
		Identity:   identity,
		Action:     action,
		RecordID:   p.RecordID,
		Text:       p.Text,
		Evidence:   p.Evidence,
		SessionID:  sessionID,
		ProjectKey: projectKey,
	})
	if err != nil {
		return confirmErrorResponse(err)
	}

	rec, err := st.GetRecord(id)
	if err != nil {
		return errResponse("internal", err.Error())
	}

	result, err := json.Marshal(ConfirmResult{
		ID:       id,
		Tier:     string(rec.Tier),
		EdgeType: string(store.ConfirmEdgeType(action)),
		TargetID: p.RecordID,
	})
	if err != nil {
		return errResponse("internal", err.Error())
	}
	return DaemonResponse{Result: result}
}

// confirmErrorResponse maps a store.Confirm error to a DaemonResponse. Every
// case here names a caller-fixable problem (a bad record_id, missing or
// unknown evidence, a cross-project record, a same-session self-promotion)
// and so is reported as invalid-params, the same rule handleNote applies to
// store.UnknownEdgeTargetError; a rate cap gets its own rate-limited code;
// anything else is an opaque internal error.
func confirmErrorResponse(err error) DaemonResponse {
	var capErr *store.CapError
	if errors.As(err, &capErr) {
		return errResponse("rate-limited", capErr.Error())
	}
	var edgeErr *store.UnknownEdgeTargetError
	if errors.As(err, &edgeErr) {
		return errResponse("invalid-params", edgeErr.Error())
	}
	var evErr *store.UnknownEvidenceError
	if errors.As(err, &evErr) {
		return errResponse("invalid-params", evErr.Error())
	}
	var crossErr *store.CrossProjectRecordError
	if errors.As(err, &crossErr) {
		return errResponse("invalid-params", crossErr.Error())
	}
	if errors.Is(err, store.ErrPromoteNotADraft) ||
		errors.Is(err, store.ErrPromoteSameSession) ||
		errors.Is(err, store.ErrContradictRequiresEvidence) {
		return errResponse("invalid-params", err.Error())
	}
	return errResponse("internal", err.Error())
}
