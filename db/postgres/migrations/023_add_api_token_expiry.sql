-- +goose Up
ALTER TABLE api_tokens ADD COLUMN expires_at TIMESTAMPTZ NULL;
CREATE INDEX idx_api_tokens_expires ON api_tokens (expires_at);

-- +goose Down
DROP INDEX idx_api_tokens_expires;
ALTER TABLE api_tokens DROP COLUMN expires_at;
