
DELETE FROM sites
WHERE branch = ?
  AND project_id IN (
    SELECT id FROM projects
    WHERE name = ? OR name LIKE ? ESCAPE '\'
  )