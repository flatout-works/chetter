-- +goose Up
ALTER TABLE chetter_audit_log ADD COLUMN token_id VARCHAR(64) NULL AFTER payload;
ALTER TABLE chetter_audit_log ADD COLUMN token_name VARCHAR(128) NULL AFTER token_id;
ALTER TABLE chetter_audit_log ADD KEY idx_audit_token (token_id);

-- +goose Down
ALTER TABLE chetter_audit_log DROP KEY idx_audit_token;
ALTER TABLE chetter_audit_log DROP COLUMN token_name;
ALTER TABLE chetter_audit_log DROP COLUMN token_id;
