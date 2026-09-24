// Package recall answers "what's the backstory on X": an anchor (a
// project, a record, or free text) resolves to an ordered, trust-annotated
// set of ledger items, rendered at one of three altitudes under a token
// budget (PLAN.md §Phase 4, the recall_thread model). It is the engine the
// MCP recall tool, the CLI, the This Week view and export are meant to
// share — none of which this package wires up; it reads the store and
// returns data, nothing else.
package recall

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/store"
)

// maxProjectRecords bounds a project anchor's own store query, independent
// of the token budget that trims the rendered result afterward — the same
// pattern internal/mcp/recall.go's v0 stub used for decisions
// (maxRecallDecisions): a project with thousands of records must not load
// them all before the budget ever gets a chance to cut the list down.
const maxProjectRecords = 500

// maxTextResults bounds a free-text anchor's own FTS query the same way.
const maxTextResults = 200

// edgeWalkHops is how many hops a record anchor's edge walk follows, both
// directions, before stopping (PLAN.md §Phase 4: "walk its edges ... one or
// two hops, both directions").
const edgeWalkHops = 2

// AnchorKind is the kind of anchor Build resolves to a starting record set.
type AnchorKind string

const (
	AnchorProject AnchorKind = "project"
	AnchorRecord  AnchorKind = "record"
	AnchorText    AnchorKind = "text"
)

// Anchor is recall's starting point. Only the fields the Kind uses are
// read; ProjectAnchor, RecordAnchor and TextAnchor build a well-formed
// value each rather than callers setting fields by hand.
type Anchor struct {
	Kind       AnchorKind
	ProjectKey string
	RecordID   string
	Text       string
}

// ProjectAnchor anchors on a project: its whole ledger (every record kind,
// including tombstoned ones), newest first by sequence.
func ProjectAnchor(projectKey string) Anchor {
	return Anchor{Kind: AnchorProject, ProjectKey: projectKey}
}

// RecordAnchor anchors on a single record, named by its full id or a short
// prefix unique to it, plus the records reachable from it within
// edgeWalkHops of any edge type, either direction.
func RecordAnchor(idOrPrefix string) Anchor {
	return Anchor{Kind: AnchorRecord, RecordID: idOrPrefix}
}

// TextAnchor anchors on a free-text FTS match, scoped to projectKey.
func TextAnchor(projectKey, query string) Anchor {
	return Anchor{Kind: AnchorText, ProjectKey: projectKey, Text: query}
}

// Altitude is how much of each item Build renders.
type Altitude string

const (
	// AltitudeHeadline renders one line per item: short id, kind, tier,
	// status, first line of text. ~500 tokens.
	AltitudeHeadline Altitude = "headline"
	// AltitudeSummary renders a short body per item. ~2k tokens.
	AltitudeSummary Altitude = "summary"
	// AltitudeFull renders each item's full text, up to the budget.
	AltitudeFull Altitude = "full"
)

// Status is a recall item's provenance status, derived only from the
// record's own tombstone column and its supersedes/contradicts edges —
// never inferred any other way.
type Status string

const (
	StatusCurrent      Status = "current"
	StatusSuperseded   Status = "superseded"
	StatusTombstoned   Status = "tombstoned"
	StatusContradicted Status = "contradicted"
	// StatusPossiblyStale is a handoff-only status (decision bcc9fa54):
	// positive evidence — a contradiction, a later same-project record, or a
	// later timeline event — arrived after the handoff's event_cursor with
	// no later `confirm affirm` informing it since. It takes precedence over
	// StatusContradicted for handoff records (store.HandoffFreshness folds
	// that same contradiction in as its own reason, affirm-aware in a way
	// the plain StatusContradicted check below never is); no other kind can
	// carry it.
	StatusPossiblyStale Status = "possibly-stale"
)

// Item is one ledger record in a recall result.
type Item struct {
	ID   string
	Kind store.RecordKind
	// Tier is the record's stored tier, verbatim — never computed from
	// anything else (PLAN.md §Phase 4: "never computed from anything but
	// the stored tier").
	Tier store.Tier
	// Text is the record's body, empty when Status is StatusTombstoned
	// (SCHEMA.md invariant 1: recall omits a tombstoned record's text,
	// keeps its edges).
	Text string
	// Edges are every edge touching this record, both directions, every
	// type, unaffected by Status — in particular still populated when
	// Status is StatusTombstoned, per SCHEMA.md invariant 1 ("keeps its
	// edges").
	Edges []store.Edge

	Status Status
	// SupersededByID is set only when Status is StatusSuperseded: the id of
	// the record that supersedes this one, from the incoming `supersedes`
	// edge.
	SupersededByID string
	// ContradictionEvidence is set only when Status is StatusContradicted:
	// the timeline event ids the contradicting record cited as its own
	// evidence, reached only via a `contradicts` edge into this record.
	ContradictionEvidence []int64
	// StaleReasons is set only when Status is StatusPossiblyStale: every
	// reason store.HandoffFreshness found, each carrying its own evidence
	// ids.
	StaleReasons []store.FreshnessReason
}

// Result is Build's return value: the items that made the altitude's
// budget cut, in the order rendered, and the rendered text itself. Items
// always matches what Rendered shows — Build never trims one without the
// other.
type Result struct {
	Items    []Item
	Rendered string
}

// Build resolves anchor to an ordered set of records — walking edges for a
// record anchor — annotates each with its trust tier and status, and
// renders the result at altitude under budgetTokens, using the same
// estimator the SessionStart block budgets against (block.EstimateTokens).
// It only reads st.
func Build(st *store.Store, anchor Anchor, altitude Altitude, budgetTokens int) (Result, error) {
	recs, err := resolve(st, anchor)
	if err != nil {
		return Result{}, err
	}

	items := make([]Item, len(recs))
	for i, rec := range recs {
		item, err := annotate(st, rec)
		if err != nil {
			return Result{}, err
		}
		items[i] = item
	}

	return fit(items, altitude, budgetTokens), nil
}

// resolve turns anchor into an ordered record set, before status
// annotation or altitude rendering: project and record anchors are already
// in sequence order (RecordsForProjectAll / RecordsByIDs); a text anchor's
// FTS match set is re-ordered into sequence order the same way, so every
// anchor kind's output obeys the one ordering rule (SCHEMA.md invariant
// 10: order by sequence, never ts).
func resolve(st *store.Store, anchor Anchor) ([]store.Record, error) {
	switch anchor.Kind {
	case AnchorProject:
		recs, err := st.RecordsForProjectAll(anchor.ProjectKey, maxProjectRecords)
		if err != nil {
			return nil, fmt.Errorf("recall: project anchor: %w", err)
		}
		return recs, nil
	case AnchorRecord:
		return resolveRecordAnchor(st, anchor.RecordID)
	case AnchorText:
		return resolveTextAnchor(st, anchor.ProjectKey, anchor.Text)
	default:
		return nil, fmt.Errorf("recall: unknown anchor kind %q", anchor.Kind)
	}
}

// resolveRecordAnchor resolves idOrPrefix to a record, then breadth-first
// walks every edge touching it — any of the six edge types, both
// directions — for edgeWalkHops hops, and returns the anchor record plus
// everything reached, newest first by sequence. An anchor that resolves to
// no record (unknown id, or an ambiguous short prefix) returns an empty
// set, not an error: recall answering "nothing here" is this anchor's
// honest empty state.
func resolveRecordAnchor(st *store.Store, idOrPrefix string) ([]store.Record, error) {
	root, ok, err := st.FindRecordByIDPrefix(idOrPrefix)
	if err != nil {
		return nil, fmt.Errorf("recall: record anchor: %w", err)
	}
	if !ok {
		return nil, nil
	}

	visited := map[string]bool{root.ID: true}
	frontier := []string{root.ID}
	for hop := 0; hop < edgeWalkHops && len(frontier) > 0; hop++ {
		var next []string
		for _, id := range frontier {
			edges, err := st.EdgesTouching(id)
			if err != nil {
				return nil, fmt.Errorf("recall: record anchor edge walk: %w", err)
			}
			for _, e := range edges {
				for _, other := range [2]string{e.FromID, e.ToID} {
					if !visited[other] {
						visited[other] = true
						next = append(next, other)
					}
				}
			}
		}
		frontier = next
	}

	ids := make([]string, 0, len(visited))
	for id := range visited {
		ids = append(ids, id)
	}
	recs, err := st.RecordsByIDs(ids)
	if err != nil {
		return nil, fmt.Errorf("recall: record anchor: %w", err)
	}
	return recs, nil
}

// resolveTextAnchor runs the FTS match scoped to projectKey
// (store.SearchRecordsInProject) and re-orders the match set into sequence
// order.
func resolveTextAnchor(st *store.Store, projectKey, query string) ([]store.Record, error) {
	results, err := st.SearchRecordsInProject(projectKey, query, maxTextResults)
	if err != nil {
		return nil, fmt.Errorf("recall: text anchor: %w", err)
	}
	ids := make([]string, 0, len(results))
	for _, r := range results {
		ids = append(ids, r.ID)
	}
	recs, err := st.RecordsByIDs(ids)
	if err != nil {
		return nil, fmt.Errorf("recall: text anchor: %w", err)
	}
	return recs, nil
}

// annotate builds an Item from rec: tier carried verbatim, status derived
// in a fixed precedence — tombstoned first (text omitted per SCHEMA.md
// invariant 1, so nothing downstream can leak it back in), then superseded,
// then contradicted, else current. A record can technically satisfy more
// than one condition (e.g. a tombstoned record superseded before deletion);
// this precedence picks the single status callers see, always the
// strongest one, rather than leaving it ambiguous which wins.
func annotate(st *store.Store, rec store.Record) (Item, error) {
	edges, err := st.EdgesTouching(rec.ID)
	if err != nil {
		return Item{}, fmt.Errorf("recall: annotate %s: %w", rec.ID, err)
	}

	item := Item{
		ID:     rec.ID,
		Kind:   rec.Kind,
		Tier:   rec.Tier,
		Text:   rec.Text,
		Status: StatusCurrent,
		Edges:  edges,
	}
	if rec.TombstonedAt != nil {
		item.Status = StatusTombstoned
		item.Text = ""
		return item, nil
	}

	for _, e := range edges {
		if e.Type == store.EdgeSupersedes && e.ToID == rec.ID {
			item.Status = StatusSuperseded
			item.SupersededByID = e.FromID
			return item, nil
		}
	}

	// A handoff's contradiction signal is folded into HandoffFreshness's own
	// reasons (FreshnessContradicted), affirm-aware in a way the plain
	// StatusContradicted check below is not, so a handoff never falls
	// through to that check at all — flagged or not.
	if rec.Kind == store.KindHandoff {
		reasons, err := st.HandoffFreshness(rec)
		if err != nil {
			return Item{}, fmt.Errorf("recall: annotate %s: freshness: %w", rec.ID, err)
		}
		if len(reasons) > 0 {
			item.Status = StatusPossiblyStale
			item.StaleReasons = reasons
		}
		return item, nil
	}

	for _, e := range edges {
		if e.Type == store.EdgeContradicts && e.ToID == rec.ID {
			evidenceRec, err := st.GetRecord(e.FromID)
			if err != nil {
				return Item{}, fmt.Errorf("recall: annotate %s: contradiction source: %w", rec.ID, err)
			}
			item.Status = StatusContradicted
			item.ContradictionEvidence = evidenceRec.Evidence
			return item, nil
		}
	}

	return item, nil
}

// fit renders items at altitude, keeping only as many — in their given
// order — as fit budgetTokens (block.EstimateTokens, the same estimator
// the SessionStart block budgets against): it accumulates the actual
// joined rendering incrementally and stops before a next item would push
// the estimate over budget, so Result.Rendered's own estimate is always
// <= budgetTokens and Result.Items is exactly the set that rendering
// reflects.
func fit(items []Item, altitude Altitude, budgetTokens int) Result {
	sep := altitudeSeparator(altitude)
	var kept []Item
	rendered := ""
	for _, item := range items {
		line := renderItem(item, altitude)
		candidate := line
		if rendered != "" {
			candidate = rendered + sep + line
		}
		if block.EstimateTokens(candidate) > budgetTokens {
			break
		}
		rendered = candidate
		kept = append(kept, item)
	}
	return Result{Items: kept, Rendered: rendered}
}

func altitudeSeparator(altitude Altitude) string {
	if altitude == AltitudeHeadline {
		return "\n"
	}
	return "\n\n"
}

// shortIDLen is how many leading characters of a record id headline
// rendering shows — enough to eyeball-distinguish items in a list, short
// enough that "one line per item" (DONE WHEN clause 2) stays true even for
// a long first line of text.
const shortIDLen = 8

func shortID(id string) string {
	if len(id) <= shortIDLen {
		return id
	}
	return id[:shortIDLen]
}

// summaryBodyChars bounds the summary altitude's per-item body length —
// "short bodies of key records" (PLAN.md §Phase 4) — before the overall
// token budget ever gets a chance to cut items.
const summaryBodyChars = 240

// renderItem renders one item at altitude: headline is always exactly one
// line (firstLine strips any embedded newline, and no other part of the
// line can contain one), summary truncates the body, full does not.
func renderItem(item Item, altitude Altitude) string {
	header := fmt.Sprintf("%s · %s · %s · %s", shortID(item.ID), item.Kind, item.Tier, statusLabel(item))
	switch altitude {
	case AltitudeHeadline:
		if fl := firstLine(item.Text); fl != "" {
			return header + " · " + fl
		}
		return header
	case AltitudeSummary:
		return renderBody(header, item.Text, summaryBodyChars)
	case AltitudeFull:
		return renderBody(header, item.Text, 0)
	default:
		return header
	}
}

func renderBody(header, text string, maxChars int) string {
	if maxChars > 0 {
		r := []rune(text)
		if len(r) > maxChars {
			text = string(r[:maxChars]) + "…"
		}
	}
	if text == "" {
		return header
	}
	return header + "\n" + text
}

func statusLabel(item Item) string {
	switch item.Status {
	case StatusSuperseded:
		return "superseded by " + shortID(item.SupersededByID)
	case StatusTombstoned:
		return "tombstoned"
	case StatusContradicted:
		return "contradicted (evidence " + joinInt64s(item.ContradictionEvidence) + ")"
	case StatusPossiblyStale:
		return "possibly stale: " + staleReasonSummary(item.StaleReasons)
	case StatusCurrent:
		return "current"
	default:
		return "current"
	}
}

// staleReasonSummary renders every reason in item.StaleReasons as "<kind>
// (ids <evidence>)", joined with "; ".
func staleReasonSummary(reasons []store.FreshnessReason) string {
	parts := make([]string, len(reasons))
	for i, r := range reasons {
		ids := make([]string, 0, len(r.RecordIDs)+len(r.EventIDs))
		for _, id := range r.RecordIDs {
			ids = append(ids, shortID(id))
		}
		ids = append(ids, intsToStrs(r.EventIDs)...)
		parts[i] = fmt.Sprintf("%s (ids %s)", r.Kind, strings.Join(ids, ","))
	}
	return strings.Join(parts, "; ")
}

func intsToStrs(ids []int64) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = strconv.FormatInt(id, 10)
	}
	return out
}

func firstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}

func joinInt64s(ids []int64) string {
	strs := make([]string, len(ids))
	for i, v := range ids {
		strs[i] = strconv.FormatInt(v, 10)
	}
	return strings.Join(strs, ",")
}
