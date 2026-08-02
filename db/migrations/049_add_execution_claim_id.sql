-- +goose Up
ALTER TABLE chetter_execution_attempts
    ADD COLUMN claim_id VARCHAR(64) NOT NULL DEFAULT '' AFTER runner_id;

UPDATE chetter_execution_attempts
SET claim_id = CONCAT('legacy_', LEFT(MD5(id), 57))
WHERE status = 'running' AND claim_id = '';

CREATE INDEX idx_execution_attempts_claim ON chetter_execution_attempts (claim_id);

-- +goose Down
DROP INDEX idx_execution_attempts_claim ON chetter_execution_attempts;
ALTER TABLE chetter_execution_attempts DROP COLUMN claim_id;
