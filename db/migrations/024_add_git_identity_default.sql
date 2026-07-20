-- +goose Up
ALTER TABLE git_identities ADD COLUMN is_default BOOL NOT NULL DEFAULT false AFTER credential_type;

-- +goose Down
ALTER TABLE git_identities DROP COLUMN is_default;
