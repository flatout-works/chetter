-- name: InsertTaskArtifact :exec
INSERT INTO chetter_task_artifacts (id, task_id, artifact_type, repo, number, url, ref, sha, created_at, discovered_at, discovery_source)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListTaskArtifacts :many
SELECT id, task_id, artifact_type, repo, number, url, ref, sha, created_at, discovered_at, discovery_source
FROM chetter_task_artifacts
WHERE (task_id = ? OR ? = '')
  AND (artifact_type = ? OR ? = '')
  AND (repo = ? OR ? = '')
ORDER BY discovered_at DESC
LIMIT ?;
