-- 0010_records_tombstone_scrub.sql — delete actually forgets (decision
-- 02c511b3 D3, task 4aa3a99f). records_no_update now permits, besides a
-- no-op, exactly one mutation: tombstoned_at NULL -> value, optionally with
-- text scrubbed to '' in the same UPDATE. It also covers every column
-- (event_cursor, git_head were missing). The replacement trigger's text
-- lives in Go (recordsNoUpdateTriggerSQL, created by this migration's data
-- hook) so the workspace sweep recreates the identical definition.
DROP TRIGGER records_no_update;
