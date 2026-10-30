# Changelog

All notable changes to Backstory are documented in this file. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions
are [Semantic Versioning](https://semver.org/) (`vMAJOR.MINOR.PATCH`).

v1.0.0 was cut on 2026-10-06 (decision `2deb6c91`, superseding `262cf929`), and
v1.1.0 on 2026-10-19 on the same terms. The measurement on a machine that is not
Brian's now gates the AUR push and the marketplace submission rather than the tag.
See `RELEASING.md` for the release flow and its approval gates.

## [Unreleased]

### Added

- `pair` sets up both sides (task 13d3682a, decision 2ac4c54f D6/D8). After `backstory pair <host>` on one computer, the other has a devices row and an enabled sync peer for it, so `backstory sync` and `status` sync health work there too, and records it imported show `from <device name>`. The other side reaches back at `<your user>@<address it saw>` or `pair --back <ssh-host>`; it checks that path at pair time and, when it fails, `pair` still exits 0 and prints one line naming the path and the command to run there, while that side's status shows the peer as not ok. `sync-serve` also records the name of any device that connects, so stores paired one-sided before this change show names after a sync. `pair --remove` still only acts on the side it runs on.
- Sync fails loudly (task 33412bb2, decision 2ac4c54f D8). One rule, `sync.Health`, decides when sync is on and not syncing (the latest attempt errored, recorded `unreachable` attempts span more than `sync_unreachable_hours` (default 24), or the installed sync timer is not active). While failing, `backstory status` prints `SYNC FAILING with <peer>: <reason> (last ok <time>|never)` first and exits 3, NEEDS YOU gets a `sync-failing` item (PANEL-CONTRACT.md), every SessionStart block carries a warning line that no budget drops, and `sync --quiet` sends one desktop notification (notify-send) when sync starts failing and one on recovery.
- `docs/USER-MANUAL.md`: a plain-language user manual covering the five note kinds with examples, the panel and its keys, every command with examples, privacy controls, settings and troubleshooting. `TestManualCoversEveryCommand` fails if a user-facing command has no manual entry or the manual names a command that doesn't exist.

### Fixed

- Sync: every session of a shared project syncs, notes or not, and arrives under that project (task 3a274a07, decision 1e53165a). Before, a session that wrote no note never reached the other computer, and stubs arrived with no project so `projects` could not count them. Session stubs now carry `project_sync_id`; a project that gains a remote or link also re-queues its earlier sessions. Sessions of unshared projects, the home directory and scratch folders still never leave the device.

- Sync finds backstory on a computer whose SSH server gives remote commands a bare PATH (Tailscale SSH; task 3147c689). The remote command now runs `backstory` from the remote PATH, else `$HOME/.local/bin/backstory` (the installer's location), then `sync-serve`. When neither exists, `pair` and `sync` say so, naming both places and noting the SSH server may not load your PATH; it is still classified `error`. `BACKSTORY_SYNC_SSH` still works; the remote words are now `sh -c '<script>'`.
- A placeholder project (`sync:<id>`, made when a record arrives for a project this machine has no checkout of) now has its sync identity, with source `synced`. Deleting or purging a record there reaches the computer it came from at the next sync (it used to stay readable there), and `backstory link list` and `backstory projects` show the id instead of `-` (task ac644f60).

### Changed

- `this-week` is about 12x faster, with identical output: 1.35 s to 0.11 s on a store with 110k events and 32 projects, measured 29 Oct before the focus-history change (#5) (it runs on every panel open, every minute in the bar and on each bar click). Migrations 0023-0025 add indexes on `sessions(project_key)`, `timeline_events(session_id, ts)` and `sessions(agent, harness_session_id)`; no data changes. Per-project reads no longer scan every session or every event; `sessionsForHome` reads only candidate sessions in one query; run keys are checked only for the sessions being read; agent activity seeks each session's latest event; and `this-week` asks git about each folder once (`project.CachedGit`) and reads the process table once per run, not per window.
- Sync serves a session stub only when a record the same feed would serve belongs to that session (task 7b489b17, brian 2026-10-22). Sessions in the home directory, scratch repos and other projects with no sync identity no longer leak when and how much you worked. A session whose project later gains a remote or link is served then, with its records. Stubs a peer already holds are left alone.
- README rewritten as a short front page for people new to Linux: a three-step install, what `make install` does, and links into the manual. All install, update, uninstall and privacy facts are kept. Both now say where to set `BACKSTORY_PROJECT_ROOTS` on Omarchy: `~/.config/uwsm/env.d/`, then log out and back in.

## [1.1.0] - 2026-10-19

### Fixed
- `backstory purge` on an already-purged session (or a project containing one) now erases events still attached to it, e.g. leaked by a backfill before task 3a28a070. The session keeps its original `purged_at`; the "purged N sessions, M events" line counts it as 0 sessions but M events, and `--dry-run` reports the same (task bb1080ad).
- Backfill importers no longer count events the purge gate refused as created, and a refused append no longer aborts the import (task bb1080ad).
- Backfill no longer re-imports into a purged live session (task 3a28a070). Codex and Hermes checked purge only when a backfill cursor existed, and Pi not at all, so the first backfill of a live-captured session that was then purged attached to it and wrote its events back. Every importer now refuses a purged run whether or not a cursor exists and records a cursor on the purged session so it is not re-decided each run; the store also refuses any `source=backfill` event for a purged session (`ErrSessionPurged`), so a future importer cannot forget it. Live appends are unaffected.

### Changed

- The post-commit outcome ask now reaches Codex and Pi (task 8626f7ca). The Codex PostToolUse hook prints the same `additionalContext` envelope for a Bash `git commit` that moved HEAD (hooks.json is unchanged, so existing trust entries stay valid). `--harness pi` sets the ask only when the payload carries `backstory_commit_ask: true`; before, a Pi session's first commit silently used up its ask because the extension discarded the hook's stdout. The Pi extension's new `tool_result` handler for bash awaits the hook (2 s bound) and appends the line to the result, does not forward that call again from `tool_execution_end`, and sends the exit code Pi reports (`structuredContent.exit_code`) only when it is a number. The `install codex` "Claude-only" note is removed.
- The every-turn Stop handoff nudge is replaced by one post-commit ask per session (task de47659e). `install claude` no longer registers a Stop hook and removes the Backstory Stop entry an older install wrote, leaving user Stop hooks byte-identical; `--check` no longer lists it; `backstory hook stop` stays as a silent exit-0 no-op so a stale registration is harmless. Instead, the Claude PostToolUse hook returns one line of `additionalContext` (not a block, no extra model turn) asking for an outcome `note` after the session's own first `git commit` that moved HEAD, only if the session has written no record, at most once per session. HEAD moving without the session's own commit never triggers it. The daemon records `session.head` events as bookkeeping. The `install codex` note now says the post-commit ask is Claude-only.
- Hermes gets the same post-commit ask (task 9a4fce0f) through a `transform_terminal_output` hook the memory provider registers: the hook calls the daemon synchronously (2s bound) with `commit_ask` and appends the ask line to that terminal result; `sync_turn` never sends `commit_ask` and skips calls the hook already recorded. An older Hermes without `register_hook` works as before, with no ask.

## [1.0.0] - 2026-10-06

### Added

- `backstory hook stop` and a Claude Stop hook registered by `install claude` (task 914964ae): when a session changed files or had failed commands and wrote no handoff, it asks once (Claude's block decision, naming the observed counts) for `note handoff` or a one-line reason none is needed. Silent on `stop_hook_active`, `BACKSTORY_NO_SESSION`, capture off, no daemon, or any error. The daemon only reports counts and never authors a handoff.
- The Stop hook now sees edits made through Bash (heredocs, `sed`) (task 8efad0ce): a live session records a start-of-session `session.git_state` (`phase: start`, with HEAD and uncommitted paths), and at Stop a differing observed repo state counts as edit evidence. Falls back to the tool-event rule when either observation is could-not-observe.

### Changed

- Docs corrected to what V1 does (task d05fb955): adoption in `status`, retention/pruning, outcome three-state, the inferred tier, `recall` anchors (project, record id, free text — not path or ref), the project key (common dir only), the workspace root row, the SessionStart budget (default 1,500, not yet user-settable), the exact redacted secret shapes, install/uninstall steps, a privacy section, the single-user identity caveat, the CTRL+SHIFT+B shadowing note and the Qt 6 `qmltestrunner` note. The `recall` tool's description text changed accordingly; its parameters did not.
- Install and uninstall hardening (task 024f209a): a bare `backstory install` reports a harness with a foreign entry (e.g. another Hermes `memory.provider`) as skipped, leaves it byte-identical, and installs the rest, exiting 0 when anything installed; `make uninstall` runs `install --remove` and `install bash --remove` before removing the binary; hook commands and MCP entries use the binary's absolute path (`--binary` overrides); `install --check` also dials the daemon; stub lines tell the agent to end with `note handoff` carrying `next` and `supersedes`; the Claude SessionStart matcher includes `compact`; `install bash` prints a line; the installer's self-check no longer leaves a session row.
- One resolver for "the records that belong to location L" (task ed31b744): recall, `export`, `records --here`/`--project`, `purge --project`, This Week's row handoff and the SessionStart Resume now all read the repo-key records plus the workspace-homed ones (`note` files handoffs under the workspace key) whose writing session belongs to that location; for the workspace root, exactly those labelled with the root. recall honours its `project` parameter (a project key or an absolute path) and falls back to the caller's own location. `purge --project` now also tombstones the key's sessions' workspace-homed records.
- Panel redesign (task fc1f340d, decision 63ce9687): the panel is now "Backstory" with a HERE
  card for the project you are in, a Continue that focuses the project's open window or launches
  Omarchy's default agent (and says when that differs from the agent last used), 7-day session
  strips on Recent rows, a one-line week summary in place of The Week, keyboard navigation
  (j/k, Enter, t, m, r, Esc), and a bar widget showing the project name with an attention dot
  (left toggle, right Continue, middle refresh).

Phases 0–4 of `PLAN.md`, shipped to `main` (run `backstory --help` for the
full command list):

### Added

- Repo scaffold, Go module, `make check` gate (fmt + vet + lint + race
  tests + the Quickshell panel's QML test suite), GitHub Actions running it
  on push (Phase 0).
- Append-only SQLite/FTS5 store: timeline events, sessions with origin,
  ledger records with kind/tier/`about[]`/edges; `punch`, `claim`, `stage`
  reserved record kinds; human-only tombstone supersession (Phase 1).
- Provenance tiers set from caller identity (never a parameter); secret
  redaction, write rate/size caps, capture-off flag file (`backstory capture
  off|on|status`) (Phase 1).
- Project identity from the git common dir (the first remote is not part of the key); unix socket server
  identifying callers via `SO_PEERCRED` → `/proc` ancestry, with fakes for
  the untested hops (Phase 1).
- `systemd --user` unit (`ops/backstory.service`), restart on crash (Phase 1).
- `backstory mcp` stdio shim with five frozen v0 tools: `recall`, `note`,
  `timeline`, `confirm`, `status` (Phase 2).
- SessionStart warm-context block (five fixed slots, token-budgeted), the
  skill file, and the Claude Code installer (`backstory install claude`,
  with `--check` and `--remove`) wiring MCP/hook/skill entries, deduped
  against anything already present (Phase 2).
- Claude transcript backfill (`backstory backfill claude`): replays transcripts as
  timeline events (`source = backfill`), not ledger records. The `inferred` tier is
  reserved; nothing produces an inferred record in V1 (Phase 3).
- PostToolUse capture for Claude Code, recording observed file writes as
  live timeline events (Phase 3).
- Read-only CLI reading the store directly, no daemon required: `backstory
  recall`, `backstory timeline`, `backstory this-week`, and human-only
  `backstory delete <id>` (tombstone) (Phase 4).
- `backstory group set|clear|list`: human-set project groups, and a
  Quickshell This Week panel (`panel/`) showing Attention, Where you left
  off, and The week, with a group editor (Phase 4).
- `backstory export`: per-project markdown mirror (headline/summary
  altitude, atomic `--out` write) for another tool's memory file to read
  (Claude auto-memory, Hermes `MEMORY.md`, OpenClaw imports) (Phase 4).
- Release discipline: `RELEASING.md`, `ops/aur/PKGBUILD`, and `make
  release-check` validating VERSION/CHANGELOG/PKGBUILD agree (Phase 4).

[Unreleased]: https://github.com/RidgetopAi/backstory/compare/main...HEAD
