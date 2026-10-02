-- 0013_record_next.sql — a handoff's optional one-line next step (task
-- e1a0a683, decision 63ce9687). NULL for every pre-existing record and for
-- any record that carries none. The replacement records_no_update trigger
-- (recordsNoUpdateTriggerSQL, created by this migration's data hook) makes
-- next immutable, except that a tombstone scrub may clear it with the text.
ALTER TABLE records ADD COLUMN next TEXT;
DROP TRIGGER records_no_update;
