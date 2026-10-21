-- 0017_project_links.sql — human-set sync identity for a project (PLAN.md
-- Phase 8, decision 2ac4c54f D2, task e0876069). A project's sync identity is
-- its normalized remote when it has one; project_links gives one to a project
-- with no remote (or a workspace) and overrides the remote. Written only by
-- the human path (SetProjectLink), never through the socket.
CREATE TABLE project_links (
  local_key  TEXT PRIMARY KEY REFERENCES projects(key),
  sync_id    TEXT NOT NULL,
  set_at     INTEGER NOT NULL           -- unix nanoseconds, UTC
);
CREATE INDEX project_links_sync_id ON project_links(sync_id);
