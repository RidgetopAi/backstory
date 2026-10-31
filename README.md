# Backstory

**Memory for your AI coding agents, on [Omarchy](https://omarchy.org).**

Start a new Claude Code, Codex, Hermes or Pi session and the agent already knows what happened
last time: what you decided, what got finished, and what's next. You stop re-explaining
yesterday.

Backstory runs quietly in the background on your own computer. It watches what your agents do,
keeps the notes they leave, and hands a short summary to the next session. A panel
(`CTRL + SHIFT + B`) shows your week and takes you back into any project in one click. Nothing
leaves your machine.

- **New here?** Follow [Install](#install-from-source) below, then read the
  **[User Manual](docs/USER-MANUAL.md)**. It explains the panel, every command, and how to
  ask your agent to remember things.
- **Updating?** See [Updating](#updating).

License: MIT.

## Install from source

Before you install: [what Backstory touches](docs/WHAT-BACKSTORY-TOUCHES.md) (every file it writes and
reads, and that it makes no network connections of its own) and [SECURITY.md](SECURITY.md)
(private vulnerability reports, and how to verify a release).

### Prerequisites

[Omarchy](https://omarchy.org), plus `git`, `make` and Go 1.27 or newer (the version in
`go.mod`). Omarchy has `git` and `make` already, but not a new enough Go. One command fixes
that (mise ships with Omarchy): `mise use -g go@1.27`.

`make install` checks Go first and stops before building if it's missing or too old.

### Install

Open a terminal (`SUPER + Enter`) and run:

```
git clone https://github.com/RidgetopAi/backstory && cd backstory
make install
```

That's it. When it finishes it prints `backstory <version> installed` and a `next:` line.
Two follow-ups:

1. **Keyboard shortcut.** If the installer prints a `require("hypr.backstory")` line, add it to
   `~/.config/hypr/hyprland.lua` so `CTRL + SHIFT + B` works. (The installer never edits that
   file for you.)
2. **Import your past sessions** so memory isn't empty on day one:

   ```
   backstory backfill claude
   ```

   Use `codex`, `hermes` or `pi` for those agents.

Then open the panel with **`CTRL + SHIFT + B`** and check everything is connected with
`backstory install --check`. Agent-specific steps (Codex asks you to trust it once; Hermes
needs a restart) are under [What works today](#what-works-today).

This builds `main`, and `backstory version` prints a commit from `git describe`. To install a
release instead, run `git checkout <tag>` (for example `v1.2.0`) before `make install`.

### What `make install` does

- builds the program and puts it at `~/.local/bin/backstory`
- installs and starts the background service (`systemd --user` unit `backstory`)
- installs the panel into `~/.config/omarchy/plugins/backstory.this-week` and turns it on with
  `omarchy plugin enable backstory.this-week` (if the `omarchy` command isn't found, it prints
  that command for you to run). It restarts the Omarchy shell if your session is unlocked; if
  it's locked, it restarts the shell once you unlock (and tells you the manual
  `omarchy restart shell` fallback)
- installs the keyboard-shortcut file `~/.config/hypr/backstory.lua`
- runs `backstory install`, which connects every agent it finds, then checks the service
  answers (or prints a failure line naming the daemon and exits non-zero)

It refuses to overwrite a plugin folder that isn't a Backstory panel. `make uninstall`
reverses it all and leaves your memory alone.

**The panel shortcut.** `CTRL + SHIFT + B` is set in `~/.config/hypr/backstory.lua`; you can
also open the panel from its bar icon (see `panel/README.md`). It shadows Chromium's
bookmarks-bar shortcut while loaded. To change it, edit `BACKSTORY_BIND` in that file and
reload Hyprland. `make install` overwrites the file on upgrade, so if you want a different
chord to survive, bind it in your own `hyprland.lua` instead.

**Optional:** `backstory install bash` also records the commands you type in bash (it adds a
few lines to `~/.bashrc`).

### To do it by hand instead of `make install`

```
make build
install -Dm755 bin/backstory ~/.local/bin/backstory
install -Dm644 ops/backstory.service ~/.config/systemd/user/backstory.service
systemctl --user daemon-reload
systemctl --user enable --now backstory
cp -r panel ~/.config/omarchy/plugins/backstory.this-week
omarchy plugin enable backstory.this-week
install -Dm644 ops/hyprland/backstory.lua ~/.config/hypr/backstory.lua
omarchy restart shell
backstory install
backstory install bash
```

## Updating

From your `backstory` folder:

```
git fetch --tags
git checkout <new tag>   # e.g. v1.2.0; or `git pull` to follow main
make install
backstory version
```

`make install` keeps the previous program as `~/.local/bin/backstory.prev`, restarts the
background service, and backs up the previous panel beside itself. `backstory version` should
print the new tag (or, on main, the new commit).

If you checked out a tag, git leaves you on that tag rather than on a branch, so a later
`git pull` won't work. To follow `main` again: `git checkout main && git pull`.

## What works today

- **Claude Code**: `backstory install claude` gives every new session a short summary at
  start, and the `note`, `recall`, `timeline`, `confirm` and `status` tools, with nothing for
  you to configure. After a session's first `git commit` that moves HEAD, the agent gets one
  short line asking it to record an outcome `note` (skipped if trivial). It asks at most once
  per session, only if the session hasn't written a note yet, and never blocks or costs an
  extra model turn. Commits by other agents, pulls and failed commits never trigger it.
  `backstory hook stop` is a silent no-op: older installs registered a Stop nudge, and
  `backstory install` removes it (your own Stop hooks are left byte-identical).
- **Codex**: `backstory install codex` registers the tools and the SessionStart/PostToolUse
  hooks under `~/.codex` and adds an `AGENTS.md` stub. **One manual step:** open `codex` once
  in a project, choose "Trust and Continue" to trust the folder, then "Trust all and continue"
  to approve the Backstory hooks. Until then Codex gets no summary and nothing is recorded;
  `backstory install --check` shows the hooks as not-trusted and the panel shows a reminder.
  Backstory never approves itself, and an upgrade that changes a hook may ask again. Codex
  gets the same once-per-session outcome ask as Claude Code.
- **Hermes**: `backstory install hermes` installs a memory-provider plugin and the backstory
  skill under `$HERMES_HOME` (default `~/.hermes`). Restart Hermes after installing: a running
  Hermes doesn't load a newly installed memory-provider plugin. Hermes gets the outcome ask
  through a `transform_terminal_output` hook (an older Hermes without hooks gets none).
- **Pi**: `backstory install pi` installs an extension under `~/.pi/agent`, with the same
  outcome ask added to a bash result.
- **Local models**: Pi with any local OpenAI-compatible provider (e.g. ollama, a llama.cpp
  server) is recorded and recalled like a hosted one; Codex transcripts with a custom
  `model_provider` import the same way.
- **Shell capture**: `backstory install bash` records commands you type in bash. To keep a
  folder (and everything under it) out of shell capture, put an empty `.backstory-ignore` file
  in it; this repo ships one so a fresh clone is never captured.
- **Pausing and per-session opt-out**: `backstory capture off` pauses recording for every
  agent until `backstory capture on`. To keep one agent session out of Backstory without
  touching anything else, start it with `BACKSTORY_OFF=1`, e.g. `BACKSTORY_OFF=1 codex` for a
  review session that shouldn't see Claude Code's history. Any non-empty value other than `0`
  turns it off. That session gets no summary at start, nothing it does is recorded, and its
  tools answer that Backstory is off for this session; the Hermes plugin honours the same
  variable. It affects only that program and what it starts: other sessions and harnesses,
  and the same agent started without it, are unchanged.
- **The panel and This Week**: `CTRL + SHIFT + B`, or `backstory this-week` in a terminal.
  It shows what needs you, where you left off in each project, and the week's activity.
- **Your memory, your controls**: `backstory projects` lists every project and its key;
  `records`, `recall` and `timeline` read what's saved; `edit`, `delete`, `purge` and
  `affirm` fix or forget it; `group` folds related projects into one row; `export` writes a
  project's memory as markdown for another tool. All of them are explained, with examples, in
  the [User Manual](docs/USER-MANUAL.md#6-command-reference).

## Project roots

By default every folder an agent works in can show up as a project in the panel, the bar and
This Week. To keep scratch folders, an agent's own install folder and config-tweak sessions
out of those views, name the folders your real projects live under:

```bash
BACKSTORY_PROJECT_ROOTS=~/Code
```

The value is a colon-separated list of absolute paths (`~` is expanded, a trailing slash is
fine), read the same way as `BACKSTORY_WORKSPACE_DIRS`. A project is in scope when its folder
is one of the roots or under one; a sibling that merely starts with a root's name (`~/Code2`)
is not. Unset, every project is in scope.

On Omarchy, set it for your whole desktop in `~/.config/uwsm/env.d/50-backstory` (create the
folder with `mkdir -p ~/.config/uwsm/env.d`), as a shell line:

```sh
export BACKSTORY_PROJECT_ROOTS="$HOME/projects"
```

then log out and back in, so the panel and every terminal get it. The
[User Manual](docs/USER-MANUAL.md#where-to-set-them) has a no-logout alternative for the panel.

This only changes what the panel, the bar and `backstory this-week` list: the current project
is always the most recent in-scope one. Recording, recall and the store are untouched, so a
session outside the roots still gets its own memory, `backstory status` run there says the
folder is outside `BACKSTORY_PROJECT_ROOTS`, and nothing is lost if you change the setting
later.

## Uninstall

`make uninstall` does all of this for you except the store and the line in your
`hyprland.lua`: it runs `backstory install --remove` and `backstory install bash --remove`
first, stops, disables and removes the service, removes `~/.local/bin/backstory` and
`backstory.prev`, removes the optional sync timer and its service
(`~/.config/systemd/user/backstory-sync.timer` and `backstory-sync.service`) when present,
removes `~/.config/hypr/backstory.lua`, and removes the panel plugin (and its
hidden `.backstory.this-week.prev` backup) if it is a Backstory panel. It leaves your memory
alone and doesn't restart the Omarchy shell. Remove `require("hypr.backstory")` from your
`hyprland.lua` yourself.

By hand, undo each integration first, while the program is still installed:

```
backstory install claude --remove
backstory install codex --remove
backstory install hermes --remove
backstory install pi --remove
backstory install bash --remove
```

Then stop the service and remove it, the panel plugin, and the program:

```
systemctl --user disable --now backstory
rm ~/.config/systemd/user/backstory.service
systemctl --user disable --now backstory-sync.timer   # only if you ran `backstory install sync`
rm -f ~/.config/systemd/user/backstory-sync.timer ~/.config/systemd/user/backstory-sync.service
systemctl --user daemon-reload
rm -r ~/.config/omarchy/plugins/backstory.this-week
rm -rf ~/.config/omarchy/plugins/.backstory.this-week.prev
rm ~/.local/bin/backstory ~/.local/bin/backstory.prev
```

Then remove `require("hypr.backstory")` from `~/.config/hypr/hyprland.lua` **before or with**
deleting the Lua file. Deleting `~/.config/hypr/backstory.lua` while that line is still there
breaks your Hyprland config:

```
# edit hyprland.lua: delete the require("hypr.backstory") line, then
rm ~/.config/hypr/backstory.lua
omarchy restart shell
```

Last, and only if you want it gone, the store. **This deletes all Backstory memory (every
note and every recorded event) and it cannot be undone.** Skip it to keep your memory for a
later reinstall.

```
rm -r ~/.local/share/backstory
```

(With `XDG_DATA_HOME` set, the store is `$XDG_DATA_HOME/backstory` instead.)

## Privacy and trust

- Backstory opens no network connections: the service, its hooks and its agent tools talk
  over local sockets only.
- What it hands an agent (the start-of-session summary, `recall`) goes to that agent's model
  provider like anything else in its context. Backstory can't keep it local once an agent
  has it.
- Known secret shapes are removed before anything is stored: bearer tokens, `sk-` API keys,
  GitHub tokens (`ghp_`/`gho_`/`ghu_`/`ghs_`/`ghr_`/`github_pat_`), Slack tokens (`xox…`),
  JWTs, AWS access key ids (`AKIA…`), PEM blocks, `password=…`, and the value of any
  `NAME=value` assignment where NAME ends in KEY, TOKEN, SECRET or PASSWORD. Nothing else is
  scanned for; don't rely on it for other secret shapes.
- Deleting or purging wipes the text from the store, but the backups the service keeps beside
  it (`backstory.db.pre-sweep-*` and `backstory.db.pre-identity-*`, written before a
  project-key merge) keep the deleted text until you remove them.
- Only you can delete, edit, purge, pause or group. The tools agents get can add notes but
  never remove them.
- Single-user trust model: any process of yours named `claude`, `codex`, `copilot`, `hermes`,
  `opencode` or `pi` is accepted as that agent. Backstory doesn't defend against another
  process running as you.

## Where the store lives

`$XDG_DATA_HOME/backstory/backstory.db`, or `~/.local/share/backstory/backstory.db` when
`XDG_DATA_HOME` is unset. It's append-only, mode 0600, owned by you, and never shared.

Omarchy installs with LUKS full-disk encryption by default (measured on Omarchy 4.0.4: root is
`crypto_LUKS`), so the store is encrypted at rest on a default install.

## For contributors

`make build` writes `bin/backstory`; `make check` (fmt + vet + lint + race tests + the
Quickshell panel's QML suite) is the gate CI runs on every push.

The panel suite needs Qt 6's `qmltestrunner`. `make` uses `/usr/lib/qt6/bin/qmltestrunner`
(or `/usr/lib64/qt6/bin/qmltestrunner`) when it exists, ahead of any `qmltestrunner` on `PATH`,
which on Omarchy is the Qt 5 one and won't load the panel. Elsewhere, point the target at
Qt 6's: `make check QMLTESTRUNNER=/path/to/qmltestrunner`.

Bare `backstory install` detects the agent harnesses on the machine (Claude Code: `~/.claude`
or `~/.claude.json`; Codex: `~/.codex`; Hermes: `$HERMES_HOME`, else `~/.hermes`; Pi:
`~/.pi/agent`) and sets up each one, reporting any it skipped and why. It exits non-zero and
writes nothing if none is found. Name harnesses to skip detection:
`backstory install claude codex hermes pi` (`agents` is the generic AGENTS.md fallback and is
only installed by name). `--check` shows what's present and `--remove` undoes it, both on the
detected set unless harnesses are named.

Deeper references: [AGENT-CONTRACT.md](AGENT-CONTRACT.md) (what agents get and may write),
[SCHEMA.md](SCHEMA.md) (the store), [PANEL-CONTRACT.md](PANEL-CONTRACT.md) and
[panel/README.md](panel/README.md) (the panel), [CONTRIBUTING.md](CONTRIBUTING.md),
[RELEASING.md](RELEASING.md), [PLAN.md](PLAN.md).
