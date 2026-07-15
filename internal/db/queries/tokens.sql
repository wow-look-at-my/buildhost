-- name: InsertToken :execresult
INSERT INTO api_tokens (name, token_hash, token_prefix, project_id, scopes)
VALUES (?, ?, ?, ?, ?);

-- name: GetTokenByHash :one
SELECT id, name, token_hash, token_prefix, project_id, scopes, expires_at, created_at, last_used_at
FROM api_tokens WHERE token_hash = ?;

-- name: UpdateTokenLastUsed :exec
UPDATE api_tokens SET last_used_at = datetime('now') WHERE id = ?;

-- name: ListAllTokens :many
SELECT id, name, token_prefix, project_id, scopes, expires_at, created_at, last_used_at
FROM api_tokens ORDER BY created_at DESC;

-- name: UpdateTokenByID :exec
UPDATE api_tokens SET name = ?, scopes = ? WHERE id = ?;

-- name: DeleteTokenByID :execresult
DELETE FROM api_tokens WHERE id = ?;
