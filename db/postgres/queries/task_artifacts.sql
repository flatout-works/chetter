-- name: InsertTaskArtifact :exec
INSERT INTO chetter_task_artifacts (id, task_id, agent_session_id, user_prompt_id, execution_attempt_id, artifact_type, repo, number, url, ref, sha, created_at, discovered_at, discovery_source, search_text)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
ON CONFLICT (task_id, artifact_type, repo, number, execution_attempt_id) DO NOTHING;

-- name: ListTaskArtifacts :many
SELECT id, task_id, agent_session_id, user_prompt_id, execution_attempt_id, artifact_type, repo, number, url, ref, sha, created_at, discovered_at, discovery_source
FROM chetter_task_artifacts
WHERE (task_id = sqlc.arg(task_id) OR sqlc.arg(task_id) = '')
  AND (agent_session_id = sqlc.arg(agent_session_id) OR sqlc.arg(agent_session_id) = '')
  AND (user_prompt_id = sqlc.arg(user_prompt_id) OR sqlc.arg(user_prompt_id) = '')
  AND (execution_attempt_id = sqlc.arg(execution_attempt_id) OR sqlc.arg(execution_attempt_id) = '')
  AND (artifact_type = sqlc.arg(artifact_type) OR sqlc.arg(artifact_type) = '')
  AND (repo = sqlc.arg(repo) OR sqlc.arg(repo) = '')
ORDER BY discovered_at DESC, id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: SearchTaskArtifacts :many
SELECT id, task_id, agent_session_id, user_prompt_id, execution_attempt_id, artifact_type, repo, number, url, ref, sha, created_at, discovered_at, discovery_source
FROM chetter_task_artifacts
WHERE (task_id = sqlc.arg(task_id) OR sqlc.arg(task_id) = '')
  AND (agent_session_id = sqlc.arg(agent_session_id) OR sqlc.arg(agent_session_id) = '')
  AND (user_prompt_id = sqlc.arg(user_prompt_id) OR sqlc.arg(user_prompt_id) = '')
  AND (execution_attempt_id = sqlc.arg(execution_attempt_id) OR sqlc.arg(execution_attempt_id) = '')
  AND (artifact_type = sqlc.arg(artifact_type) OR sqlc.arg(artifact_type) = '')
  AND (repo = sqlc.arg(repo) OR sqlc.arg(repo) = '')
  AND search_text ILIKE '%' || sqlc.arg(search) || '%'
ORDER BY discovered_at DESC, id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);
