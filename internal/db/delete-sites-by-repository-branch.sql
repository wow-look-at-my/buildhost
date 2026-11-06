
SELECT sites.project_id, projects.name, sites.branch, sites.storage_key
FROM sites
JOIN projects ON projects.id = sites.project_id
WHERE sites.branch = ?
  AND (projects.name = ? OR projects.name LIKE ? ESCAPE '\')
ORDER BY projects.name