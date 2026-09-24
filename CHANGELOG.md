# Changelog

All notable changes to Backstory are documented in this file. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions
are [Semantic Versioning](https://semver.org/) (`vMAJOR.MINOR.PATCH`).

Nothing has been tagged yet — v1 is cut only once `PLAN.md`'s Phase 4
done-when holds on a box we do not own (Brian's desktop). See `RELEASING.md`
for the release flow and its approval gates.

## [Unreleased]

Phases 0–3 of `PLAN.md`, shipped to `main`:

### Added

- Repo scaffold, Go module, `make check` gate (fmt + vet + lint + race
  tests), GitHub Actions running it on push (Phase 0).
- Append-only SQLite/FTS5 store: timeline events, sessions with origin,
  ledger records with kind/tier/`about[]`/edges; `punch`, `claim`, `stage`
  reserved record kinds; human-only tombstone supersession (Phase 1).
- Provenance tiers set from caller identity (never a parameter); secret
  redaction, write rate/size caps, capture-off flag file (Phase 1).
- Project identity from git common dir + first remote; unix socket server
  identifying callers via `SO_PEERCRED` → `/proc` ancestry, with fakes for
  the untested hops (Phase 1).
- `systemd --user` unit (`ops/backstory.service`), restart on crash (Phase 1).
- `backstory mcp` stdio shim with five frozen v0 tools: `recall`, `note`,
  `timeline`, `confirm`, `status` (Phase 2).
- SessionStart warm-context block (five fixed slots, token-budgeted), the
  skill file, and per-harness installer with deduped MCP/hook/skill wiring
  (Phase 2).
- Backfill importers with fixture transcripts for Claude, Codex, opencode,
  Pi/omp, Hermes, and Copilot, ingested at the inferred tier (Phase 3).
- PostToolUse capture and bash preexec/precmd command capture for Tier A
  harnesses; usage record at
  `~/.local/state/omarchy/agents/usage/backstory.json` (Phase 3).

[Unreleased]: https://github.com/RidgetopAi/backstory/compare/main...HEAD
