-- name: UpsertDefinitionSource :exec
INSERT INTO definition_sources (
    id, name, scope, team_id, repo, repo_url, branch, path, enabled, created_at, updated_at, last_sync_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    scope = VALUES(scope),
    team_id = VALUES(team_id),
    repo = VALUES(repo),
    repo_url = VALUES(repo_url),
    branch = VALUES(branch),
    path = VALUES(path),
    enabled = VALUES(enabled),
    updated_at = VALUES(updated_at),
    last_sync_at = COALESCE(VALUES(last_sync_at), last_sync_at);

-- name: GetDefinitionSource :one
SELECT * FROM definition_sources
WHERE id = ?;

-- name: GetDefinitionSourceByName :one
SELECT * FROM definition_sources
WHERE name = ?;

-- name: ListDefinitionSources :many
SELECT * FROM definition_sources
ORDER BY scope ASC, name ASC;

-- name: MarkDefinitionSourceSynced :exec
UPDATE definition_sources
SET last_sync_at = ?, updated_at = ?
WHERE id = ?;

-- name: DeactivateDefinitionsBySource :exec
UPDATE definitions
SET active = false, updated_at = ?
WHERE source_id = ? AND active = true;

-- name: UpsertDefinition :exec
INSERT INTO definitions (
    id, source_id, definition_type, name, scope, team_id, repo, path, source_commit,
    content_hash, content, metadata, active, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    scope = VALUES(scope),
    team_id = VALUES(team_id),
    repo = VALUES(repo),
    source_commit = VALUES(source_commit),
    content_hash = VALUES(content_hash),
    content = VALUES(content),
    metadata = VALUES(metadata),
    active = VALUES(active),
    updated_at = VALUES(updated_at);

-- name: ListDefinitions :many
SELECT * FROM definitions
WHERE (? = '' OR definition_type = ?)
  AND (? = '' OR source_id = ?)
  AND active = true
ORDER BY definition_type ASC, name ASC, scope ASC;

-- name: GetDefinitionBySourceTypeName :one
SELECT * FROM definitions
WHERE source_id = ? AND definition_type = ? AND name = ? AND active = true
ORDER BY updated_at DESC
LIMIT 1;

-- name: InsertDefinitionSyncRun :exec
INSERT INTO definition_sync_runs (
    id, source_id, status, source_commit, definitions_count, error, started_at, ended_at, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListDefinitionSyncRuns :many
SELECT * FROM definition_sync_runs
WHERE (? = '' OR source_id = ?)
ORDER BY created_at DESC
LIMIT ?;
