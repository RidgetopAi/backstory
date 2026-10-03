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
//   - `omarchy-default-agent` / `omarchy agent --pick`: read the user's
//     Omarchy default agent, and let them choose one when it is unset.
//   - `hyprctl dispatch ...`: focus an already-open window the way
//     omarchy-launch-or-focus does (Continue when `window` is set).
//   - `tmux select-window` / `select-pane`: after that focus, jump to the
//     tmux window holding the project (Continue when `tmux` is set).
//   - `omarchy-shell shell toggle backstory.this-week {}`: the bar widget's
//     click (round 4 desk defect A). MEASURED ON BRIAN'S DESK: BarWidget
//     used to flip a shared PanelState singleton directly, but the host
//     only ever shows a plugin panel through its OWN toggle — the host's
//     own plugin, ridgetopai.omarcade's Marquee.qml:253, does exactly this
//     `Quickshell.execDetached(["omarchy-shell", "shell", "toggle", ...])`
//     to open its own panel, confirmed working. this-week's own IpcHandler
//     (Panel.qml) still answers the "panel" kind contract's open()/close()/
//     toggle() the host may also call directly.

function thisWeekCommand() {
  return ["backstory", "this-week", "--json", "--here", "auto"]
}

function barToggleCommand() {
  return ["omarchy-shell", "shell", "toggle", "backstory.this-week", "{}"]
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

// Memory view (decision 02c511b3 D5). Every id / project key / RFC3339
// bound below is its own argv element — never interpolated into a string —
// the same rule terminalLaunchCommand holds for cwd.
//
// The Memory view and Forget act on exactly the ROW the human opened (task
// a9a784ec): every records / purge call carries the row's own cwd as
// `--location`, because a non-git folder under a workspace shares the
// workspace's project key with every sibling — the key alone would list (or
// purge) the whole workspace.
function recordsCommand(projectKey, location, history) {
  var argv = ["backstory", "records", "--project", projectKey, "--location", location, "--json"]
  if (history) argv.push("--history")
  return argv
}

// editCommand runs `backstory edit <id>` in a terminal ($EDITOR needs a
// TTY), started detached; the id is the LAST argv element on its own.
function editCommand(recordId) {
  return ["xdg-terminal-exec", "--", "backstory", "edit", recordId]
}

// deleteCommand always carries --yes: the panel's own inline "Forget this
// record?" confirm is the human's confirmation, and there is no TTY here.
function deleteCommand(recordId) {
  return ["backstory", "delete", recordId, "--yes"]
}

// purgeCommand builds `backstory purge --project K --location L [--since T]
// <mode>`; dryRun true previews (--dry-run), false performs it (--yes).
// since is an RFC3339 string, or "" for everything in the row.
function purgeCommand(projectKey, location, since, dryRun) {
  var argv = ["backstory", "purge", "--project", projectKey, "--location", location]
  if (since) argv.push("--since", since)
  argv.push(dryRun ? "--dry-run" : "--yes")
  return argv
}

// terminalLaunchCommand opens a terminal at an arbitrary project cwd — the
// row's own cwd, not necessarily the shell's.
//
// MEASURED ON BRIAN'S DESK (round 2 defect A): Omarchy's default terminal
// is Ghostty launched `--gtk-single-instance=true`; the already-running
// instance ignores `--dir` / `--working-directory` entirely, so plain
// `xdg-terminal-exec --dir=<cwd>` (and the uwsm-app-wrapped form) both land
// in `~`. The fix Brian confirmed working: run a shell that `cd`s to the
// target itself, `exec`ing the user's shell there once it has. cwd is
// passed as sh's own positional argument ($1 in the script), NEVER
// interpolated into the script string — a project folder containing a
// single quote must not be able to inject shell code into `-c`'s argument.
// workingDirectory is still set too (see Panel.qml) as a second line of
// defense for a terminal that does honor it.
function terminalLaunchCommand(cwd) {
  var script = 'cd "$1" && exec "${SHELL:-sh}"'
  return ["xdg-terminal-exec", "--", "sh", "-c", script, "sh", cwd]
}

// agentPromptCommand hands the handoff's own first line to the agent as
// its resume prompt; the caller sets workingDirectory to the project's cwd
// so the agent starts there rather than wherever the shell happened to be
// running.
function agentPromptCommand(handoffText) {
  return ["omarchy", "agent", "prompt", handoffText]
}

// defaultAgentCommand prints the user's Omarchy default agent id
// (claude/codex/pi/...), or nothing when none is set.
function defaultAgentCommand() {
  return ["omarchy-default-agent"]
}

// agentPickCommand lets the user choose their default agent.
function agentPickCommand() {
  return ["omarchy", "agent", "--pick"]
}

// isWindowAddress: a Hyprland window address, and nothing else — the value
// reaches a hyprctl dispatch string, so anything but 0x<hex> is refused.
function isWindowAddress(addr) {
  return typeof addr === "string" && /^0x[0-9a-fA-F]+$/.test(addr)
}

// focusWindowCommand is what omarchy-launch-or-focus runs; the fallback is
// for a Hyprland without the hl.dsp Lua dispatcher. The address is checked
// by isWindowAddress before either is built.
function focusWindowCommand(addr) {
  return ["hyprctl", "dispatch", 'hl.dsp.focus({ window = "address:' + addr + '" })']
}

function focusWindowFallbackCommand(addr) {
  return ["hyprctl", "dispatch", "focuswindow", "address:" + addr]
}

// isTmuxTarget: a tmux target as PANEL-CONTRACT.md §here defines it —
// session:window.pane with numeric indexes — and nothing else; it reaches a
// tmux argv, so a ';' (tmux's command separator), a space or a leading '-'
// is refused.
function isTmuxTarget(target) {
  return typeof target === "string" && /^[A-Za-z0-9_][A-Za-z0-9_-]*:[0-9]+\.[0-9]+$/.test(target)
}

// tmuxSelectWindowCommand / tmuxSelectPaneCommand jump a tmux client to the
// window / pane of a validated target. select-window takes session:window,
// so the pane suffix is dropped for it.
function tmuxSelectWindowCommand(target) {
  return ["tmux", "select-window", "-t", target.slice(0, target.lastIndexOf("."))]
}

function tmuxSelectPaneCommand(target) {
  return ["tmux", "select-pane", "-t", target]
}

// continueFocusPlan is what Continue runs for a project already open: the
// hyprctl focus argv for `window` plus, when `tmux` is a valid target, the
// tmux select argvs (in order). null when `window` is not an address — the
// caller then launches the agent instead. It never builds a terminal launch.
function continueFocusPlan(window, tmux) {
  if (!isWindowAddress(window)) return null
  return {
    focus: focusWindowCommand(window),
    focusFallback: focusWindowFallbackCommand(window),
    tmuxCommands: isTmuxTarget(tmux) ? [tmuxSelectWindowCommand(tmux), tmuxSelectPaneCommand(tmux)] : []
  }
}
