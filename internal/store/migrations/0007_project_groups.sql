-- 0007_project_groups.sql — user-owned project groups over observed project
-- keys (decision bcc9fa54, task 57ec6e62).
--
-- Project identity stays observed (git repository, internal/project.Key);
-- this table is a grouping layer on top of it, set by a human only (CLI and
-- panel — a later punch's job), never by an agent. project_key is the
-- table's primary key, not just a unique index, because a project is in at
-- most one group: re-setting it (a "move") overwrites the row instead of
-- adding a second one. group_name carries no uniqueness constraint of its
-- own — many projects share one group.
CREATE TABLE project_groups (
  project_key  TEXT PRIMARY KEY REFERENCES projects(key),
  group_name   TEXT NOT NULL,
  set_at       INTEGER NOT NULL        -- unix nanoseconds, UTC; last set/move time
);
CREATE INDEX project_groups_name ON project_groups(group_name);
