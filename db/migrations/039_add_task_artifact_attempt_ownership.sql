-- +goose Up
ALTER TABLE chetter_task_artifacts
    ADD COLUMN execution_attempt_id VARCHAR(64) NOT NULL DEFAULT '' AFTER user_prompt_id;

UPDATE chetter_task_artifacts artifact
JOIN (
    SELECT user_prompt_id, MIN(id) AS execution_attempt_id
    FROM chetter_execution_attempts
    GROUP BY user_prompt_id
    HAVING COUNT(*) = 1
) attempt ON attempt.user_prompt_id = artifact.user_prompt_id
SET artifact.execution_attempt_id = attempt.execution_attempt_id;

ALTER TABLE chetter_task_artifacts
    DROP INDEX idx_task_artifacts_dedup;

ALTER TABLE chetter_task_artifacts
    ADD UNIQUE KEY idx_task_artifacts_dedup (task_id, artifact_type, repo, number, execution_attempt_id),
    ADD KEY idx_task_artifacts_execution_attempt (execution_attempt_id);

-- +goose Down
DELETE duplicate FROM chetter_task_artifacts duplicate
JOIN chetter_task_artifacts retained
  ON duplicate.id > retained.id
 AND duplicate.task_id = retained.task_id
 AND duplicate.artifact_type = retained.artifact_type
 AND duplicate.repo = retained.repo
 AND duplicate.number = retained.number;

ALTER TABLE chetter_task_artifacts
    DROP INDEX idx_task_artifacts_dedup,
    DROP INDEX idx_task_artifacts_execution_attempt;

ALTER TABLE chetter_task_artifacts
    DROP COLUMN execution_attempt_id;

ALTER TABLE chetter_task_artifacts
    ADD UNIQUE KEY idx_task_artifacts_dedup (task_id, artifact_type, repo, number);
