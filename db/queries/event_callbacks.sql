-- name: InsertEventCallback :exec
INSERT INTO chetter_event_callbacks
    (id, team_id, name, event_type, action_type, action_config, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetEventCallbackByID :one
SELECT * FROM chetter_event_callbacks
WHERE id = ?;

-- name: GetEventCallbackByName :one
SELECT * FROM chetter_event_callbacks
WHERE name = ?
  AND (team_id <=> sqlc.arg(team_id));

-- name: ListEventCallbacks :many
SELECT * FROM chetter_event_callbacks
WHERE (sqlc.arg(enabled_only) = false OR enabled = true)
  AND (COALESCE(sqlc.arg(event_type_filter), '') = '' OR event_type = sqlc.arg(event_type_filter))
  AND (
    sqlc.arg(include_global) = true
    OR team_id <=> sqlc.arg(team_id)
  )
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: ListEventCallbacksByTeams :many
SELECT * FROM chetter_event_callbacks
WHERE (sqlc.arg(enabled_only) = false OR enabled = true)
  AND (COALESCE(sqlc.arg(event_type_filter), '') = '' OR event_type = sqlc.arg(event_type_filter))
  AND team_id IN (sqlc.slice(team_ids))
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: ListEnabledEventCallbacksForEvent :many
SELECT * FROM chetter_event_callbacks
WHERE enabled = true
  AND (team_id <=> sqlc.arg(team_id) OR team_id IS NULL)
  AND (
    event_type = sqlc.arg(event_type)
    OR (RIGHT(event_type, 2) = '.*' AND sqlc.arg(event_type) LIKE CONCAT(LEFT(event_type, CHAR_LENGTH(event_type) - 1), '%'))
  )
ORDER BY created_at ASC;

-- name: UpdateEventCallback :execrows
UPDATE chetter_event_callbacks
SET event_type = ?,
    action_type = ?,
    action_config = ?,
    enabled = ?,
    updated_at = ?
WHERE name = ?
  AND (team_id <=> sqlc.arg(team_id));

-- name: DeleteEventCallback :execrows
DELETE FROM chetter_event_callbacks
WHERE name = ?
  AND (team_id <=> sqlc.arg(team_id));
