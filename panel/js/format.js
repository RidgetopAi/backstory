.pragma library
.import "model.js" as Model

// format.js: pure display helpers (names, relative time, the 7-day strip,
// agent chips). It reads no JSON field and names no command — those
// stay in model.js and launchers.js.

// A footer that wraps only between hints: "p pin" never splits, and a new
// line never starts with the separator dot.
function footerWrappingBetweenHints(footerHint) {
  return footerHint.split(" · ")
    .map(function (hint) { return hint.replace(/ /g, " ") })
    .join(" · ")
}

var AGENT_NAMES = {
  claude: "Claude Code",
  codex: "Codex",
  pi: "Pi",
  omp: "Oh My Pi",
  hermes: "Hermes",
  opencode: "OpenCode",
  gemini: "Gemini"
}

var WORKSPACE_KEY_PREFIX = "workspace:"
var STRIP_DAYS = 7

// RECENT_ACTIVE_MINUTES: a Recent row whose last activity is at most this
// old shows a labelled Continue/Switch button instead of the bare glyph.
var RECENT_ACTIVE_MINUTES = 15
var DAY_MS = 24 * 60 * 60 * 1000

function isKnownAgent(id) {
  return Object.prototype.hasOwnProperty.call(AGENT_NAMES, id)
}

// agentName: the display name of a known agent, else the id itself.
function agentName(id) {
  return isKnownAgent(id) ? AGENT_NAMES[id] : id
}

function continueLabel(defaultAgent) {
  return defaultAgent ? "Continue in " + agentName(defaultAgent) : "Choose default agent"
}

// MAX_AGENT_CHIPS: agent chips shown on one row before the rest collapse
// into a "+N" chip.
var MAX_AGENT_CHIPS = 3

// chipAgents: the agents[] entries drawn as chips (the first
// MAX_AGENT_CHIPS, newest first) and how many are folded into "+N".
function chipAgents(agents) {
  var all = agents || []
  return { shown: all.slice(0, MAX_AGENT_CHIPS), extra: Math.max(0, all.length - MAX_AGENT_CHIPS) }
}

// fitChipCount: how many of the candidate chips (widths, in order) fit in
// `avail` after `lead` (the chevron) while leaving room for the "+N" chip
// (`moreWidth`) whenever any of the `total` agents stay folded. The first chip
// always counts, even if it alone overflows.
function fitChipCount(widths, total, avail, lead, moreWidth, spacing) {
  var used = lead
  var n = 0
  for (var i = 0; i < widths.length; i++) {
    var need = used + spacing + widths[i]
    var reserve = (i + 1 < total) ? spacing + moreWidth : 0
    if (n >= 1 && need + reserve > avail) break
    used = need
    n++
  }
  return n
}

// nextPrefix: the handoff line's label, naming the agent that wrote it.
// doneLine: a done handoff's muted line in place of its Next line.
function doneLine(doneAt) {
  var date = shortDate(doneAt)
  return date === "" ? "Done" : "Done " + date
}

// A handoff written on a known git branch adds "on <branch>" to the label.
function nextPrefix(handoffAgent, handoffBranch) {
  var parts = []
  if (handoffAgent) parts.push(agentName(handoffAgent))
  if (handoffBranch) parts.push("on " + handoffBranch)
  return parts.length ? "Next (" + parts.join(", ") + "): " : "Next: "
}

function isWorkspaceKey(projectKey) {
  return !!projectKey && projectKey.indexOf(WORKSPACE_KEY_PREFIX) === 0
}

function relativeTime(iso, nowMs) {
  var t = Date.parse(iso)
  if (isNaN(t)) return ""
  var s = Math.max(0, Math.round((nowMs - t) / 1000))
  if (s < 60) return "just now"
  var m = Math.floor(s / 60)
  if (m < 60) return m + "m ago"
  var h = Math.floor(m / 60)
  if (h < 24) return h + "h ago"
  return Math.floor(h / 24) + "d ago"
}

// stripDays: the last STRIP_DAYS UTC calendar days, oldest first, ending
// with the day of nowMs (the this-week window's own day rule).
// recentActionLabel: the text of a Recent row's labelled action button —
// "Switch" when the project has an open window, else "Continue" — or "" when
// the row's last activity is older than RECENT_ACTIVE_MINUTES (or unparseable),
// meaning the row keeps only the glyph button.
function recentActionLabel(iso, nowMs, hasWindow) {
  var t = Date.parse(iso)
  if (isNaN(t)) return ""
  if (nowMs - t > RECENT_ACTIVE_MINUTES * 60 * 1000) return ""
  return hasWindow ? "Switch" : "Continue"
}

function stripDays(nowMs) {
  var out = []
  for (var i = STRIP_DAYS - 1; i >= 0; i--) {
    out.push(new Date(nowMs - i * DAY_MS).toISOString().slice(0, 10))
  }
  return out
}

// stripAlpha: shade of a strip cell, 0 for an idle day, scaled by sessions
// relative to the busiest day shown.
function stripAlpha(sessions, maxSessions) {
  if (sessions <= 0 || maxSessions <= 0) return 0
  return 0.3 + 0.7 * sessions / maxSessions
}

// handoffPrompt: the text `omarchy agent prompt` gets — names the handoff
// id and its Next line so the agent can pick the work up.
function handoffPrompt(displayName, handoffId, next) {
  var text = "Continue work on " + Model.shortName(displayName) + "."
  if (handoffId) text += " Read backstory handoff " + handoffId + "."
  if (next) text += " Next: " + next
  return text
}

// A possibly-stale handoff's reason is "<label>: handoff possibly stale
// \u2014 <detail>"; the label is all the row takes from it. Everything else
// on the row comes from the structured fields (handoff_time,
// handoff_summary, reasons).
function attentionMain(reason) {
  var i = reason.indexOf(" \u2014 ")
  return i < 0 ? reason : reason.slice(0, i)
}
function attentionLabel(reason) {
  var main = attentionMain(reason)
  var i = main.indexOf(": handoff possibly stale")
  return i < 0 ? main : main.slice(0, i)
}

var MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]
// "Nov 3" for an ISO time, "" when there is none.
function shortDate(iso) {
  var d = new Date(iso)
  if (!iso || isNaN(d.getTime())) return ""
  return MONTHS[d.getMonth()] + " " + d.getDate()
}
// "Nov 3, 14:05" for an ISO time, "" when there is none.
function shortDateTime(iso) {
  var date = shortDate(iso)
  if (date === "") return ""
  var d = new Date(iso)
  return date + ", " + (d.getHours() < 10 ? "0" : "") + d.getHours() + ":" + (d.getMinutes() < 10 ? "0" : "") + d.getMinutes()
}

// The collapsed stale row's headline.
function staleHeadline(label, handoffTime, looksFinished) {
  if (looksFinished) return label + ": this looks finished"
  var date = shortDate(handoffTime)
  return label + ": handoff from " + date + " may be out of date"
}

// Which reason leads "Since then:", strongest first.
var STALE_REASON_ORDER = ["contradicted", "later-record", "later-failure", "later-unrecorded-work", "later-activity"]
function strongestReason(reasons) {
  var best = null
  var bestRank = STALE_REASON_ORDER.length
  for (var i = 0; i < (reasons || []).length; i++) {
    var rank = STALE_REASON_ORDER.indexOf(reasons[i].kind)
    if (rank < 0) rank = STALE_REASON_ORDER.length
    if (best === null || rank < bestRank) { best = reasons[i]; bestRank = rank }
  }
  return best
}

// The later outcome record's reason (it carries outcome_id), else null.
function outcomeReason(reasons) {
  for (var i = 0; i < (reasons || []).length; i++) {
    if (reasons[i].kind === "later-record" && reasons[i].outcome_id) return reasons[i]
  }
  return null
}

// "Since then: ..." from the strongest structured reason; "" without one.
function sinceThen(reasons, looksFinished) {
  var outcome = looksFinished ? outcomeReason(reasons) : null
  var r = outcome !== null ? outcome : strongestReason(reasons)
  if (r === null) return ""
  var date = shortDate(r.when)
  var when = date === "" ? "" : " (" + date + ")"
  if (outcome !== null) return "Since then: " + r.text + when
  if (r.kind === "later-record") return "Since then: a newer note" + when + ": " + r.text
  if (r.kind === "later-failure") return "Since then: a command failed" + (r.where ? " in " + r.where : "") + when
  return "Since then: " + r.text + when
}

// The opening prompt of Update: names the handoff and, in plain words, what
// flagged it, and asks for a reconciled handoff.
function updatePrompt(handoffId, looksFinished, reasons) {
  var why = sinceThen(reasons, looksFinished)
  var text = "Backstory handoff " + handoffId + " may be out of date."
  if (why) text += " " + why + "."
  return text + " Read the handoff, reconcile it with what happened since, and write a fresh handoff."
}

// An expanded reason's small "when, where" line.
function reasonWhenWhere(reason) {
  var parts = []
  var when = shortDateTime(reason.when)
  if (when !== "") parts.push(when)
  if (reason.where) parts.push(reason.where)
  return parts.join(" \u00b7 ")
}

function summaryLine(totals) {
  return "This week: " + totals.projects + " projects \u00b7 " + totals.sessions + " sessions \u00b7 "
    + totals.files + " files \u00b7 " + totals.notes + " notes"
}

// The HERE card's hint: where `here.source` came from. Only the sources
// this-week really reports get one; arg, none and anything unknown give "".
function hereSourceHint(source) {
  if (source === "focused") return "from your focused terminal"
  if (source === "last_focused") return "from your last focused terminal"
  if (source === "recent") return "most recent project"
  return ""
}
