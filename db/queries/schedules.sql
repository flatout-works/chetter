-- name: GetScheduleByID :one
SELECT * FROM chetter_schedules
WHERE id = ?;

-- name: GetScheduleByName :one
SELECT * FROM chetter_schedules
WHERE name = ?;

-- name: ListSchedules :many
SELECT * FROM chetter_schedules
ORDER BY created_at DESC;

-- name: ListEnabledSchedules :many
SELECT * FROM chetter_schedules
WHERE enabled = TRUE
ORDER BY created_at DESC;

-- name: ListSchedulesByTeam :many
SELECT * FROM chetter_schedules
WHERE team_id = sqlc.arg(team_id)
ORDER BY created_at DESC;

-- name: ListEnabledSchedulesByTeam :many
SELECT * FROM chetter_schedules
WHERE team_id = sqlc.arg(team_id)
  AND enabled = TRUE
ORDER BY created_at DESC;

-- name: CreateSchedule :exec
INSERT INTO chetter_schedules
    (id, team_id, name, cron_expr, prompt, git_url, git_ref, agent_image, agent, provider_id, model_id, variant_id, skills, timeout_sec, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, TRUE, ?, ?);

-- name: UpdateSchedule :exec
UPDATE chetter_schedules
SET name = sqlc.arg(new_name), cron_expr = ?, prompt = ?,
    git_url = ?, git_ref = ?, agent_image = ?,
    agent = ?, provider_id = ?, model_id = ?, variant_id = ?,
    skills = ?, timeout_sec = ?, enabled = ?,
    updated_at = ?
WHERE name = sqlc.arg(old_name);

-- name: SetScheduleNextRun :exec
UPDATE chetter_schedules
SET next_run_at = ?, updated_at = ?
WHERE id = ?;

-- name: DeleteSchedule :exec
DELETE FROM chetter_schedules
WHERE name = ?;

-- name: SetScheduleLastRun :exec
UPDATE chetter_schedules
SET last_run_at = ?, updated_at = ?
WHERE id = ?;

-- name: InsertScheduleRun :exec
INSERT INTO chetter_schedule_runs (id, schedule_id, task_id, status, scheduled_for, created_at)
VALUES (?, ?, ?, ?, ?, ?);
