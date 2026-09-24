// Package week builds This Week: one struct — Attention, Where you left
// off, The week — that `backstory this-week` renders as text and as
// --json, the Quickshell panel's single data source (decision 9be5c1d5,
// task 56d8c63d). It reads the store; it never writes to it.
package week

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// WindowDays is how many trailing calendar days (UTC), Now's own day
// included, This Week covers — every section (Attention's project scope,
// Where-you-left-off's "activity in the last 7 days", The week's per-day
// grid) reads off the same window (decision 9be5c1d5: "This Week"). It is
// a fixed part of the view's own definition, not a per-call tunable: the
// name is what a caller reads, not a flag anyone sets.
const WindowDays = 7

// maxRecordsPerProject bounds a project's own record read for the window,
// the same defensive cap recall.maxProjectRecords applies to its own
// project-anchor query: a project with years of history must not load its
// entire ledger before the window ever gets a chance to filter it down.
const maxRecordsPerProject = 1000

// Params is Build's input.
type Params struct {
	Store *store.Store
	// Now is the reference instant for the window and every "possibly
	// stale" / "expired" check. Zero means time.Now().
	Now time.Time
}

// AttentionKind is the kind of positive evidence an AttentionItem reports
// (decision 9be5c1d5's four kinds).
type AttentionKind string

const (
	AttentionPossiblyStaleHandoff    AttentionKind = "possibly-stale-handoff"
	AttentionUncommittedAtSessionEnd AttentionKind = "uncommitted-at-session-end"
	AttentionContradiction           AttentionKind = "contradiction"
	AttentionExpiredClaim            AttentionKind = "expired-claim"
)

// attentionKindOrder fixes AttentionItem's sort order across kinds — the
// order decision 9be5c1d5 lists them in — so rendered and --json output is
// deterministic regardless of which store query happened to run first.
var attentionKindOrder = map[AttentionKind]int{
	AttentionPossiblyStaleHandoff:    0,
	AttentionUncommittedAtSessionEnd: 1,
	AttentionContradiction:           2,
	AttentionExpiredClaim:            3,
}

// AttentionItem is one item in the Attention section: positive evidence
// only, never elapsed time and never the absence of activity
// (AGENT-CONTRACT.md §Outcomes are three-state; decision 9be5c1d5).
type AttentionItem struct {
	Kind       AttentionKind
	ProjectKey string
	// Reason is a one-line, human-readable explanation.
	Reason string
	// EvidenceIDs is the positive evidence this item cites: record ids and
	// timeline event ids (formatted as decimal strings), in the same mixed
	// list shape block.staleMarker and recall's status labels already use
	// for a freshness reason's own evidence.
	EvidenceIDs []string
}

// ProjectSummary is one project's Where-you-left-off row (or one child of
// a group row): everything decision 9be5c1d5's clause 2 lists.
type ProjectSummary struct {
	ProjectKey   string
	DisplayName  string
	CWD          string
	LastActivity time.Time
	// HandoffID and HandoffFirstLine are empty when the project has no
	// handoff record at all.
	HandoffID        string
	HandoffFirstLine string
	// HandoffStale mirrors whether an AttentionPossiblyStaleHandoff item
	// exists for this project's handoff — the same store.HandoffFreshness
	// call feeds both, so the two can never disagree.
	HandoffStale bool
}

// WhereLeftOffRow is one row: either a single project (Group == "") or a
// user group collapsed into one row with its members as Children (decision
// 9be5c1d5 clause 2: "Projects in a user group collapse into one group row
// with children").
type WhereLeftOffRow struct {
	// Group is the group name for a group row, else "".
	Group string
	// Project is set only when Group == "".
	Project ProjectSummary
	// Children is set only when Group != "", one entry per grouped
	// project, most recently active first.
	Children []ProjectSummary
}

// sortKey is the row's own most-recent-activity instant, the field
// Build sorts Where-you-left-off rows by (most recent first): a plain
// row's own LastActivity, or a group row's most recently active child.
func (r WhereLeftOffRow) sortKey() time.Time {
	if r.Group == "" {
		return r.Project.LastActivity
	}
	latest := r.Children[0].LastActivity
	for _, c := range r.Children[1:] {
		if c.LastActivity.After(latest) {
			latest = c.LastActivity
		}
	}
	return latest
}

// sortName is the row's tie-break key when two rows share a sortKey.
func (r WhereLeftOffRow) sortName() string {
	if r.Group == "" {
		return r.Project.ProjectKey
	}
	return r.Group
}

// DayProjectStats is one project's activity for one calendar day (UTC) in
// the window — The week section (decision 9be5c1d5 clause 3).
type DayProjectStats struct {
	// Day is the UTC calendar day, truncated to midnight.
	Day            time.Time
	ProjectKey     string
	DisplayName    string
	Sessions       int
	FilesTouched   int
	RecordsWritten int
}

// Result is Build's return value.
type Result struct {
	Attention    []AttentionItem
	WhereLeftOff []WhereLeftOffRow
	Week         []DayProjectStats
	// Rendered is the text form: a heading per non-empty section, in
	// Attention / Where you left off / The week order, each section
	// omitted entirely — no heading, no filler line — when it has nothing
	// to show (decision 9be5c1d5: "Empty -> empty array, and the text
	// render prints NOTHING for the section").
	Rendered string
}

// windowStart returns the UTC midnight that starts the WindowDays-day
// window ending on now's own UTC day (now's day included) — the one cutoff
// every section in Build reads against, so "This Week" means the same 7
// days everywhere in the result.
func windowStart(now time.Time) time.Time {
	today := now.UTC().Truncate(24 * time.Hour)
	return today.AddDate(0, 0, -(WindowDays - 1))
}

// Build reads st and returns This Week as of p.Now.
func Build(p Params) (Result, error) {
	now := p.Now
	if now.IsZero() {
		now = time.Now()
	}
	since := windowStart(now)

	keys, err := activeProjectKeys(p.Store, since)
	if err != nil {
		return Result{}, err
	}

	var attention []AttentionItem
	summaries := make(map[string]ProjectSummary, len(keys))
	for _, key := range keys {
		summary, items, err := buildProject(p.Store, key, since, now)
		if err != nil {
			return Result{}, err
		}
		summaries[key] = summary
		attention = append(attention, items...)
	}

	whereLeftOff, err := buildWhereLeftOff(p.Store, keys, summaries)
	if err != nil {
		return Result{}, err
	}

	weekGrid, err := buildWeekGrid(p.Store, keys, since, now)
	if err != nil {
		return Result{}, err
	}

	sortAttention(attention)

	// Empty, never nil: every list marshals as [] (PANEL-CONTRACT.md).
	if attention == nil {
		attention = []AttentionItem{}
	}
	res := Result{Attention: attention, WhereLeftOff: whereLeftOff, Week: weekGrid}
	res.Rendered = render(res)
	return res, nil
}

// activeProjectKeys resolves the window's project set: every project_key
// with activity since the window start, workspace identities excluded
// (decision bcc9fa54: "a workspace is not a project"), sorted for a
// deterministic iteration order.
func activeProjectKeys(st *store.Store, since time.Time) ([]string, error) {
	all, err := st.ActiveProjectKeys(since)
	if err != nil {
		return nil, fmt.Errorf("week: active project keys: %w", err)
	}
	keys := make([]string, 0, len(all))
	for _, k := range all {
		if project.IsWorkspaceKey(k) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// buildProject computes one active project's ProjectSummary and every
// AttentionItem it contributes. Both read the SAME handoff +
// store.HandoffFreshness call, so the summary's HandoffStale flag and an
// AttentionPossiblyStaleHandoff item can never disagree with each other.
func buildProject(st *store.Store, projectKey string, since, now time.Time) (ProjectSummary, []AttentionItem, error) {
	summary := ProjectSummary{ProjectKey: projectKey}

	name, err := displayName(st, projectKey)
	if err != nil {
		return ProjectSummary{}, nil, err
	}
	summary.DisplayName = name

	if sess, ok, err := st.LatestSessionForProject(projectKey); err != nil {
		return ProjectSummary{}, nil, fmt.Errorf("week: latest session for %s: %w", projectKey, err)
	} else if ok {
		summary.CWD = sess.CWD
	}

	if last, ok, err := st.LastActivity(projectKey, now); err != nil {
		return ProjectSummary{}, nil, fmt.Errorf("week: last activity for %s: %w", projectKey, err)
	} else if ok {
		summary.LastActivity = last
	}

	var attention []AttentionItem

	handoff, hasHandoff, err := st.LatestRecord(projectKey, store.KindHandoff)
	if err != nil {
		return ProjectSummary{}, nil, fmt.Errorf("week: latest handoff for %s: %w", projectKey, err)
	}
	if hasHandoff {
		summary.HandoffID = handoff.ID
		summary.HandoffFirstLine = firstLine(handoff.Text)

		reasons, err := st.HandoffFreshness(handoff)
		if err != nil {
			return ProjectSummary{}, nil, fmt.Errorf("week: handoff freshness for %s: %w", projectKey, err)
		}
		if len(reasons) > 0 {
			summary.HandoffStale = true
			attention = append(attention, AttentionItem{
				Kind:        AttentionPossiblyStaleHandoff,
				ProjectKey:  projectKey,
				Reason:      "handoff possibly stale: " + freshnessReasonSummary(reasons),
				EvidenceIDs: freshnessEvidenceIDs(reasons),
			})
		}
	}

	uncommitted, err := uncommittedSessionEndItems(st, projectKey, since)
	if err != nil {
		return ProjectSummary{}, nil, err
	}
	attention = append(attention, uncommitted...)

	contradictions, err := st.ContradictionsSince(projectKey, since)
	if err != nil {
		return ProjectSummary{}, nil, fmt.Errorf("week: contradictions for %s: %w", projectKey, err)
	}
	for _, c := range contradictions {
		attention = append(attention, AttentionItem{
			Kind:        AttentionContradiction,
			ProjectKey:  projectKey,
			Reason:      fmt.Sprintf("record %s is contradicted by %s", shortID(c.TargetID), shortID(c.SourceID)),
			EvidenceIDs: intIDs(c.Evidence),
		})
	}

	expiredClaims, err := st.ExpiredClaimsWithoutOutcome(projectKey, now)
	if err != nil {
		return ProjectSummary{}, nil, fmt.Errorf("week: expired claims for %s: %w", projectKey, err)
	}
	for _, claim := range expiredClaims {
		attention = append(attention, AttentionItem{
			Kind:        AttentionExpiredClaim,
			ProjectKey:  projectKey,
			Reason:      fmt.Sprintf("claim %s expired with no outcome recorded", shortID(claim.ID)),
			EvidenceIDs: []string{claim.ID},
		})
	}

	return summary, attention, nil
}

// uncommittedSessionEndItems is Attention kind (b): a session.git_state
// event with CouldNotObserve false and UncommittedCount > 0 — never a
// could-not-observe event, which carries no positive evidence at all
// (payload.SessionGitState's own doc: "a writer must never fold that into
// UncommittedCount 0").
func uncommittedSessionEndItems(st *store.Store, projectKey string, since time.Time) ([]AttentionItem, error) {
	events, err := st.EventsForTimeline(projectKey, since, payload.KindSessionGitState, 0)
	if err != nil {
		return nil, fmt.Errorf("week: session git-state events for %s: %w", projectKey, err)
	}
	var out []AttentionItem
	for _, e := range events {
		var gs payload.SessionGitState
		if err := json.Unmarshal([]byte(e.Payload), &gs); err != nil {
			return nil, fmt.Errorf("week: parse session.git_state payload (event %d): %w", e.ID, err)
		}
		if gs.CouldNotObserve || gs.UncommittedCount == nil || *gs.UncommittedCount <= 0 {
			continue
		}
		reason := fmt.Sprintf("session ended with %d uncommitted change(s)", *gs.UncommittedCount)
		if gs.Branch != "" {
			reason += " on branch " + gs.Branch
		}
		out = append(out, AttentionItem{
			Kind:        AttentionUncommittedAtSessionEnd,
			ProjectKey:  projectKey,
			Reason:      reason,
			EvidenceIDs: []string{strconv.FormatInt(e.ID, 10)},
		})
	}
	return out, nil
}

// sortAttention orders items by decision 9be5c1d5's own kind order, then
// project key, then reason — a total order so Build's output is
// deterministic across runs regardless of query iteration order.
func sortAttention(items []AttentionItem) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if attentionKindOrder[a.Kind] != attentionKindOrder[b.Kind] {
			return attentionKindOrder[a.Kind] < attentionKindOrder[b.Kind]
		}
		if a.ProjectKey != b.ProjectKey {
			return a.ProjectKey < b.ProjectKey
		}
		return a.Reason < b.Reason
	})
}

// buildWhereLeftOff groups summaries by project_groups membership
// (decision 9be5c1d5 clause 2): a grouped project becomes a child of its
// group's row; an ungrouped project is its own row. Rows are sorted most
// recently active first.
func buildWhereLeftOff(st *store.Store, keys []string, summaries map[string]ProjectSummary) ([]WhereLeftOffRow, error) {
	groups := map[string][]ProjectSummary{}
	var rows []WhereLeftOffRow

	for _, key := range keys {
		groupName, grouped, err := st.GroupOf(key)
		if err != nil {
			return nil, fmt.Errorf("week: group of %s: %w", key, err)
		}
		if !grouped {
			rows = append(rows, WhereLeftOffRow{Project: summaries[key]})
			continue
		}
		groups[groupName] = append(groups[groupName], summaries[key])
	}

	for name, children := range groups {
		sort.SliceStable(children, func(i, j int) bool {
			if !children[i].LastActivity.Equal(children[j].LastActivity) {
				return children[i].LastActivity.After(children[j].LastActivity)
			}
			return children[i].ProjectKey < children[j].ProjectKey
		})
		rows = append(rows, WhereLeftOffRow{Group: name, Children: children})
	}

	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		ak, bk := a.sortKey(), b.sortKey()
		if !ak.Equal(bk) {
			return ak.After(bk)
		}
		return a.sortName() < b.sortName()
	})
	return rows, nil
}

// buildWeekGrid computes The week: per UTC calendar day in the window, per
// active project, sessions/files-touched/records-written (decision
// 9be5c1d5 clause 3). It fetches each project's window-bounded events and
// records once, then buckets them by day in Go, rather than issuing one
// query per (project, day) pair.
func buildWeekGrid(st *store.Store, keys []string, since, now time.Time) ([]DayProjectStats, error) {
	days := make([]time.Time, WindowDays)
	for i := range days {
		days[i] = since.AddDate(0, 0, i)
	}

	var out []DayProjectStats
	for _, key := range keys {
		name, err := displayName(st, key)
		if err != nil {
			return nil, err
		}

		events, err := st.EventsForTimeline(key, since, "", 0)
		if err != nil {
			return nil, fmt.Errorf("week: events for %s: %w", key, err)
		}
		records, err := st.RecordsForProjectAll(key, maxRecordsPerProject)
		if err != nil {
			return nil, fmt.Errorf("week: records for %s: %w", key, err)
		}

		for _, day := range days {
			dayEnd := day.AddDate(0, 0, 1)
			stats := dayStats(events, records, day, dayEnd, now)
			if stats.Sessions == 0 && stats.FilesTouched == 0 && stats.RecordsWritten == 0 {
				continue
			}
			stats.Day = day
			stats.ProjectKey = key
			stats.DisplayName = name
			out = append(out, stats)
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.Day.Equal(b.Day) {
			return a.Day.After(b.Day)
		}
		return a.ProjectKey < b.ProjectKey
	})
	return out, nil
}

// dayStats buckets events and records into [dayStart, dayEnd): sessions is
// the count of distinct session ids among that day's events (every event
// carries a session id, including session.start, so a session that merely
// started that day with no tool use still counts); filesTouched is the
// same payload.IsMutatingFileTool rule the SessionStart block's delta slot
// and HandoffFreshness both apply (task 393d174c); recordsWritten counts
// records whose own ts falls in the day. now bounds dayEnd from above too,
// so a fixture (or a clock skew) that inserted something after "now" is
// never counted into a future day.
func dayStats(events []store.TimelineEvent, records []store.Record, dayStart, dayEnd, now time.Time) DayProjectStats {
	if dayEnd.After(now) {
		dayEnd = now
	}

	sessions := map[string]bool{}
	files := map[string]bool{}
	var recordsWritten int

	for _, e := range events {
		if e.TS.Before(dayStart) || !e.TS.Before(dayEnd) {
			continue
		}
		if e.SessionID != "" {
			sessions[e.SessionID] = true
		}
		if e.Kind == payload.KindToolUse {
			var tu payload.ToolUse
			if json.Unmarshal([]byte(e.Payload), &tu) == nil && tu.Path != "" && payload.IsMutatingFileTool(tu.Name) {
				files[tu.Path] = true
			}
		}
	}
	for _, r := range records {
		if !r.TS.Before(dayStart) && r.TS.Before(dayEnd) {
			recordsWritten++
		}
	}

	return DayProjectStats{
		Sessions:       len(sessions),
		FilesTouched:   len(files),
		RecordsWritten: recordsWritten,
	}
}

// displayName is projectKey's human-readable name: the basename of its
// stored toplevel path, or projectKey itself when the project has no
// projects row (should not happen for a key ActiveProjectKeys returned,
// since every session/record write upserts one first, but a caller must
// never crash over a stale or hand-seeded key that skipped it).
func displayName(st *store.Store, projectKey string) (string, error) {
	proj, ok, err := st.GetProject(projectKey)
	if err != nil {
		return "", fmt.Errorf("week: get project %s: %w", projectKey, err)
	}
	if !ok || proj.Toplevel == "" {
		return projectKey, nil
	}
	return filepath.Base(filepath.Clean(proj.Toplevel)), nil
}

// freshnessReasonSummary renders reasons as "<kind>, <kind>, ..." — This
// Week's compact form, distinct from block.staleMarker's more verbose,
// chat-context prose (block renders for an agent to read once; this is a
// data field a panel re-renders every time).
func freshnessReasonSummary(reasons []store.FreshnessReason) string {
	kinds := make([]string, len(reasons))
	for i, r := range reasons {
		kinds[i] = string(r.Kind)
	}
	return strings.Join(kinds, ", ")
}

// freshnessEvidenceIDs flattens every reason's record and event ids into
// one mixed list, the same shape block.staleReasonClause's idList and
// recall.staleReasonSummary's ids both already use for a freshness
// reason's evidence.
func freshnessEvidenceIDs(reasons []store.FreshnessReason) []string {
	ids := []string{} // never null in --json (PANEL-CONTRACT.md)
	for _, r := range reasons {
		ids = append(ids, r.RecordIDs...)
		ids = append(ids, intIDs(r.EventIDs)...)
	}
	return ids
}

func intIDs(ids []int64) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = strconv.FormatInt(id, 10)
	}
	return out
}

// shortIDLen mirrors recall.shortIDLen: enough of a record id to
// eyeball-distinguish items in a one-line reason.
const shortIDLen = 8

func shortID(id string) string {
	if len(id) <= shortIDLen {
		return id
	}
	return id[:shortIDLen]
}

func firstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}
