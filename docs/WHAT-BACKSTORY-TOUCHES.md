# What Backstory touches

This page lists everything Backstory writes, reads and runs on your machine, so
you can check it before you install. Each written path names the source file
that does it, so you can read the code behind the claim.

Paths use `~` for your home folder. `XDG_*` variables, when you have set them,
move the folders as noted.

## What it never does

- **No network of its own.** Backstory's own code opens no internet
  connection. It talks to its background service over a local unix socket
  only. This is enforced by a test, `ops/nonetwork_test.go`, which fails the
  build if shipped code imports `net/http`, `net/rpc`, `net/smtp` or
  `crypto/tls`, or dials or listens on anything but a unix socket.
- **Sync is optional and uses your ssh.** If you pair two computers, Backstory
  runs the `ssh` command you already have (or the command in
  `BACKSTORY_SYNC_SSH`) to reach the other computer. The connection, keys and
  host checking are ssh's. Source: `internal/sync/transport.go`. If you never
  pair a computer, nothing runs.
- **No root or sudo.** Everything is installed under your home folder and runs
  as a `systemd --user` service. The installer never calls `sudo`.
- **No telemetry.** Nothing is reported anywhere.
- **Your data stays in one local SQLite file** (see below).
- **It does not edit your `hyprland.lua`.** It installs its own file beside it
  and prints the one line for you to add.

## What it writes

### The program and its service (`make install`)

Source: `ops/install.sh` unless noted.

| Path | What |
| --- | --- |
| `~/.local/bin/backstory` | The program. The previous one is kept as `~/.local/bin/backstory.prev` for rollback. |
| `~/.config/systemd/user/backstory.service` | The background service (from `ops/backstory.service`). Enabled and started. |
| `~/.config/hypr/backstory.lua` | The `CTRL + SHIFT + B` shortcut file (from `ops/hyprland/backstory.lua`). |
| `~/.config/omarchy/plugins/backstory.this-week/` | The panel (QML files and `manifest.json` from `panel/`). The previous panel is kept as `~/.config/omarchy/plugins/.backstory.this-week.prev`. The installer refuses to overwrite a folder there that is not a Backstory panel. |
| a transient user unit `backstory-shell-restart` | Only if your session is locked at install time: waits (up to 24 hours) for you to unlock, then restarts the Omarchy shell once. Gone afterwards. |

The installer also runs `omarchy plugin enable backstory.this-week` and
restarts the Omarchy shell when the session is unlocked.

`XDG_CONFIG_HOME` moves the `~/.config` paths. `BINDIR` moves the program.

### Your memory and runtime files

| Path | What | Source |
| --- | --- | --- |
| `~/.local/share/backstory/backstory.db` (`$XDG_DATA_HOME/backstory/` if set) | The memory store, one SQLite file, mode 0600. SQLite also creates `backstory.db-wal` and `backstory.db-shm` beside it while the service runs. | `cmd/backstory/daemon.go` (`storePath`), `internal/store/store.go` |
| `~/.local/share/backstory/sync.lock` | A lock file so two syncs do not overlap. | `internal/sync/lock.go` |
| `$XDG_RUNTIME_DIR/backstory/sock` (else `/run/user/<uid>/backstory/sock`, else `~/.local/state/backstory/sock`) | The unix socket the service listens on. Removed and recreated at start. | `cmd/backstory/daemon.go` (`socketPath`), `internal/socket/socket.go` |
| `$XDG_RUNTIME_DIR/backstory/capture-off` (same fallbacks) | An empty flag file, present only while you have run `backstory capture off`. | `cmd/backstory/capture.go`, `cmd/backstory/daemon.go` (`captureOffPath`) |
| `~/.local/state/omarchy/agents/usage/backstory.json` (`$XDG_STATE_HOME` if set) | A small usage summary (counts per day) that the Omarchy panel reads. Mode 0600. | `internal/usage/usage.go` |
| `$XDG_RUNTIME_DIR/.backstory-shell-<pid>-<n>` (else `$TMPDIR`, else `/tmp`) | Only if you opted in to the bash hook: a tiny per-shell state file, removed when the shell exits. | `cmd/backstory/shell.go` |

Two commands write where you point them: `backstory export --out <path>` writes
that file, and `backstory edit` uses a temporary file (mode 0600, removed
afterwards) for your editor. Sources: `cmd/backstory/export.go`,
`internal/export/atomic.go`, `cmd/backstory/edit.go`.

### Agent configuration (`backstory install`)

`make install` runs `backstory install`, which sets up every agent it finds
under your home folder, by looking for the folders named below. It changes only
what is listed here, writes each file atomically, leaves anything of yours in
place, and refuses to overwrite a file it did not write. Source for all of it:
`cmd/backstory/install.go` and `internal/install/`.

| Agent | Found by | What it edits | Source |
| --- | --- | --- | --- |
| Claude Code | `~/.claude` or `~/.claude.json` | `~/.claude.json` (adds a `backstory` MCP server); `~/.claude/settings.json` (adds SessionStart, PostToolUse, PostToolUseFailure and Stop hooks that run `backstory hook ...`); `~/.claude/skills/backstory/SKILL.md` (the skill); `~/.claude/CLAUDE.md` (adds a short marked stub) | `internal/install/install.go`, `internal/install/binary.go`, `internal/install/adapter.go` |
| Codex | `~/.codex` | `~/.codex/config.toml` (a marked block that adds the MCP server); `~/.codex/hooks.json` (hooks); `~/.codex/AGENTS.md` (marked stub); `~/.codex/skills/backstory/SKILL.md` | `internal/install/codex.go` |
| Hermes | `$HERMES_HOME`, else `~/.hermes` | `config.yaml` (a marked block that selects the memory provider); `plugins/backstory/plugin.yaml` and `plugins/backstory/__init__.py` (the plugin); `skills/backstory/SKILL.md`. All under the Hermes folder. | `internal/install/hermes.go` |
| Pi | `~/.pi/agent` | `~/.pi/agent/extensions/backstory/index.ts` (the extension); `~/.pi/agent/AGENTS.md` (marked stub); `~/.pi/agent/skills/backstory/SKILL.md` | `internal/install/pi.go` |
| bash (only if you run `backstory install bash`) | opt-in | `~/.bashrc` (one marked `backstory:begin` ... `backstory:end` block that records the commands you type) | `internal/install/bashrc.go`, `cmd/backstory/shell.go` |
| Sync timer (only if you run `backstory install sync`) | opt-in | `~/.config/systemd/user/backstory-sync.service` and `backstory-sync.timer`, enabled | `internal/install/syncunit.go`, `internal/install/units/` |
| Generic agents (only if you run `backstory install agents`) | opt-in, by name | `~/.agents/mcp.json` (adds the MCP server); `~/.config/AGENTS.md` (marked stub) | `internal/install/agents.go` |

Skill files are written by `internal/skill/skill.go`. If a skill file already
there differs from Backstory's, the install saves it first as `SKILL.md.bak-<hash>`
beside it, and writes the new one. Run `backstory install --check` to see the
state of each item.

Pairing another computer (`backstory pair`) records it as a peer in the store. It
does not install anything on that computer: the remote side only needs
`backstory` on its PATH or at `~/.local/bin/backstory` (`internal/sync/transport.go`).

## What it reads

- **Agent transcripts, for backfill** (`backstory backfill ...`, read only):
  `~/.claude/projects`, `~/.codex/sessions`, `~/.pi/agent/sessions` and
  `$HERMES_HOME/state.db` (else `~/.hermes/state.db`). Sources:
  `internal/backfill/claude`, `codex`, `pi`, `hermes`.
- **`/proc`**, to tell which agent process and working folder a request came
  from (`internal/ident`, `internal/mcp/daemon.go`) and for the "here" view in
  the panel (`cmd/backstory/thisweek_here.go`).
- **`hyprctl`** (`activewindow`, `clients`), to find the window you are in
  (`cmd/backstory/thisweek_here.go`). Missing is fine.
- **`git`**, to learn a project's identity, remote and commits
  (`internal/project/git.go`). Read-only subcommands.
- **`tailscale status --json`**, only during sync, to learn this computer's
  names (`internal/sync/tailnet.go`).
- **`notify-send`**, only to show a desktop notice when sync starts failing
  (`internal/sync/health.go`).
- Your own **`$VISUAL` / `$EDITOR`** is run by `backstory edit`.
- Your **`hyprland.lua`** is read (never written) to see whether the shortcut
  line is already there.

## How to remove it

From your `backstory` folder:

```
make uninstall
```

This (`ops/install.sh`, `uninstall`) first runs `backstory install --remove`
and `backstory install bash --remove`, which take Backstory out of each agent's
config above and delete the files it added, then removes the sync timer if
present, stops and disables the service, deletes the program, its `.prev`
copy, the service unit, the shortcut file and the panel (and runs
`omarchy plugin disable backstory.this-week`).

What it leaves:

- **Your memory store** (`~/.local/share/backstory/`). Delete that folder
  yourself if you want the data gone.
- The `require("hypr.backstory")` line in your `hyprland.lua`. Remove it by
  hand.
- A panel folder that is not a Backstory panel, which it will not touch.
- Any `SKILL.md.bak-<hash>` backup an install made beside a skill file you had
  changed. Skill files that are not Backstory's are left alone.
- The usage summary `~/.local/state/omarchy/agents/usage/backstory.json` and
  the runtime socket folder, which are tiny and recreated only if the service
  runs again.

If you used the generic `agents` option, remove it with
`backstory install agents --remove` before uninstalling.
