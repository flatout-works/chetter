-- name: InsertModelCatalog :exec
INSERT INTO chetter_model_catalogs (id, name, active, source, checksum, yaml, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    active = VALUES(active),
    source = VALUES(source),
    checksum = VALUES(checksum),
    yaml = VALUES(yaml),
    updated_at = VALUES(updated_at);

-- name: DeactivateModelCatalogs :exec
UPDATE chetter_model_catalogs
SET active = false, updated_at = ?
WHERE active = true;

-- name: GetActiveModelCatalog :one
SELECT * FROM chetter_model_catalogs
WHERE active = true
ORDER BY updated_at DESC
LIMIT 1;

-- name: GetModelCatalogByName :one
SELECT * FROM chetter_model_catalogs
WHERE name = ?;

-- name: ListModelCatalogs :many
SELECT * FROM chetter_model_catalogs
ORDER BY updated_at DESC;
