-- callback_deliveries is the durable outbound queue for webhook/slack event
-- callback actions (issue #357). Rows are inserted transactionally with their
-- task_events row and claimed by a leased multi-replica delivery worker.
-- Statuses: pending, in_flight, completed, failed, dead_letter.

-- name: InsertCallbackDelivery :exec
INSERT INTO callback_deliveries
    (id, callback_id, event_id, task_id, team_id, event_type, endpoint_url, method, headers,
     payload, status, attempts, max_attempts, error, lease_expires_at, next_attempt_at,
     processed_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE id = id;

-- name: MarkCallbackDeliveryInFlight :execrows
UPDATE callback_deliveries
SET status = 'in_flight',
    lease_expires_at = sqlc.arg(lease_expires_at),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND (
    status IN ('pending', 'failed')
    OR (status = 'in_flight' AND lease_expires_at < sqlc.arg(now))
  );

-- name: MarkCallbackDeliverySucceeded :execrows
UPDATE callback_deliveries
SET status = 'completed',
    error = NULL,
    lease_expires_at = NULL,
    next_attempt_at = NULL,
    processed_at = sqlc.arg(processed_at),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND status = 'in_flight';

-- name: FailCallbackDelivery :execrows
UPDATE callback_deliveries
SET status = sqlc.arg(status),
    attempts = attempts + 1,
    error = sqlc.arg(error),
    lease_expires_at = NULL,
    next_attempt_at = sqlc.arg(next_attempt_at),
    processed_at = NULL,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND status = 'in_flight';
