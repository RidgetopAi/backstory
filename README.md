# Backstory

Backstory is the OS memory daemon for [Omarchy](https://omarchy.org) (Arch + Hyprland): a
`systemd --user` service, one static Go binary, backed by an append-only SQLite/FTS5 store,
reachable over a unix socket and an MCP stdio shim. It watches what an agent does in a
project, keeps a trust-annotated ledger of it, and hands a warm summary back at the next
session — so an agent never starts cold and a human never has to re-explain what happened
yesterday.

License: MIT.

## Install from source

```
git clone https://github.com/RidgetopAi/backstory && cd backstory
make build
install -Dm755 bin/backstory ~/.local/bin/backstory
install -Dm644 ops/backstory.service ~/.config/systemd/user/backstory.service
systemctl --user daemon-reload
systemctl --user enable --now backstory
backstory install
backstory install bash
```

`make build` writes `bin/backstory`; `make check` (fmt + vet + lint + race tests + the
Quickshell panel's QML suite) is the gate CI runs on every push.

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
  hooks under `~/.codex` and adds an `AGENTS.md` stub.
- **Hermes**: `backstory install hermes` installs a memory-provider plugin under `$HERMES_HOME`
  (default `~/.hermes`).
- **Pi**: `backstory install pi` installs an extension under `~/.pi/agent`.
- **Shell capture**: `backstory install bash` wires bash's preexec/precmd command capture into
  `~/.bashrc`.
- **Backfill**: `backstory backfill claude` imports existing Claude transcripts so the first
  recall isn't empty.
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

## Coming for v1 (decision `3e14db82`)

- Local-model harness support.

## Where the store lives

`$XDG_DATA_HOME/backstory/backstory.db`, falling back to `~/.local/share/backstory/backstory.db`
when `XDG_DATA_HOME` is unset. It is append-only: 0600, owned by the daemon's user, never
shared.

Omarchy installs with LUKS full-disk encryption by default (measured on Omarchy 4.0.4: root is
`crypto_LUKS`), so the store is encrypted at rest on a default install.
