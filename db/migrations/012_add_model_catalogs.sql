-- +goose Up
CREATE TABLE IF NOT EXISTS chetter_model_catalogs (
    id VARCHAR(64) NOT NULL,
    name VARCHAR(128) NOT NULL,
    active BOOL NOT NULL DEFAULT false,
    source VARCHAR(255) NULL,
    checksum CHAR(64) NOT NULL,
    yaml MEDIUMTEXT NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_model_catalogs_name (name),
    KEY idx_model_catalogs_active_updated (active, updated_at)
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS chetter_model_catalogs;
