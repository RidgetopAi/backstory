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
	"time"

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
	// Scope, when set on a project or text anchor, widens "the project's
	// records" to everything belonging to a location (store.LocationScope):
	// the repo-key records plus the workspace-homed ones — a repo session's
	// handoff is filed under the workspace key (task ed31b744). Nil keeps
	// the bare ProjectKey reading.
	Scope *store.LocationScope
}

// WithScope returns a with its records read through ls.
func (a Anchor) WithScope(ls store.LocationScope) Anchor {
	a.Scope = &ls
	return a
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
	// StatusExpired marks a record whose own ExpiresAt has passed: it was
	// declared valid only until then, so it must never read as current.
	StatusExpired Status = "expired"
)

// now is the clock annotate judges ExpiresAt against; a var so tests can pin it.
var now = time.Now

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
	// Next is a handoff's one-line next step, empty for any other kind, for
	// a handoff with none, and when Status is StatusTombstoned.
	Next string
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
	// ExpiresAt is the record's own expiry, when it declared one.
	ExpiresAt *time.Time

	// StaleReasons is set only when Status is StatusPossiblyStale: every
	// reason store.HandoffFreshness found, each carrying its own evidence
	// ids.
	StaleReasons []store.FreshnessReason

	// Git is the record's write-time repo HEAD compared with the repo's HEAD
	// now; nil when the record carries no git_head (or is tombstoned).
	Git *GitStamp
}

// Result is Build's return value: the items that made the altitude's
// budget cut, in the order rendered, and the rendered text itself. Items
// always matches what Rendered shows — Build never trims one without the
// other.
type Result struct {
	Items    []Item
	Rendered string
	// Notice is set when a text anchor matched nothing and recall fell back to
	// the location's latest handoff: "no match for <query>; latest handoff
	// shown". Empty otherwise.
	Notice string
}

// Build resolves anchor to an ordered set of records — walking edges for a
// record anchor — annotates each with its trust tier and status, and
// renders the result at altitude under budgetTokens, using the same
// estimator the SessionStart block budgets against (block.EstimateTokens).
// It only reads st. workspaces is the caller's already-resolved workspace
// directory list (task 482b2320, decision f3fa04c7's clause 7): recall
// never resolves workspace dirs itself (no project.DefaultWorkspaceDirs
// call, no env read) — it only needs the list to pass through to
// store.HandoffFreshness when annotating a handoff's possibly-stale status.
func Build(st *store.Store, anchor Anchor, altitude Altitude, budgetTokens int, workspaces []string) (Result, error) {
	recs, notice, err := resolve(st, anchor)
	if err != nil {
		return Result{}, err
	}

	stamper := newGitStamper(st)
	items := make([]Item, len(recs))
	for i, rec := range recs {
		item, err := annotate(st, rec, workspaces)
		if err != nil {
			return Result{}, err
		}
		if item.Status != StatusTombstoned {
			if item.Git, err = stamper.stamp(rec); err != nil {
				return Result{}, err
			}
		}
		items[i] = item
	}

	res := fit(items, altitude, budgetTokens)
	if len(res.Items) > 0 {
		res.Notice = notice
	}
	return res, nil
}

// resolve turns anchor into an ordered record set, before status
// annotation or altitude rendering: project and record anchors are already
// in sequence order (RecordsForProjectAll / RecordsByIDs); a text anchor's
// FTS match set is re-ordered into sequence order the same way, so every
// anchor kind's output obeys the one ordering rule (SCHEMA.md invariant
// 10: order by sequence, never ts).
func resolve(st *store.Store, anchor Anchor) ([]store.Record, string, error) {
	switch anchor.Kind {
	case AnchorProject:
		recs, err := projectRecords(st, anchor)
		return recs, "", err
	case AnchorRecord:
		recs, err := resolveRecordAnchor(st, anchor.RecordID)
		return recs, "", err
	case AnchorText:
		return resolveTextAnchor(st, anchor)
	default:
		return nil, "", fmt.Errorf("recall: unknown anchor kind %q", anchor.Kind)
	}
}

// projectRecords reads anchor's whole ledger (scoped to its location when it
// has one), newest first by sequence.
func projectRecords(st *store.Store, anchor Anchor) ([]store.Record, error) {
	var recs []store.Record
	var err error
	if anchor.Scope != nil {
		recs, err = st.RecordsForLocation(*anchor.Scope, maxProjectRecords)
	} else {
		recs, err = st.RecordsForProjectAll(anchor.ProjectKey, maxProjectRecords)
	}
	if err != nil {
		return nil, fmt.Errorf("recall: project anchor: %w", err)
	}
	return recs, nil
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

// handoffKindWords are the query words that name the handoff record kind
// itself: a query containing one matches every handoff at the location, since
// an agent asking for "resume" or the "handoff" wants the handoff whatever
// words it happens to contain.
var handoffKindWords = map[string]bool{"handoff": true, "handoffs": true, "resume": true}

// resolveTextAnchor runs the FTS match (any query term, best bm25 first;
// store.SearchRecordsInProject / SearchRecordsInScope), adds the location's
// handoffs the index cannot see — those whose next text contains a query term
// or when the query names the handoff kind — and re-orders the match set into
// sequence order. When nothing matches it falls back to the location's latest
// handoff and says so in the returned notice.
func resolveTextAnchor(st *store.Store, anchor Anchor) ([]store.Record, string, error) {
	var results []store.SearchResult
	var err error
	if anchor.Scope != nil {
		results, err = st.SearchRecordsInScope(*anchor.Scope, anchor.Text, maxTextResults)
	} else {
		results, err = st.SearchRecordsInProject(anchor.ProjectKey, anchor.Text, maxTextResults)
	}
	if err != nil {
		return nil, "", fmt.Errorf("recall: text anchor: %w", err)
	}
	ids := make([]string, 0, len(results))
	seen := map[string]bool{}
	for _, r := range results {
		ids = append(ids, r.ID)
		seen[r.ID] = true
	}

	all, err := projectRecords(st, anchor)
	if err != nil {
		return nil, "", err
	}
	terms := strings.Fields(strings.ToLower(anchor.Text))
	var latest *store.Record
	for i, rec := range all {
		if rec.Kind != store.KindHandoff || rec.TombstonedAt != nil {
			continue
		}
		if latest == nil {
			latest = &all[i]
		}
		if !seen[rec.ID] && handoffMatches(rec, terms) {
			seen[rec.ID] = true
			ids = append(ids, rec.ID)
		}
	}

	notice := ""
	if len(ids) == 0 && latest != nil {
		ids = append(ids, latest.ID)
		notice = fmt.Sprintf("no match for %s; latest handoff shown", anchor.Text)
	}
	recs, err := st.RecordsByIDs(ids)
	if err != nil {
		return nil, "", fmt.Errorf("recall: text anchor: %w", err)
	}
	return recs, notice, nil
}

// handoffMatches reports whether a handoff answers terms (lower-cased query
// words): one names the handoff kind, or appears in its next text.
func handoffMatches(h store.Record, terms []string) bool {
	next := strings.ToLower(h.Next)
	for _, t := range terms {
		if handoffKindWords[t] || (next != "" && strings.Contains(next, t)) {
			return true
		}
	}
	return false
}

// Annotate is annotate's exported form: the single status code path, shared
// with callers (the `records` CLI) that list records outside a recall
// narrative but must label them exactly as recall does.
func Annotate(st *store.Store, rec store.Record, workspaces []string) (Item, error) {
	return annotate(st, rec, workspaces)
}

// annotate builds an Item from rec: tier carried verbatim, status derived
// in a fixed precedence — tombstoned first (text omitted per SCHEMA.md
// invariant 1, so nothing downstream can leak it back in), then superseded,
// then contradicted, else current. A record can technically satisfy more
// than one condition (e.g. a tombstoned record superseded before deletion);
// this precedence picks the single status callers see, always the
// strongest one, rather than leaving it ambiguous which wins.
func annotate(st *store.Store, rec store.Record, workspaces []string) (Item, error) {
	edges, err := st.EdgesTouching(rec.ID)
	if err != nil {
		return Item{}, fmt.Errorf("recall: annotate %s: %w", rec.ID, err)
	}

	item := Item{
		ID:     rec.ID,
		Kind:   rec.Kind,
		Tier:   rec.Tier,
		Text:   rec.Text,
		Next:   rec.Next,
		Status: StatusCurrent,
		Edges:  edges,
	}
	if rec.ExpiresAt != nil {
		item.ExpiresAt = rec.ExpiresAt
	}
	if rec.TombstonedAt != nil {
		item.Status = StatusTombstoned
		item.Text = ""
		item.Next = ""
		return item, nil
	}

	for _, e := range edges {
		if e.Type == store.EdgeSupersedes && e.ToID == rec.ID {
			item.Status = StatusSuperseded
			item.SupersededByID = e.FromID
			return item, nil
		}
	}

	if rec.ExpiresAt != nil && !rec.ExpiresAt.After(now()) {
		item.Status = StatusExpired
		return item, nil
	}

	// A handoff's contradiction signal is folded into HandoffFreshness's own
	// reasons (FreshnessContradicted), affirm-aware in a way the plain
	// StatusContradicted check below is not, so a handoff never falls
	// through to that check at all — flagged or not.
	if rec.Kind == store.KindHandoff {
		reasons, err := st.HandoffFreshness(rec, workspaces)
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
	if item.Git != nil {
		header += " · " + gitLabel(item.Git)
	}
	switch altitude {
	case AltitudeHeadline:
		out := header
		if fl := firstLine(item.Text); fl != "" {
			out += " · " + fl
		}
		if nl := firstLine(item.Next); nl != "" {
			out += " · Next: " + nl
		}
		return out
	case AltitudeSummary:
		return withNext(renderBody(header, item.Text, summaryBodyChars), item.Next)
	case AltitudeFull:
		return withNext(renderBody(header, item.Text, 0), item.Next)
	default:
		return header
	}
}

// withNext appends a handoff's next step on its own "Next:" line.
func withNext(body, next string) string {
	if next == "" {
		return body
	}
	return body + "\nNext: " + next
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

// StatusLabel is the engine's own human-readable label for item's status.
func StatusLabel(item Item) string { return statusLabel(item) }

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
	case StatusExpired:
		return "expired " + item.ExpiresAt.UTC().Format(time.RFC3339)
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
