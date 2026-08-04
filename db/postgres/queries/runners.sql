-- name: UpsertRunnerHeartbeat :exec
INSERT INTO runners
    (id, status, image_ref, image_digest, version,
     max_concurrent, running_tasks, available_slots, isolation_enabled, total_started, total_completed, total_errors,
     started_at, first_seen_at, last_seen_at, updated_at, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
ON CONFLICT (id) DO UPDATE SET
    status = EXCLUDED.status,
    image_ref = EXCLUDED.image_ref,
    image_digest = EXCLUDED.image_digest,
    version = EXCLUDED.version,
    max_concurrent = EXCLUDED.max_concurrent,
    running_tasks = EXCLUDED.running_tasks,
    available_slots = EXCLUDED.available_slots,
    isolation_enabled = EXCLUDED.isolation_enabled,
    total_started = EXCLUDED.total_started,
    total_completed = EXCLUDED.total_completed,
    total_errors = EXCLUDED.total_errors,
    started_at = COALESCE(EXCLUDED.started_at, runners.started_at),
    last_seen_at = EXCLUDED.last_seen_at,
    updated_at = EXCLUDED.updated_at,
    metadata = EXCLUDED.metadata;

-- name: GetRunnerHeartbeatMetadata :one
SELECT metadata
FROM runners
WHERE id = $1;

-- name: ListLiveRunners :many
SELECT id, status, image_ref, image_digest, version, max_concurrent, running_tasks, available_slots, total_started, total_completed, total_errors, started_at, first_seen_at, last_seen_at, updated_at, metadata
FROM runners
WHERE last_seen_at >= $1
ORDER BY last_seen_at DESC;

-- name: GetRunnerIsolationEnabled :one
SELECT isolation_enabled
FROM runners
WHERE id = $1;
