-- name: ListServerSettings :many
SELECT name, value FROM server_settings ORDER BY name;

-- name: SetServerSetting :exec
INSERT INTO server_settings (name, value, updated_at) VALUES (?, ?, datetime('now'))
ON CONFLICT(name) DO UPDATE SET value = excluded.value, updated_at = datetime('now');

-- name: DeleteServerSetting :exec
DELETE FROM server_settings WHERE name = ?;
