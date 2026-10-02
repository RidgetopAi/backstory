.pragma library
.import "model.js" as Model

// derive.js: values computed from the this-week payload. Field reads stay
// in model.js (the only file allowed to name a JSON field); this file only
// calls its accessors.

// allSummaries flattens Where-you-left-off into its project summaries
// (standalone rows and group children), in row order.
function allSummaries(data) {
  var out = []
  var rows = Model.topWhereLeftOff(data)
  for (var i = 0; i < rows.length; i++) {
    if (Model.rowProject(rows[i])) out.push(Model.rowProject(rows[i]))
    var kids = Model.rowChildren(rows[i])
    for (var j = 0; j < kids.length; j++) out.push(kids[j])
  }
  return out
}

// hereSummary resolves the `here` block to the summary the Continue action
// works on: the where-you-left-off project with the same key (and cwd — a
// workspace key is shared by sibling folders), else a bare summary built
// from `here` itself (no handoff, no window). null when there is no `here`.
function hereSummary(data) {
  var h = Model.topHere(data)
  if (!h) return null
  var all = allSummaries(data)
  var byKey = null
  for (var i = 0; i < all.length; i++) {
    if (Model.summaryProjectKey(all[i]) !== Model.hereProjectKey(h)) continue
    if (Model.summaryCwd(all[i]) === Model.hereCwd(h)) return all[i]
    if (byKey === null) byKey = all[i]
  }
  if (byKey !== null) return byKey
  if (!Model.hereProjectKey(h) && !Model.hereCwd(h)) return null
  return Model.bareSummary(Model.hereProjectKey(h), Model.hereDisplayName(h), Model.hereCwd(h))
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
