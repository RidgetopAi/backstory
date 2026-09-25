# Backstory This Week — Quickshell plugin

A first-party-style Omarchy shell plugin (`kinds: ["panel"]`,
`docs/omarchy-shell.md` in the Omarchy repo) that shows Attention, Where
you left off, and The week from `backstory this-week --json`. It is a
CLIENT only: it runs the `backstory` CLI and reads its stdout; it never
opens the store directly (PLAN.md §Phase 4 "Human look", decision
`9be5c1d5`).

## What it does

- **Attention** — rendered only when the JSON's `attention` array is
  non-empty (an empty array is itself "nothing to flag this week", not an
  absent section).
- **Where you left off** — one row per project, most recently active
  first. A project in a group (`backstory group set`) collapses into one
  expandable row with its group-mates as children. Click a row to open a
  terminal at that project's `cwd` (`xdg-terminal-exec -- sh -c 'cd "$1"
  && exec "${SHELL:-sh}"' sh <cwd>` — cwd is passed as `sh`'s own
  positional argument, never interpolated into the script, so it survives
  Ghostty's `--gtk-single-instance=true` reusing an already-running window
  that ignores `--dir`); a project with a handoff shows a "Resume in
  agent" button that runs `omarchy agent prompt <handoff text>` in that
  same `cwd`, falling back to copying the handoff id to the clipboard if
  the agent launch itself fails to start.
- **The week** — a small per-project activity bar for each day in the
  window.
- **Gear icon** — opens a group editor over `backstory group
  set|clear|list`: every project is a clickable row (its display name,
  never a key you type) drawn from `group list --json`; pick a project,
  then pick an existing group chip or type a new group name, and the "+"
  (enabled only once both are chosen) sets it. A failed `group set|clear`
  shows its stderr in the editor instead of failing silently. The panel
  refetches `this-week` after every change so rows regroup immediately.
- **Bar widget** — a small glyph you can place in the Omarchy bar; click
  it to open/close the panel (see Install below). It shares its open/
  closed state with the panel itself, so toggling from either place keeps
  the other in sync.

A group is for two projects that are really one body of work — e.g.
`~/projects/mandrel` and `~/projects/ra-runtime`, where a session in either
one is really work on the same thing and you'd rather see one row than
two. Most projects need no group at all: This Week already gives every
project its own row, one per project folder, so grouping is for the
exception, not the default.

Every field this plugin reads out of the JSON goes through
[`js/model.js`](js/model.js); every external command it can run is named in
[`js/launchers.js`](js/launchers.js) (`backstory`, `xdg-terminal-exec`,
`omarchy`) — nothing else. `panel_contract_test.go` is the automated half
of that claim: it extracts every field `js/model.js` reads and asserts it
exists in the committed `backstory this-week --json` golden
(`../cmd/backstory/testdata/thisweek/all.json.golden`), so a contract
change turns that test red.

## Requirements

- [Omarchy](https://omarchy.org) with its Quickshell-based desktop shell
  (`omarchy-shell`) running — this plugin is an Omarchy shell plugin, not a
  standalone Quickshell config.
- The `backstory` binary on `PATH` (`make build` in the repo root, or the
  AUR package once published — see `RELEASING.md`).
- `xdg-terminal-exec` on `PATH` for the terminal-launch action (ships with
  Omarchy; it is the same primitive `omarchy-launch-terminal` wraps).
- `omarchy` on `PATH` for the "Resume in agent" action (`omarchy agent
  prompt`).

## Install

Plugins live under `~/.config/omarchy/plugins/<id>/` (`docs/omarchy-shell.md`
"Installing a third-party plugin"). This plugin's id is
`backstory.this-week` (`manifest.json`):

```sh
mkdir -p ~/.config/omarchy/plugins/backstory.this-week
cp -r panel/* ~/.config/omarchy/plugins/backstory.this-week/
omarchy-shell shell rescanPlugins
omarchy plugin enable backstory.this-week
```

Or, from a git checkout of this repo, symlink instead of copying so `git
pull` keeps the installed plugin current:

```sh
ln -s "$(pwd)/panel" ~/.config/omarchy/plugins/backstory.this-week
omarchy-shell shell rescanPlugins
omarchy plugin enable backstory.this-week
```

This plugin declares two kinds (`manifest.json`): `"panel"` (the This
Week window itself) and `"bar-widget"` (a small glyph that toggles it),
using the same `barWidget` shape Omarchy's own bar-widget plugins ship
(e.g. `ridgetopai.omarcade`): `{ "displayName", "description",
"category", "allowMultiple": false, "defaultSection": "right" }` — no
`placement`/`order`, since placement is chosen at enable time, not baked
into the manifest. Enable the plugin and give it a placement in one step:

```sh
omarchy plugin enable backstory.this-week right
```

`omarchy plugin enable <id> [placement]` accepts any of the sections
your bar supports (e.g. `left`, `center`, `right` — `right` matches this
plugin's own `defaultSection`); see `omarchy plugin enable --help` for
the exact list your Omarchy version ships.

### Upgrading

Two things Brian hit on his own desk upgrading an already-installed copy:

- **A copy enabled panel-only never gets the bar widget.** Omarchy's own
  `PluginRegistry.qml:528` only inserts a plugin into the bar when its id is
  found NOWHERE in the bar's existing `shell.json` `plugins` list — so if
  you enabled this plugin before it had a `bar-widget` kind (or ever ran
  `omarchy bar put` by hand), re-running `omarchy plugin enable` or
  `omarchy bar put` silently no-ops. Disable and re-enable it instead:

  ```sh
  omarchy plugin disable backstory.this-week && omarchy plugin enable backstory.this-week right
  ```

- **New `.qml` files added to an already-installed plugin don't show up.**
  The running shell's type loader caches the plugin directory's file
  listing, so a `git pull` (or `cp -r`) that adds a NEW `.qml` file the
  shell hasn't seen before fails with a "File name case mismatch"-style
  error until the shell itself restarts:

  ```sh
  omarchy-restart-shell
  ```

## Open it

Click the bar glyph. It toggles the panel; clicking it again, or the
panel's own close button, closes it. The panel can also be driven without
the bar widget, e.g. for a keybinding:

```sh
omarchy-shell shell summon backstory.this-week '{}'
omarchy-shell shell toggle backstory.this-week '{}'   # summon if closed, hide if open
omarchy-shell shell hide backstory.this-week
```

Bind `omarchy-shell shell toggle backstory.this-week '{}'` to a keybinding
(Hyprland `bindd`) for one-key access, the same way Omarchy's own OSD and
menu plugins are bound — the bar widget's own click runs this exact command
(`js/launchers.js`'s `barToggleCommand()`), so the bar widget and a
keybinding both drive the same host toggle and never fall out of sync.

## Testing

```sh
go test ./panel/...
```

`panel_contract_test.go` is Go, not QML — it has no Quickshell dependency
and runs anywhere the rest of this repo's `go test ./...` runs (`make
check`). `launcher_contract_test.go` and `groups_selection_test.go` go
one step further for the pure-JS logic in `js/launchers.js` and
`js/groups.js`: they run those actual `.js` files under Node
(`js_runtime_test.go`'s `evalJS`), asserting on the real output rather
than a Go reimplementation. They FAIL (not skip) if `node` isn't on
`PATH` (round 3 defect C) — `make check` is the gate for this contract,
so a Node-less machine must not appear to pass it silently. Node needs to
be on `PATH` wherever `make check` runs, even though the plugin itself
needs only Quickshell/QML at runtime, never Node.

```sh
make panel-qml-test   # part of `make check`
```

`panel/qmltest/` is a headless QML test harness (round 4, task 4fe02e30
Part 1): `qmltestrunner` (Qt 6's QtTest QML runner, `QT_QPA_PLATFORM=
offscreen`) loads every `panel/*.qml` file against `panel/qmltest/stubs`'s
minimal stand-ins for `qs.Ui`, `qs.Commons`, `Quickshell`, `Quickshell.Io`
and `Quickshell.Wayland` — the actual Omarchy/Quickshell runtime this
plugin targets, not a Go proxy for it. `panel/qmltest/tests/` feeds
`Panel.qml` the committed `this-week` golden, `{}`, and an all-empty-
arrays payload through the stub `Process`; walks the visible item tree
with the panel open and with the group editor open, asserting every item
sits inside `PanelWindow`'s own bounds; and drives real simulated clicks
(the bar widget, a Where-you-left-off row, the group editor's add flow)
asserting the resulting argv. `QT_FATAL_WARNINGS=1` is this harness's
"zero QML warnings" gate — see `panel/qmltest/tests/tst_load_all.qml`'s
own doc comment for why (no `QQmlEngine.warnings` hook is reachable from
plain QML). `make panel-qml-test` FAILS, naming `qmltestrunner`, if it
isn't on `PATH`.

The one thing this harness cannot exercise headlessly is a real Wayland
compositor's own layer-shell placement/exclusion-zone behavior — that
stays proof-kind "look", the human check below.
