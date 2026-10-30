-- 0024_timeline_session_ts_index.sql — seek a session's events by time.
-- this-week reads, per active project, the events since the window start
-- (EventsForTimeline) and the latest event at or before now (LastActivity,
-- AgentActivityForSessions). timeline_session (session_id, id) found a
-- session's events but not their time range, so SQLite read every event the
-- session ever had and filtered ts afterwards. With (session_id, ts) it seeks
-- the range, and the MAX(ts) reads use the index alone: this-week went from
-- 256 ms to 198 ms on a store with 110k events. timeline_session stays: id
-- order reads still use it. The index changes no data.
CREATE INDEX timeline_session_ts ON timeline_events(session_id, ts);
