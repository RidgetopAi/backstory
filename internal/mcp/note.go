package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// NoteParams is note's argument shape (AGENT-CONTRACT.md §`note` — one
// write, two required fields). It deliberately has no Tier or Session
// field: whatever a caller sends under those keys is dropped by
// json.Unmarshal, never read (AGENT-CONTRACT.md §The never-list, item 2).
type NoteParams struct {
	Kind       string   `json:"kind"`
	Text       string   `json:"text"`
	About      []string `json:"about,omitempty"`
	Supersedes string   `json:"supersedes,omitempty"`
	Evidence   []int64  `json:"evidence,omitempty"`
	// Links is accepted per AGENT-CONTRACT.md's note field list; SCHEMA.md's
	// records table has no column for it yet, so v0 accepts and drops it.
	Links   []string `json:"links,omitempty"`
	Expires string   `json:"expires,omitempty"` // RFC 3339
}

// NoteResult is note's return value.
type NoteResult struct {
	ID   string `json:"id"`
	Tier string `json:"tier"`
}

// noteKinds is the set of record kinds the note tool may write. It excludes
// punch/stage/confirm (SCHEMA.md §Reserved for the loop, and confirm has its
// own tool) even though store.RecordKind allows them.
var noteKinds = map[string]store.RecordKind{
	string(store.KindDecision): store.KindDecision,
	string(store.KindOutcome):  store.KindOutcome,
	string(store.KindHandoff):  store.KindHandoff,
	string(store.KindNote):     store.KindNote,
	string(store.KindClaim):    store.KindClaim,
}

// handleNote validates a note request and, if valid, calls
// store.InsertRecord. The tier in the returned NoteResult comes back from a
// GetRecord read-after-write — the daemon never trusts its own belief about
// what tier InsertRecord assigned; it reads what actually landed.
func handleNote(st *store.Store, identity store.Identity, sessionID, projectKey string, raw json.RawMessage) DaemonResponse {
	var p NoteParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return errResponse("invalid-params", "invalid note params: "+err.Error())
	}
	if p.Kind == "" {
		return errResponse("invalid-params", `missing required field "kind"`)
	}
	kind, ok := noteKinds[p.Kind]
	if !ok {
		return errResponse("invalid-params", fmt.Sprintf("unknown kind %q", p.Kind))
	}
	if p.Text == "" {
		return errResponse("invalid-params", `missing required field "text"`)
	}

	var expiresAt *time.Time
	if p.Expires != "" {
		t, err := time.Parse(time.RFC3339, p.Expires)
		if err != nil {
			return errResponse("invalid-params", `invalid "expires": `+err.Error())
		}
		expiresAt = &t
	}

	id, err := st.InsertRecord(store.InsertRecordParams{
		Identity:   identity,
		Kind:       kind,
		Text:       p.Text,
		About:      p.About,
		SessionID:  sessionID,
		ProjectKey: projectKey,
		Evidence:   p.Evidence,
		ExpiresAt:  expiresAt,
	})
	if err != nil {
		var capErr *store.CapError
		if errors.As(err, &capErr) {
			return errResponse("rate-limited", capErr.Error())
		}
		return errResponse("internal", err.Error())
	}

	if p.Supersedes != "" {
		if err := st.LinkEdge(id, p.Supersedes, store.EdgeSupersedes, sessionID); err != nil {
			return errResponse("internal", err.Error())
		}
	}

	rec, err := st.GetRecord(id)
	if err != nil {
		return errResponse("internal", err.Error())
	}

	result, err := json.Marshal(NoteResult{ID: id, Tier: string(rec.Tier)})
	if err != nil {
		return errResponse("internal", err.Error())
	}
	return DaemonResponse{Result: result}
}
