-- 0018_sync_changes.sql — the change feed and the 'synced' session origin
-- (PLAN.md Phase 8, decision 2ac4c54f D3/D4/D7, task 99a70aeb).
--   sync_changes   per-store, append-only, monotone feed. seq is the cursor a
--                  peer pulls from; it is never a timestamp. kind is
--                  'record' | 'tombstone' | 'edge' | 'session'; ref names the
--                  row (record id, session id, or "from|to|type" for an
--                  edge). The feed holds no content: what is served is read
--                  from the row at serve time, so a tombstone scrub reaches
--                  the wire without any text ever being copied here.
--   sessions.origin  gains 'synced': a stub imported from a paired device
--                  (id, agent, origin_device, started_at, ended_at only).
-- Triggers write the feed in the same transaction as the change, so a
-- tombstone of an already-synced record produces its own change.
CREATE TABLE sync_changes (
  seq   INTEGER PRIMARY KEY AUTOINCREMENT,
  kind  TEXT NOT NULL CHECK (kind IN ('record','tombstone','edge','session')),
  ref   TEXT NOT NULL
);

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
  origin              TEXT NOT NULL CHECK (origin IN ('live','backfilled','synced')),
  exit_kind           TEXT,
  parent_session_id   TEXT REFERENCES sessions(id),
  purged_at           INTEGER,
  origin_device       TEXT
);
INSERT INTO sessions_new (id, agent, harness_session_id, pid, cwd, project_key, workspace, window,
                          started_at, ended_at, origin, exit_kind, parent_session_id, purged_at, origin_device)
  SELECT id, agent, harness_session_id, pid, cwd, project_key, workspace, window,
         started_at, ended_at, origin, exit_kind, parent_session_id, purged_at, origin_device
  FROM sessions;
DROP TABLE sessions;
ALTER TABLE sessions_new RENAME TO sessions;
CREATE INDEX sessions_parent ON sessions(parent_session_id);

-- History that predates the feed is queued once, oldest first.
INSERT INTO sync_changes (kind, ref) SELECT 'session', id FROM sessions ORDER BY rowid;
INSERT INTO sync_changes (kind, ref) SELECT 'record', id FROM records WHERE tombstoned_at IS NULL ORDER BY rowid;
INSERT INTO sync_changes (kind, ref) SELECT 'tombstone', id FROM records WHERE tombstoned_at IS NOT NULL ORDER BY rowid;
INSERT INTO sync_changes (kind, ref) SELECT 'edge', from_id || '|' || to_id || '|' || type FROM edges ORDER BY rowid;

CREATE TRIGGER sync_changes_no_update BEFORE UPDATE ON sync_changes
BEGIN SELECT RAISE(ABORT, 'sync_changes: append-only'); END;
CREATE TRIGGER sync_changes_no_delete BEFORE DELETE ON sync_changes
BEGIN SELECT RAISE(ABORT, 'sync_changes: append-only'); END;

-- (no CASE...END in a trigger body: splitStatements counts every END)
CREATE TRIGGER sync_record_ai AFTER INSERT ON records
WHEN new.tombstoned_at IS NULL BEGIN
  INSERT INTO sync_changes (kind, ref) VALUES ('record', new.id);
END;
CREATE TRIGGER sync_record_ai_tombstoned AFTER INSERT ON records
WHEN new.tombstoned_at IS NOT NULL BEGIN
  INSERT INTO sync_changes (kind, ref) VALUES ('tombstone', new.id);
END;
CREATE TRIGGER sync_record_tombstone AFTER UPDATE OF tombstoned_at ON records
WHEN old.tombstoned_at IS NULL AND new.tombstoned_at IS NOT NULL BEGIN
  INSERT INTO sync_changes (kind, ref) VALUES ('tombstone', new.id);
END;
CREATE TRIGGER sync_edge_ai AFTER INSERT ON edges BEGIN
  INSERT INTO sync_changes (kind, ref) VALUES ('edge', new.from_id || '|' || new.to_id || '|' || new.type);
END;
CREATE TRIGGER sync_session_ai AFTER INSERT ON sessions BEGIN
  INSERT INTO sync_changes (kind, ref) VALUES ('session', new.id);
END;
CREATE TRIGGER sync_session_ended AFTER UPDATE OF ended_at ON sessions
WHEN new.ended_at IS NOT old.ended_at BEGIN
  INSERT INTO sync_changes (kind, ref) VALUES ('session', new.id);
END;
