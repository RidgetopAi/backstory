.pragma library

// groups.js is the pure-JS half of GroupEditor.qml's project/group
// selection: with no free-text project field (round 2 defect C), "which
// project is selected" and "can the add control submit" are plain data
// transforms over `backstory group list --json` rows, testable without a
// QML engine (task 4fe02e30 DONE WHEN clause 5's "any pure JS the editor
// uses for selection/enablement is covered by a test").

// displayNameForKey looks up projectKey's display name in projects — the
// `backstory group list --json` `projects` array (task 4fe02e30 round 5
// fix: {key, display_name, group} entries, one per project, display_name
// computed store-side by the SAME function this-week's own rows use). This
// is the ONE place GroupEditor.qml and GroupProjectRow.qml get a project's
// display name from; neither derives one from the key's own shape — round
// 5's own desk defect was exactly that: a git project_key's text after the
// last "/" is the remote URL's basename ("omarcade.git"), never the
// project's real name. Falls back to projectKey itself only when projects
// has no matching entry at all — should not happen for any key `group
// list --json` itself surfaced (every groups[].projects/ungrouped key also
// appears in projects), but the editor must never crash over a stale or
// malformed payload.
function displayNameForKey(projects, projectKey) {
  for (var i = 0; i < projects.length; i++) {
    if (projects[i].key === projectKey) return projects[i].display_name
  }
  return projectKey
}

// canSubmitGroup gates the "+" button: a project must be picked from a
// group-list row (never typed) and a group name must be non-blank once
// whitespace is trimmed.
function canSubmitGroup(projectKey, groupName) {
  return projectKey.length > 0 && groupName.trim().length > 0
}
