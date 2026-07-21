-- name: GetTriggerByID :one
SELECT * FROM chetter_triggers
WHERE id = ?;

-- name: GetTriggerByName :one
SELECT * FROM chetter_triggers
WHERE name = ?;

-- name: ListTriggers :many
SELECT * FROM chetter_triggers
ORDER BY created_at DESC;

-- name: ListEnabledTriggers :many
SELECT * FROM chetter_triggers
WHERE enabled = TRUE
ORDER BY created_at DESC;

-- name: ListTriggersByTeam :many
SELECT * FROM chetter_triggers
WHERE team_id = sqlc.arg(team_id)
ORDER BY created_at DESC;

-- name: ListEnabledTriggersByTeam :many
SELECT * FROM chetter_triggers
WHERE team_id = sqlc.arg(team_id)
  AND enabled = TRUE
ORDER BY created_at DESC;

-- name: ListTriggersByTeams :many
SELECT * FROM chetter_triggers
WHERE team_id IN (sqlc.slice(team_ids))
ORDER BY created_at DESC;

-- name: ListEnabledTriggersByTeams :many
SELECT * FROM chetter_triggers
WHERE team_id IN (sqlc.slice(team_ids))
  AND enabled = TRUE
ORDER BY created_at DESC;

-- name: ListEnabledTriggersByType :many
SELECT * FROM chetter_triggers
WHERE enabled = TRUE
  AND trigger_type = sqlc.arg(trigger_type)
ORDER BY created_at DESC;

-- name: ListEnabledPRReviewTriggersByRepo :many
SELECT * FROM chetter_triggers
WHERE enabled = TRUE
  AND trigger_type = 'pr_review'
  AND trigger_config->>'$.repo' = sqlc.arg(repo)
ORDER BY created_at DESC;

-- name: ListEnabledIssueTriggersByRepo :many
SELECT * FROM chetter_triggers
WHERE enabled = TRUE
  AND trigger_type = 'issue'
  AND trigger_config->>'$.repo' = sqlc.arg(repo)
ORDER BY created_at DESC;

-- name: CreateTrigger :exec
INSERT INTO chetter_triggers
    (id, team_id, name, trigger_type, trigger_config, cron_expr, prompt, git_url, git_ref, agent_image, agent, provider_id, model_id, variant_id, harness, skills, timeout_sec, enabled, source_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, TRUE, ?, ?, ?);

-- name: UpdateTrigger :exec
UPDATE chetter_triggers
SET name = sqlc.arg(new_name), trigger_type = ?, trigger_config = ?, cron_expr = ?, prompt = ?,
    git_url = ?, git_ref = ?, agent_image = ?,
    agent = ?, provider_id = ?, model_id = ?, variant_id = ?,
    harness = ?, skills = ?, timeout_sec = ?, enabled = ?,
    updated_at = ?
WHERE name = sqlc.arg(old_name);

-- name: SetTriggerNextRun :exec
UPDATE chetter_triggers
SET next_run_at = ?, updated_at = ?
WHERE id = ?;

-- name: UpsertTrigger :exec
INSERT INTO chetter_triggers
    (id, team_id, name, trigger_type, trigger_config, cron_expr, prompt, git_url, git_ref, agent_image, agent, provider_id, model_id, variant_id, harness, skills, timeout_sec, enabled, source_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
    source_id = VALUES(source_id),
    updated_at = VALUES(updated_at);

-- name: DeleteTrigger :exec
DELETE FROM chetter_triggers
WHERE name = ?;

-- name: DeleteTriggersBySource :exec
DELETE FROM chetter_triggers
WHERE source_id = ?;

-- name: SetTriggerLastRun :exec
UPDATE chetter_triggers
SET last_run_at = ?, updated_at = ?
WHERE id = ?;

-- name: InsertTriggerRun :exec
INSERT INTO chetter_trigger_runs (id, trigger_id, team_id, task_id, status, triggered_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE status = VALUES(status);

-- name: ListTriggerRunsByTeam :many
SELECT sr.id, sr.trigger_id, s.name AS trigger_name, sr.task_id, sr.status, sr.triggered_at, sr.created_at
FROM chetter_trigger_runs sr
JOIN chetter_triggers s ON s.id = sr.trigger_id
WHERE s.team_id = sqlc.arg(team_id)
ORDER BY sr.created_at DESC
LIMIT ? OFFSET ?;

-- name: ListTriggerRunsByTeams :many
SELECT sr.id, sr.trigger_id, s.name AS trigger_name, sr.task_id, sr.status, sr.triggered_at, sr.created_at
FROM chetter_trigger_runs sr
JOIN chetter_triggers s ON s.id = sr.trigger_id
WHERE s.team_id IN (sqlc.slice(team_ids))
ORDER BY sr.created_at DESC
LIMIT ? OFFSET ?;

-- name: ListTriggerRunsByTrigger :many
SELECT sr.id, sr.trigger_id, s.name AS trigger_name, sr.task_id, sr.status, sr.triggered_at, sr.created_at
FROM chetter_trigger_runs sr
JOIN chetter_triggers s ON s.id = sr.trigger_id
WHERE sr.trigger_id = ?
ORDER BY sr.created_at DESC
LIMIT ? OFFSET ?;

-- name: UpdateTriggerRunStatusByTask :exec
UPDATE chetter_trigger_runs
SET status = ?
WHERE task_id = ?;
