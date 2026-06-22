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

-- name: ListEnabledTriggersByType :many
SELECT * FROM chetter_schedules
WHERE enabled = TRUE
  AND trigger_type = sqlc.arg(trigger_type)
ORDER BY created_at DESC;

-- name: ListEnabledPRReviewTriggersByRepo :many
SELECT * FROM chetter_schedules
WHERE enabled = TRUE
  AND trigger_type = 'pr_review'
  AND trigger_config->>'$.repo' = sqlc.arg(repo)
ORDER BY created_at DESC;

-- name: ListEnabledIssueTriggersByRepo :many
SELECT * FROM chetter_schedules
WHERE enabled = TRUE
  AND trigger_type = 'issue'
  AND trigger_config->>'$.repo' = sqlc.arg(repo)
ORDER BY created_at DESC;

-- name: CreateSchedule :exec
INSERT INTO chetter_schedules
    (id, team_id, name, trigger_type, trigger_config, cron_expr, prompt, git_url, git_ref, agent_image, agent, provider_id, model_id, variant_id, harness, skills, timeout_sec, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, TRUE, ?, ?);

-- name: UpdateSchedule :exec
UPDATE chetter_schedules
SET name = sqlc.arg(new_name), trigger_type = ?, trigger_config = ?, cron_expr = ?, prompt = ?,
    git_url = ?, git_ref = ?, agent_image = ?,
    agent = ?, provider_id = ?, model_id = ?, variant_id = ?,
    harness = ?, skills = ?, timeout_sec = ?, enabled = ?,
    updated_at = ?
WHERE name = sqlc.arg(old_name);

-- name: SetScheduleNextRun :exec
UPDATE chetter_schedules
SET next_run_at = ?, updated_at = ?
WHERE id = ?;

-- name: UpsertSchedule :exec
INSERT INTO chetter_schedules
    (id, team_id, name, trigger_type, trigger_config, cron_expr, prompt, git_url, git_ref, agent_image, agent, provider_id, model_id, variant_id, harness, skills, timeout_sec, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    trigger_type = VALUES(trigger_type),
    trigger_config = VALUES(trigger_config),
    cron_expr = VALUES(cron_expr),
    prompt = VALUES(prompt),
    git_url = VALUES(git_url),
    git_ref = VALUES(git_ref),
    agent_image = VALUES(agent_image),
    agent = VALUES(agent),
    provider_id = VALUES(provider_id),
    model_id = VALUES(model_id),
    variant_id = VALUES(variant_id),
    harness = VALUES(harness),
    skills = VALUES(skills),
    timeout_sec = VALUES(timeout_sec),
    enabled = VALUES(enabled),
    updated_at = VALUES(updated_at);

-- name: DeleteSchedule :exec
DELETE FROM chetter_schedules
WHERE name = ?;

-- name: SetScheduleLastRun :exec
UPDATE chetter_schedules
SET last_run_at = ?, updated_at = ?
WHERE id = ?;

-- name: InsertScheduleRun :exec
INSERT INTO chetter_schedule_runs (id, schedule_id, team_id, task_id, status, scheduled_for, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListScheduleRunsByTeam :many
SELECT sr.id, sr.schedule_id, s.name AS schedule_name, sr.task_id, sr.status, sr.scheduled_for, sr.created_at
FROM chetter_schedule_runs sr
JOIN chetter_schedules s ON s.id = sr.schedule_id
WHERE s.team_id = sqlc.arg(team_id)
ORDER BY sr.created_at DESC
LIMIT ? OFFSET ?;

-- name: ListScheduleRunsBySchedule :many
SELECT sr.id, sr.schedule_id, s.name AS schedule_name, sr.task_id, sr.status, sr.scheduled_for, sr.created_at
FROM chetter_schedule_runs sr
JOIN chetter_schedules s ON s.id = sr.schedule_id
WHERE sr.schedule_id = ?
ORDER BY sr.created_at DESC
LIMIT ? OFFSET ?;
