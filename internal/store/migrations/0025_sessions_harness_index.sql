-- 0025_sessions_harness_index.sql — find a session's twin rows by run.
-- stampRunKeys runs on every event read and asks, for each session in the
-- events, whether another session row shares its (agent,
-- harness_session_id) (task a757b754). It used to group every session row by
-- that pair each time, about 25 ms of this-week. With this index each twin
-- check is one seek. The index changes no data.
CREATE INDEX sessions_harness ON sessions(agent, harness_session_id);
