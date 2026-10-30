-- 0023_sessions_project_index.sql — index a project's sessions.
-- LastActivity and EventsForTimeline run once per project for this-week and
-- select a project's sessions (joined to their timeline events) by
-- project_key. With no index, SQLite scanned every session, or walked every
-- timeline event through timeline_ts and checked each one's session: about
-- 1.4 s for `this-week` on a store with 110k events and 32 projects, 0.5 s
-- with this index. The index changes no data.
CREATE INDEX sessions_project ON sessions(project_key);
