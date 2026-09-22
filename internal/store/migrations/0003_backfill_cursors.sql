-- 0003_backfill_cursors.sql — per-file backfill progress cursors.
--
-- A backfill importer (e.g. internal/backfill/claude) processes a source
-- file incrementally: byte_offset is how far into the file it has already
-- read, last_uuid is the last transcript line's uuid imported, and
-- session_id is the store session that file's lines belong to (backfill
-- creates it once and only ever appends events to it afterwards). A rerun
-- over an unchanged file reads nothing new and mints nothing; a rerun over
-- an appended file resumes at byte_offset.
CREATE TABLE backfill_cursors (
  source       TEXT NOT NULL,          -- importer name, e.g. "claude"
  path         TEXT NOT NULL,          -- absolute source file path
  session_id   TEXT NOT NULL REFERENCES sessions(id),
  last_uuid    TEXT,
  byte_offset  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (source, path)
);
