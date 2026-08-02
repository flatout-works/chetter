-- +goose Up
ALTER TABLE chetter_tasks
    ADD COLUMN IF NOT EXISTS self_test_run_id VARCHAR(64) NULL,
    ADD COLUMN IF NOT EXISTS self_test_profile VARCHAR(32) NULL,
    ADD COLUMN IF NOT EXISTS self_test_check VARCHAR(128) NULL,
    ADD COLUMN IF NOT EXISTS self_test_nonce VARCHAR(128) NULL;

CREATE INDEX IF NOT EXISTS idx_chetter_tasks_self_test_run ON chetter_tasks (self_test_run_id, created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_chetter_tasks_self_test_run;
ALTER TABLE chetter_tasks
    DROP COLUMN IF EXISTS self_test_nonce,
    DROP COLUMN IF EXISTS self_test_check,
    DROP COLUMN IF EXISTS self_test_profile,
    DROP COLUMN IF EXISTS self_test_run_id;
