-- 0005_event_cursor.sql — give every record its timeline position
-- (critic T1 on 7d3954f0, task 214eb30e, SCHEMA.md invariant 10).
--
-- The SessionStart delta's membership boundary used to be the latest
-- handoff's wall-clock ts: an event with timeline_events.id greater than
-- every event at handoff time (i.e. genuinely AFTER it) but carrying an
-- earlier ts (a backfilled transcript's file clock lies) was silently
-- dropped. records.event_cursor fixes the boundary to the handoff's
-- position in the sequence, not its clock reading: InsertRecord sets it to
-- MAX(timeline_events.id) at insert time (0 when the timeline is empty),
-- inside the same transaction as the insert, and it is never a caller
-- parameter — the socket API has no field for it, the same way
-- records.tier isn't (SCHEMA.md invariant 2). The SessionStart delta and
-- any future caller read it back via EventsSinceID(cursor).
--
-- Backfill for rows that predate this column: the nearest preceding event
-- by ts, best effort. There is no better signal available after the fact —
-- a pre-migration record was never assigned a sequence position at insert
-- time, and its own ts is exactly the lying-clock problem this migration
-- exists to stop relying on going forward. New rows get a true cursor from
-- here on; this UPDATE only keeps existing rows from starting at a
-- nonsensical 0 when a preceding timeline already exists.
ALTER TABLE records ADD COLUMN event_cursor INTEGER NOT NULL DEFAULT 0;

-- Dropped so the backfill UPDATE below can write event_cursor; recreated
-- immediately after with event_cursor added to the append-only column list
-- (the same drop-rewrite-recreate shape 0003's migrationDataHooks uses for
-- project_key, here in pure SQL since no NUL-byte truncation applies to an
-- INTEGER column).
DROP TRIGGER records_no_update;

UPDATE records SET event_cursor = COALESCE(
  (SELECT MAX(e.id) FROM timeline_events e WHERE e.ts <= records.ts), 0
);

CREATE TRIGGER records_no_update
BEFORE UPDATE ON records
WHEN NOT (
  old.id           = new.id AND
  old.ts           = new.ts AND
  old.kind         = new.kind AND
  old.tier         = new.tier AND
  old.text         = new.text AND
  old.about        = new.about AND
  old.session_id   IS new.session_id AND
  old.project_key  IS new.project_key AND
  old.evidence     = new.evidence AND
  old.outcome      IS new.outcome AND
  old.promoter     IS new.promoter AND
  old.expires_at   IS new.expires_at AND
  old.event_cursor = new.event_cursor
)
BEGIN
  SELECT RAISE(ABORT, 'records: append-only, only tombstoned_at may change');
END;
