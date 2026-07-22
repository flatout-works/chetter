-- name: InsertTaskEvent :exec
INSERT INTO chetter_task_events (id, task_id, agent_session_id, user_prompt_id, execution_attempt_id, subject, status, event_type, payload, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListTaskEvents :many
SELECT * FROM chetter_task_events
WHERE task_id = ?
ORDER BY created_at DESC, id DESC
LIMIT ?
OFFSET ?;

-- name: ListTaskEventsSince :many
SELECT * FROM chetter_task_events
WHERE task_id = ? AND created_at > ?
ORDER BY created_at ASC, id ASC;
