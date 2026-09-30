.pragma library

// memory.js: pure presentation helpers for MemoryView.qml. Reads no JSON
// fields (js/records.js does) and names no binary (js/launchers.js does).

var HOUR_MS = 60 * 60 * 1000
var MINUTE_MS = 60 * 1000
var DAY_MS = 24 * HOUR_MS

// tierLabel maps a record's tier to the human-facing word.
function tierLabel(tier) {
  if (tier === "human-declared") return "You"
  if (tier === "agent-declared") return "Agent"
  if (tier === "inferred") return "Inferred"
  return tier || ""
}

// kindLabel maps a record's kind to the human-facing word. A kind the store
// does not define shows as itself, capitalised ("" for no kind).
var KIND_LABELS = {
  decision: "Decision",
  outcome: "Outcome",
  handoff: "Handoff",
  note: "Note",
  claim: "Claim",
  punch: "Punch",
  stage: "Stage",
  confirm: "Confirm"
}

function kindLabel(kind) {
  if (Object.prototype.hasOwnProperty.call(KIND_LABELS, kind)) return KIND_LABELS[kind]
  if (!kind) return ""
  return kind.charAt(0).toUpperCase() + kind.substring(1)
}

// statusMark is "" for a current record (no mark at all), else the word.
function statusMark(status) {
  if (!status || status === "current") return ""
  if (status === "possibly-stale") return "stale"
  return status
}

function firstLine(text) {
  var i = text.indexOf("\n")
  return i < 0 ? text : text.substring(0, i)
}

// relativeAge renders ts (RFC3339) as "just now" / "5m" / "3h" / "12d".
function relativeAge(ts, nowMs) {
  var t = Date.parse(ts)
  if (isNaN(t)) return ""
  var d = nowMs - t
  if (d < MINUTE_MS) return "just now"
  if (d < HOUR_MS) return Math.floor(d / MINUTE_MS) + "m"
  if (d < DAY_MS) return Math.floor(d / HOUR_MS) + "h"
  return Math.floor(d / DAY_MS) + "d"
}

function rfc3339(date) {
  return date.toISOString().replace(/\.\d+Z$/, "Z")
}

// The three "Forget activity" scopes. since() returns the RFC3339 bound
// (computed here, in JS, from the local clock) or "" for the whole project.
var FORGET_SCOPES = [
  { id: "hour", label: "Last hour", since: function (nowMs) { return rfc3339(new Date(nowMs - HOUR_MS)) } },
  { id: "today", label: "Today", since: function (nowMs) {
      var n = new Date(nowMs)
      return rfc3339(new Date(n.getFullYear(), n.getMonth(), n.getDate()))
    } },
  { id: "all", label: "Everything in this project", since: function (nowMs) { return "" } }
]

// parseDryRun reads `purge --dry-run`'s "would purge N sessions, M events"
// line; null when stdout is not that shape.
function parseDryRun(stdout) {
  var m = /would purge (\d+) sessions?, (\d+) events?/.exec(stdout || "")
  return m ? { sessions: parseInt(m[1], 10), events: parseInt(m[2], 10) } : null
}
