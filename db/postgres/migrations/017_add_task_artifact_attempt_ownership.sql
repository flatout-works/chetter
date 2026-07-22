-- +goose Up
ALTER TABLE chetter_task_artifacts
    ADD COLUMN execution_attempt_id VARCHAR(64) NOT NULL DEFAULT '';

UPDATE chetter_task_artifacts artifact
SET execution_attempt_id = attempt.execution_attempt_id
FROM (
    SELECT user_prompt_id, MIN(id) AS execution_attempt_id
    FROM chetter_execution_attempts
    GROUP BY user_prompt_id
    HAVING COUNT(*) = 1
) attempt
WHERE attempt.user_prompt_id = artifact.user_prompt_id;

DROP INDEX IF EXISTS idx_task_artifacts_dedup;
CREATE UNIQUE INDEX idx_task_artifacts_dedup
    ON chetter_task_artifacts (task_id, artifact_type, repo, number, execution_attempt_id);
CREATE INDEX idx_task_artifacts_execution_attempt
    ON chetter_task_artifacts (execution_attempt_id);

-- +goose Down
DELETE FROM chetter_task_artifacts duplicate
USING chetter_task_artifacts retained
WHERE duplicate.id > retained.id
  AND duplicate.task_id = retained.task_id
  AND duplicate.artifact_type = retained.artifact_type
  AND duplicate.repo = retained.repo
  AND duplicate.number = retained.number;

DROP INDEX IF EXISTS idx_task_artifacts_dedup;
DROP INDEX IF EXISTS idx_task_artifacts_execution_attempt;
ALTER TABLE chetter_task_artifacts DROP COLUMN execution_attempt_id;
CREATE UNIQUE INDEX idx_task_artifacts_dedup
    ON chetter_task_artifacts (task_id, artifact_type, repo, number);
