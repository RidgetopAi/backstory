# Backstory — The Agent Contract

What an agent gets from Backstory, what it may write, and what the daemon will never let it
do. Every Backstory write happens in no-ask mode (Omarchy launches every agent with its
don't-ask flag), so nothing here is a prompt or a courtesy: the rules are the daemon's,
encoded, and the skill text only repeats them.

Source: Tesla's second opinion on the seven locks, Mandrel context `f9ccc4cb`
(`reports/omarchy-memory/research-2026-09-21/tesla-second-opinion.md`, §1.1–1.10, §4, §5).
The load-bearing guesses in it were probed on Brian's desktop the same night, Mandrel
context `255a4989`:

| Probe | Result |
|---|---|
| 1 · `bwrap` / unprivileged user namespaces for a `systemd --user` unit | GREEN |
| 2 · `systemd --user` sandbox directives apply without root | GREEN |
| 3 · `SO_PEERCRED` → `/proc` ancestry → Hyprland window | GREEN (direct); **tmux hop untested** — proved in `PLAN.md §Phase 1` |

Locks this page sits under: Q1 `94ba9797` (timeline/ledger tiers, sessions), Q2 `4fb0cc2c`
(provenance tiers, inference opt-in), Q3 `5d2733c0` (user daemon is home), Q4 `5f0d2107`
(warm SessionStart). Build locks L8–L10 in `6caaac1d`.

## Observed identity — never declared

An agent never tells Backstory who it is. The daemon observes it:

```
harness ── spawns ──▶ `backstory mcp` (stdio shim)
                         │ unix socket
                         ▼
                      daemon reads SO_PEERCRED (pid, uid)
                         │ walks /proc/<pid>/status PPid upward
                         ▼
                      first known harness binary = the session's process
                         │ reads its cwd, start time
                         │ `hyprctl clients -j` by pid → window, workspace
                         ▼
                      session · project · window   (timeline-tier facts)
```

- **No headers. No id parameters. HTTP is dropped** from Q3: localhost HTTP shows the daemon
  one TCP connection from 127.0.0.1 and nothing else, which is the Mandrel shared-slot bug
  reborn. Stdio keeps the caller for free because each harness spawns the shim as its own
  child.
- The harness `session_id` from hooks is a **join key** recorded on the session, not the
  session's identity.
- Subagents share the parent process and therefore the parent session, with an optional
  declared `actor` label.
- "User" is the uid: the daemon is `systemd --user` and the store is under that user's
  data dir. Multi-user is not designed into v1.
- Identity is an observation in the timeline tier — the same epistemics Q1/Q2 chose. The
  provenance tiers are only as trustworthy as the attribution under them.

## Project = git repository identity

Every recall is "for this project", and a project is **not** a cwd string. A worktree, a
subdirectory and Claude's dashed-cwd transcript slug are all the same repository.

- `project_key` = the canonical `git rev-parse --git-common-dir` **alone** (worktree-safe:
  a worktree and its main checkout share it; two clones of one remote at different paths
  stay two projects). The remote is *not* part of the key, so `git remote add`, `set-url`
  and removal never split a repo's history (task `029485ae`, shape (a)). A non-git
  directory's key is its own path. `store.Open` idempotently merges pre-existing rows
  spelled `<common-dir>|<remote>` or by the remote-less toplevel path onto the common-dir
  key (backup first, one transaction, `sweepOneTarget`), and `canonicalizeProjectKey`
  resolves the old spellings so nothing mints a second row.
- Sessions and ledger records carry `project_key`. **cwd is a timeline fact**, kept on the
  session and the events, never the key.
- Backfill normalises Claude's slug back to a path and then to a repo key.
- The "two agents in one repo" warning fires across worktrees because the key is shared.
- **A workspace is not a project** (decision `bcc9fa54`): a folder that holds many repos
  (default `~/projects`, list-valued, read from configuration — never hard-coded) must not
  become a project of its own or be merged into some repo's history. A cwd that is a
  workspace dir itself, or a non-git dir directly under one, resolves to a `workspace:`-
  prefixed identity, distinct from any repo key. A cwd inside a git repo under a workspace
  dir still resolves to that repo's own key, unchanged.
- **A note follows the repo the session edited.** A session launched at a workspace dir
  keeps the workspace key, but a non-handoff `note` it writes is filed under the single
  repo it edited (else the single repo its `about[]` names), so that repo's `recall` finds
  it. Two or more repos, or none, keep the workspace key; handoffs never move (decision
  `f3fa04c7`).

## Records carry `about[]`

"What's the backstory on this file" is the query the name sells. Q1's anchors (time,
session, window) do not include a file, so every ledger record carries `about: [paths]` —
declared by the agent, or inferred from the session's observed file writes (PostToolUse
payloads) or from `git diff` of the session's branch. It is in the v0 schema
(`SCHEMA.md §records`) because retrofitting anchors is the expensive kind of migration.

## The five tools

Flat, frozen at v0, additive-only thereafter. Small because every tool costs every OpenCode
session tokens; stable because Hermes and the Pi adapter cache tool metadata on disk and a
schema change is invisible to them until the cache refreshes. No `discover`; the
descriptions carry the contract. No delete. No settings.

| Tool | Contract |
|---|---|
| `recall` | anchor (project, path, ref, id, or free text) → ordered, trust-annotated narrative at an altitude, under a token budget; the `recall_thread` model |
| `note` | the one write; returns the record id and its provenance tier |
| `timeline` | events for my session / this project / since `<t>`, filtered — the observed truth an agent cites as evidence |
| `confirm` | promote a draft, flag a contradiction with evidence, mark supersession, or affirm a target still true (`affirm`) — kept separate from `note` so the never-list is enforceable per tool |
| `status` | who I am (session, project as the daemon sees them), who else is live here, my budget, capture on/off |

Schemas are snapshot-tested; changing a field turns the snapshot RED (`PLAN.md §First three
punches`, punch 3).

## `note` — one write, two required fields

```
note({ kind: decision | outcome | handoff | note | claim,  text })
```

- **Required:** `kind`, `text`. Nothing else. Agents in no-ask mode skip anything with five
  required fields; Mandrel's `decision_record` needed five including two enums, and that is
  why.
- **Optional:** `about[]`, `supersedes`, `evidence[]` (timeline event ids), `links`,
  `expires` (claims), `next` (handoff only: the single next step — one line, at most 200
  characters; on any other kind, with a newline, or over 200 characters the note is refused
  with an error naming the rule and nothing is written).
- **Inferred by the daemon:** session, agent, project, time, window. `project` is the
  session's own key, with one exception: a non-handoff note (decision, outcome, note,
  claim) from a session whose key is a `workspace:` key is filed under the git repo that
  session edited, when its mutating-file-tool events (the same extraction This Week uses)
  resolve to exactly one location and that location is a git repo; a session that edited
  nothing applies the same rule to the note's `about[]` paths. Zero or several locations,
  or only non-git folders, keep the workspace key. Handoffs always stay homed on the
  workspace. The result's additive `project_key` field reports where the record landed.
- **Tier is set by the daemon from the caller's identity, never from a parameter.** A note
  from an agent process is `agent-declared`. `human-declared` is reachable only from the
  panel/CLI.
- Adoption is measured from day one: **% of sessions with ≥1 declared record** is the
  number that says whether the skill works. It is visible in `status` (`PLAN.md §Phase 5`).

## Outcomes are three-state; contradiction only on positive evidence

Q2's own example — "the agent writes 'tests green' but the timeline shows no test process
ran → flagged" — commits the C1 three-state bug. Absence of an event is not evidence of
absence: a test run inside a container, over ssh, or from a harness without PostToolUse is
invisible to the daemon. A flag that is often wrong trains the user to ignore the flags
that are right.

- An outcome is **`true`**, **`false`**, or **`could-not-observe`**. The third is its own
  state with its own value, never folded into `false`.
- The daemon **contradicts only on positive evidence**: a test process ran and exited
  non-zero; a deploy exited 1. Absence is `could-not-observe`, never a contradiction.
- Observed outcomes come from PostToolUse capture (Tier A: Claude Code, Codex, Copilot) and
  from the bash preexec/precmd integration emitting `{cmd, cwd, exit, duration}`
  (`PLAN.md §Phase 3`). Until both exist for a session, the daemon records
  `could-not-observe`, never "no test ran".
- The agent's side of proof is `note({kind:'outcome', evidence:[event_id]})`; `timeline`
  exists so it can cite the id. A claim without evidence is recorded as a claim.

## Claims are advisory, expire, and are never enforced

`note({kind:'claim', about:[repo | paths], expires})` is an advisory lease — the loop's
`task_claim`, generalised. It is shown to every later SessionStart (slot 3) and to
`UserPromptSubmit` where a harness injects there. It is **never enforced**: enforcement
would make the memory layer a lock manager. Claims carry `expires_at` and lapse on their
own.

Cross-agent handoff is harness-neutral prose (no `/compact`, no Claude paths). "Resume in
<agent>" is `omarchy agent prompt "<handoff text>"` — the crash precedent; same-agent
resume is reopening a terminal at the session's cwd.

## The SessionStart block

Opens with **one header line**, ahead of every slot below, that names Backstory as the
source and says the block is already loaded — so the agent reading it can tell it came
from Backstory and can obey the next paragraph's "do not re-fetch it" without help. Before
this line existed, nothing in the block named its source: measured on Brian's desktop
(2026-09-23, binary `0e14cad`), an agent that received `Delta: 50 sessions, 105 files
touched` followed by the block's own final line could not tell it came from Backstory and
offered to call `recall` to fetch it — the exact re-fetch the next rule forbids. The header counts
against the budget below like every slot, but is the **last thing dropped, never the
first**: at a budget too small to fit any slot, the header is what survives.

Then five fixed slots, in order, each optional, under a **user-set budget** (default
~1,500 tokens, per-agent override). The budget is not the agent's choice and not
hard-coded; the industry range is narrow (Hermes ~2,200 chars, omp 5,000 tokens, Claude
auto-memory 200 lines / 25 KB).

1. **Resume pointer** — the latest `handoff` for THIS project, rendered as `Resume: (id
   <id>) <text>` so the id is available verbatim for `note`'s `supersedes`, its MODE line
   if present, and a `⚠ possibly stale: ...` marker naming its reasons and evidence ids
   when later positive evidence — a contradiction, a later record or timeline event
   touching a path it names — flags it and no later `confirm affirm` on it has arrived
   since (decision bcc9fa54)
2. **Delta** — timeline since that handoff, compressed to "N sessions, files touched, last
   exit codes"
3. **Coordination** — "another session is live in this repo on branch X" (pid alive in
   `/proc`, branch known)
4. **Attention** — unconfirmed drafts count, flagged contradictions, and whether the Resume
   handoff is possibly stale — cleared by `confirm affirm`
5. **One line** — "call recall only if you need more than this block"

The header plus these five slots are the whole of it. Harnesses with no SessionStart
(Antigravity) or no injection on passive hooks (Grok) get the block by the skill telling
the agent to call `recall` once.

**If the block is present, do not re-fetch it.** Measured on Ridge (context `206f0638`):
202/202 boot re-reads returned zero fresh tokens.

## The skill — eight lines, verbatim

One file, harness-neutral, symlinked by the same loop Omarchy uses for its own skills.
Tesla §1.9 counts eight lines: the `description:` line plus the seven body lines.

1. `description:` claims the intents — "what was I doing", "resume", "why did we",
   "backstory", "history", "last session".
2. if a SessionStart block is present, do not re-fetch; else call `recall` for this
   project once; if the Resume line carries a possibly-stale marker, verify it before
   acting and `confirm affirm` it if still true.
3. before changing a file whose recall shows a declared decision, read it.
4. when you choose between alternatives, `note decision` in one line — no ceremony.
5. claim "done" only with `note outcome` pointing at a `timeline` event id; a claim without
   evidence is recorded as a claim.
6. end with `note handoff`: what is true now, what is next, what not to do. Put the single next step in the handoff's optional `next` (one line, at most 200 characters; handoff only) so the panel can show it. Set
   `supersedes` to the Resume slot's id when it showed one; whenever any record replaces or
   corrects an earlier one, set `supersedes` to its id (or `confirm supersede`).
7. inferred records are hints; declared records are claims; the timeline is fact.
8. never put a secret in a note.

The `AGENTS.md` / `CLAUDE.md` stub is **one line** pointing at the skill and the tool. 8/10
harnesses read a global instruction file; Crush reads `~/.config/AGENTS.md` and OpenCode
reads `~/.claude/CLAUDE.md`, so one file serves several.

## The never-list — daemon-enforced invariants

Every write is unattended by construction, so the protection is the daemon's rules, not
the skill's prose (CLASSES.md: prefer the gate). Each item is a store or socket invariant
with a mutation probe in the store package (`PLAN.md §First three punches`).

1. **Never delete or edit any record.** Writes are append-only; supersession is a new
   record plus an edge; the old one stays.
2. **Never write a record at a tier above `agent-declared`.** The tier comes from
   `SO_PEERCRED` identity, not from a parameter. A human tier is reachable only from the
   panel/CLI.
3. **Never promote a draft above `agent-declared`.** The promoter is recorded; an agent
   promotion yields `agent-declared`; a draft inferred from session S cannot be promoted by
   session S.
4. **Never write to the timeline.** "I ran the tests" is a note; the daemon alone mints
   events.
5. **Never change settings:** inference model, retention, budget, capture on/off, repo
   enrollment.
6. **Never exceed the write rate/size cap.** An agent in a loop can write ten thousand
   notes in an hour; the cap is enforced and surfaced in `status`.
7. **Never store a secret.** Daemon-side redaction on write (token/key patterns), because
   the ledger is permanent while Claude sweeps transcripts after 30 days — Backstory would
   otherwise extend the life of everything in a transcript indefinitely.
8. **Never read another user's store.** Moot on a single-user laptop; kept moot by design.

## User-only powers

Four things only the human can do, through the panel or CLI, never through a tool:

- **Pause capture** — an `omarchy toggle`-style flag file, the `crash-capture-off`
  precedent. The daemon honours it on every write path.
- **Delete** — a human-only tombstone (`tombstoned_at`) that scrubs the record's text from
  `records.text` and the FTS index (the row and its edges remain). A permanent ledger without a human
  delete is a privacy product that cannot forget.
- **Purge (Forget)** — `backstory purge (--session ID | --project KEY [--location DIR] [--since T]
  [--until T]) [--dry-run] [--yes]` forgets activity by whole session AND the saved records
  written in the window: the sessions' timeline events are deleted, each session is stamped
  `purged_at` so backfill never re-imports it, every non-tombstoned in-scope record with
  `ts` in the window is tombstoned and text-scrubbed (the human Delete's own path, same
  transaction), and a counts-only `purge_log` row (sessions, events, records) is written. A
  `--session` purge forgets no records. Refuses without `--yes` on a non-TTY. No tool or socket
  call can purge.
- **Memory and Forget act on the row** — `backstory records --location DIR` and `purge --location
  DIR` scope to exactly the sessions and records This Week attributes to the row whose cwd is
  DIR (one function, `week.LocationScope`, over the same work-location labels the rows use): a
  repo row is its repo key plus workspace records/sessions attributed to that folder; a label
  row (non-git folder under a workspace) only what is attributed to that label; the workspace
  root row only what is attributed to the root, not its child labels. The panel always passes
  the row's own cwd. A session touching several rows' files is attributed to each, so forgetting
  one row forgets that whole session.
- **Edit (supersede)** — `backstory edit <id>` writes a NEW human-declared record (same kind,
  project, about) with a `supersedes` edge to the old one, via `$VISUAL`/`$EDITOR`, `--file`,
  or `--stdin`. The old record is never updated (append-only); a deleted or already-superseded
  record cannot be edited.

## Interfaces adopted, not competed with

- **Hermes memory-provider API — implement it.** A provider plugin in `~/.hermes/plugins/`
  talking to the daemon socket is the write path; ingesting `~/.hermes/state.db` is the
  safety net. Do both. Hermes runs one provider at a time; its lifecycle hooks are
  mutation-tested on the installed version.
- **`AGENTS.md` — expose the convention, do not own it.** One line per global instruction
  file pointing at the skill and the tool; adopt `~/.agents/{skills,mcp.json}` as the
  generic location. No "Backstory AGENTS.md standard".
- **claude-mem — read its store, never write its format.** If `~/.claude-mem/` exists,
  ingest it as an inferred-tier source. Do not compete for Claude-session memory; absorb it.
- **Markdown mirror export.** A daemon-written, per-project `MEMORY.md`-shaped export that
  Claude auto-memory (relocatable `autoMemoryDirectory`), Hermes `MEMORY.md` and OpenClaw
  `memory/imports/` load natively — warm context even where hooks are inert.
- **Ingest as inferred tier with `possibly_supersedes`:** Claude auto-memory, Codex
  `~/.codex/memories/`, omp local memory, Hermes `MEMORY.md`/`USER.md`. Copilot Memory is
  server-side; nothing to do.
- **Omarchy's own contracts:** a usage record at
  `~/.local/state/omarchy/agents/usage/backstory.json` (the Agents panel picks up any
  record); `omarchy-notification-send --exec <argv>` for click-to-run; the six-symlink skill
  loop; `omarchy-menu.jsonc` rows; `omarchy toggle` flag files for capture-off.
- **Not adopted:** screenpipe (pixels, not intent); Infomarchy is a partner — read its usage
  cache, offer it the socket, do not rebuild `/proc` scanning in the panel.
