-- 0020_sync_session_purged.sql — a purged session crosses (PLAN.md Phase 8,
-- decision 2ac4c54f D3, task 12d95035). Stamping sessions.purged_at queues the
-- session stub in the change feed so the stub carries purged_at to every
-- peer. The purge's tombstoned records cross on their own 'tombstone' entries
-- (sync_record_tombstone), so the peer forgets exactly the in-scope records.
CREATE TRIGGER sync_session_purged AFTER UPDATE OF purged_at ON sessions
WHEN new.purged_at IS NOT old.purged_at BEGIN
  INSERT INTO sync_changes (kind, ref) VALUES ('session', new.id);
END;
