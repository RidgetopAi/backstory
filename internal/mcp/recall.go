package mcp

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/recall"
	"github.com/RidgetopAi/backstory/internal/store"
	"github.com/RidgetopAi/backstory/internal/week"
)

// DefaultRecallBudgetTokens is recall's token budget when a caller does not
// send budget_tokens, mirroring the SessionStart block's own default
// (block.DefaultBudgetTokens): both are "how much of the ledger fits in
// context", so they share the same starting number until a setting
// overrides one independently.
const DefaultRecallBudgetTokens = block.DefaultBudgetTokens

// RecallParams is recall's argument shape (decision d9d456e7): budget_tokens
// plus the fields the frozen v0 schema reserves for this punch, query and
// altitude, and the schema's own `project`: a project key or a directory
// path naming another project to read (task ed31b744). Empty means the
// caller's own location as the daemon OBSERVES it. Identity (who is asking,
// as what tier) is never read from a request; only which ledger to read is.
type RecallParams struct {
	Query        string `json:"query,omitempty"`
	Altitude     string `json:"altitude,omitempty"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
	Project      string `json:"project,omitempty"`
}

// altitudeSummary etc. are RecallParams.Altitude's accepted wire values, per
// decision d9d456e7's own wording ("altitude headline|summary|full, default
// summary"). "detail" is also accepted, as an alias for full: it is the
// frozen schema's own declared enum value (tools-v0.json), and the schema
// itself is out of scope for this punch (no snapshot changes), so a caller
// that only ever reads the advertised schema must still get a sensible
// answer rather than an invalid-params rejection.
const (
	altitudeHeadline = "headline"
	altitudeSummary  = "summary"
	altitudeFull     = "full"
	altitudeDetail   = "detail"
)

// parseAltitude maps RecallParams.Altitude's wire value to the engine's
// recall.Altitude, defaulting an empty value to summary, and reports the
// canonical value actually used (so RecallResult.Altitude always names a
// real altitude, never the empty string or the "detail" alias).
func parseAltitude(raw string) (recall.Altitude, string, error) {
	switch raw {
	case "", altitudeSummary:
		return recall.AltitudeSummary, altitudeSummary, nil
	case altitudeHeadline:
		return recall.AltitudeHeadline, altitudeHeadline, nil
	case altitudeFull, altitudeDetail:
		return recall.AltitudeFull, altitudeFull, nil
	default:
		return "", "", fmt.Errorf("unknown altitude %q", raw)
	}
}

// altitudeAsked returns the altitude name to echo back: the caller's own
// value when they gave one (alias included), else the defaulted canonical one.
func altitudeAsked(raw, canonical string) string {
	if raw == "" {
		return canonical
	}
	return raw
}

// resolveAnchor turns a caller's query into a recall.Anchor (decision
// d9d456e7's WHAT TO BUILD: "anchor = the caller's observed project unless
// query names a record id or free text"): empty query anchors on the
// caller's own project; a query that resolves to an existing record's id or
// a prefix unique to one anchors on that record's neighbourhood; anything
// else anchors as free text, scoped to projectKey — "the project is ALWAYS
// the caller's observed one, never a declared one" applies here exactly as
// it does to the project anchor, so the free-text anchor is never given any
// other project's key.
func resolveAnchor(st *store.Store, projectKey string, scope store.LocationScope, query string) (recall.Anchor, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return recall.ProjectAnchor(projectKey).WithScope(scope), nil
	}
	if _, ok, err := st.FindRecordByIDPrefix(q); err != nil {
		return recall.Anchor{}, fmt.Errorf("resolve recall anchor: %w", err)
	} else if ok {
		return recall.RecordAnchor(q), nil
	}
	return recall.TextAnchor(projectKey, q).WithScope(scope), nil
}

// summaryTextChars bounds how much of an item's text this tool shows at
// summary altitude, mirroring internal/recall's own per-item cap
// (recall.summaryBodyChars): recall.Build's own Items always carry the full
// body (only its Rendered narrative is altitude-shaped, and that narrative
// renders ids short — this tool needs full ids in its own item list, never
// truncated, so a caller can quote one back verbatim), so altitude-shaping
// each item's Text for the wire is this tool's own job.
const summaryTextChars = 240

// renderItemText renders one item's body at altitude: headline keeps only
// its first line, full keeps it whole, summary truncates to
// summaryTextChars — the same three-way split PLAN.md §Phase 4 describes
// for the recall_thread narrative, applied to this tool's own per-item text
// field instead of a joined narrative string.
func renderItemText(text string, altitude recall.Altitude) string {
	switch altitude {
	case recall.AltitudeHeadline:
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			return text[:i]
		}
		return text
	case recall.AltitudeFull:
		return text
	default:
		r := []rune(text)
		if len(r) > summaryTextChars {
			return string(r[:summaryTextChars]) + "…"
		}
		return text
	}
}

// trustMark renders item's provenance status as the human-readable trust
// mark this punch's GOAL requires ("trust marks visible in the tool
// output"): a superseded item names the id of the record that superseded
// it, in the "superseded → <id>" shape a human (or an agent) reads directly
// off the tool's CallToolResult text without decoding a status enum.
func trustMark(item recall.Item) string {
	switch item.Status {
	case recall.StatusSuperseded:
		return "superseded → " + item.SupersededByID
	case recall.StatusTombstoned:
		return "tombstoned"
	case recall.StatusContradicted:
		if len(item.ContradictionEvidence) == 0 {
			return "contradicted"
		}
		return "contradicted → evidence " + joinInt64s(item.ContradictionEvidence)
	case recall.StatusPossiblyStale, recall.StatusExpired:
		return recall.StatusLabel(item)
	default:
		return "current"
	}
}

func joinInt64s(ids []int64) string {
	strs := make([]string, len(ids))
	for i, v := range ids {
		strs[i] = strconv.FormatInt(v, 10)
	}
	return strings.Join(strs, ",")
}

// RecallItem is one recall.Item rendered for the wire: ID is always the
// record's full id, never shortened (a caller matches it against a record
// it already holds, or quotes it back in a future note's `supersedes`);
// Tier is the record's stored tier, verbatim; Mark is the trust annotation
// (this punch's GOAL).
type RecallItem struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Tier string `json:"tier"`
	Mark string `json:"mark"`
	Text string `json:"text,omitempty"`
	// Next is a handoff's one-line next step (absent for other kinds and for
	// a handoff with none).
	Next string `json:"next,omitempty"`
	// GitHead, GitShort, CommitsSince, GitNote and GitStatus are additive
	// (task 5615ddae): the repo HEAD stamped at write, its short form, how
	// many commits the repo is ahead of it now (absent, with GitNote as the
	// reason, when that cannot be computed), and the rendered one-liner
	// ("written at a1b2c3d; repo now 2 commits later").
	GitHead      string `json:"git_head,omitempty"`
	GitShort     string `json:"git_short,omitempty"`
	CommitsSince *int   `json:"commits_since,omitempty"`
	GitNote      string `json:"git_note,omitempty"`
	GitStatus    string `json:"git_status,omitempty"`
}

// RecallResult is recall's Phase 4 return value: the caller's own project
// (always — DONE WHEN's "never a declared one" rule), which anchor kind the
// query resolved to, the altitude actually used, and the ordered,
// trust-annotated item set recall.Build produced.
//
// DisplayName and Message are additive result fields (task b172e778, real
// use 2026-09-28): every response names the project it resolved by both key
// and human-readable display name, and when Items is empty, Message says
// why in one line — "no records yet for <display_name> (N sessions
// observed)" when the anchor is the caller's own project with nothing ever
// written to it, or that the query matched nothing, so an agent can tell
// "no history yet" apart from "wrong project" apart from "this specific
// query found nothing" without guessing.
type RecallResult struct {
	ProjectKey  string `json:"project_key"`
	DisplayName string `json:"display_name"`
	Anchor      string `json:"anchor"`
	Query       string `json:"query,omitempty"`
	// Altitude echoes the value the caller asked for (so "detail" stays
	// "detail"); AltitudeLevel is the canonical level actually served.
	Altitude      string       `json:"altitude"`
	AltitudeLevel string       `json:"altitude_level"`
	Items         []RecallItem `json:"items"`
	Message       string       `json:"message,omitempty"`
}

// emptyResultMessage is the one-line "why" for an empty Items list (task
// b172e778's real-use fix): an empty query anchors on the caller's own
// project (resolveAnchor), so no query text means the honest empty state is
// "nothing has ever been written here" — named by session count, since a
// project with zero sessions and a project with a hundred sessions but zero
// records both render an empty item list otherwise indistinguishable. A
// non-empty query means the anchor is a record or free-text search that
// simply found nothing, a different, narrower fact worth saying differently
// so an agent does not read "no records yet" as "this project has no
// history at all" when only this one query came up empty.
func emptyResultMessage(st *store.Store, projectKey, displayName, query string) (string, error) {
	if strings.TrimSpace(query) == "" {
		n, err := st.SessionCountForProject(projectKey)
		if err != nil {
			return "", fmt.Errorf("session count for project %s: %w", projectKey, err)
		}
		return fmt.Sprintf("no records yet for %s (%d sessions observed)", displayName, n), nil
	}
	return fmt.Sprintf("query %q matched nothing", query), nil
}

// resolveRecallScope picks the ledger recall reads: the project the caller
// names (a key or a path), else the connection's own observed location —
// its cwd when known, so a repo session sees the handoffs `note` filed under
// the workspace for it — else its bare project key.
func resolveRecallScope(st *store.Store, git project.Git, id ident.Identity, ref string, workspaces []string) (string, store.LocationScope, error) {
	if ref = strings.TrimSpace(ref); ref != "" {
		return st.ResolveProjectRef(ref, git, workspaces)
	}
	if id.CWD != "" {
		ls, err := st.LocationScope(id.CWD, git, workspaces)
		return id.ProjectKey, ls, err
	}
	ls, err := st.LocationScopeForKey(id.ProjectKey)
	return id.ProjectKey, ls, err
}

// handleRecall serves the recall socket method by running the caller's
// query through internal/recall (decision d9d456e7: "replace the v0 stub
// ... with a call into internal/recall"). It never reads anything a request
// line declares about identity (id.ProjectKey is the daemon's own
// observation, the same rule handleBlock follows for the SessionStart
// block); the `project` parameter only selects which ledger to read.
func handleRecall(st *store.Store, git project.Git, id ident.Identity, raw json.RawMessage, workspaces []string) DaemonResponse {
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
	altitude, altitudeUsed, err := parseAltitude(p.Altitude)
	if err != nil {
		return errResponse("invalid-params", `invalid "altitude": `+err.Error())
	}

	projectKey, scope, err := resolveRecallScope(st, git, id, p.Project, workspaces)
	if err != nil {
		return errResponse("invalid-params", `invalid "project": `+err.Error())
	}

	anchor, err := resolveAnchor(st, projectKey, scope, p.Query)
	if err != nil {
		return errResponse("internal", err.Error())
	}

	built, err := recall.Build(st, anchor, altitude, budgetTokens, workspaces)
	if err != nil {
		return errResponse("internal", err.Error())
	}

	displayName, err := week.DisplayName(st, projectKey, workspaces)
	if err != nil {
		return errResponse("internal", err.Error())
	}

	items := make([]RecallItem, len(built.Items))
	for i, item := range built.Items {
		items[i] = RecallItem{
			ID:   item.ID,
			Kind: string(item.Kind),
			Tier: string(item.Tier),
			Mark: trustMark(item),
			Text: renderItemText(item.Text, altitude),
			Next: item.Next,
		}
		if g := item.Git; g != nil {
			items[i].GitHead = g.SHA
			items[i].GitShort = g.Short
			items[i].CommitsSince = g.CommitsSince
			items[i].GitNote = g.Reason
			items[i].GitStatus = recall.GitLabel(g)
		}
	}

	result := RecallResult{
		ProjectKey:    projectKey,
		DisplayName:   displayName,
		Anchor:        string(anchor.Kind),
		Query:         p.Query,
		Altitude:      altitudeAsked(p.Altitude, altitudeUsed),
		AltitudeLevel: altitudeUsed,
		Items:         items,
	}
	if len(items) == 0 {
		msg, err := emptyResultMessage(st, projectKey, displayName, p.Query)
		if err != nil {
			return errResponse("internal", err.Error())
		}
		result.Message = msg
	}

	b, err := json.Marshal(result)
	if err != nil {
		return errResponse("internal", err.Error())
	}
	return DaemonResponse{Result: b}
}
