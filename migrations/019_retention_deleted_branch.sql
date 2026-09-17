-- Deleted-branch reclaim window, in days. A published release becomes eligible
-- for deletion only when it is older than this AND the branch it was built from
-- no longer exists on the project's origin repository. 30 days by default; 0
-- turns the rule off and leaves keep-N and the abandoned sweep untouched.
--
-- The window lives beside keep_n and recency_hours so every caller -- the
-- background sweeper, the gc CLI and the admin preview -- reads one policy row
-- instead of three copies of the same number.
ALTER TABLE retention_settings ADD COLUMN deleted_branch_days INTEGER NOT NULL DEFAULT 30;
