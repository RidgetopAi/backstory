-- 0015_project_key_aliases.sql — persist legacy -> canonical project key
-- renames (task 725021e4). A workspace sweep or repo-identity merge deletes
-- the legacy projects row and recorded the rename only in the merging
-- Store's memory, so another process's long-lived Store (the daemon) kept
-- writing under the deleted key and every insert failed its foreign key.
-- The merge now writes the rename here, in the same transaction, and
-- canonicalizeProjectKey falls back to it. No data rewrite: no earlier
-- version recorded renames, and a deleted legacy row cannot be rediscovered.
CREATE TABLE project_key_aliases (
  legacy_key     TEXT PRIMARY KEY,
  canonical_key  TEXT NOT NULL REFERENCES projects(key) ON DELETE CASCADE,
  created_at     INTEGER NOT NULL      -- unix nanoseconds, UTC
);
