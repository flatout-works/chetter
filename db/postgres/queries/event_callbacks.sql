-- name: InsertEventCallback :exec
INSERT INTO chetter_event_callbacks
    (id, team_id, name, event_type, action_type, action_config, enabled, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetEventCallbackByID :one
SELECT * FROM chetter_event_callbacks WHERE id = $1;

-- name: GetEventCallbackByName :one
SELECT * FROM chetter_event_callbacks
WHERE name = $1 AND team_id IS NOT DISTINCT FROM sqlc.narg(team_id);

-- name: ListEventCallbacks :many
SELECT * FROM chetter_event_callbacks
WHERE (sqlc.arg(enabled_only) = false OR enabled = true)
  AND (COALESCE(sqlc.arg(event_type_filter), '') = '' OR event_type = sqlc.arg(event_type_filter))
  AND (sqlc.arg(include_global) = true OR team_id IS NOT DISTINCT FROM sqlc.narg(team_id))
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: ListEventCallbacksByTeams :many
SELECT * FROM chetter_event_callbacks
WHERE (sqlc.arg(enabled_only) = false OR enabled = true)
  AND (COALESCE(sqlc.arg(event_type_filter), '') = '' OR event_type = sqlc.arg(event_type_filter))
  AND team_id = ANY(sqlc.arg(team_ids)::text[])
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: ListEnabledEventCallbacksForEvent :many
SELECT * FROM chetter_event_callbacks
WHERE enabled = true
  AND (team_id IS NOT DISTINCT FROM sqlc.narg(team_id) OR team_id IS NULL)
  AND (
    event_type = sqlc.arg(event_type)
    OR (right(event_type, 2) = '.*' AND sqlc.arg(event_type) LIKE left(event_type, char_length(event_type) - 1) || '%')
  )
ORDER BY created_at ASC;

-- name: UpdateEventCallback :execrows
UPDATE chetter_event_callbacks
SET event_type = $1,
    action_type = $2,
    action_config = $3,
    enabled = $4,
    updated_at = $5
WHERE name = $6 AND team_id IS NOT DISTINCT FROM sqlc.narg(team_id);

-- name: DeleteEventCallback :execrows
DELETE FROM chetter_event_callbacks
WHERE name = $1 AND team_id IS NOT DISTINCT FROM sqlc.narg(team_id);
