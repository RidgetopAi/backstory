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

// branchLabel is "on <branch>", or "" for a record with no known branch.
function branchLabel(branch) {
  return branch ? "on " + branch : ""
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

// purgeUntil is the --until bound a Forget carries: the moment its preview
// was asked for, to the millisecond (the CLI reads RFC3339 with a fraction).
function purgeUntil(nowMs) {
  return new Date(nowMs).toISOString()
}

// The three "Forget activity" scopes. since() returns the RFC3339 bound
// (computed here, in JS, from the local clock) or "" for the whole project.
var FORGET_SCOPES = [
  { id: "hour", label: "Last hour", since: function (nowMs) { return rfc3339(new Date(nowMs - HOUR_MS)) } },
  { id: "today", label: "Today", since: function (nowMs) {
      var n = new Date(nowMs)
      return rfc3339(new Date(n.getFullYear(), n.getMonth(), n.getDate()))
    } },
  { id: "all", label: "Everything here", since: function (nowMs) { return "" } }
]

// parseDryRun reads `purge --dry-run`'s stdout: the "would purge N
// sessions, M events, R records" line, then (when there is anything to
// purge) how many backups Backstory made the purge also deletes — they hold
// the whole store, every project's text — and the paired devices the forget
// reaches at the next sync (cmd/backstory purge.go; the lines are pinned by
// cmd/backstory/testdata/purge/dry-run.golden). null when stdout is not that
// shape: a confirm that cannot say what Forget deletes is not offered.
function parseDryRun(stdout) {
  var out = stdout || ""
  var m = /would purge (\d+) sessions?, (\d+) events?, (\d+) records?/.exec(out)
  if (!m) return null
  var counts = { sessions: parseInt(m[1], 10), events: parseInt(m[2], 10), records: parseInt(m[3], 10), backups: 0, syncPeers: "" }
  if (nothingToForget(counts)) return counts
  var b = /^would also delete (\d+) backups? Backstory made/m.exec(out)
  if (b) counts.backups = parseInt(b[1], 10)
  else if (!/^no backups made by Backstory hold this text$/m.test(out)) return null
  var p = /^will also be forgotten on (.+) at next sync$/m.exec(out)
  if (p) counts.syncPeers = p[1]
  return counts
}

// nothingToForget is true only when the dry run found no sessions, no events
// and no saved records: a window with 0 sessions but saved records still has
// something to forget.
function nothingToForget(counts) {
  return counts.sessions === 0 && counts.events === 0 && counts.records === 0
}

// forgetConfirmText is the Forget confirm's sentence: everything the --yes
// run does, as the dry run reported it — the counts, the store-wide backups
// it deletes, and the paired devices it reaches. The name is the one the
// dry run was for (pending.displayName), never the view's current one.
function forgetConfirmText(pending) {
  var text = "Forget " + pending.sessions + " sessions (" + pending.events + " events) and " +
    pending.records + " saved records from " + pending.displayName + "?"
  if (pending.backups > 0) {
    text += " It also deletes all " + pending.backups + " Backstory backups (each holds every project's memory;" +
      " a fresh backup is taken after). Backups you saved elsewhere keep this text."
  }
  if (pending.syncPeers) text += " It is also forgotten on " + pending.syncPeers + " at the next sync."
  return text + " This cannot be undone."
}

// purgeScopeKey names the folder a purge acts on: its project key and its
// --location. A dry run, its confirm and the --yes run carry the one they
// started with, and it must still be the view's own.
function purgeScopeKey(projectKey, location) {
  return projectKey + "\n" + location
}

// dryRunOutcome is what a finished dry run (run: { scopeKey, label, since,
// until, projectKey, location, displayName }, its stdout) shows while the
// view shows viewScopeKey: { kind: "discard" } when the view now shows
// another folder (nothing of it is shown), "unexpected" for stdout
// parseDryRun cannot read, "nothing", or "confirm" with the pending purge.
function dryRunOutcome(run, stdout, viewScopeKey) {
  if (!run || run.scopeKey !== viewScopeKey) return { kind: "discard" }
  var counts = parseDryRun(stdout)
  if (counts === null) return { kind: "unexpected" }
  if (nothingToForget(counts)) return { kind: "nothing" }
  return { kind: "confirm", pending: {
    scopeKey: run.scopeKey, projectKey: run.projectKey, location: run.location, displayName: run.displayName,
    label: run.label, since: run.since, until: run.until,
    sessions: counts.sessions, events: counts.events, records: counts.records,
    backups: counts.backups, syncPeers: counts.syncPeers
  } }
}

// canConfirmPurge: the confirm's Forget runs only for a pending purge of the
// folder on show, and never while a purge (or a delete) is still running.
function canConfirmPurge(pending, viewScopeKey, busy) {
  return !!pending && pending.scopeKey === viewScopeKey && !busy
}
