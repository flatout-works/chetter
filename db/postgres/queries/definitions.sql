-- name: UpsertDefinitionSource :exec
INSERT INTO definition_sources (
    id, name, scope, team_id, repo, repo_url, branch, path, enabled, created_at, updated_at, last_sync_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (name) DO UPDATE SET
    scope = EXCLUDED.scope,
    team_id = EXCLUDED.team_id,
    repo = EXCLUDED.repo,
    repo_url = EXCLUDED.repo_url,
    branch = EXCLUDED.branch,
    path = EXCLUDED.path,
    enabled = EXCLUDED.enabled,
    updated_at = EXCLUDED.updated_at,
    last_sync_at = COALESCE(EXCLUDED.last_sync_at, definition_sources.last_sync_at);

-- name: GetDefinitionSource :one
SELECT * FROM definition_sources WHERE id = $1;

-- name: GetDefinitionSourceByName :one
SELECT * FROM definition_sources WHERE name = $1;

-- name: ListDefinitionSources :many
SELECT * FROM definition_sources ORDER BY scope ASC, name ASC;

-- name: MarkDefinitionSourceSynced :exec
UPDATE definition_sources SET last_sync_at = $1, updated_at = $2 WHERE id = $3;

-- name: DeactivateDefinitionsBySource :exec
UPDATE definitions SET active = false, updated_at = $1 WHERE source_id = $2 AND active = true;

-- name: UpsertDefinition :exec
INSERT INTO definitions (
    id, source_id, definition_type, name, scope, team_id, repo, path, source_commit,
    content_hash, content, metadata, active, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
ON CONFLICT (source_id, definition_type, name, path) DO UPDATE SET
    scope = EXCLUDED.scope,
    team_id = EXCLUDED.team_id,
    repo = EXCLUDED.repo,
    source_commit = EXCLUDED.source_commit,
    content_hash = EXCLUDED.content_hash,
    content = EXCLUDED.content,
    metadata = EXCLUDED.metadata,
    active = EXCLUDED.active,
    updated_at = EXCLUDED.updated_at;

-- name: ListDefinitions :many
SELECT * FROM definitions
WHERE (sqlc.arg(definition_type_filter) = '' OR definition_type = sqlc.arg(definition_type_filter))
  AND (sqlc.arg(source_id_filter) = '' OR source_id = sqlc.arg(source_id_filter))
  AND (sqlc.arg(name_filter) = '' OR name = sqlc.arg(name_filter))
  AND active = true
ORDER BY definition_type ASC, name ASC, scope ASC;

-- name: GetDefinitionBySourceTypeName :one
SELECT * FROM definitions
WHERE source_id = $1 AND definition_type = $2 AND name = $3 AND active = true
  AND (sqlc.arg(scope_filter) = '' OR scope = sqlc.arg(scope_filter))
ORDER BY CASE scope
    WHEN 'global' THEN 0
    WHEN 'team' THEN 1
    WHEN 'repo' THEN 2
    ELSE 3
END, updated_at DESC
LIMIT 1;

-- name: InsertDefinitionSyncRun :exec
INSERT INTO definition_sync_runs (
    id, source_id, status, source_commit, definitions_count, error, started_at, ended_at, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: ListDefinitionSyncRuns :many
SELECT * FROM definition_sync_runs
WHERE (sqlc.arg(source_id_filter) = '' OR source_id = sqlc.arg(source_id_filter))
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit);
