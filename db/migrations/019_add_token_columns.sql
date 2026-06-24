-- +goose Up
ALTER TABLE chetter_tasks ADD COLUMN total_input_tokens BIGINT NOT NULL DEFAULT 0 AFTER session_export;
ALTER TABLE chetter_tasks ADD COLUMN total_output_tokens BIGINT NOT NULL DEFAULT 0 AFTER total_input_tokens;
ALTER TABLE chetter_tasks ADD COLUMN total_cache_read_tokens BIGINT NOT NULL DEFAULT 0 AFTER total_output_tokens;
ALTER TABLE chetter_tasks ADD COLUMN total_cache_write_tokens BIGINT NOT NULL DEFAULT 0 AFTER total_cache_read_tokens;
ALTER TABLE chetter_tasks ADD COLUMN total_reasoning_tokens BIGINT NOT NULL DEFAULT 0 AFTER total_cache_write_tokens;
ALTER TABLE chetter_tasks ADD COLUMN cost_cents BIGINT NOT NULL DEFAULT 0 AFTER total_reasoning_tokens;

-- +goose Down
ALTER TABLE chetter_tasks DROP COLUMN total_input_tokens;
ALTER TABLE chetter_tasks DROP COLUMN total_output_tokens;
ALTER TABLE chetter_tasks DROP COLUMN total_cache_read_tokens;
ALTER TABLE chetter_tasks DROP COLUMN total_cache_write_tokens;
ALTER TABLE chetter_tasks DROP COLUMN total_reasoning_tokens;
ALTER TABLE chetter_tasks DROP COLUMN cost_cents;
