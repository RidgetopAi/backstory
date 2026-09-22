-- 0001_init.sql — Backstory schema v0 (SCHEMA.md).
--
-- projects / sessions / timeline_events / records / edges / settings, plus
-- an FTS5 index over records.text and the append-only triggers that make
-- records and timeline_events insert-only. `punch`, `stage` and `claim` are
-- reserved record kinds (PLAN.md §Reserved for the loop) even though
-- nothing writes them yet.

CREATE TABLE projects (
  key             TEXT PRIMARY KEY,
  git_common_dir  TEXT,
  remote_url      TEXT,
  toplevel        TEXT NOT NULL,
  first_seen      TEXT NOT NULL
);

CREATE TABLE sessions (
  id                  TEXT PRIMARY KEY,
  agent               TEXT NOT NULL,
  harness_session_id  TEXT,
  pid                 INTEGER,
  cwd                 TEXT NOT NULL,
  project_key         TEXT REFERENCES projects(key),
  workspace           TEXT,
  window              TEXT,
  started_at          TEXT NOT NULL,
  ended_at            TEXT,
  origin              TEXT NOT NULL CHECK (origin IN ('live','backfilled')),
  exit_kind           TEXT
);

CREATE TABLE timeline_events (
  id          INTEGER PRIMARY KEY,
  ts          TEXT NOT NULL,
  kind        TEXT NOT NULL,
  session_id  TEXT REFERENCES sessions(id),
  source      TEXT NOT NULL,
  payload     TEXT NOT NULL,
  workspace   TEXT,
  window      TEXT
);
CREATE INDEX timeline_session ON timeline_events(session_id, id);
CREATE INDEX timeline_ts      ON timeline_events(ts);

CREATE TABLE records (
  id             TEXT PRIMARY KEY,
  ts             TEXT NOT NULL,
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
  expires_at     TEXT,
  tombstoned_at  TEXT
);
CREATE INDEX records_project ON records(project_key, ts);
CREATE INDEX records_session ON records(session_id);

CREATE TABLE edges (
  from_id      TEXT NOT NULL REFERENCES records(id),
  to_id        TEXT NOT NULL REFERENCES records(id),
  type         TEXT NOT NULL CHECK (type IN
                 ('supersedes','possibly_supersedes','informs','caused',
                  'produced_outcome','contradicts')),
  declared_by  TEXT NOT NULL,
  PRIMARY KEY (from_id, to_id, type)
);

CREATE TABLE settings (
  key    TEXT PRIMARY KEY,
  value  TEXT NOT NULL
);

CREATE VIRTUAL TABLE records_fts USING fts5(
  text, content='records', content_rowid='rowid'
);

-- Insert-only sync trigger; no UPDATE/DELETE triggers on records_fts exist
-- because records and timeline_events forbid UPDATE/DELETE below.
CREATE TRIGGER records_ai AFTER INSERT ON records BEGIN
  INSERT INTO records_fts(rowid, text) VALUES (new.rowid, new.text);
END;

-- records is append-only except for records.tombstoned_at (the human
-- delete path). Any UPDATE that changes another column is refused.
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

-- timeline_events has no mutable columns at all: only the daemon writes it,
-- and even the daemon never edits or removes an observed fact.
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
