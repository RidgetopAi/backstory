# Backstory Build Plan

RidgetopAi · LOCKED 2026-09-22 · Mandrel decision `6caaac1d` · branch:backstory

Backstory is the OS memory for Omarchy: a user daemon that remembers what every agent did
and why, hands it back warm at the next session, and runs the Punch List loop on a laptop.
Ten locks define it. This page is the phased plan to build it, and how the work runs
through Mandrel and the loop. It is the north star every worker reads; **every punch cites
its section** (`PLAN.md §Phase 1`).

## The ten locks

Seven product locks (each its own Mandrel decision) plus three build locks (all three in
decision `6caaac1d`, locked by Brian 2026-09-22):

| Lock | Mandrel id | What it fixes |
|---|---|---|
| Q1 | `94ba9797` | Timeline and ledger tiers; sessions derived from the timeline |
| Q2 | `4fb0cc2c` | Provenance tiers; inference is opt-in |
| Q3 | `5d2733c0` | `systemd --user` daemon is home; the shell plugin is a client |
| Q4 | `5f0d2107` | Day one = backfill → This Week; warm SessionStart |
| Q5 | `c27aae43` | Name: Backstory; licence: MIT |
| Q6 | `63248995` | Ladder to the Omarchy Install menu |
| Q7 | `c8f9d7a9` | The loop is in scope |
| L8 | `6caaac1d` | Language: Go |
| L9 | `6caaac1d` | Search: FTS5 first, embeddings behind an interface |
| L10 | `6caaac1d` | Packaging: AUR package first, plugin depends on it |

The agent-side contract that sits under Q1–Q4 is `AGENT-CONTRACT.md`; the store shape is
`SCHEMA.md` (v0, draft until the first punch lands it).

## The three build locks (L8–L10) — LOCKED

These were the three choices the seven product decisions left open. Brian locked all three
as recommended on 2026-09-22 (decision `6caaac1d`). They are not open; the rationale is kept
so a punch can cite it.

### Lock 8 — Language: Go

Why. "Single binary" rules out Node. Go gives one static binary, SQLite with FTS5 compiled
in with no C toolchain, peer-credential and `/proc` work in the standard library, an MCP
stdio server in an afternoon, and builds in seconds inside a confined lane. Sonnet workers
write good Go and its test culture fits the critic's RED/GREEN rule.

Not Rust. It fits Omarchy's taste, but a slower compile and a harder bar for lane workers
cost the loop its iteration speed for no product gain in v1.

### Lock 9 — Search: FTS5 first, embeddings behind an interface

Why. Recall in v1 is anchored (project, path, time, session), and full-text over the ledger
covers "why did we" on a laptop. Embeddings need a local model and CPU; that is a later
opt-in like inference. The store gets a `Search` interface now so the swap costs no
migration.

### Lock 10 — Packaging: AUR package first, plugin depends on it

Why. The marketplace plugin repo is QML with no symlinks; a compiled daemon cannot live
there. Package `backstory` on AUR ships the binary, the user unit, the CLI and the skill.
The marketplace plugin (rung 1 of the ladder) depends on the package. The listed branch is
`release`, never `main`, because the marketplace clones HEAD.

## v1 scope (decision `3e14db82`)

v1's agent surface is frozen at the five calls the MCP shim exposes: `recall`, `note`,
`timeline`, `confirm`, `status` (Phase 2's five frozen v0 tools). Shell command capture and
additional harnesses (Codex, Hermes, Pi, local-model) are still coming for v1. Two things are
explicitly **after v1**, not required to tag:

- Copilot and OpenClaw harness integration.
- Window, notification and clipboard capture (the socket2 events, notification and clipboard
  JSON named in Phase 3's "Human" section).

## Phases

Each phase has a done-when in the same shape as a punch. **lane** means the loop builds it
with fakes and tests. **human** means Ridge or Brian proves it on the real desktop
(`ssh Ridgetop-desktop`).

## Phase 0 — The repo exists and the loop can build in it

hand-built · Foreman waves from this session · ~1 day

**Done when:** a trivial punch tagged `repo:backstory` goes ready → built → critic PASS →
merged with no hand intervention.

### Deliverables

- `RidgetopAi/backstory`, MIT, Go module, `make check` = vet + lint + `go test -race`
- `PLAN.md` (this plan), `AGENT-CONTRACT.md` (Tesla's page), `SCHEMA.md` v0
- GitHub Actions running `make check` on push
- Canonical clone on the OVH box; `repo:backstory` in the picker and merge-actor env with
  `make check` as the gate
- House rules for lanes: foreground tests, commit before full run, mutation receipt

### Proves

- Loop runs a third repo with config only
- Gate has three states in the lane (GREEN / RED / could-not-measure)

## Phase 1 — Store and daemon core

lane-heavy · week 1

**Done when:** `backstory` runs as a `systemd --user` unit on Brian's desktop, owns a 0600
SQLite store, and an untrusted client on the socket is identified without declaring
anything.

### Lane test

- Schema v0: timeline events, sessions with origin, ledger records with kind / tier /
  `about[]` / edges; `punch`, `claim`, `stage` reserved as record kinds
- Append-only enforced in the store: no update, no delete, supersession is a new record
  plus an edge; human tombstone only
- Provenance tier set from caller identity, never from a parameter; agent-confirm yields
  `agent-declared`
- Secret redaction on write; write rate and size cap; capture-off flag file honoured
- Project identity = git common dir + first remote, cwd kept as a timeline fact
- Unix socket server; `SO_PEERCRED` → `/proc` ancestry → harness process, with fakes

### Human desktop

- Unit file (`ops/backstory.service`), restart on crash — the probe-2 mount-namespace
  sandbox directives (`ProtectSystem`/`ProtectHome`/`PrivateTmp`/`PrivateNetwork`/
  `ReadWritePaths`) are omitted: measured on Brian's desktop 2026-09-22 (context
  `d38ca301`) to leave `/proc/<harness_pid>/cwd` unreadable under `systemd --user`'s
  implicit user namespace.
- Ancestry hop through tmux → terminal → Hyprland window (the untested hop from probe 3)
- socket2 discovery by globbing `$XDG_RUNTIME_DIR/hypr/*/`, survives a Hyprland restart

## Phase 2 — The agent surface

lane-heavy · week 1–2

**Done when:** Brian's Claude Code on the desktop wakes with a warm SessionStart block from
Backstory and can `note` and `recall` without a header, an id, or a config line beyond the
installer's.

### Lane

- `backstory mcp` stdio shim → socket; five tools frozen at v0: `recall`, `note`,
  `timeline`, `confirm`, `status`
- `note`: one write, two required fields, everything else inferred
- SessionStart block: five fixed slots, budget is a user setting (default ~1,500 tokens)
- The skill file (eight lines) and the one-line `AGENTS.md` / `CLAUDE.md` stub
- Installer: MCP + hooks + skill symlinks per harness, deduped (Grok reads Claude's hooks,
  omp inherits MCP configs); each hook mutation-tested on install

### Human

- Tier A on day one: Claude Code, Codex, Copilot. Verify each hook actually fires on the
  installed version
- Hermes memory-provider plugin: Backstory as the provider (write path), `state.db` ingest
  as the safety net

## Phase 3 — Capture and backfill

lane with fixtures · week 2

**Done when:** a fresh install on an Omarchy box shows a populated "This Week" within two
minutes of the unit starting, from transcripts alone, and the timeline records commands,
windows and notifications from then on.

### Lane

- Backfill importers with fixture transcripts: Claude `projects/**/*.jsonl` (slug → path →
  repo key), Codex sessions, `opencode.db`, Pi/omp, Hermes `state.db`, Copilot events
- Ingest as inferred tier with `possibly_supersedes`: Claude auto-memory, Codex memories,
  Hermes `MEMORY.md`, claude-mem store if present
- PostToolUse capture for Tier A → observed file writes and command exits
- Bash preexec/precmd integration emitting `{cmd, cwd, exit, duration}`; a session with no
  integration records could-not-observe, never "no test ran"
- Usage record written to `~/.local/state/omarchy/agents/usage/backstory.json`

### Human

- socket2 events (focus, workspace), notification and clipboard JSON, crash → default-agent
  precedent
- Inference exclusion: Backstory's own sessions never feed the next inference

## Phase 4 — Recall, This Week, and the v1 release

mixed · week 3–4 · ships v1 = memory

**Done when:** the Q4 targets hold on a box we do not own: panel content under two minutes,
a warm session under an hour; AUR package published, marketplace submission filed (ladder
rung 1).

### Lane

- Recall engine: anchor → ordered, trust-annotated narrative at an altitude under a token
  budget (the `recall_thread` model)
- Contradiction on positive evidence only; absence is could-not-observe
- CLI: `backstory this-week`, `recall`, `timeline`, `delete` (human tombstone), `capture off`
- Per-project markdown mirror export for Claude auto-memory, Hermes `MEMORY.md`, OpenClaw
  imports
- Release discipline: `release` branch, semver tags, changelog, AUR PKGBUILD
- Project groups and workspaces (`backstory group set|clear|list`, the This Week panel's
  group editor) — decisions `bcc9fa54`, `9be5c1d5`, `f3fa04c7`; a Phase 4 addition beyond
  the original lock list, not itself a v1-scope item

### Human look

- Quickshell plugin: This Week panel, attention items, click to reopen a terminal at cwd,
  "resume in <agent>" via `omarchy agent prompt`. Theme-aware, Nerd Font glyph. Brian
  screenshots it
- Marketplace manifest, README, submission issue
- Q6 instrument decided: stars, issues answered, AUR votes, marketplace copies

## Phase 5 — Inference, opt-in

lane with a harness adapter · after v1

**Done when:** with a model chosen from the Omarchy roster, drafts appear with a confirm
chip, and the adoption number (% of sessions with at least one declared record) is visible
in `status`.

- Heuristics on user turns first; model calls only when configured
- Headless-run adapter per harness (`claude -p`, `codex exec`); the same adapter the loop
  uses
- Drafts expire or prune; a draft inferred from session S cannot be promoted by session S

## Phase 6 — The loop on a laptop

optional unit `backstory-loop` · separate package · after the sandbox is proven

**Done when:** a first user files a punch, a sandboxed worker builds it, a sandboxed critic
proves it RED/GREEN, and one notification offers [Merge] [Discard]. No merge actor, no
nightly, no push, no `browser` proof kind.

### Lane

- `punch`, `claim` and `stage` as ledger record kinds, event-sourced
- Picker, lane runner over `bwrap` (probe 1 GREEN), worker and critic adapters for Claude
  and Codex
- Gate command per enrolled repo with three-state rc
- Idle + AC gating, battery-low hook pause, pause above a usage-limit threshold

### Human

- Consent screens, each its own act: enable loop, which subscription runs workers, per-repo
  enrollment, run on battery, daily punch cap
- Click-to-run notification via `omarchy-notification-send --exec`
- Worker never sees the store

## Phase 7 — Ladder rungs 2 to 4

human-led · ongoing

- Prove on ~100 boxes we do not own; answer every issue
- Contribute upward: diagnose-crash timeline query, Agents-panel collector, warm-context
  launcher option
- One PR: install script, Install › AI row, glyph. Never before it works elsewhere

## Week one, the minimum

### By the end of week one: Brian's Claude wakes warm on his desktop from Backstory.

- Phase 0 complete: repo, gate, loop enrolled, one punch round-tripped
- Phase 1 core: schema v0, store with append-only and tiers, socket server with identity,
  unit running on the desktop
- Phase 2 core: MCP shim with `note`, `recall`, `status`; SessionStart block; installer for
  Claude Code only
- Phase 3 slice: Claude transcript backfill so the first recall is not empty

Everything else in phases 1–3 lands in week two. Codex and Copilot follow Claude; the panel
follows the CLI.

## How it runs through Mandrel and the loop

| Question | Answer |
|---|---|
| Mandrel project | Stay in `ridgetopai`, one board, tags `backstory` + `repo:backstory` + `phase:N`. A separate project splits the picker's board and Brian's digest for no gain. Revisit when a second person joins. |
| Branch spine | The plan decision (`6caaac1d`). Every punch is a member; the nightly handoff carries `phase:N` as the trunk position. |
| What the loop builds | Every lane item: pure Go with fakes for systemd, cgroups, `/proc`, `hyprctl`, and fixture transcripts. Proof kind `test`. Critic reruns RED with product reverted / GREEN applied. |
| What Ridge builds | Phase 0 by Foreman waves in worktrees. Every human item as a clause on the same punch, proven over `ssh Ridgetop-desktop` and recorded as evidence, the deploy-verify pattern. |
| What Brian does | Locks, consent-shaped decisions, and look proofs of the panel. Reads the 07:00 digest. Says "build" once. |
| Gate | `make check` in the merge actor (static). Nightly runs the same plus `-tags integration`. CI runs it on push; the CI conclusion on the merged ref is the authoritative green. |
| Punch sizing | S/M only. A punch touches one package. Anything touching ≥3 packages is split before filing. Three to four punches in flight; siblings that touch the same package are serialized by `touches[]`. |
| Context management | `PLAN.md` in the repo is the north star every worker can read; each punch cites its section. This session files punches, reads evidence, runs desktop checks, and writes a nightly handoff. Clear and reboot from the handoff whenever it gets heavy. |

### First three punches after phase 0

1. **Schema v0 and the store package.** Migrations, append-only enforcement, tier from
   identity, redaction, rate cap. Mutation probes: allow an UPDATE → RED; skip redaction →
   RED. (`SCHEMA.md`; `PLAN.md §Phase 1`)
2. **Socket server and observed identity.** `SO_PEERCRED` → `/proc` ancestry → harness
   process → project key, with a fake `/proc` tree. Mutation probe: trust a declared session
   id → RED. (`AGENT-CONTRACT.md §Observed identity`; `PLAN.md §Phase 1`)
3. **MCP shim with `note` and `status`.** Stdio to socket, tool schemas frozen and
   snapshot-tested. Mutation probe: change a schema field → the snapshot test goes RED.
   (`AGENT-CONTRACT.md §The five tools`; `PLAN.md §Phase 2`)

## Known unknowns, and where each gets proved

| Unknown | Proved in |
|---|---|
| tmux → terminal → window hop for identity | Phase 1, first time Brian has tmux attached |
| Codex and Hermes hooks actually fire on the installed versions | Phase 2 installer mutation test |
| Headless flags for harnesses other than Claude | Phase 5 adapter, Claude + Codex first |
| Omarchy default full-disk encryption | Before the README claims anything about the store |
| Tool-list caching on Codex, Copilot, Crush, Grok | Phase 2, one measurement per harness; frozen schemas make it moot if true |
| A domain for the project site | Not needed for v1. backstory.dev, .app and getbackstory.com are taken |

**Anti-drift:** every punch traces to a phase here, and every phase traces to one of the ten
locks. If a punch cannot name its phase, it is drift.
