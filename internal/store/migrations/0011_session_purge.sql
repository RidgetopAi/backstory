-- 0011_session_purge.sql — the human purge (decision 02c511b3 D4, task
-- 15817735). sessions.purged_at stamps a session whose timeline rows the
-- human erased, so backfill never re-imports its transcript. purge_log keeps
-- COUNTS ONLY per purge (never content): what scope, how many sessions and
-- events.
ALTER TABLE sessions ADD COLUMN purged_at INTEGER;

CREATE TABLE purge_log (
  id       INTEGER PRIMARY KEY,
  ts       INTEGER NOT NULL,
  scope    TEXT NOT NULL,
  sessions INTEGER NOT NULL,
  events   INTEGER NOT NULL
);
