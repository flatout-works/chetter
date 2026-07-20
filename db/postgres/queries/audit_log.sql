-- name: InsertAuditLog :exec
INSERT INTO chetter_audit_log (id, event_type, created_at, source_type, source_id, target_type, target_id, repo, github_event, github_action, github_delivery_id, parent_event_id, detail, search_text, payload, token_id, token_name)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17);

-- name: ListAuditLog :many
SELECT id, event_type, created_at, source_type, source_id, target_type, target_id, repo, github_event, github_action, github_delivery_id, parent_event_id, detail, payload, token_id, token_name
FROM chetter_audit_log
WHERE (event_type = sqlc.arg(event_type_filter) OR sqlc.arg(event_type_filter) = '')
  AND (source_type = sqlc.arg(source_type_filter) OR sqlc.arg(source_type_filter) = '')
  AND (source_id = sqlc.arg(source_id_filter) OR sqlc.arg(source_id_filter) = '')
  AND (target_type = sqlc.arg(target_type_filter) OR sqlc.arg(target_type_filter) = '')
  AND (target_id = sqlc.arg(target_id_filter) OR sqlc.arg(target_id_filter) = '')
  AND (repo = sqlc.arg(repo_filter) OR sqlc.arg(repo_filter) = '')
  AND (created_at >= sqlc.narg(created_after) OR sqlc.narg(created_after) IS NULL)
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: SearchAuditLog :many
SELECT id, event_type, created_at, source_type, source_id, target_type, target_id, repo, github_event, github_action, github_delivery_id, parent_event_id, detail, payload, token_id, token_name
FROM chetter_audit_log
WHERE (event_type = sqlc.arg(event_type_filter) OR sqlc.arg(event_type_filter) = '')
  AND (source_type = sqlc.arg(source_type_filter) OR sqlc.arg(source_type_filter) = '')
  AND (source_id = sqlc.arg(source_id_filter) OR sqlc.arg(source_id_filter) = '')
  AND (target_type = sqlc.arg(target_type_filter) OR sqlc.arg(target_type_filter) = '')
  AND (target_id = sqlc.arg(target_id_filter) OR sqlc.arg(target_id_filter) = '')
  AND (repo = sqlc.arg(repo_filter) OR sqlc.arg(repo_filter) = '')
  AND (created_at >= sqlc.narg(created_after) OR sqlc.narg(created_after) IS NULL)
  AND search_text ILIKE '%' || sqlc.arg(search) || '%'
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);
