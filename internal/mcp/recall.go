package mcp

import (
	"encoding/json"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/store"
)

// DefaultRecallBudgetTokens is recall's token budget when a caller does not
// send budget_tokens, mirroring the SessionStart block's own default
// (block.DefaultBudgetTokens): both are "how much of the ledger fits in
// context", so they share the same starting number until a setting
// overrides one independently.
const DefaultRecallBudgetTokens = block.DefaultBudgetTokens

// maxRecallDecisions bounds recall's decisions query itself, independent of
// the token budget that trims the response afterward: a project with
// thousands of declared decisions must not make every recall call load them
// all into memory before the budget ever gets a chance to cut the list
// down. Generous enough that the token budget is normally what limits
// output first.
const maxRecallDecisions = 200

// RecallParams is recall's argument shape for v0 (this punch's minimal,
// project-anchored recall — PLAN.md's `recall_thread` altitude/trust-
// narrative engine is Phase 4, out of scope here). It deliberately has no
// Project field even though the frozen v0 schema advertises one: "the
// caller's own project as the daemon OBSERVES it, never a declared one" —
// the same rule NoteParams applies to tier/session, applied here to
// project. Query and altitude are schema fields reserved for the Phase 4
// engine; unmarshaling into this struct drops them the same way.
type RecallParams struct {
	BudgetTokens int `json:"budget_tokens,omitempty"`
}

// RecallItem is one ledger record in a recall result: enough to identify it
// (ID) and to weigh how much to trust it (Tier) — AGENT-CONTRACT.md's
// "trust-annotated" requirement, minus the Phase 4 narrative altitude.
type RecallItem struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Tier string `json:"tier"`
	Text string `json:"text"`
	TS   string `json:"ts"`
}

// RecallResult is recall's v0 return value: the caller's own project's
// latest handoff, its declared decisions newest first, and a compressed
// recent-timeline summary — in that order (AGENT-CONTRACT.md §The five
// tools; this punch's WHAT TO BUILD). ProjectKey is always set, even when
// every other field is empty, so an empty result still names the project it
// is honestly empty for (DONE WHEN clause 3).
type RecallResult struct {
	ProjectKey string       `json:"project_key"`
	Handoff    *RecallItem  `json:"handoff,omitempty"`
	Decisions  []RecallItem `json:"decisions,omitempty"`
	Timeline   string       `json:"timeline,omitempty"`
}

// handleRecall serves the recall socket method. It never reads anything a
// request line declares about identity or project (id.ProjectKey is the
// daemon's own observation, the same rule handleBlock follows for the
// SessionStart block) — a request naming a different project cannot make
// recall answer for it.
func handleRecall(st *store.Store, id ident.Identity, raw json.RawMessage) DaemonResponse {
	var p RecallParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return errResponse("invalid-params", "invalid recall params: "+err.Error())
		}
	}
	budgetTokens := p.BudgetTokens
	if budgetTokens <= 0 {
		budgetTokens = DefaultRecallBudgetTokens
	}

	projectKey := id.ProjectKey

	handoffRec, hasHandoff, err := st.LatestRecord(projectKey, store.KindHandoff)
	if err != nil {
		return errResponse("internal", err.Error())
	}
	decisionRecs, err := st.RecordsForProject(projectKey, store.KindDecision, maxRecallDecisions)
	if err != nil {
		return errResponse("internal", err.Error())
	}

	// The recent-timeline summary covers the same span the SessionStart
	// block's delta slot does: since the latest handoff's event cursor, or
	// every project event when there is none (block.Render's own rule) —
	// recall's timeline summary is "what happened since the last handoff",
	// not an unbounded project history.
	var sinceID int64
	if hasHandoff {
		sinceID = handoffRec.EventCursor
	}
	events, err := st.EventsSinceID(projectKey, sinceID)
	if err != nil {
		return errResponse("internal", err.Error())
	}
	timelineSummary := block.DeltaSummary(events)

	result := assembleRecall(projectKey, handoffRec, hasHandoff, decisionRecs, timelineSummary, budgetTokens)

	b, err := json.Marshal(result)
	if err != nil {
		return errResponse("internal", err.Error())
	}
	return DaemonResponse{Result: b}
}

// assembleRecall builds RecallResult and trims it to budgetTokens by
// cutting from the end: the timeline summary first, then decisions from the
// oldest (decisionRecs is already newest-first, so "oldest" is the tail of
// the slice) — the handoff is never cut, truncated, or dropped (this
// punch's DONE WHEN clause 2: "the handoff survives"), unlike the
// SessionStart block's own budget cut, which as a last resort truncates its
// resume slot's text.
func assembleRecall(projectKey string, handoffRec store.Record, hasHandoff bool, decisionRecs []store.Record, timelineSummary string, budgetTokens int) RecallResult {
	var handoffItem *RecallItem
	handoffTokens := 0
	if hasHandoff {
		item := toRecallItem(handoffRec)
		handoffItem = &item
		handoffTokens = block.EstimateTokens(item.Text)
	}

	decisionItems := make([]RecallItem, len(decisionRecs))
	decisionTokens := make([]int, len(decisionRecs))
	total := handoffTokens
	for i, rec := range decisionRecs {
		decisionItems[i] = toRecallItem(rec)
		decisionTokens[i] = block.EstimateTokens(decisionItems[i].Text)
		total += decisionTokens[i]
	}

	timelineTokens := block.EstimateTokens(timelineSummary)
	total += timelineTokens

	includeTimeline := timelineSummary != ""
	if includeTimeline && total > budgetTokens {
		total -= timelineTokens
		includeTimeline = false
	}

	kept := len(decisionItems)
	for kept > 0 && total > budgetTokens {
		kept--
		total -= decisionTokens[kept]
	}
	decisionItems = decisionItems[:kept]

	result := RecallResult{ProjectKey: projectKey, Handoff: handoffItem, Decisions: decisionItems}
	if includeTimeline {
		result.Timeline = timelineSummary
	}
	return result
}

func toRecallItem(rec store.Record) RecallItem {
	return RecallItem{
		ID:   rec.ID,
		Kind: string(rec.Kind),
		Tier: string(rec.Tier),
		Text: rec.Text,
		TS:   rec.TS.UTC().Format(time.RFC3339),
	}
}
