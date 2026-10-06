// Package week builds This Week: one struct — Attention, Where you left
// off, The week — that `backstory this-week` renders as text and as
// --json, the Quickshell panel's single data source (decision 9be5c1d5,
// task 56d8c63d). It reads the store; it never writes to it.
package week

import (
	"encoding/json"
	"fmt"
	"regexp"
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
	// Git resolves a project's toplevel repo identity for decision
	// f3fa04c7's per-repo handoff resolution (store.LatestHandoffAt) when a
	// project's latest session lives inside a workspace, and (clause 5,
	// task 482b2320) for deriving a home's own active work-location labels
	// from its sessions' observed file-touching events. nil is safe as long
	// as no active project_key in the window is itself a workspace
	// identity: Build skips home-label derivation entirely rather than
	// calling a nil Git, the same "no workspace activity to resolve" case
	// every pre-workspace caller and test already exercises.
	Git project.Git
	// WorkspaceDirs is the caller's already-resolved workspace directory
	// list (task 482b2320, decision f3fa04c7's clause 7): week never
	// resolves workspace dirs itself (no project.DefaultWorkspaceDirs call,
	// no env read), so its output depends only on what the caller passes,
	// never on the process environment. nil is safe the same way a nil Git
	// is: Build then finds no active project_key resolves inside a
	// workspace.
	WorkspaceDirs []string
	// Locations is the excluded-location rule set (LocationRules, rule R2),
	// resolved by the caller from $HOME and the environment. The zero value
	// excludes nothing.
	Locations LocationRules
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
	// HandoffNext is the handoff's one-line next step: its stored `next`
	// when set, else its first line with leading boilerplate stripped
	// (handoffNextFromText). "" when there is no handoff.
	HandoffNext string
	// LastAgent is the agent of the project's most recently started session,
	// "" when unknown.
	LastAgent string
	// Agents is every known harness active in the window for this row,
	// newest last_activity first; LastAgent is Agents[0].Agent.
	Agents []AgentSummary
	// HandoffAgent is the agent of the session that wrote the handoff, ""
	// when unknown.
	HandoffAgent string
	// HandoffStale mirrors whether an AttentionPossiblyStaleHandoff item
	// exists for this project's handoff — the same store.HandoffFreshness
	// call feeds both, so the two can never disagree.
	HandoffStale bool
}

// AgentSummary is one agent's footprint in a row's window.
type AgentSummary struct {
	Agent        string
	LastActivity time.Time
	SessionCount int
}

func agentSummaries(as []store.AgentActivity) []AgentSummary {
	out := make([]AgentSummary, len(as))
	for i, a := range as {
		out[i] = AgentSummary{Agent: a.Agent, LastActivity: a.LastActivity, SessionCount: a.SessionCount}
	}
	return out
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

	repoKeys, homeKeys, err := partitionActiveProjectKeys(p.Store, since, p.Locations)
	if err != nil {
		return Result{}, err
	}

	workspaces := p.WorkspaceDirs

	var attention []AttentionItem
	summaries := make(map[string]ProjectSummary, len(repoKeys))
	identities := make([]string, 0, len(repoKeys))
	coveredLabels := map[string]bool{}

	for _, key := range repoKeys {
		summary, items, err := buildProject(p.Store, key, since, now, p.Git, workspaces)
		if err != nil {
			return Result{}, err
		}
		summaries[key] = summary
		identities = append(identities, key)
		coveredLabels[summary.DisplayName] = true
		attention = append(attention, items...)
	}

	// Work-location labels (decision f3fa04c7 clause 5, task 482b2320): a
	// home's sessions can touch repos that never got a direct session of
	// their own (a session started at the workspace root itself), so those
	// repos never surface as an active project_key above — their own
	// row/week-bars come from the home's active labels instead. A label
	// already covered by a repoKey's own display name above (the ordinary
	// case: a session started directly inside the repo) is skipped here so
	// the same repo never produces two rows. p.Git == nil skips this
	// entirely rather than resolving any label: Params.Git's own contract
	// ("nil is safe as long as no active project's session resolves to a
	// workspace home") extends to this home-wide scan, so a caller with no
	// workspace-active session to worry about (every pre-workspace test)
	// keeps working with no Git at all, exactly as before this punch.
	var homeUnits []homeLabelUnit
	for _, home := range homeKeys {
		if p.Git == nil {
			break
		}
		locs, err := p.Store.ActiveHomeLabels(home, since, now, p.Git, workspaces, func(sess store.Session) (bool, error) {
			return p.Store.SessionHasRealWork(sess, since, RealWork)
		})
		if err != nil {
			return Result{}, fmt.Errorf("week: active home labels for %s: %w", home, err)
		}
		for _, loc := range locs {
			if coveredLabels[loc.Label] || p.Locations.Excluded(loc.Dir) {
				continue
			}
			coveredLabels[loc.Label] = true

			key := project.Key(loc.Dir, p.Git, workspaces)
			summary, items, err := buildHomeLabelProject(p.Store, since, now, loc, key, p.Git, workspaces)
			if err != nil {
				return Result{}, err
			}
			identity := homeLabelIdentity(home, loc.Label)
			summaries[identity] = summary
			identities = append(identities, identity)
			attention = append(attention, items...)
			homeUnits = append(homeUnits, homeLabelUnit{loc: loc, key: key})
		}
	}

	whereLeftOff, err := buildWhereLeftOff(p.Store, identities, summaries)
	if err != nil {
		return Result{}, err
	}

	weekGrid, err := buildWeekGrid(p.Store, repoKeys, since, now, workspaces)
	if err != nil {
		return Result{}, err
	}
	homeWeekGrid, err := buildHomeLabelWeekGrid(p.Store, homeUnits, since, now, p.Git, workspaces)
	if err != nil {
		return Result{}, err
	}
	weekGrid = append(weekGrid, homeWeekGrid...)
	sortWeekGrid(weekGrid)

	sortAttention(attention)

	// Empty, never nil: every list marshals as [] (PANEL-CONTRACT.md).
	if attention == nil {
		attention = []AttentionItem{}
	}
	res := Result{Attention: attention, WhereLeftOff: whereLeftOff, Week: weekGrid}
	res.Rendered = render(res)
	return res, nil
}

// partitionActiveProjectKeys splits ActiveProjectKeys(since) into repo keys
// (This Week's original one-row-per-project set, workspace identities
// excluded per decision bcc9fa54: "a workspace is not a project") and home
// keys — workspace identities themselves active in the window, either
// because a handoff was filed there (HOME) or because a session started at
// the workspace root itself (decision f3fa04c7 clause 5). Both lists are
// sorted for a deterministic iteration order.
//
// Only keys with real work (RealWork, rule R1) are considered, and repo keys
// whose location is excluded (rules, R2) are dropped — read-side only.
func partitionActiveProjectKeys(st *store.Store, since time.Time, rules LocationRules) (repoKeys, homeKeys []string, err error) {
	all, err := st.RealWorkProjectKeys(since, RealWork)
	if err != nil {
		return nil, nil, fmt.Errorf("week: active project keys: %w", err)
	}
	for _, k := range all {
		if project.IsWorkspaceKey(k) {
			homeKeys = append(homeKeys, k)
			continue
		}
		dir, err := projectDir(st, k)
		if err != nil {
			return nil, nil, err
		}
		if rules.Excluded(dir) {
			continue
		}
		repoKeys = append(repoKeys, k)
	}
	sort.Strings(repoKeys)
	sort.Strings(homeKeys)
	return repoKeys, homeKeys, nil
}

// homeLabelIdentity is the internal (never-displayed) identity a home
// label's ProjectSummary is keyed and sorted by: home and label joined by a
// NUL byte, a separator no real project_key or label can contain in
// practice (the same reasoning project.Key's own keySeparator comment
// gives), so it can never collide with a repoKey's own identity string.
func homeLabelIdentity(home, label string) string {
	return home + "\x00" + label
}

// homeLabelUnit carries one active home label's own store.ActiveWorkLocation
// alongside the project.Key already resolved for it (buildHomeLabelProject's
// own computation, reused rather than recomputed) — buildHomeLabelWeekGrid's
// input.
type homeLabelUnit struct {
	loc store.ActiveWorkLocation
	key string
}

// sortWeekGrid orders Week entries most-recent-day-first, project_key as
// the tie-break — the same order buildWeekGrid's own sort applied before
// home-label entries existed, now applied once to the merged repo + home
// label grid.
func sortWeekGrid(grid []DayProjectStats) {
	sort.SliceStable(grid, func(i, j int) bool {
		a, b := grid[i], grid[j]
		if !a.Day.Equal(b.Day) {
			return a.Day.After(b.Day)
		}
		return a.ProjectKey < b.ProjectKey
	})
}

// buildProject computes one active project's ProjectSummary and every
// AttentionItem it contributes. Both read the SAME handoff +
// store.HandoffFreshness call, so the summary's HandoffStale flag and an
// AttentionPossiblyStaleHandoff item can never disagree with each other.
func buildProject(st *store.Store, projectKey string, since, now time.Time, git project.Git, workspaces []string) (ProjectSummary, []AttentionItem, error) {
	summary := ProjectSummary{ProjectKey: projectKey}

	name, err := DisplayName(st, projectKey, workspaces)
	if err != nil {
		return ProjectSummary{}, nil, err
	}
	summary.DisplayName = name

	sess, hasSess, err := st.LatestSessionForProject(projectKey)
	if err != nil {
		return ProjectSummary{}, nil, fmt.Errorf("week: latest session for %s: %w", projectKey, err)
	} else if hasSess {
		summary.CWD = sess.CWD
	}

	ids, err := st.ProjectSessionIDs(projectKey)
	if err != nil {
		return ProjectSummary{}, nil, fmt.Errorf("week: sessions for %s: %w", projectKey, err)
	}
	acts, err := st.AgentActivityForSessions(ids, since, now)
	if err != nil {
		return ProjectSummary{}, nil, fmt.Errorf("week: agents for %s: %w", projectKey, err)
	}
	summary.Agents = agentSummaries(acts)
	if len(acts) > 0 {
		summary.LastAgent = acts[0].Agent
	}

	if last, ok, err := st.LastActivity(projectKey, now); err != nil {
		return ProjectSummary{}, nil, fmt.Errorf("week: last activity for %s: %w", projectKey, err)
	} else if ok {
		summary.LastActivity = last
	}

	var attention []AttentionItem

	handoff, hasHandoff, err := projectHandoff(st, projectKey, sess, hasSess, git, workspaces)
	if err != nil {
		return ProjectSummary{}, nil, fmt.Errorf("week: latest handoff for %s: %w", projectKey, err)
	}
	if hasHandoff {
		summary.HandoffID = handoff.ID
		agent, err := st.SessionAgent(handoff.SessionID)
		if err != nil {
			return ProjectSummary{}, nil, err
		}
		summary.HandoffAgent = agent
		summary.HandoffFirstLine = firstLine(handoff.Text)
		summary.HandoffNext = handoffNext(handoff)

		reasons, err := st.HandoffFreshness(handoff, workspaces)
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

// projectHandoff resolves projectKey's own handoff for the Where-you-left-
// off / Attention slot (decision f3fa04c7, task ed31b744): a session inside
// a workspace files its handoffs under the workspace's home key, so the
// row's handoff is the newest one belonging to the row's location
// (store.LatestHandoffAt — the resolver block's Resume, recall, export and
// `records --location` share). Outside any workspace (or with no session at
// all), this is exactly the old st.LatestRecord(projectKey, KindHandoff).
func projectHandoff(st *store.Store, projectKey string, sess store.Session, hasSess bool, git project.Git, workspaces []string) (store.Record, bool, error) {
	if hasSess {
		if _, ok := project.WorkspaceHome(sess.CWD, workspaces); ok {
			return st.LatestHandoffAt(sess.CWD, git, workspaces)
		}
	}
	return st.LatestRecord(projectKey, store.KindHandoff)
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
	// Only the LATEST observable event counts: events arrive oldest-first, so
	// the last one that is not could-not-observe is the project's current
	// state. An earlier dirty event is superseded by a later clean one, and a
	// later could-not-observe event carries no evidence either way.
	var latest *store.TimelineEvent
	var latestGS payload.SessionGitState
	for i := range events {
		e := events[i]
		var gs payload.SessionGitState
		if err := json.Unmarshal([]byte(e.Payload), &gs); err != nil {
			return nil, fmt.Errorf("week: parse session.git_state payload (event %d): %w", e.ID, err)
		}
		if gs.CouldNotObserve || gs.UncommittedCount == nil {
			continue
		}
		latest, latestGS = &events[i], gs
	}
	var out []AttentionItem
	if latest != nil && *latestGS.UncommittedCount > 0 {
		reason := fmt.Sprintf("session ended with %d uncommitted change(s)", *latestGS.UncommittedCount)
		if latestGS.Branch != "" {
			reason += " on branch " + latestGS.Branch
		}
		out = append(out, AttentionItem{
			Kind:        AttentionUncommittedAtSessionEnd,
			ProjectKey:  projectKey,
			Reason:      reason,
			EvidenceIDs: []string{strconv.FormatInt(latest.ID, 10)},
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
func buildWeekGrid(st *store.Store, keys []string, since, now time.Time, workspaces []string) ([]DayProjectStats, error) {
	days := make([]time.Time, WindowDays)
	for i := range days {
		days[i] = since.AddDate(0, 0, i)
	}

	var out []DayProjectStats
	for _, key := range keys {
		name, err := DisplayName(st, key, workspaces)
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

	sortWeekGrid(out)
	return out, nil
}

// buildHomeLabelProject computes a synthetic ProjectSummary for a
// work-location label whose only observed activity comes from a home
// session with no per-repo project_key of its own (decision f3fa04c7 clause
// 5, task 482b2320): a session started at the workspace root itself, whose
// file-touching events resolve to a repo/folder that never got its own
// session. Its handoff resolves exactly as projectHandoff's home-scoped
// branch does (store.LatestHandoffAt), and the same store.HandoffFreshness
// call feeds both the summary's HandoffStale flag and any
// AttentionPossiblyStaleHandoff item, so the two can never disagree — the
// same invariant buildProject's own handoff handling keeps. Unlike
// buildProject, this contributes no uncommitted/contradiction/expired-claim
// Attention items: those all read a real project_key's own record scope
// (session.git_state events, decisions, claims), which a label backed only
// by a home session's file paths never carries one of its own (class
// members deferred: documented in this task's commit, not silently
// dropped).
func buildHomeLabelProject(st *store.Store, since, now time.Time, loc store.ActiveWorkLocation, key string, git project.Git, workspaces []string) (ProjectSummary, []AttentionItem, error) {
	summary := ProjectSummary{
		ProjectKey:   key,
		DisplayName:  loc.Label,
		CWD:          loc.Dir,
		LastActivity: loc.LastActivity,
	}

	acts, err := st.AgentActivityForSessions(loc.AgentSessionIDs, since, now)
	if err != nil {
		return ProjectSummary{}, nil, fmt.Errorf("week: agents for label %s: %w", loc.Label, err)
	}
	summary.Agents = agentSummaries(acts)
	if len(acts) > 0 {
		summary.LastAgent = acts[0].Agent
	}

	var attention []AttentionItem
	handoff, hasHandoff, err := st.LatestHandoffAt(loc.Dir, git, workspaces)
	if err != nil {
		return ProjectSummary{}, nil, fmt.Errorf("week: handoff for label %s: %w", loc.Label, err)
	}
	if hasHandoff {
		summary.HandoffID = handoff.ID
		agent, err := st.SessionAgent(handoff.SessionID)
		if err != nil {
			return ProjectSummary{}, nil, err
		}
		summary.HandoffAgent = agent
		summary.HandoffFirstLine = firstLine(handoff.Text)
		summary.HandoffNext = handoffNext(handoff)

		reasons, err := st.HandoffFreshness(handoff, workspaces)
		if err != nil {
			return ProjectSummary{}, nil, fmt.Errorf("week: handoff freshness for label %s: %w", loc.Label, err)
		}
		if len(reasons) > 0 {
			summary.HandoffStale = true
			attention = append(attention, AttentionItem{
				Kind:        AttentionPossiblyStaleHandoff,
				ProjectKey:  summary.ProjectKey,
				Reason:      "handoff possibly stale: " + freshnessReasonSummary(reasons),
				EvidenceIDs: freshnessEvidenceIDs(reasons),
			})
		}
	}

	return summary, attention, nil
}

// buildHomeLabelWeekGrid computes The week's per-label bars (decision
// f3fa04c7 clause 5, task 482b2320) for every home label built above: the
// per-repo-key analog of buildWeekGrid, but scoped by a label's own
// contributing sessions (store.EventsForSessionsSince /
// RecordsForSessionsSince) rather than by project_key, since a home label
// has no project_key of its own with records filed under it.
func buildHomeLabelWeekGrid(st *store.Store, units []homeLabelUnit, since, now time.Time, git project.Git, workspaces []string) ([]DayProjectStats, error) {
	days := make([]time.Time, WindowDays)
	for i := range days {
		days[i] = since.AddDate(0, 0, i)
	}

	var out []DayProjectStats
	for _, u := range units {
		events, err := st.EventsForSessionsSince(u.loc.SessionIDs, since)
		if err != nil {
			return nil, fmt.Errorf("week: events for label %s: %w", u.loc.Label, err)
		}
		records, err := st.RecordsForSessionsSince(u.loc.SessionIDs, since)
		if err != nil {
			return nil, fmt.Errorf("week: records for label %s: %w", u.loc.Label, err)
		}

		for _, day := range days {
			dayEnd := day.AddDate(0, 0, 1)
			stats := homeLabelDayStats(events, records, u.loc.Label, day, dayEnd, now, git, workspaces)
			if stats.Sessions == 0 && stats.FilesTouched == 0 && stats.RecordsWritten == 0 {
				continue
			}
			stats.Day = day
			stats.ProjectKey = u.key
			stats.DisplayName = u.loc.Label
			out = append(out, stats)
		}
	}
	return out, nil
}

// homeLabelDayStats is dayStats' own bucketing, narrowed to one label: a
// session under a home can touch more than one label the same day, so
// (unlike dayStats, where every event in the input unambiguously belongs to
// one project) sessions and filesTouched only count a file-touching event
// whose own path resolves (project.LabelForPath) to label — a bare
// session.start or a Read carries no file identity, so it never counts
// toward any one label here. recordsWritten counts every record ts in the
// day among the label's own contributing sessions (RecordsForSessionsSince
// is already scoped to those): a session that notes one handoff covering
// several labels attributes that one record to each label its session
// touched, since the record itself names no single repo of its own.
func homeLabelDayStats(events []store.TimelineEvent, records []store.Record, label string, dayStart, dayEnd, now time.Time, git project.Git, workspaces []string) DayProjectStats {
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
		if e.Kind != payload.KindToolUse {
			continue
		}
		var tu payload.ToolUse
		if json.Unmarshal([]byte(e.Payload), &tu) != nil || tu.Path == "" || !payload.IsMutatingFileTool(tu.Name) {
			continue
		}
		if project.LabelForPath(tu.Path, git, workspaces) != label {
			continue
		}
		files[tu.Path] = true
		if e.SessionID != "" {
			sessions[e.SessionID] = true
		}
	}
	for _, r := range records {
		// A tombstoned record was deleted by the human: it is not "written"
		// (RecordsForProjectAll returns tombstoned rows).
		if r.TombstonedAt != nil {
			continue
		}
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
		// A tombstoned record was deleted by the human: it is not "written"
		// (RecordsForProjectAll returns tombstoned rows).
		if r.TombstonedAt != nil {
			continue
		}
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

// DisplayName is projectKey's human-readable name: its stored toplevel
// path, made workspace-relative when it lives inside a workspace ("
// projects/omarcade", decision f3fa04c7's LABELS display rule) — else the
// basename it always was (project.WorkspaceRelativeName's fallback is
// exactly the old filepath.Base(toplevel), so this is a strict superset) —
// or projectKey itself when the project has no projects row (should not
// happen for a key ActiveProjectKeys returned, since every session/record
// write upserts one first, but a caller must never crash over a stale or
// hand-seeded key that skipped it).
//
// Exported (task 4fe02e30 round 5) so `backstory group list --json`'s
// `projects` array computes display_name through this SAME function
// rather than a second, drifting copy of the workspace-relative rule
// (decision 9be5c1d5's group-list fix: a git project_key's last path
// segment is `<repo>.git`, never a display name a human should see).
func DisplayName(st *store.Store, projectKey string, workspaces []string) (string, error) {
	proj, ok, err := st.GetProject(projectKey)
	if err != nil {
		return "", fmt.Errorf("week: get project %s: %w", projectKey, err)
	}
	if !ok || proj.Toplevel == "" {
		return projectKey, nil
	}
	return project.WorkspaceRelativeName(proj.Toplevel, workspaces), nil
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

// handoffBoilerplate matches the leading boilerplate of a handoff's first
// line: optional ★, the word HANDOFF, and an ISO date with optional time and
// Z, each followed by the separators - — – . : and whitespace. Every piece
// is optional, but separators are only consumed after a piece, so a line
// that starts with none of them is left alone.
var handoffBoilerplate = regexp.MustCompile(
	`^(?:★[\s]*)?` +
		`(?:(?i:HANDOFF)\b[\s\-—–.:]*)?` +
		`(?:\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?)?(?:Z|[+-]\d{2}:?\d{2})?[\s\-—–.:]*)?`)

// handoffNext is the Next line a project's panel row shows: the handoff's
// stored next when set, else the first line with boilerplate stripped. A
// first line that is nothing but boilerplate falls back to itself rather
// than going blank.
func handoffNext(h store.Record) string {
	if next := strings.TrimSpace(h.Next); next != "" {
		return next
	}
	line := strings.TrimSpace(firstLine(h.Text))
	if stripped := strings.TrimSpace(handoffBoilerplate.ReplaceAllString(line, "")); stripped != "" {
		return stripped
	}
	return line
}

func firstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}

// projectDir is the directory a project key's location rules are judged
// against: its latest session's cwd, else its recorded toplevel, else the
// key itself (a non-repo project's key is its cwd).
func projectDir(st *store.Store, key string) (string, error) {
	sess, ok, err := st.LatestSessionForProject(key)
	if err != nil {
		return "", fmt.Errorf("week: latest session for %s: %w", key, err)
	}
	if ok && sess.CWD != "" {
		return sess.CWD, nil
	}
	proj, ok, err := st.GetProject(key)
	if err != nil {
		return "", fmt.Errorf("week: project %s: %w", key, err)
	}
	if ok && proj.Toplevel != "" {
		return proj.Toplevel, nil
	}
	return key, nil
}
