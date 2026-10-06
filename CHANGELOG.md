# Changelog

All notable changes to Backstory are documented in this file. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions
are [Semantic Versioning](https://semver.org/) (`vMAJOR.MINOR.PATCH`).

Nothing has been tagged yet — v1 is cut only once `PLAN.md`'s Phase 4
done-when holds on a box we do not own: a machine that is not Brian's. See
`RELEASING.md` for the release flow and its approval gates.

## [Unreleased]

### Changed

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
- Project identity from git common dir + first remote; unix socket server
  identifying callers via `SO_PEERCRED` → `/proc` ancestry, with fakes for
  the untested hops (Phase 1).
- `systemd --user` unit (`ops/backstory.service`), restart on crash (Phase 1).
- `backstory mcp` stdio shim with five frozen v0 tools: `recall`, `note`,
  `timeline`, `confirm`, `status` (Phase 2).
- SessionStart warm-context block (five fixed slots, token-budgeted), the
  skill file, and the Claude Code installer (`backstory install claude`,
  with `--check` and `--remove`) wiring MCP/hook/skill entries, deduped
  against anything already present (Phase 2).
- Claude transcript backfill (`backstory backfill claude`), ingested at the
  inferred tier (Phase 3).
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
