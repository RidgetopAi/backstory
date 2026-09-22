# Backstory Store — Schema v0 (DRAFT)

**Status: DRAFT.** Nothing here is implemented. The first punch after Phase 0
(`PLAN.md §First three punches`, punch 1) implements it as the `store` package with
migrations; the DDL below is a sketch for that punch to make concrete, and the open
questions at the end are the ones it must answer or explicitly defer. Derived from Q1
`94ba9797` (timeline / ledger / sessions), Q2 `4fb0cc2c` (provenance tiers) and
`AGENT-CONTRACT.md`. Storage is SQLite with FTS5 compiled in (L9, decision `6caaac1d`),
one file, mode 0600, owned by the `systemd --user` daemon (Q3 `5d2733c0`).

## The three tiers, in one line each

- **Timeline** (`timeline_events`) — observed fact. Only the daemon writes it. Append-only,
  retention-bounded, ordered by sequence only.
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
that copies it) is `internal/project.Key`'s output: `git_common_dir` and the first remote
URL joined with `|`, a printable separator — not the NUL byte v0 used, which came back
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

```sql
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE schema_version (
  version     INTEGER NOT NULL,           -- v0 = 0
  applied_at  INTEGER NOT NULL            -- unix nanoseconds, UTC
);

CREATE TABLE projects (
  key             TEXT PRIMARY KEY,        -- git common dir + first remote (AGENT-CONTRACT §Project)
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
  tombstoned_at  INTEGER                      -- unix nanoseconds, UTC; human-only, the sole mutable column
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
  key    TEXT PRIMARY KEY,                  -- budget, capture_on, inference_model, retention_days, ...
  value  TEXT NOT NULL
);

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
   `records.tombstoned_at`, writable only from the human path (CLI/panel), and a tombstoned
   record still exists (recall omits its text, keeps its edges). Enforced by triggers that
   `RAISE(ABORT)` on any other UPDATE and on every DELETE.
2. **Tier is never a parameter.** `records.tier` is set by the daemon from `SO_PEERCRED`
   identity: an agent process → `agent-declared`; inference → `inferred`; the CLI/panel →
   `human-declared`. The socket API has no field for it.
3. **Promotion records the promoter and caps at the promoter's tier.** `confirm` by an agent
   yields `agent-declared`; a draft inferred from session S cannot be promoted by session S.
4. **Only the daemon writes `timeline_events`.** The socket API exposes no event write.
5. **Rate and size caps** per session (numbers open below); a rejected write is surfaced in
   `status`.
6. **Redaction on write.** Token/key patterns are redacted from `records.text` and
   `timeline_events.payload` before insert; the raw value is never stored.
7. **`outcome` is three-state.** `could-not-observe` is a value, never folded into `false`;
   a `contradicts` edge is minted only on positive evidence (`AGENT-CONTRACT.md §Outcomes`).
8. **Capture-off flag file honoured** on every write path: while set, no events and no
   records are inserted.
9. **Retention bounds the timeline only.** `timeline_events` are pruned by age;
   `records` are never pruned except expired `claim`s and expired `inferred` drafts
   (`expires_at`), which are pruned, not tombstoned.
10. **Ordering is by sequence.** Recall and the SessionStart delta order events by
    `timeline_events.id`, never by `ts`; backfilled sessions carry file clocks that lie.

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
