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
  terminal at that project's `cwd`; a project with a handoff shows a
  "Resume in agent" button that runs `omarchy agent prompt <handoff text>`
  in that same `cwd`, falling back to copying the handoff id to the
  clipboard if the agent launch itself fails to start.
- **The week** — a small per-project activity bar for each day in the
  window.
- **Gear icon** — opens a group editor that calls `backstory group
  set|clear|list` to add/remove a project from a group; the panel refetches
  `this-week` after every change so rows regroup immediately.

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

## Open it

A "panel" plugin is loaded when summoned:

```sh
omarchy-shell shell summon backstory.this-week '{}'
omarchy-shell shell toggle backstory.this-week '{}'   # summon if closed, hide if open
omarchy-shell shell hide backstory.this-week
```

Bind `omarchy-shell shell toggle backstory.this-week '{}'` to a keybinding
(Hyprland `bindd`) for one-key access, the same way Omarchy's own OSD and
menu plugins are bound.

## Testing

```sh
go test ./panel/...
```

`panel_contract_test.go` is Go, not QML — it has no Quickshell dependency
and runs anywhere the rest of this repo's `go test ./...` runs (`make
check`). The plugin's own QML has no automated runtime test: Quickshell
requires an actual Wayland/Omarchy desktop, which is why the human check
below is proof-kind "look", not "run" — see the task's own DONE WHEN list.
