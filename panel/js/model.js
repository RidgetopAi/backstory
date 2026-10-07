.pragma library

// model.js is the ONLY place in this plugin that reads a field out of
// `backstory this-week --json` (PANEL-CONTRACT.md). Every other .qml file
// goes through one of these functions instead of touching `.field` on a
// parsed JSON object directly, so panel_contract_test.go can find every
// field this plugin depends on by reading this one file, and a contract
// change (a renamed or removed field in PANEL-CONTRACT.md's golden) shows
// up here as a single failing assertion instead of a silent `undefined`
// somewhere in a QML binding.

// Top level (PANEL-CONTRACT.md "Top level"): all three arrays are always
// present in a real `this-week --json` payload, so no caller needs an
// existence check before iterating — but round 3's desk log flooded with
// `TypeError: Cannot read property 'length' of undefined` from exactly
// this gap: a malformed/partial payload (this plugin's own `{}` fallback
// on a JSON.parse failure, Panel.qml's `root.data = null` catch) reaches
// these accessors before the "always present" contract ever applies. Every
// list accessor in this file falls back to `[]` for an absent field so a
// caller's `.length` is always safe, never a caller's own guard.
function topAttention(data) { return data.attention || [] }
function topWhereLeftOff(data) { return data.where_left_off || [] }
function topWeek(data) { return data.week || [] }

// here (PANEL-CONTRACT.md "`here` and `window`"): only present with
// `--here`; null when absent so callers branch on one value.
function topHere(data) { return data.here || null }
function hereProjectKey(h) { return h.project_key }
function hereDisplayName(h) { return h.display_name }
function hereCwd(h) { return h.cwd }
function hereSource(h) { return h.source }

// bareSummary: a summary for a project with no where-you-left-off row (the
// `here` folder has no activity yet): no handoff, no window.
function bareSummary(projectKey, displayName, cwd) {
  return { project_key: projectKey, display_name: displayName, cwd: cwd }
}

// Attention — AttentionItem (PANEL-CONTRACT.md "Attention").
function attentionKind(item) { return item.kind }
function attentionProjectKey(item) { return item.project_key }
function attentionReason(item) { return item.reason }
// Set only on a possibly-stale-handoff item: the id `backstory affirm` takes.
function attentionHandoffId(item) { return item.handoff_id || "" }
function attentionEvidenceIds(item) { return item.evidence_ids || [] }

// Where you left off — one row is either `{project}` (standalone) or
// `{group, children}` (group row); a caller branches on rowGroup(row)
// being non-empty (PANEL-CONTRACT.md "Where you left off").
function rowGroup(row) { return row.group }
function rowProject(row) { return row.project }
function rowChildren(row) { return row.children || [] }

// ProjectSummary — a standalone row's `project`, or one entry of a group
// row's `children` (PANEL-CONTRACT.md "ProjectSummary"). handoff_id,
// handoff_first_line and handoff_stale are only present on a project with
// a handoff record; QML callers treat their absence as "no handoff", never
// as an error.
function summaryProjectKey(p) { return p.project_key }
function summaryDisplayName(p) { return p.display_name }
function summaryCwd(p) { return p.cwd }
function summaryLastActivity(p) { return p.last_activity }
function summaryHandoffId(p) { return p.handoff_id }
function summaryHandoffFirstLine(p) { return p.handoff_first_line }
function summaryHandoffStale(p) { return p.handoff_stale }
function summaryHandoffNext(p) { return p.handoff_next }
function summaryLastAgent(p) { return p.last_agent }
// agents: the per-agent footprint, newest first ({agent, last_activity,
// session_count}); handoff_agent: the agent that wrote the row's handoff,
// "" when unknown.
function summaryAgents(p) { return p.agents || [] }
function summaryHandoffAgent(p) { return p.handoff_agent || "" }
function agentId(a) { return a.agent }
function agentLastActivity(a) { return a.last_activity }
function agentSessionCount(a) { return a.session_count }
// window: Hyprland address of an open window for the project; only with
// `--here auto`, "" when none.
function summaryWindow(p) { return p.window }
// tmux: tmux target session:window.pane of the project's pane inside that
// window; present exactly when `window` is, "" when none.
function summaryTmux(p) { return p.tmux }

// The week — DayProjectStats (PANEL-CONTRACT.md "The week").
function dayDay(d) { return d.day }
function dayProjectKey(d) { return d.project_key }
function dayDisplayName(d) { return d.display_name }
function daySessions(d) { return d.sessions }
function dayFilesTouched(d) { return d.files_touched }
function dayRecordsWritten(d) { return d.records_written }

// The workspace directory every display name is relative to; shortName
// drops it so rows read "foo", not "projects/foo".
var WORKSPACE_DIR_PREFIX = "projects/"

// shortName: a display name without its leading "projects/". Other names
// are unchanged, and the result is never empty ("projects" stays "projects").
function shortName(displayName) {
  var n = displayName || ""
  if (n.indexOf(WORKSPACE_DIR_PREFIX) !== 0 || n.length === WORKSPACE_DIR_PREFIX.length) return n
  return n.slice(WORKSPACE_DIR_PREFIX.length)
}
