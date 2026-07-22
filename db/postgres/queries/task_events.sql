-- name: InsertTaskEvent :exec
INSERT INTO chetter_task_events (id, task_id, agent_session_id, user_prompt_id, execution_attempt_id, subject, status, event_type, payload, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: ListTaskEvents :many
SELECT * FROM chetter_task_events
WHERE task_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3;

-- name: ListTaskEventsSince :many
SELECT * FROM chetter_task_events
WHERE task_id = $1 AND created_at > $2
ORDER BY created_at ASC, id ASC;
