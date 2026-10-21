-- 0019_sync_peers.sql — the transport side of sync (PLAN.md Phase 8, decision
-- 2ac4c54f D1/D6/D8, task b32a0fcb).
--   sync_peers.pull_cursor / push_cursor  the two feed positions per peer: how
--                  far into the PEER's feed this store has imported, and how
--                  far into THIS store's feed the peer has acknowledged. Both
--                  are written only after the batch they cover has committed.
--   sync_attempts  append-only log of every sync attempt, one row per peer
--                  per attempt: result ok | unreachable | error | paused.
--                  Nothing in it is ever updated or deleted.
ALTER TABLE sync_peers ADD COLUMN pull_cursor INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sync_peers ADD COLUMN push_cursor INTEGER NOT NULL DEFAULT 0;

CREATE TABLE sync_attempts (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  ts         INTEGER NOT NULL,          -- unix nanoseconds, UTC
  peer       TEXT NOT NULL,             -- the peer's name (or ssh host when unnamed)
  device_id  TEXT,                      -- the peer's device id, when known
  result     TEXT NOT NULL CHECK (result IN ('ok','unreachable','error','paused')),
  detail     TEXT NOT NULL DEFAULT '',
  pulled     INTEGER NOT NULL DEFAULT 0,
  pushed     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX sync_attempts_peer_ts ON sync_attempts(peer, ts);
CREATE TRIGGER sync_attempts_no_update BEFORE UPDATE ON sync_attempts
BEGIN SELECT RAISE(ABORT, 'sync_attempts: append-only'); END;
CREATE TRIGGER sync_attempts_no_delete BEFORE DELETE ON sync_attempts
BEGIN SELECT RAISE(ABORT, 'sync_attempts: append-only'); END;
