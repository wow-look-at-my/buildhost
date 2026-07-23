-- name: RecordDownloadEvent :exec
INSERT INTO download_events (artifact_id, fmt, client_ip, user_agent, principal)
VALUES (?, ?, ?, ?, ?);

-- name: ListDownloadEventsByProject :many
SELECT de.id, de.artifact_id, de.fmt, de.client_ip, de.user_agent, de.principal, de.created_at,
       a.os, a.arch, r.version, r.git_branch
FROM download_events de
JOIN artifacts a ON de.artifact_id = a.id
JOIN releases r ON a.release_id = r.id
WHERE r.project_id = ?
ORDER BY de.created_at DESC, de.id DESC
LIMIT ?;

-- name: ListDownloadEventsByRelease :many
SELECT de.id, de.artifact_id, de.fmt, de.client_ip, de.user_agent, de.principal, de.created_at,
       a.os, a.arch
FROM download_events de
JOIN artifacts a ON de.artifact_id = a.id
WHERE a.release_id = ?
ORDER BY de.created_at DESC, de.id DESC
LIMIT ?;
