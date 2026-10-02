.pragma library

// format.js: pure display helpers (names, relative time, the 7-day strip,
// the agent notice). It reads no JSON field and names no command — those
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
var WORKSPACE_DIR_PREFIX = "projects/"
var STRIP_DAYS = 7
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

// agentNotice: the one line telling the user Continue goes somewhere other
// than where they last worked. "" when the agents match, the last agent is
// unknown/empty, or there is no default.
function agentNotice(lastAgent, defaultAgent) {
  if (!lastAgent || !defaultAgent || !isKnownAgent(lastAgent) || lastAgent === defaultAgent) return ""
  return "Last worked in " + agentName(lastAgent) + " · Continue opens " + agentName(defaultAgent) + " (your Omarchy default)"
}

// shortName drops the leading "projects/" of a workspace-relative name.
function shortName(displayName) {
  var n = displayName || ""
  return n.indexOf(WORKSPACE_DIR_PREFIX) === 0 ? n.slice(WORKSPACE_DIR_PREFIX.length) : n
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
  var text = "Continue work on " + shortName(displayName) + "."
  if (handoffId) text += " Read backstory handoff " + handoffId + "."
  if (next) text += " Next: " + next
  return text
}

function summaryLine(totals) {
  return "This week: " + totals.projects + " projects \u00b7 " + totals.sessions + " sessions \u00b7 "
    + totals.files + " files \u00b7 " + totals.notes + " notes"
}
