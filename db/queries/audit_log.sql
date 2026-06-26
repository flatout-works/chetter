-- name: InsertAuditLog :exec
INSERT INTO chetter_audit_log (id, event_type, created_at, source_type, source_id, target_type, target_id, repo, github_event, github_action, github_delivery_id, parent_event_id, detail, search_text, payload)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAuditLog :many
SELECT id, event_type, created_at, source_type, source_id, target_type, target_id, repo, github_event, github_action, github_delivery_id, parent_event_id, detail, payload
FROM chetter_audit_log
WHERE (event_type = ? OR ? = '')
  AND (source_type = ? OR ? = '')
  AND (source_id = ? OR ? = '')
  AND (target_type = ? OR ? = '')
  AND (target_id = ? OR ? = '')
  AND (repo = ? OR ? = '')
  AND (created_at >= ? OR ? IS NULL)
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: SearchAuditLog :many
SELECT id, event_type, created_at, source_type, source_id, target_type, target_id, repo, github_event, github_action, github_delivery_id, parent_event_id, detail, payload
FROM chetter_audit_log
WHERE (event_type = ? OR ? = '')
  AND (source_type = ? OR ? = '')
  AND (source_id = ? OR ? = '')
  AND (target_type = ? OR ? = '')
  AND (target_id = ? OR ? = '')
  AND (repo = ? OR ? = '')
  AND (created_at >= ? OR ? IS NULL)
  AND (search_text LIKE CONCAT('%', sqlc.arg(search), '%'))
ORDER BY created_at DESC
LIMIT ? OFFSET ?;
