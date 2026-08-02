-- +goose Up
ALTER TABLE chetter_tasks
    ADD COLUMN self_test_run_id VARCHAR(64) NULL AFTER submission_source,
    ADD COLUMN self_test_profile VARCHAR(32) NULL AFTER self_test_run_id,
    ADD COLUMN self_test_check VARCHAR(128) NULL AFTER self_test_profile,
    ADD COLUMN self_test_nonce VARCHAR(128) NULL AFTER self_test_check;

CREATE INDEX idx_chetter_tasks_self_test_run ON chetter_tasks (self_test_run_id, created_at);

-- +goose Down
DROP INDEX idx_chetter_tasks_self_test_run ON chetter_tasks;
ALTER TABLE chetter_tasks
    DROP COLUMN self_test_nonce,
    DROP COLUMN self_test_check,
    DROP COLUMN self_test_profile,
    DROP COLUMN self_test_run_id;
