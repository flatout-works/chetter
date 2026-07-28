-- +goose Up
ALTER TABLE api_tokens ADD COLUMN expires_at TIMESTAMPTZ NULL;

-- +goose Down
ALTER TABLE api_tokens DROP COLUMN expires_at;
