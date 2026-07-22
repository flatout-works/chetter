-- +goose Up
ALTER TABLE chetter_agent_sessions
    ADD COLUMN harness VARCHAR(32) NULL,
    ADD COLUMN skills JSONB NULL,
    ADD COLUMN mcp_endpoints JSONB NULL,
    ADD COLUMN env JSONB NULL,
    ADD COLUMN commit_author_name VARCHAR(128) NULL,
    ADD COLUMN commit_author_email VARCHAR(255) NULL,
    ADD COLUMN git_identity_id VARCHAR(64) NULL;

UPDATE chetter_agent_sessions s
SET harness = t.env->>'__chetter_harness',
    skills = t.skills,
    mcp_endpoints = t.mcp_endpoints,
    env = t.env - '__chetter_harness',
    commit_author_name = t.commit_author_name,
    commit_author_email = t.commit_author_email,
    git_identity_id = t.git_identity_id
FROM chetter_tasks t
WHERE t.id = s.task_id;

ALTER TABLE chetter_agent_sessions
    ALTER COLUMN skills SET NOT NULL,
    ALTER COLUMN env SET NOT NULL;

ALTER TABLE chetter_tasks
    DROP COLUMN agent_image,
    DROP COLUMN agent,
    DROP COLUMN provider_id,
    DROP COLUMN model_id,
    DROP COLUMN variant_id,
    DROP COLUMN commit_author_name,
    DROP COLUMN commit_author_email,
    DROP COLUMN git_identity_id,
    DROP COLUMN skills,
    DROP COLUMN mcp_endpoints,
    DROP COLUMN env;

-- +goose Down
ALTER TABLE chetter_tasks
    ADD COLUMN agent_image VARCHAR(512) NULL,
    ADD COLUMN agent VARCHAR(128) NULL,
    ADD COLUMN provider_id VARCHAR(128) NULL,
    ADD COLUMN model_id VARCHAR(255) NULL,
    ADD COLUMN variant_id VARCHAR(128) NULL,
    ADD COLUMN commit_author_name VARCHAR(128) NULL,
    ADD COLUMN commit_author_email VARCHAR(255) NULL,
    ADD COLUMN git_identity_id VARCHAR(64) NULL,
    ADD COLUMN skills JSONB NULL,
    ADD COLUMN mcp_endpoints JSONB NULL,
    ADD COLUMN env JSONB NULL;

UPDATE chetter_tasks t
SET agent_image = s.agent_image,
    agent = s.agent,
    provider_id = s.provider_id,
    model_id = s.model_id,
    variant_id = s.variant_id,
    commit_author_name = s.commit_author_name,
    commit_author_email = s.commit_author_email,
    git_identity_id = s.git_identity_id,
    skills = s.skills,
    mcp_endpoints = s.mcp_endpoints,
    env = CASE WHEN s.harness IS NULL THEN s.env ELSE s.env || jsonb_build_object('__chetter_harness', s.harness) END
FROM chetter_agent_sessions s
WHERE s.task_id = t.id
  AND s.sequence = (SELECT MAX(s2.sequence) FROM chetter_agent_sessions s2 WHERE s2.task_id = t.id);

ALTER TABLE chetter_tasks
    ALTER COLUMN skills SET NOT NULL,
    ALTER COLUMN env SET NOT NULL;

ALTER TABLE chetter_agent_sessions
    DROP COLUMN harness,
    DROP COLUMN skills,
    DROP COLUMN mcp_endpoints,
    DROP COLUMN env,
    DROP COLUMN commit_author_name,
    DROP COLUMN commit_author_email,
    DROP COLUMN git_identity_id;
