# Backstory

Backstory is the OS memory daemon for [Omarchy](https://omarchy.org) (Arch + Hyprland): a
`systemd --user` service, one static Go binary, backed by an append-only SQLite/FTS5 store,
reachable over a unix socket and an MCP stdio shim. It watches what an agent does in a
project, keeps a trust-annotated ledger of it, and hands a warm summary back at the next
session. It is warm from day one from your existing transcripts (`backstory backfill claude`)
and warmest once agents leave handoffs; with no handoff the block is thinner, and when the
daemon is down there is no block at all. The aim is that a human rarely has to re-explain
what happened yesterday.

License: MIT.

## Install from source

Prerequisites: [Omarchy](https://omarchy.org), Go 1.27 or newer (the version in `go.mod`),
`git` and `make`. Stock Omarchy does not ship that Go; one line fixes it (mise ships with
Omarchy): `mise use -g go@1.27`. `make install` checks this first and stops before building
if Go is missing or older.

```
git clone https://github.com/RidgetopAi/backstory && cd backstory
make install
```

The repo has no release tags yet, so this builds `main`; `backstory version` prints a commit
(from `git describe`) until a version is tagged. Once a release tag exists, run
`git checkout <tag>` before `make install`.

`make install` builds the binary and installs it to `~/.local/bin/backstory`, installs and
starts the `systemd --user` unit, installs the panel plugin into
`~/.config/omarchy/plugins/backstory.this-week` (restarting the Omarchy shell if your session
is unlocked; if it is locked it tells you to run `omarchy restart shell`), installs the
Hyprland file `~/.config/hypr/backstory.lua`, then runs `backstory install` and prints
`backstory version`, and checks the daemon answers (it prints `backstory <version> installed`
and a `next:` line, or a failure line naming the daemon and exits non-zero). It never edits
your `hyprland.lua`: if `require("hypr.backstory")` isn't already in it, the installer prints
the line for you to add, otherwise it says the keybind is already loaded. It refuses to overwrite a plugin directory that isn't a
Backstory panel. `make uninstall` reverses it and leaves your memory store alone.

To open the panel, press `CTRL + SHIFT + B` (set in `~/.config/hypr/backstory.lua`) or click
the bar glyph (see `panel/README.md`). That chord shadows Chromium's bookmarks-bar shortcut
(Ctrl+Shift+B) while the keybind is loaded; to rebind it, change `BACKSTORY_BIND` in
`~/.config/hypr/backstory.lua` (a Hyprland reload applies it). `make install` overwrites that
file on upgrade, so keep a different chord in your own `hyprland.lua` if you want it to
survive. Then `backstory install --check` shows which harnesses
are set up; `backstory install bash` adds shell command capture to `~/.bashrc`.

To do it by hand instead of `make install`:

```
make build
install -Dm755 bin/backstory ~/.local/bin/backstory
install -Dm644 ops/backstory.service ~/.config/systemd/user/backstory.service
systemctl --user daemon-reload
systemctl --user enable --now backstory
cp -r panel ~/.config/omarchy/plugins/backstory.this-week
install -Dm644 ops/hyprland/backstory.lua ~/.config/hypr/backstory.lua
omarchy restart shell
backstory install
backstory install bash
```

## Updating

```
git fetch --tags
git checkout <new tag>   # if a release tag exists; otherwise `git pull` on main
make install
backstory version
```

`make install` keeps the previous binary as `~/.local/bin/backstory.prev`, restarts the
running daemon, and backs up the previous panel beside itself. `backstory version` should
print the new tag (or, until a version is tagged, the new commit).

`make build` writes `bin/backstory`; `make check` (fmt + vet + lint + race tests + the
Quickshell panel's QML suite) is the gate CI runs on every push.

The panel suite needs Qt 6's `qmltestrunner`. Omarchy's default `qmltestrunner` on `PATH` is
the Qt 5 one and will not load the panel; point the target at Qt 6's:
`make check QMLTESTRUNNER=/usr/lib/qt6/bin/qmltestrunner` (or `make panel-qml-test
QMLTESTRUNNER=...`). Check with `qmltestrunner -version`.

Bare `backstory install` detects the agent harnesses on the machine (Claude Code: `~/.claude`
or `~/.claude.json`; Codex: `~/.codex`; Hermes: `$HERMES_HOME`, else `~/.hermes`; Pi:
`~/.pi/agent`) and sets up each one, reporting any it skipped and why. It exits non-zero and
writes nothing if none is found. Name harnesses to skip detection:
`backstory install claude codex hermes pi` (`agents` is the generic AGENTS.md fallback and is
only installed by name). `--check` shows what's present and `--remove` undoes it, both on the
detected set unless harnesses are named. `backstory install bash` adds shell command capture
to `~/.bashrc`.

## What works today

- **Claude Code**: `backstory install claude` gets a warm SessionStart block on every session
  start, and the `note`, `recall`, `timeline` (plus `confirm`, `status`) MCP tools with no
  header, id, or config line beyond the installer's.
- **Codex**: `backstory install codex` registers the MCP server and SessionStart/PostToolUse
  hooks under `~/.codex` and adds an `AGENTS.md` stub. **One manual step:** open `codex` once
  in a project, trust the directory, and choose "Trust all and continue" to approve the
  Backstory hooks. Until then Codex gets no warm block and nothing is captured;
  `backstory install --check` shows the hooks as not-trusted and `backstory this-week` lists
  an Attention item. Backstory never writes Codex's trust entry itself, and an upgrade that
  changes a hook may ask again.
- **Hermes**: `backstory install hermes` installs a memory-provider plugin and the backstory skill under `$HERMES_HOME`
  (default `~/.hermes`).
- **Pi**: `backstory install pi` installs an extension under `~/.pi/agent`.
- **Local models**: Pi with any local OpenAI-compatible provider (e.g. ollama, a llama.cpp server) is
  captured and recalled like a hosted one; Codex transcripts with a custom `model_provider` import
  identically.
- **Shell capture**: `backstory install bash` wires bash's preexec/precmd command capture into
  `~/.bashrc`. To keep a folder (and everything under it) out of shell capture, put an empty
  `.backstory-ignore` file in it; this repo ships one so a fresh clone is never captured.
- **Backfill**: `backstory backfill claude` imports existing Claude transcripts as timeline
  events so the first recall isn't empty.
- **This Week**: `backstory this-week` on the CLI, and the same view in the Omarchy Quickshell
  panel (`panel/`) — Attention, Where you left off, and The week.
- **Groups**: `backstory group set|clear|list` groups related projects into one row in This
  Week and the panel.
- **Records**: `backstory records [--project KEY|--here] [--location DIR] [--kind K] [--history] [--json]` lists
  what Backstory has saved for a project, newest first, with each record's tier and status
  (current, superseded, possibly-stale, expired, deleted); superseded and deleted records show
  only with `--history`.
- **Edit**: `backstory edit <id> [--file PATH | --stdin]` corrects a record by writing a new
  human-declared record that supersedes it (opens `$VISUAL`/`$EDITOR` on a terminal); the old
  record stays in the ledger, shown as superseded under `records --history`.
- **Purge**: `backstory purge (--session ID | --project KEY [--location DIR] [--since T]
  [--until T]) [--dry-run] [--yes]` forgets a project's captured activity by whole session
  **and the saved records written in the window** (`--dry-run` prints `would purge N sessions,
  M events, R records`); purged sessions are stamped so transcript backfill never re-imports
  them, and the records are tombstoned like `delete`. `--location DIR` (with `--project`) acts
  on exactly the row This Week shows for DIR — see `records --location` — instead of every
  session and record sharing the project key (a non-git folder under a workspace shares the
  workspace's key with its siblings). A per-`--session` purge leaves records alone.
- **Read-only CLI**: `backstory recall` and `backstory timeline` read the store directly, no
  daemon round trip required; `backstory export` writes a per-project markdown mirror for
  another tool's memory file (Claude auto-memory, Hermes `MEMORY.md`, OpenClaw imports).

## Uninstall

`make uninstall` does all of this for you except the store and the line in your
`hyprland.lua`: it runs `backstory install --remove` and `backstory install bash --remove`
first, stops and disables the unit and removes it, removes `~/.local/bin/backstory` and
`backstory.prev`, removes `~/.config/hypr/backstory.lua`, and removes the panel plugin (and its
hidden `.backstory.this-week.prev` backup) if it is a Backstory panel. It leaves your memory
store alone and does not restart the Omarchy shell. Remove `require("hypr.backstory")` from
your `hyprland.lua` yourself.

By hand, undo each integration first, while the binary is still on disk:

```
backstory install claude --remove
backstory install codex --remove
backstory install hermes --remove
backstory install pi --remove
backstory install bash --remove
```

Then stop the daemon and remove its unit, the panel plugin, and the binary:

```
systemctl --user disable --now backstory
rm ~/.config/systemd/user/backstory.service
systemctl --user daemon-reload
rm -r ~/.config/omarchy/plugins/backstory.this-week
rm -rf ~/.config/omarchy/plugins/.backstory.this-week.prev
rm ~/.local/bin/backstory ~/.local/bin/backstory.prev
```

Then remove `require("hypr.backstory")` from `~/.config/hypr/hyprland.lua` **before or with**
deleting the Lua file — deleting `~/.config/hypr/backstory.lua` while that line is still there
breaks your Hyprland config:

```
# edit hyprland.lua: delete the require("hypr.backstory") line, then
rm ~/.config/hypr/backstory.lua
omarchy restart shell
```

Last, and only if you want it gone, the store. **This deletes all Backstory memory — every
ledger, note and captured event — and it cannot be undone.** Skip it to keep your memory for
a later reinstall.

```
rm -r ~/.local/share/backstory
```

(With `XDG_DATA_HOME` set, the store is `$XDG_DATA_HOME/backstory` instead.)

## Privacy and trust

- Backstory opens no network connections: its daemon, hooks and MCP shim talk over unix
  sockets only.
- The text it hands an agent (the SessionStart block, `recall`) goes to that agent's model
  provider like anything else in its context. Backstory cannot keep it local once an agent
  has it.
- Known secret shapes are redacted before they are stored: bearer tokens, `sk-` API keys,
  GitHub tokens (`ghp_`/`gho_`/`ghu_`/`ghs_`/`ghr_`/`github_pat_`), Slack tokens (`xox…`),
  JWTs, AWS access key ids (`AKIA…`), PEM blocks, `password=…`, and the value of any
  `NAME=value` assignment where NAME ends in KEY, TOKEN, SECRET or PASSWORD. Nothing else is
  scanned for; do not rely on it for other secret shapes.
- Deleting or purging scrubs the text from the store, but the backups the daemon keeps beside
  the store (`backstory.db.pre-sweep-*` and `backstory.db.pre-identity-*`, written before a
  project-key merge) keep the deleted text until you remove them.
- Single-user trust model: any process of yours whose name is `claude`, `codex`, `copilot`,
  `hermes`, `opencode` or `pi` is accepted as that harness. Backstory does not defend against
  another process running as you.

## Where the store lives

`$XDG_DATA_HOME/backstory/backstory.db`, falling back to `~/.local/share/backstory/backstory.db`
when `XDG_DATA_HOME` is unset. It is append-only: 0600, owned by the daemon's user, never
shared.

Omarchy installs with LUKS full-disk encryption by default (measured on Omarchy 4.0.4: root is
`crypto_LUKS`), so the store is encrypted at rest on a default install.
