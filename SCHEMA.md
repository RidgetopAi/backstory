# Backstory Store — Schema v0

The first punch after Phase 0
(`PLAN.md §First three punches`, punch 1) implements it as the `store` package with
migrations; the DDL below is a sketch for that punch to make concrete, and the open
questions at the end are the ones it must answer or explicitly defer. Derived from Q1
`94ba9797` (timeline / ledger / sessions), Q2 `4fb0cc2c` (provenance tiers) and
`AGENT-CONTRACT.md`. Storage is SQLite with FTS5 compiled in (L9, decision `6caaac1d`),
one file, mode 0600, owned by the `systemd --user` daemon (Q3 `5d2733c0`).

## The three tiers, in one line each

- **Timeline** (`timeline_events`) — observed fact. Only the daemon writes it. Append-only,
  ordered by sequence only. Retention is not implemented in V1: nothing prunes it, so it
  grows until the human purges sessions.
- **Sessions** (`sessions`) — derived from the timeline: one agent run, live or backfilled.
- **Ledger** (`records` + `edges`) — meaning: decisions, outcomes, handoffs, claims, and
  the loop's punch/claim/stage. Permanent, append-only, tiered by provenance.

## DDL sketch

Every `ts`-like column is `INTEGER` — unix nanoseconds, UTC — never `TEXT`. v0 stored
them as RFC3339Nano text and compared them as strings (e.g. the rate-cap window's
`... AND ts >= ?`); RFC3339Nano omits the fractional part when `ns == 0`, so a
whole-second timestamp's text sorted AFTER a timestamp a fraction of a second later
(PLAN.md §Phase 1, critic T2 on `7c4dfc90`). Migration `0002_ts_integer.sql` converts
every existing installation; the Go API is unaffected (`time.Time` in, `time.Time`
out — see `internal/store/times.go`). `edges` carries no timestamp column.

`project_key` (`projects.key`, and every `sessions.project_key` / `records.project_key`
that copies it) is `internal/project.Key`'s output: the canonical `git_common_dir` alone
(the first remote is not part of the key; see `AGENT-CONTRACT.md §Project`). Older
installs spelled it `<common-dir>|<remote>`, joined by a printable `|` — not the NUL byte v0
used, which came back
from `status` over JSON as a `\u0000` escape every agent rendered literally (critic T1 on
`14704ebe`, task `e7951178`). Migration `0003_printable_project_key_separator.sql`
rewrites every NUL-separated key already on disk; its data rewrite runs in Go
(`internal/store/migrate_data.go`), not SQL, because this build's sqlite driver
(`modernc.org/sqlite`) silently truncates `length()`/`substr()`/`REPLACE()` at an embedded
NUL byte even though `SELECT`/`INSERT`/`UPDATE` round-trip the full bytes correctly.

`note`'s optional `links[]` field (`AGENT-CONTRACT.md §note`) is not a `records` column: each
linked id becomes an `informs` edge from the linked record to the new one
(`store.EdgeInforms`), inserted in the same transaction as the record itself
(`store.InsertRecordWithEdges`) alongside any `supersedes` edge. An id that names no
existing record fails the whole write — the record and every edge, not just the bad one —
so `note` never leaves a record with a dangling `links` reference.

`records.event_cursor` is a record's position in the timeline: `MAX(timeline_events.id)` at
insert time, `0` when the timeline was empty. It is never a parameter — the socket API has
no field for it, the same way `tier` isn't (invariant 2 below) — `InsertRecordWithEdges`
computes it in the same transaction as the insert. It exists so the SessionStart delta's
membership boundary (`internal/block`) can be `EventsSinceID(handoff.EventCursor)`, a
sequence position, instead of a `ts` comparison: a backfilled event can be appended AFTER a
handoff (a higher `timeline_events.id`, genuinely later) while carrying a `ts` EARLIER than
the handoff's, because a backfilled transcript's file clock lies (invariant 10). A `ts`-based
boundary silently dropped that row (critic T1 on `7d3954f0`, task `214eb30e`). Migration
`0004_event_cursor.sql` adds the column and backfills existing rows from the nearest
preceding `timeline_events` row by `ts`, best effort: a pre-migration record was never
assigned a sequence position at insert time, so its own `ts` is the only signal left, and
that's exactly the lying-clock problem the column exists to stop relying on going forward.

`project_groups` (decision `bcc9fa54`, task `57ec6e62`) is a user-owned grouping layer over
`projects.key`, not a redefinition of project identity: identity stays observed (git common
dir only, invariant unchanged), while a group name is a label the human attaches
on top, so `project_key` is the table's primary key — a project is in at most one group,
and re-setting it moves it rather than adding a second membership. `SetProjectGroup` and
`ClearProjectGroup` (`internal/store/groups.go`) take an `Identity` and reject anything that
is not `IdentityHuman` (`ErrProjectGroupRequiresHuman`), the same pattern `TombstoneRecord`
uses for `records.tombstoned_at` (invariant 1); the socket API exposes no write path for
groups, only the CLI and panel (separate, later punches) do. `GroupOf` and `ListGroups` are
read-only and take no `Identity`. Migration `0007_project_groups.sql` adds the table; it
carries no data rewrite, since no prior schema version had anything to migrate into it.

A workspace folder has ONE spelling in the store (task `d65ef8ff`, superseding decision
`1e53165a`'s read-side alias): the pre-`79f7b20e` plain path a build once wrote for it never
coexists with its `workspace:`-prefixed key. `store.Open(path, workspaceDirs, git)` runs an
idempotent sweep in one transaction on every open: for each configured workspace dir `W`
where `project.Key(W, git, dirs) == "workspace:"+W` (a workspace dir that is itself a git
repo keeps its own repo key and is never swept), every `projects`/`sessions`/`records`/
`project_groups` row exactly matching plain `W` is merged into `"workspace:"+W` (earliest
`first_seen`, newer `project_groups.set_at` wins) and the plain row is deleted. Before the
first rewrite, `VACUUM INTO` backs up the pre-sweep file beside the database. Every store
write taking a project key (`UpsertProject`, `StartSession`, `InsertRecordWithEdges`,
`SetProjectGroup`, `ClearProjectGroup`) and every project-scoped read funnels through the
same `Store.canonicalizeProjectKey`, so a caller still spelling a workspace folder the old
way — a stale daemon that has not restarted since an upgrade, a human typing the old path —
can never mint a second row for it. Removing a workspace dir from `BACKSTORY_WORKSPACE_DIRS`
and later re-adding it recreates two spellings (any session/record written while it was
unconfigured falls back to a plain key again); the next `Open` that has it configured sweeps
those rows the same way.

```sql
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE schema_version (
  version     INTEGER NOT NULL,           -- v0 = 0
  applied_at  INTEGER NOT NULL            -- unix nanoseconds, UTC
);

CREATE TABLE projects (
  key             TEXT PRIMARY KEY,        -- git common dir (AGENT-CONTRACT §Project)
  git_common_dir  TEXT,
  remote_url      TEXT,
  toplevel        TEXT NOT NULL,           -- fallback identity for non-git dirs
  first_seen      INTEGER NOT NULL         -- unix nanoseconds, UTC
);

CREATE TABLE sessions (
  id                  TEXT PRIMARY KEY,    -- daemon-minted
  agent               TEXT NOT NULL,       -- harness binary name (claude, codex, ...)
  harness_session_id  TEXT,                -- join key from hooks; NOT the identity
  pid                 INTEGER,             -- NULL when backfilled
  cwd                 TEXT NOT NULL,       -- a timeline fact, never the project key
  project_key         TEXT REFERENCES projects(key),
  workspace           TEXT,
  window              TEXT,
  started_at          INTEGER NOT NULL,    -- unix nanoseconds, UTC
  ended_at            INTEGER,             -- unix nanoseconds, UTC
  origin              TEXT NOT NULL CHECK (origin IN ('live','backfilled')),
  exit_kind           TEXT                 -- NULL while live or unobserved
);

CREATE TABLE timeline_events (
  id          INTEGER PRIMARY KEY,         -- rowid: the ONLY ordering
  ts          INTEGER NOT NULL,            -- unix nanoseconds, UTC; wall clock, informational
  kind        TEXT NOT NULL,               -- see open questions: enum TBD
  session_id  TEXT REFERENCES sessions(id),-- NULL for OS events with no session
  source      TEXT NOT NULL,               -- posttooluse | shell | socket2 | notification | clipboard | backfill | daemon
  payload     TEXT NOT NULL,               -- JSON
  workspace   TEXT,
  window      TEXT
);
CREATE INDEX timeline_session ON timeline_events(session_id, id);
CREATE INDEX timeline_ts      ON timeline_events(ts);

CREATE TABLE records (
  id             TEXT PRIMARY KEY,
  ts             INTEGER NOT NULL,            -- unix nanoseconds, UTC
  kind           TEXT NOT NULL CHECK (kind IN
                   ('decision','outcome','handoff','note','claim','punch','stage','confirm')),
  tier           TEXT NOT NULL CHECK (tier IN ('human-declared','agent-declared','inferred')),
  text           TEXT NOT NULL,
  about          TEXT NOT NULL DEFAULT '[]',  -- JSON array of paths
  session_id     TEXT REFERENCES sessions(id),
  project_key    TEXT REFERENCES projects(key),
  evidence       TEXT NOT NULL DEFAULT '[]',  -- JSON array of timeline_events.id
  outcome        TEXT CHECK (outcome IN ('true','false','could-not-observe')), -- kind = outcome only
  promoter       TEXT,                        -- session id or 'human'; set by confirm
  expires_at     INTEGER,                     -- unix nanoseconds, UTC; claims and inferred drafts
  tombstoned_at  INTEGER,                     -- unix nanoseconds, UTC; human-only, the sole mutable column
  event_cursor   INTEGER NOT NULL DEFAULT 0   -- MAX(timeline_events.id) at insert time; never a parameter
);
CREATE INDEX records_project ON records(project_key, ts);
CREATE INDEX records_session ON records(session_id);

CREATE TABLE edges (
  from_id      TEXT NOT NULL REFERENCES records(id),
  to_id        TEXT NOT NULL REFERENCES records(id),
  type         TEXT NOT NULL CHECK (type IN
                 ('supersedes','possibly_supersedes','informs','caused',
                  'produced_outcome','contradicts')),
  declared_by  TEXT NOT NULL,               -- session id, 'human', or 'daemon'
  PRIMARY KEY (from_id, to_id, type)
);

CREATE TABLE settings (
  key    TEXT PRIMARY KEY,                  -- reserved names (budget, capture_on, inference_model, retention_days, ...); V1 reads the SessionStart budget and capture pauses from here, and no command writes the budget
  value  TEXT NOT NULL
);

CREATE TABLE project_groups (
  project_key  TEXT PRIMARY KEY REFERENCES projects(key), -- a project is in at most one group
  group_name   TEXT NOT NULL,
  set_at       INTEGER NOT NULL           -- unix nanoseconds, UTC; last set/move time
);
CREATE INDEX project_groups_name ON project_groups(group_name);

CREATE VIRTUAL TABLE records_fts USING fts5(
  text, content='records', content_rowid='rowid'
);
-- insert-only sync trigger; no UPDATE/DELETE triggers exist because none are permitted
CREATE TRIGGER records_ai AFTER INSERT ON records BEGIN
  INSERT INTO records_fts(rowid, text) VALUES (new.rowid, new.text);
END;
```

## Invariants the store enforces

These are enforced in the `store` package and, where SQLite can express them, in the DDL.
Each carries a mutation probe in the first punch: allow the forbidden write → RED;
unmutated → GREEN.

1. **No UPDATE, no DELETE on `records` or `timeline_events`.** Supersession is a new record
   plus a `supersedes` edge; the old record stays. The one mutable column is
   `records.tombstoned_at`, writable only from the human path (CLI/panel). Tombstoning
   *forgets*: in one transaction `TombstoneRecord` removes the text from `records_fts` (FTS5
   external-content `'delete'`), sets `tombstoned_at` and scrubs `records.text` to `''`; the row
   and its edges remain. `records_no_update` permits only that one UPDATE (tombstoned_at
   NULL→value, optionally with text→`''`) and refuses every other column change — including
   `event_cursor` and `git_head` — and any re-tombstone. Enforced by triggers that
   `RAISE(ABORT)` on any other UPDATE and on every DELETE. The one exception to the
   no-DELETE rule is the human `purge` (invariant 9): `PurgeSessions` deletes a session's
   `timeline_events` inside one transaction that drops and recreates `timeline_events_no_delete`
   from its single definition.
2. **Tier is never a parameter.** `records.tier` is set by the daemon from `SO_PEERCRED`
   identity: an agent process → `agent-declared`; inference → `inferred`; the CLI/panel →
   `human-declared`. The socket API has no field for it.
3. **Promotion records the promoter and caps at the promoter's tier.** `confirm` by an agent
   yields `agent-declared`; a draft inferred from session S cannot be promoted by session S.
4. **Only the daemon writes `timeline_events`** (and only the human `purge` deletes from it).
   The socket API exposes no event write and no purge.
5. **Rate and size caps** per session (numbers open below); a rejected write is surfaced in
   `status`.
6. **Redaction on write.** Token/key patterns are redacted from `records.text` and
   `timeline_events.payload` before insert; the raw value is never stored.
7. **Absence is not evidence.** `could-not-observe` is a value, never folded into `false`
   or `0` (V1 applies it to `session.git_state`; nothing writes `records.outcome`); the
   daemon mints no `contradicts` edge itself — one exists only when `confirm` files it with
   evidence (`AGENT-CONTRACT.md §Outcomes`).
8. **Capture-off flag file honoured** on every write path: while set, no events and no
   records are inserted.
9. **Retention is not implemented in V1.** Nothing prunes `timeline_events` by age; the
   timeline grows until the human deletes from it. The human `backstory purge` is the only
   delete: it erases whole sessions' events,
   stamps `sessions.purged_at` (backfill never re-imports a purged session) and appends a
   counts-only `purge_log` row (`ts, scope, sessions, events, records`); a project purge also
   tombstones the in-scope records written in its window (human Delete path). Session rows, `backfill_cursors`
   and `records` stay; record evidence / `event_cursor` ids may dangle and readers tolerate it.
   `records` are never pruned in V1: an expired `claim` or `inferred` draft stays and is
   merely shown as expired.
10. **Ordering is by sequence.** Recall and the SessionStart delta order events by
    `timeline_events.id`, never by `ts`; backfilled sessions carry file clocks that lie. The
    delta's membership boundary is the same sequence, not a clock reading: it is
    `EventsSinceID(handoff.EventCursor)`, where `records.event_cursor` is the handoff's own
    position in that sequence at insert time, never a comparison against `handoff.ts`.

## Event payload kinds

`timeline_events.kind` is free text in v0 (open question below), but every writer and
reader in this codebase agrees on the shape of `payload` for the four kinds actually in
use — `internal/payload` defines the Go types, and a test in that package (task
`8ba5487a`) fails if `internal/block` or `internal/backfill/claude` declares a private
json-tagged payload struct of its own instead of importing them. Before this package
existed, the backfill importer wrote a tool's file path under `detail` while the block's
delta slot read `path` — two independent structs, silently disagreeing, so a project with
thousands of tool events rendered "0 files touched".

| kind               | Go type                     | fields                                                  |
|--------------------|------------------------------|----------------------------------------------------------|
| `session.start`    | `payload.SessionStart`      | `prompt`, `version?`, `git_branch?`                       |
| `session.end`      | `payload.SessionEnd`        | `reason?`                                                 |
| `session.git_state`| `payload.SessionGitState`   | `branch?`, `uncommitted_count?`, `could_not_observe?`     |
| `tool.use`         | `payload.ToolUse`           | `tool_use_id?`, `name`, `path?` (file tools), `command?` (Bash) |
| `tool.result`      | `payload.ToolResult`        | `tool_use_id?`, `is_error?`, `exit?`, `content?`, `interrupted?` |

`tool.result.content` is a bounded, redacted excerpt of the tool output (head + tail, elision marker, at most `payload.ToolOutputExcerptMaxRunes` runes; redaction runs over the FULL output before the cut). A live PostToolUse `tool.result` with no outcome is enriched in place by backfill (`is_error`, `exit` parsed from a leading `Exit code <N>` line, `content`) — the one sanctioned update of `timeline_events`. `interrupted` is additive.

`session.git_state` is written only by the daemon's own live `EndSession` path
(`internal/mcp/daemon.go`'s `recordSessionEndGitState`, task `c2573b35`) — the observed
`git status --porcelain` state of the session's cwd at the moment a live session ends, so
This Week's Attention can flag "ended with uncommitted changes" on positive evidence. A
backfilled session never gets one: the importer replays a transcript that already ended,
with no live cwd left to observe. `could_not_observe` is `true`, with `branch` and
`uncommitted_count` both absent, when git failed or cwd was not a working tree — a writer
must never record `uncommitted_count: 0` in that case (invariant 7 below).

`tool.use.path` is set for the file-editing tools (Edit/Write/Read/MultiEdit/NotebookEdit);
`command` is set for Bash; a tool outside both groups (Grep, Glob, WebFetch, ...) carries
neither. `tool.result.exit` is nil unless the writer observed a real process exit code. A
Bash `tool_result` replayed from a Claude transcript carries one only when its text begins
with an `Exit code <N>` line (the backfill parses that line, as described above); otherwise
it stays nil, and a writer must never invent `0`. Live PostToolUse capture and the bash
integration record the real exit. The SessionStart delta's "last exit codes" is empty only
for history where no exit was observed or parsed.

`payload.MutatingFileTools` (Edit/Write/MultiEdit/NotebookEdit — NOT Read) is the one
definition, in this codebase, of "changed a file." The SessionStart delta's "files
touched" figure counts only distinct `tool.use.path` values from these tools: a Read
populates `path` too and remains a first-class event, but it did not change anything, so
it does not count. Counting every path-carrying tool.use there (task 393d174c) overstated
the figure 4x on real history — 411 distinct paths opened against 105 files actually
changed — because an agent reads far more files than it edits.

**Changing a payload's shape requires a migration, not just a code change.** Before
`internal/payload` existed, the Claude backfill wrote a tool.use file path under `detail`;
after task `8ba5487a` unified every writer and reader onto `payload.ToolUse`'s `path`
field, every `timeline_events` row already on disk still carried the old key, and the
reader silently found nothing — not an error, a delta that read "0 files touched" on a
correct, deployed fix (task `e96d1a21`, Brian's desktop, 2026-09-22). `timeline_events` is
append-only and has no reader-side fallback between shapes, so **a payload shape change is
a schema change**: it needs its own numbered migration (`internal/store/migrations/`) that
bumps `SchemaVersion`, plus a Go-side path (`migrationDataHooks` in `migrate.go`) that
rewrites — never deletes — every row already written in the old shape, exactly the way
migration `0006_tool_use_payload_shape.sql` rewrites `detail` into `path`/`command`. Not
every store on disk was ever migrated to the shape a payload change assumes; a code review
of a payload struct's fields should ask what migrates the rows that predate it.

## Reserved for the loop (Q7 `c8f9d7a9`)

`punch`, `stage` and `claim` are record kinds from v0 so `PLAN.md §Phase 6` needs no
migration: a punch is a record; each stage transition is a `stage` record with a `caused`
edge back to the punch; a lane's lease is a `claim` with `expires_at`. Event-sourced, never
a mutable task row.

## Open questions (the first punch answers or defers each, in its report)

- **Retention numbers.** Days for `timeline_events`; whether `source = backfill` events get
  a separate bound.
- **Draft expiry.** `expires_at` default for `inferred` drafts and for `claim`s; prune
  cadence.
- **Event kinds enum.** `timeline_events.kind` is free text in v0; the enum is fixed once
  Phase 3 capture sources exist (file write, command exit, focus, workspace, notification,
  clipboard, session start/end).
- **Rate/size cap values** and whether they are `settings` or compile-time.
- **`about[]` normalisation.** Paths relative to `projects.toplevel` or absolute.
- **FTS over `payload`.** v0 indexes `records.text` only; whether timeline payloads join the
  index is a Phase 4 recall question.
