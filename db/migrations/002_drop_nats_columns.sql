-- +goose Up
ALTER TABLE chetter_runners
    DROP COLUMN listen_subject,
    DROP COLUMN result_subject;

-- +goose Down
ALTER TABLE chetter_runners
    ADD COLUMN listen_subject VARCHAR(255) NULL AFTER version,
    ADD COLUMN result_subject VARCHAR(255) NULL AFTER listen_subject;
