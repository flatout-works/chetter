-- name: GetTriggerByID :one
SELECT * FROM chetter_triggers WHERE id = $1;

-- name: GetTriggerByName :one
SELECT * FROM chetter_triggers WHERE name = $1;

-- name: ListTriggers :many
SELECT * FROM chetter_triggers ORDER BY created_at DESC;

-- name: ListEnabledTriggers :many
SELECT * FROM chetter_triggers WHERE enabled = true ORDER BY created_at DESC;

-- name: ListTriggersByTeam :many
SELECT * FROM chetter_triggers WHERE team_id = sqlc.arg(team_id) ORDER BY created_at DESC;

-- name: ListEnabledTriggersByTeam :many
SELECT * FROM chetter_triggers
WHERE team_id = sqlc.arg(team_id) AND enabled = true
ORDER BY created_at DESC;

-- name: ListTriggersByTeams :many
SELECT * FROM chetter_triggers
WHERE team_id = ANY(sqlc.arg(team_ids)::text[])
ORDER BY created_at DESC;

-- name: ListEnabledTriggersByTeams :many
SELECT * FROM chetter_triggers
WHERE team_id = ANY(sqlc.arg(team_ids)::text[]) AND enabled = true
ORDER BY created_at DESC;

-- name: ListEnabledTriggersByType :many
SELECT * FROM chetter_triggers
WHERE enabled = true AND trigger_type = sqlc.arg(trigger_type)
ORDER BY created_at DESC;

-- name: ListEnabledPRReviewTriggersByRepo :many
SELECT * FROM chetter_triggers
WHERE enabled = true
  AND trigger_type = 'pr_review'
  AND trigger_config ->> 'repo' = sqlc.arg(repo)
ORDER BY created_at DESC;

-- name: ListEnabledIssueTriggersByRepo :many
SELECT * FROM chetter_triggers
WHERE enabled = true
  AND trigger_type = 'issue'
  AND trigger_config ->> 'repo' = sqlc.arg(repo)
ORDER BY created_at DESC;

-- name: CreateTrigger :exec
INSERT INTO chetter_triggers
    (id, team_id, name, trigger_type, trigger_config, cron_expr, prompt, git_url, git_ref, agent_image, agent, provider_id, model_id, variant_id, harness, skills, timeout_sec, enabled, source_id, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, true, $18, $19, $20);

-- name: UpdateTrigger :exec
UPDATE chetter_triggers
SET name = sqlc.arg(new_name),
    trigger_type = $1,
    trigger_config = $2,
    cron_expr = $3,
    prompt = $4,
    git_url = $5,
    git_ref = $6,
    agent_image = $7,
    agent = $8,
    provider_id = $9,
    model_id = $10,
    variant_id = $11,
    harness = $12,
    skills = $13,
    timeout_sec = $14,
    enabled = $15,
    updated_at = $16
WHERE name = sqlc.arg(old_name);

-- name: SetTriggerNextRun :exec
UPDATE chetter_triggers SET next_run_at = $1, updated_at = $2 WHERE id = $3;

-- name: UpsertTrigger :exec
INSERT INTO chetter_triggers
    (id, team_id, name, trigger_type, trigger_config, cron_expr, prompt, git_url, git_ref, agent_image, agent, provider_id, model_id, variant_id, harness, skills, timeout_sec, enabled, source_id, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
ON CONFLICT (name) DO UPDATE SET
    trigger_type = EXCLUDED.trigger_type,
    trigger_config = EXCLUDED.trigger_config,
    cron_expr = EXCLUDED.cron_expr,
    prompt = EXCLUDED.prompt,
    git_url = EXCLUDED.git_url,
    git_ref = EXCLUDED.git_ref,
    agent_image = EXCLUDED.agent_image,
    agent = EXCLUDED.agent,
    provider_id = EXCLUDED.provider_id,
    model_id = EXCLUDED.model_id,
    variant_id = EXCLUDED.variant_id,
    harness = EXCLUDED.harness,
    skills = EXCLUDED.skills,
    timeout_sec = EXCLUDED.timeout_sec,
    enabled = EXCLUDED.enabled,
    source_id = EXCLUDED.source_id,
    updated_at = EXCLUDED.updated_at;

-- name: DeleteTrigger :exec
DELETE FROM chetter_triggers WHERE name = $1;

-- name: DeleteTriggersBySource :exec
DELETE FROM chetter_triggers WHERE source_id = $1;

-- name: SetTriggerLastRun :exec
UPDATE chetter_triggers SET last_run_at = $1, updated_at = $2 WHERE id = $3;

-- name: InsertTriggerRun :exec
INSERT INTO chetter_trigger_runs (id, trigger_id, team_id, task_id, status, triggered_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (trigger_id, task_id) DO NOTHING;

-- name: ListTriggerRunsByTeam :many
SELECT sr.id, sr.trigger_id, s.name AS trigger_name, sr.task_id, sr.status, sr.triggered_at, sr.created_at
FROM chetter_trigger_runs sr
JOIN chetter_triggers s ON s.id = sr.trigger_id
WHERE s.team_id = sqlc.arg(team_id)
ORDER BY sr.created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: ListTriggerRunsByTeams :many
SELECT sr.id, sr.trigger_id, s.name AS trigger_name, sr.task_id, sr.status, sr.triggered_at, sr.created_at
FROM chetter_trigger_runs sr
JOIN chetter_triggers s ON s.id = sr.trigger_id
WHERE s.team_id = ANY(sqlc.arg(team_ids)::text[])
ORDER BY sr.created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: ListTriggerRunsByTrigger :many
SELECT sr.id, sr.trigger_id, s.name AS trigger_name, sr.task_id, sr.status, sr.triggered_at, sr.created_at
FROM chetter_trigger_runs sr
JOIN chetter_triggers s ON s.id = sr.trigger_id
WHERE sr.trigger_id = $1
ORDER BY sr.created_at DESC
LIMIT $2 OFFSET $3;
