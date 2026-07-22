-- +goose Up
ALTER TABLE chetter_tasks
    DROP COLUMN total_input_tokens,
    DROP COLUMN total_output_tokens,
    DROP COLUMN total_cache_read_tokens,
    DROP COLUMN total_cache_write_tokens,
    DROP COLUMN total_reasoning_tokens,
    DROP COLUMN cost_cents;

-- +goose Down
ALTER TABLE chetter_tasks
    ADD COLUMN total_input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN total_output_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN total_cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN total_cache_write_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN total_reasoning_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN cost_cents BIGINT NOT NULL DEFAULT 0;
