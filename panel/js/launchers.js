.pragma library

// launchers.js is the ONLY place in this plugin that names an external
// binary to invoke. Every process this plugin ever spawns is one of the
// three calls below — nothing else — so `git diff panel/` is enough to
// audit that claim by eye (the task's own proof for it) without having to
// read every .qml file.
//
//   - `backstory this-week --json` / `backstory group ...`: the plugin's
//     one data source and its one write path, never the store directly
//     (PLAN.md Phase 4 "Human look": "it never reads the store").
//   - `xdg-terminal-exec --dir=<cwd>`: the freedesktop terminal-launch
//     primitive Omarchy's own `omarchy-launch-terminal` wraps, called here
//     with an explicit `--dir` (a row's `cwd`, not the caller's own) since
//     the click target is a specific project, not "whatever terminal has
//     focus".
//   - `omarchy agent prompt <text>`: the documented cross-agent handoff
//     launcher (AGENT-CONTRACT.md "Resume in <agent>").

function thisWeekCommand() {
  return ["backstory", "this-week", "--json"]
}

function groupListCommand() {
  return ["backstory", "group", "list", "--json"]
}

function groupSetCommand(groupName, projectKey) {
  return ["backstory", "group", "set", groupName, projectKey]
}

function groupClearCommand(projectKey) {
  return ["backstory", "group", "clear", projectKey]
}

// terminalLaunchCommand opens a terminal at an arbitrary project cwd — the
// row's own cwd, not necessarily the shell's. cwd is passed with
// workingDirectory too (see Panel.qml) so a terminal that ignores --dir
// still lands in the right place.
function terminalLaunchCommand(cwd) {
  return ["xdg-terminal-exec", "--dir=" + cwd]
}

// agentPromptCommand hands the handoff's own first line to the agent as
// its resume prompt; the caller sets workingDirectory to the project's cwd
// so the agent starts there rather than wherever the shell happened to be
// running.
function agentPromptCommand(handoffText) {
  return ["omarchy", "agent", "prompt", handoffText]
}
