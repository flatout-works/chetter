-- +goose Up
ALTER TABLE chetter_user_prompts
    ADD COLUMN source_user_prompt_id VARCHAR(64) NULL AFTER prompt;
ALTER TABLE chetter_user_prompts
    ADD KEY idx_user_prompts_source (source_user_prompt_id);

-- +goose Down
ALTER TABLE chetter_user_prompts
    DROP INDEX idx_user_prompts_source;
ALTER TABLE chetter_user_prompts
    DROP COLUMN source_user_prompt_id;
