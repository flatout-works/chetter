-- name: InsertModelCatalog :exec
INSERT INTO chetter_model_catalogs (id, name, active, source, checksum, yaml, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (name) DO UPDATE SET
    active = EXCLUDED.active,
    source = EXCLUDED.source,
    checksum = EXCLUDED.checksum,
    yaml = EXCLUDED.yaml,
    updated_at = EXCLUDED.updated_at;

-- name: DeactivateModelCatalogs :exec
UPDATE chetter_model_catalogs SET active = false, updated_at = $1 WHERE active = true;

-- name: GetActiveModelCatalog :one
SELECT * FROM chetter_model_catalogs WHERE active = true ORDER BY updated_at DESC LIMIT 1;

-- name: GetModelCatalogByName :one
SELECT * FROM chetter_model_catalogs WHERE name = $1;

-- name: ListModelCatalogs :many
SELECT * FROM chetter_model_catalogs ORDER BY updated_at DESC;
