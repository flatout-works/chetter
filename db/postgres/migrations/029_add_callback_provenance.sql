-- +goose Up
-- Event-callback provenance columns (issue #312): see db/migrations/053 for
-- details. PostgreSQL migrations use no AFTER clauses and are unaffected by
-- the TiDB multi-column restriction.
ALTER TABLE tasks
    ADD COLUMN IF NOT EXISTS callback_parent_task_id VARCHAR(64) NULL;
ALTER TABLE tasks
    ADD COLUMN IF NOT EXISTS callback_depth INT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE tasks
    DROP COLUMN IF EXISTS callback_depth;
ALTER TABLE tasks
    DROP COLUMN IF EXISTS callback_parent_task_id;
