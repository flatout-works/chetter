-- +goose Up
ALTER TABLE chetter_user_prompts
    ADD COLUMN source_user_prompt_id VARCHAR(64);
CREATE INDEX idx_user_prompts_source
    ON chetter_user_prompts (source_user_prompt_id);

-- +goose Down
DROP INDEX IF EXISTS idx_user_prompts_source;
ALTER TABLE chetter_user_prompts
    DROP COLUMN IF EXISTS source_user_prompt_id;
