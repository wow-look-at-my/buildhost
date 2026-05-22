-- name: InsertOIDCPolicy :execresult
INSERT INTO oidc_policies (issuer, subject_pattern, audience, project_id, scopes)
VALUES (?, ?, ?, ?, ?);

-- name: ListAllOIDCPolicies :many
SELECT id, issuer, subject_pattern, audience, project_id, scopes, created_at
FROM oidc_policies ORDER BY created_at DESC;

-- name: ListOIDCPoliciesByIssuer :many
SELECT id, issuer, subject_pattern, audience, project_id, scopes, created_at
FROM oidc_policies WHERE issuer = ?;

-- name: DeleteOIDCPolicyByID :execresult
DELETE FROM oidc_policies WHERE id = ?;

-- name: GetOIDCPolicyByID :one
SELECT id, issuer, subject_pattern, audience, project_id, scopes, created_at
FROM oidc_policies WHERE id = ?;
