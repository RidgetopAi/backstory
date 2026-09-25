.pragma library

// groups.js is the pure-JS half of GroupEditor.qml's project/group
// selection: with no free-text project field (round 2 defect C), "which
// project is selected" and "can the add control submit" are plain data
// transforms over `backstory group list --json` rows, testable without a
// QML engine (task 4fe02e30 DONE WHEN clause 5's "any pure JS the editor
// uses for selection/enablement is covered by a test").

// projectDisplayName turns a raw project key (a filesystem path, or a
// "workspace:<path>" key — the same project_key shape PANEL-CONTRACT.md
// documents for this-week) into the basename a human recognizes, the same
// "last path segment" rule internal/week's own displayName (week.go) uses.
// `backstory group list --json` has no display_name field of its own (a
// different, older CLI surface than this-week — GroupEditor.qml's own doc
// comment), so this is computed client-side instead of read off JSON.
function projectDisplayName(projectKey) {
  var key = projectKey.indexOf("workspace:") === 0 ? projectKey.slice("workspace:".length) : projectKey
  key = key.replace(/\/+$/, "")
  var slash = key.lastIndexOf("/")
  var base = slash >= 0 ? key.slice(slash + 1) : key
  return base.length > 0 ? base : projectKey
}

// canSubmitGroup gates the "+" button: a project must be picked from a
// group-list row (never typed) and a group name must be non-blank once
// whitespace is trimmed.
function canSubmitGroup(projectKey, groupName) {
  return projectKey.length > 0 && groupName.trim().length > 0
}
