.pragma library
.import "model.js" as Model
.import "format.js" as Format

// derive.js: values computed from the this-week payload. Field reads stay
// in model.js (the only file allowed to name a JSON field); this file only
// calls its accessors.

// allSummaries flattens Where-you-left-off into its project summaries
// in row order.
function allSummaries(data) {
  var out = []
  var rows = Model.topWhereLeftOff(data)
  for (var i = 0; i < rows.length; i++) {
    if (Model.rowProject(rows[i])) out.push(Model.rowProject(rows[i]))
  }
  return out
}

// hereSummary resolves the `here` block to the summary the Continue action
// works on: the where-you-left-off row for that folder, else a bare summary
// built from `here` itself (no handoff, no window) — the empty state for
// THAT folder. A repo key names one project, so its row is the folder's
// whatever the subfolder; a workspace key is shared by sibling folders, so
// only the row whose cwd is the folder is (as `this-week --here auto` maps a
// window to a row, PANEL-CONTRACT.md): a sibling's row is never borrowed.
// null when there is no `here`.
function hereSummary(data) {
  var h = Model.topHere(data)
  if (!h) return null
  var key = Model.hereProjectKey(h)
  var all = allSummaries(data)
  var byKey = null
  for (var i = 0; i < all.length; i++) {
    if (Model.summaryProjectKey(all[i]) !== key) continue
    if (Model.summaryCwd(all[i]) === Model.hereCwd(h)) return all[i]
    if (byKey === null && !Format.isWorkspaceKey(key)) byKey = all[i]
  }
  if (byKey !== null) return byKey
  if (!key && !Model.hereCwd(h)) return null
  return Model.bareSummary(key, Model.hereDisplayName(h), Model.hereCwd(h))
}

// sessionsOnDay: the sessions the week array records for one project on one
// UTC day (0 when it has no entry — there is no zero-filled row).
function sessionsOnDay(week, projectKey, day) {
  var n = 0
  for (var i = 0; i < week.length; i++) {
    if (Model.dayProjectKey(week[i]) === projectKey && Model.dayDay(week[i]) === day) n += Model.daySessions(week[i])
  }
  return n
}

function sessionsInWeek(week, projectKey) {
  var n = 0
  for (var i = 0; i < week.length; i++) {
    if (Model.dayProjectKey(week[i]) === projectKey) n += Model.daySessions(week[i])
  }
  return n
}

// weekTotals sums the whole week array for the one-line summary.
function weekTotals(week) {
  var keys = {}
  var t = { projects: 0, sessions: 0, files: 0, notes: 0 }
  for (var i = 0; i < week.length; i++) {
    keys[Model.dayProjectKey(week[i])] = true
    t.sessions += Model.daySessions(week[i])
    t.files += Model.dayFilesTouched(week[i])
    t.notes += Model.dayRecordsWritten(week[i])
  }
  t.projects = Object.keys(keys).length
  return t
}
