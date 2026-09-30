-- 0012_purge_log_records.sql — Forget also tombstones the records written in
-- the purged window (decision 1e78acc2, task a9a784ec). purge_log stays
-- COUNTS ONLY: it gains how many records the purge tombstoned.
ALTER TABLE purge_log ADD COLUMN records INTEGER NOT NULL DEFAULT 0;
