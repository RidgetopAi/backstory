-- 0002_ts_integer.sql — timestamps as INTEGER unix nanoseconds (UTC).
--
-- v0 stored every ts-like column as RFC3339Nano TEXT and compared it as a
-- string (e.g. the rate-cap window's `... AND ts >= ?`). RFC3339Nano omits
-- the fractional part when ns == 0, so a whole-second timestamp's text
-- sorts AFTER a timestamp 100ms later as a string ("...22Z" > "...22.1Z")
-- even though it is earlier as a time (PLAN.md §Phase 1, critic T2 on
-- 7c4dfc90). Every ts-like column becomes INTEGER (unix nanoseconds, UTC),
-- so every comparison and ORDER BY is numeric from here on. `edges` carries
-- no timestamp column and is untouched.
--
-- The store is pre-alpha (no installed base to preserve): this migration is
-- a DESTRUCTIVE rewrite of the affected tables. Existing TEXT timestamps are
-- reparsed with sqlite's strftime('%s', ...), which truncates to whole
-- seconds — every row is kept (row counts are preserved), only the
-- sub-second precision of any pre-migration timestamp is lost.
--
-- SQLite has no ALTER COLUMN TYPE, so every table with a ts-like column is
-- recreated (create-copy-drop-rename), parents before children. The
-- `records` and `timeline_events` rowids are preserved explicitly:
-- `records_fts` is an external-content FTS5 index keyed by `records.rowid`,
-- and `timeline_events.id` (the sequence SCHEMA.md invariant 10 orders by)
-- is itself the rowid. Foreign-key enforcement is suspended for the
-- duration of this migration by store.applyMigration, which also runs
-- `PRAGMA foreign_key_check` before commit to verify nothing is left
-- dangling by the out-of-order recreation.

-- schema_version.applied_at. store.Open bootstraps this table (outside the
-- migrations/ embed, before migration 0001 runs on a brand-new database)
-- already typed INTEGER, so a fresh database's migration-0001 row is
-- already nanoseconds here; an existing database migrating from v1 still
-- has the original RFC3339Nano TEXT. typeof() tells the two apart.
CREATE TABLE schema_version_new (
  version    INTEGER NOT NULL,
  applied_at INTEGER NOT NULL
);
INSERT INTO schema_version_new (version, applied_at)
  SELECT version,
         CASE WHEN typeof(applied_at) IN ('integer', 'real') THEN CAST(applied_at AS INTEGER)
              ELSE CAST(strftime('%s', applied_at) AS INTEGER) * 1000000000 END
  FROM schema_version;
DROP TABLE schema_version;
ALTER TABLE schema_version_new RENAME TO schema_version;

-- projects.first_seen
CREATE TABLE projects_new (
  key             TEXT PRIMARY KEY,
  git_common_dir  TEXT,
  remote_url      TEXT,
  toplevel        TEXT NOT NULL,
  first_seen      INTEGER NOT NULL
);
INSERT INTO projects_new (key, git_common_dir, remote_url, toplevel, first_seen)
  SELECT key, git_common_dir, remote_url, toplevel,
         CAST(strftime('%s', first_seen) AS INTEGER) * 1000000000
  FROM projects;
DROP TABLE projects;
ALTER TABLE projects_new RENAME TO projects;

-- sessions.started_at, sessions.ended_at
CREATE TABLE sessions_new (
  id                  TEXT PRIMARY KEY,
  agent               TEXT NOT NULL,
  harness_session_id  TEXT,
  pid                 INTEGER,
  cwd                 TEXT NOT NULL,
  project_key         TEXT REFERENCES projects(key),
  workspace           TEXT,
  window              TEXT,
  started_at          INTEGER NOT NULL,
  ended_at            INTEGER,
  origin              TEXT NOT NULL CHECK (origin IN ('live','backfilled')),
  exit_kind           TEXT
);
INSERT INTO sessions_new (id, agent, harness_session_id, pid, cwd, project_key, workspace, window, started_at, ended_at, origin, exit_kind)
  SELECT id, agent, harness_session_id, pid, cwd, project_key, workspace, window,
         CAST(strftime('%s', started_at) AS INTEGER) * 1000000000,
         CASE WHEN ended_at IS NULL THEN NULL ELSE CAST(strftime('%s', ended_at) AS INTEGER) * 1000000000 END,
         origin, exit_kind
  FROM sessions;
DROP TABLE sessions;
ALTER TABLE sessions_new RENAME TO sessions;

-- timeline_events.ts
CREATE TABLE timeline_events_new (
  id          INTEGER PRIMARY KEY,
  ts          INTEGER NOT NULL,
  kind        TEXT NOT NULL,
  session_id  TEXT REFERENCES sessions(id),
  source      TEXT NOT NULL,
  payload     TEXT NOT NULL,
  workspace   TEXT,
  window      TEXT
);
INSERT INTO timeline_events_new (id, ts, kind, session_id, source, payload, workspace, window)
  SELECT id, CAST(strftime('%s', ts) AS INTEGER) * 1000000000, kind, session_id, source, payload, workspace, window
  FROM timeline_events;
DROP TABLE timeline_events;
ALTER TABLE timeline_events_new RENAME TO timeline_events;
CREATE INDEX timeline_session ON timeline_events(session_id, id);
CREATE INDEX timeline_ts      ON timeline_events(ts);

CREATE TRIGGER timeline_events_no_update
BEFORE UPDATE ON timeline_events
BEGIN
  SELECT RAISE(ABORT, 'timeline_events: append-only, no update');
END;

CREATE TRIGGER timeline_events_no_delete
BEFORE DELETE ON timeline_events
BEGIN
  SELECT RAISE(ABORT, 'timeline_events: append-only, no delete');
END;

-- records.ts, records.expires_at, records.tombstoned_at
CREATE TABLE records_new (
  id             TEXT PRIMARY KEY,
  ts             INTEGER NOT NULL,
  kind           TEXT NOT NULL CHECK (kind IN
                   ('decision','outcome','handoff','note','claim','punch','stage','confirm')),
  tier           TEXT NOT NULL CHECK (tier IN ('human-declared','agent-declared','inferred')),
  text           TEXT NOT NULL,
  about          TEXT NOT NULL DEFAULT '[]',
  session_id     TEXT REFERENCES sessions(id),
  project_key    TEXT REFERENCES projects(key),
  evidence       TEXT NOT NULL DEFAULT '[]',
  outcome        TEXT CHECK (outcome IN ('true','false','could-not-observe')),
  promoter       TEXT,
  expires_at     INTEGER,
  tombstoned_at  INTEGER
);
INSERT INTO records_new (rowid, id, ts, kind, tier, text, about, session_id, project_key, evidence, outcome, promoter, expires_at, tombstoned_at)
  SELECT rowid, id, CAST(strftime('%s', ts) AS INTEGER) * 1000000000, kind, tier, text, about, session_id, project_key, evidence, outcome, promoter,
         CASE WHEN expires_at IS NULL THEN NULL ELSE CAST(strftime('%s', expires_at) AS INTEGER) * 1000000000 END,
         CASE WHEN tombstoned_at IS NULL THEN NULL ELSE CAST(strftime('%s', tombstoned_at) AS INTEGER) * 1000000000 END
  FROM records;
DROP TABLE records;
ALTER TABLE records_new RENAME TO records;
CREATE INDEX records_project ON records(project_key, ts);
CREATE INDEX records_session ON records(session_id);

-- Insert-only sync trigger; no UPDATE/DELETE triggers on records_fts exist
-- because records forbids UPDATE/DELETE below. The FTS index itself needs
-- no rebuild: records_new preserved the original rowids above.
CREATE TRIGGER records_ai AFTER INSERT ON records BEGIN
  INSERT INTO records_fts(rowid, text) VALUES (new.rowid, new.text);
END;

CREATE TRIGGER records_no_update
BEFORE UPDATE ON records
WHEN NOT (
  old.id          = new.id AND
  old.ts          = new.ts AND
  old.kind        = new.kind AND
  old.tier        = new.tier AND
  old.text        = new.text AND
  old.about       = new.about AND
  old.session_id  IS new.session_id AND
  old.project_key IS new.project_key AND
  old.evidence    = new.evidence AND
  old.outcome     IS new.outcome AND
  old.promoter    IS new.promoter AND
  old.expires_at  IS new.expires_at
)
BEGIN
  SELECT RAISE(ABORT, 'records: append-only, only tombstoned_at may change');
END;

CREATE TRIGGER records_no_delete
BEFORE DELETE ON records
BEGIN
  SELECT RAISE(ABORT, 'records: append-only, no delete');
END;
