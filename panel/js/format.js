.pragma library
.import "model.js" as Model

// format.js: pure display helpers (names, relative time, the 7-day strip,
// agent chips). It reads no JSON field and names no command — those
// stay in model.js and launchers.js.

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

// nextPrefix: the handoff line's label, naming the agent that wrote it.
function nextPrefix(handoffAgent) {
  return handoffAgent ? "Next (" + agentName(handoffAgent) + "): " : "Next: "
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

function summaryLine(totals) {
  return "This week: " + totals.projects + " projects \u00b7 " + totals.sessions + " sessions \u00b7 "
    + totals.files + " files \u00b7 " + totals.notes + " notes"
}
