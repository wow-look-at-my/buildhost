-- name: GetGitHubCredential :one
SELECT token, updated_at FROM github_credential WHERE id = 1;

-- name: SetGitHubCredential :exec
INSERT INTO github_credential (id, token, updated_at)
VALUES (1, ?, datetime('now'))
ON CONFLICT(id) DO UPDATE SET
    token = excluded.token,
    updated_at = datetime('now');

-- name: DeleteGitHubCredential :exec
DELETE FROM github_credential WHERE id = 1;
