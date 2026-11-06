-- 0026_record_git_branch.sql — Resume is per git branch (task 64cd2b5e).
-- git_branch is the branch checked out in the writing session's repo at record
-- write, stamped next to git_head. NULL means unknown: a record written before
-- this migration, a detached HEAD, a non-git folder, or a branch that could
-- not be observed. Nothing is backfilled; the block treats NULL as matching
-- any branch. Immutable like git_head: records_no_update (recreated by this
-- migration's data hook) refuses any UPDATE of it.
ALTER TABLE records ADD COLUMN git_branch TEXT;
DROP TRIGGER records_no_update;
