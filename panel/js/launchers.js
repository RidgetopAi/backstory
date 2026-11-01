.pragma library

// launchers.js is the ONLY place in this plugin that names an external
// binary to invoke. Every process this plugin ever spawns is one of the
// calls below — nothing else — so `git diff panel/` is enough to
// audit that claim by eye (the task's own proof for it) without having to
// read every .qml file.
//
//   - `backstory this-week --json` / `backstory group ...`: the plugin's
//     one data source and its one write path, never the store directly
//     (PLAN.md Phase 4 "Human look": "it never reads the store").
//   - `backstory affirm <handoff-id>`: the Needs You Dismiss action.
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
//   - `omarchy-shell shell summon backstory.this-week <snapshot>`: the bar
//     popup's pop-out opens the window where the popup was, through the
//     host's own summon (the host shows a plugin panel only through its own
//     summon/toggle), and `omarchy-shell backstory.this-week.popup close`:
//     the window, once in the popup's place, closes the popup.
//   - `omarchy-shell backstory.this-week focus`: the bar icon, while the
//     window is open, focuses it instead of opening the popup.
//   - Hyprland dispatch requests (Hyprland.dispatch, not a process): pin,
//     move, resize and focus the window itself, its address checked by
//     isWindowAddress first.

// windowSummonCommand opens the plugin's window (its "panel" kind): the
// shell routes summon to the panel loader for a plugin that is also a bar
// widget, so this never reopens the bar popup. payloadJson, when given, is
// the view snapshot the window opens at (Panel.qml open()).
function windowSummonCommand(payloadJson) {
  return ["omarchy-shell", "shell", "summon", "backstory.this-week", payloadJson || "{}"]
}

function thisWeekCommand() {
  return ["backstory", "this-week", "--json", "--here", "auto"]
}

// affirmCommand dismisses a stale handoff's Needs You row: the CLI writes a
// human-declared affirm the freshness boundary honours (decision 6caaac1d).
function affirmCommand(handoffId) {
  return ["backstory", "affirm", handoffId]
}

function groupListCommand() {
  return ["backstory", "group", "list", "--json"]
}

// toolsCommand lists the agent tools (name + description) straight from the
// Go ToolsV0() the MCP server serves, so the help pane carries no copy.
function toolsCommand() {
  return ["backstory", "tools", "--json"]
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

// LAUNCH_TABLE: per-harness argv for launching THAT agent (a row's agent
// chip / sub-row), keyed by agents[].agent. Each entry mirrors the launch
// line omarchy-agent uses for the same harness; `prompt` says how the
// opening prompt joins it: "" = a trailing positional argument, otherwise
// the flag prefix glued to the prompt (hermes: `--query=<prompt>`).
//
// TEMPORARY: this table exists only until omarchy-agent gains an --agent
// flag, at which point every launch goes back through it (decision
// 7026e48e). An agent absent from the table falls back to
// agentPromptCommand (Omarchy's default agent).
var LAUNCH_TABLE = {
  claude: { argv: ["claude", "--permission-mode", "auto"], prompt: "" },
  codex: { argv: ["codex", "--approve-for-me"], prompt: "" },
  hermes: { argv: ["hermes", "chat", "--yolo", "--tui"], prompt: "--query=" },
  pi: { argv: ["pi"], prompt: "" }
}

function hasLauncher(agent) {
  return Object.prototype.hasOwnProperty.call(LAUNCH_TABLE, agent)
}

// agentLaunchCommand opens a terminal at cwd running the table's argv for
// `agent` with the handoff prompt; null when the agent is not in the table.
// Like terminalLaunchCommand, cwd and the argv are passed as sh's own
// positional arguments (`cd "$1" && shift && exec "$@"`), never interpolated
// into the script string.
function agentLaunchCommand(agent, cwd, prompt) {
  if (!hasLauncher(agent)) return null
  var entry = LAUNCH_TABLE[agent]
  var argv = entry.argv.slice()
  argv.push(entry.prompt === "" ? prompt : entry.prompt + prompt)
  var script = 'cd "$1" && shift && exec "$@"'
  return ["xdg-terminal-exec", "--", "sh", "-c", script, "sh", cwd].concat(argv)
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
  return ["hyprctl", "dispatch", focusWindowDispatch(addr)]
}

// pinWindowDispatch / moveWindowDispatch / focusWindowDispatch are Hyprland
// dispatch requests (Hyprland.dispatch) for the window's own address,
// checked by isWindowAddress first: pin or unpin it (pinned, it shows on
// every workspace), move it to a workspace id, and focus it.
function pinWindowDispatch(addr, on) {
  return 'hl.dsp.window.pin({ action = "' + (on ? "on" : "off") + '", window = "address:' + addr + '" })'
}

function moveWindowDispatch(addr, workspaceId) {
  return 'hl.dsp.window.move({ workspace = "' + Math.floor(workspaceId) + '", window = "address:' + addr + '" })'
}

function focusWindowDispatch(addr) {
  return 'hl.dsp.focus({ window = "address:' + addr + '" })'
}

// resizeWindowDispatch / moveWindowToDispatch size and place the window (the
// pop-out); windowAnimDispatch lets Hyprland animate it ("0") or hands back
// to its rule ("unset"). Sizes and positions are rounded to whole pixels.
function resizeWindowDispatch(addr, w, h) {
  return 'hl.dsp.window.resize({ x = ' + Math.round(w) + ', y = ' + Math.round(h) + ', window = "address:' + addr + '" })'
}

function moveWindowToDispatch(addr, x, y) {
  return 'hl.dsp.window.move({ x = ' + Math.round(x) + ', y = ' + Math.round(y) + ', window = "address:' + addr + '" })'
}

function windowAnimDispatch(addr, animate) {
  return 'hl.dsp.window.set_prop({ prop = "no_anim", value = "' + (animate ? "0" : "unset") + '", window = "address:' + addr + '" })'
}

// popupCloseCommand: the window, once it sits where the bar popup's card is,
// closes the popup, which fades out over it (BarWidget.qml's IPC target).
function popupCloseCommand() {
  return ["omarchy-shell", "backstory.this-week.popup", "close"]
}

// The window (Panel.qml): its title, the title it maps under during a
// pop-out, and Quickshell's app id, as ops/hyprland/backstory.lua matches it.
// Panel.qml's windowTitle is the source (the ops tests read it there);
// tst_panel.qml checks these match it.
var WINDOW_TITLE = "backstory"
var WINDOW_POPOUT_TITLE = WINDOW_TITLE + "-popout"

// isPanelWindow: whether a Hyprland toplevel is the window.
function isPanelWindow(t) {
  return !!t && (t.title === WINDOW_TITLE || t.title === WINDOW_POPOUT_TITLE)
    && !!t.wayland && t.wayland.appId === "org.quickshell"
}

// windowFocusCommand: the bar icon, while the window is open, focuses it
// (Panel.qml's IPC target) instead of opening the popup beside it.
function windowFocusCommand() {
  return ["omarchy-shell", "backstory.this-week", "focus"]
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
