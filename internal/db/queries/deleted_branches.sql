-- name: RecordBranchDeletedForRepo :exec
-- Marks the branch deleted on every project the GitHub repo owns. OR IGNORE
-- keeps the first deletion time, so a repeated delivery does not restart the TTL.
INSERT OR IGNORE INTO deleted_branches (project_id, branch, deleted_at)
SELECT p.id, sqlc.arg(branch), datetime(sqlc.arg(deleted_at))
FROM projects p
WHERE lower(p.github_repo) = lower(sqlc.arg(github_repo));

-- name: ClearBranchDeletedForRepo :exec
DELETE FROM deleted_branches
WHERE branch = sqlc.arg(branch)
  AND project_id IN (SELECT p.id FROM projects p WHERE lower(p.github_repo) = lower(sqlc.arg(github_repo)));

-- name: RecordBranchDeleted :exec
INSERT OR IGNORE INTO deleted_branches (project_id, branch, deleted_at)
VALUES (sqlc.arg(project_id), sqlc.arg(branch), datetime(sqlc.arg(deleted_at)));

-- name: ClearBranchDeleted :exec
DELETE FROM deleted_branches WHERE project_id = ? AND branch = ?;

-- name: DeleteProjectDeletedBranches :exec
DELETE FROM deleted_branches WHERE project_id = ?;

-- name: ListDeletedBranches :many
SELECT project_id, branch, deleted_at FROM deleted_branches WHERE project_id = ? ORDER BY branch;

-- name: ListGitHubRepoProjects :many
-- Every project tied to a GitHub repo, which is every project the branch sync
-- can check.
SELECT id, name, github_repo, default_branch FROM projects WHERE github_repo != '' ORDER BY id;

-- name: ListProjectReleaseBranches :many
-- The distinct branches a project holds releases on.
SELECT DISTINCT git_branch FROM releases WHERE project_id = ? AND git_branch != '' ORDER BY git_branch;
