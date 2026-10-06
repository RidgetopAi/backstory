-- 0014_repair_zero_ended_at.sql — repair sessions.ended_at values that a
-- backfill importer wrote from Go's zero time.Time (task 456410f0). A zero
-- time converts to unix nanoseconds as -6795364578871345152, which renders
-- as 1754-08-30. Each such session (ended_at <= 0) gets its latest valid
-- (ts > 0) event time, or NULL when it has no valid event.
--
-- timeline_events is append-only: the timeline_events_no_update trigger
-- refuses every UPDATE, so the existing events whose ts is <= 0 are LEFT as
-- they are. Readers render a ts <= 0 as unknown instead (cmd/backstory
-- timeline, internal/week, internal/block). Importers no longer write them.
UPDATE sessions
   SET ended_at = (SELECT MAX(e.ts) FROM timeline_events e
                    WHERE e.session_id = sessions.id AND e.ts > 0)
 WHERE ended_at IS NOT NULL AND ended_at <= 0;
