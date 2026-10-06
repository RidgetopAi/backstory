# Changelog

All notable changes to Backstory are documented in this file. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions
are [Semantic Versioning](https://semver.org/) (`vMAJOR.MINOR.PATCH`).

Nothing has been tagged yet — v1 is cut only once `PLAN.md`'s Phase 4
done-when holds on a box we do not own: a machine that is not Brian's. See
`RELEASING.md` for the release flow and its approval gates.

## [Unreleased]

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
