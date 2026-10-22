-- 0022_sync_changes_supersede.sql — one feed entry per row (task 7abf3211).
-- Requeueing a project used to append a second copy of every record, so a
-- feed batch boundary could fall between the copies and re-serve records
-- (decision c60db1d1). The feed stays append-only, with one exception: an
-- entry may be deleted once a newer entry for the same ref exists, which is
-- what a requeue does. A peer behind the old copy still reaches the new one.
DROP TRIGGER sync_changes_no_delete;
CREATE TRIGGER sync_changes_no_delete BEFORE DELETE ON sync_changes
WHEN NOT EXISTS (SELECT 1 FROM sync_changes n
  WHERE n.ref = OLD.ref AND n.seq > OLD.seq
    AND n.kind IN ('record', 'tombstone', 'session')
    AND OLD.kind IN ('record', 'tombstone', 'session'))
BEGIN SELECT RAISE(ABORT, 'sync_changes: append-only'); END;

-- Collapse the copies requeues already made, keeping the newest.
DELETE FROM sync_changes WHERE kind IN ('record', 'tombstone', 'session')
  AND seq < (SELECT MAX(n.seq) FROM sync_changes n WHERE n.ref = sync_changes.ref
    AND n.kind IN ('record', 'tombstone', 'session'));
