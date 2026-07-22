-- name: InsertTaskArtifact :exec
INSERT IGNORE INTO chetter_task_artifacts (id, task_id, agent_session_id, user_prompt_id, execution_attempt_id, artifact_type, repo, number, url, ref, sha, created_at, discovered_at, discovery_source, search_text)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListTaskArtifacts :many
SELECT id, task_id, agent_session_id, user_prompt_id, execution_attempt_id, artifact_type, repo, number, url, ref, sha, created_at, discovered_at, discovery_source
FROM chetter_task_artifacts
WHERE (task_id = ? OR ? = '')
  AND (agent_session_id = ? OR ? = '')
  AND (user_prompt_id = ? OR ? = '')
  AND (execution_attempt_id = ? OR ? = '')
  AND (artifact_type = ? OR ? = '')
  AND (repo = ? OR ? = '')
ORDER BY discovered_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: SearchTaskArtifacts :many
SELECT id, task_id, agent_session_id, user_prompt_id, execution_attempt_id, artifact_type, repo, number, url, ref, sha, created_at, discovered_at, discovery_source
FROM chetter_task_artifacts
WHERE (task_id = ? OR ? = '')
  AND (agent_session_id = ? OR ? = '')
  AND (user_prompt_id = ? OR ? = '')
  AND (execution_attempt_id = ? OR ? = '')
  AND (artifact_type = ? OR ? = '')
  AND (repo = ? OR ? = '')
  AND (search_text LIKE CONCAT('%', sqlc.arg(search), '%'))
ORDER BY discovered_at DESC, id DESC
LIMIT ? OFFSET ?;
