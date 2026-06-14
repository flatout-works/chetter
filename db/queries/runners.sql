-- name: UpsertRunnerHeartbeat :exec
INSERT INTO chetter_runners
    (id, status, image_ref, image_digest, version,
     max_concurrent, running_tasks, available_slots, total_started, total_completed, total_errors,
     started_at, first_seen_at, last_seen_at, updated_at, metadata)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    status = VALUES(status),
    image_ref = VALUES(image_ref),
    image_digest = VALUES(image_digest),
    version = VALUES(version),
    max_concurrent = VALUES(max_concurrent),
    running_tasks = VALUES(running_tasks),
    available_slots = VALUES(available_slots),
    total_started = VALUES(total_started),
    total_completed = VALUES(total_completed),
    total_errors = VALUES(total_errors),
    started_at = COALESCE(VALUES(started_at), started_at),
    last_seen_at = VALUES(last_seen_at),
    updated_at = VALUES(updated_at),
    metadata = VALUES(metadata);

-- name: ListLiveRunners :many
SELECT id, status, image_ref, image_digest, version, max_concurrent, running_tasks, available_slots, total_started, total_completed, total_errors, started_at, first_seen_at, last_seen_at, updated_at, metadata
FROM chetter_runners
WHERE last_seen_at >= ?
ORDER BY last_seen_at DESC;
