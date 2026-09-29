-- 0009_record_git_head.sql — deterministic staleness stamp (task 5615ddae).
-- git_head is the full sha of HEAD in the writing session's repo at record
-- write; recall compares it to the repo's HEAD at read time. NULL for every
-- pre-existing record and for any record written outside a git repo.
ALTER TABLE records ADD COLUMN git_head TEXT;
