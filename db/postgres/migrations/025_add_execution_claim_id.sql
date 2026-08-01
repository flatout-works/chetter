-- +goose Up
ALTER TABLE chetter_execution_attempts
    ADD COLUMN IF NOT EXISTS claim_id VARCHAR(64) NOT NULL DEFAULT '';

UPDATE chetter_execution_attempts
SET claim_id = 'legacy_' || LEFT(md5(id), 57)
WHERE status = 'running' AND claim_id = '';

CREATE INDEX IF NOT EXISTS idx_execution_attempts_claim ON chetter_execution_attempts (claim_id);

-- +goose Down
DROP INDEX IF EXISTS idx_execution_attempts_claim;
ALTER TABLE chetter_execution_attempts DROP COLUMN IF EXISTS claim_id;
