-- 0008_session_parent.sql — link a subagent session to its parent
-- (decision 3e14db82, task e9cb97dd). Codex's own subagent runs are each a
-- separate rollout with a full session_meta of their own (unlike Claude's
-- Task-tool subagents, which share the parent's session and are only
-- distinguished by tool_use.agent_id) — parent_session_id is how such a
-- session is linked back to the session that spawned it. NULL for every
-- session that has no parent (the overwhelming common case).
ALTER TABLE sessions ADD COLUMN parent_session_id TEXT REFERENCES sessions(id);
CREATE INDEX sessions_parent ON sessions(parent_session_id);
