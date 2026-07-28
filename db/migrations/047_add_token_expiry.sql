-- +goose Up
ALTER TABLE api_tokens ADD COLUMN expires_at DATETIME(6) NULL AFTER user_id;

-- +goose Down
ALTER TABLE api_tokens DROP COLUMN expires_at;
