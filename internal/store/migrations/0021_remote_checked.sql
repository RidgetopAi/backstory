-- 0019_remote_checked.sql — decide each remote-less project once
-- (task 2b45c834). projects.remote_checked_at is the unix-nanosecond time
-- fillMissingRemotes last asked git about the project and found no remote.
-- It only gates that repair: a live session start or backfill upsert that
-- observes a remote still writes remote_url regardless of the marker.
ALTER TABLE projects ADD COLUMN remote_checked_at INTEGER;
