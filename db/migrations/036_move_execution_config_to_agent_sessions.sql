-- +goose Up
ALTER TABLE chetter_agent_sessions
    ADD COLUMN harness VARCHAR(32) NULL AFTER variant_id;
ALTER TABLE chetter_agent_sessions
    ADD COLUMN skills JSON NULL AFTER harness;
ALTER TABLE chetter_agent_sessions
    ADD COLUMN mcp_endpoints JSON NULL AFTER skills;
ALTER TABLE chetter_agent_sessions
    ADD COLUMN env JSON NULL AFTER mcp_endpoints;
ALTER TABLE chetter_agent_sessions
    ADD COLUMN commit_author_name VARCHAR(128) NULL AFTER env;
ALTER TABLE chetter_agent_sessions
    ADD COLUMN commit_author_email VARCHAR(255) NULL AFTER commit_author_name;
ALTER TABLE chetter_agent_sessions
    ADD COLUMN git_identity_id VARCHAR(64) NULL AFTER commit_author_email;

UPDATE chetter_agent_sessions s
JOIN chetter_tasks t ON t.id = s.task_id
SET s.harness = JSON_UNQUOTE(JSON_EXTRACT(t.env, '$.__chetter_harness')),
    s.skills = t.skills,
    s.mcp_endpoints = t.mcp_endpoints,
    s.env = JSON_REMOVE(t.env, '$.__chetter_harness'),
    s.commit_author_name = t.commit_author_name,
    s.commit_author_email = t.commit_author_email,
    s.git_identity_id = t.git_identity_id;

ALTER TABLE chetter_agent_sessions
    MODIFY COLUMN skills JSON NOT NULL,
    MODIFY COLUMN env JSON NOT NULL;

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
    ADD COLUMN agent_image VARCHAR(512) NULL AFTER git_ref;
ALTER TABLE chetter_tasks
    ADD COLUMN agent VARCHAR(128) NULL AFTER agent_image;
ALTER TABLE chetter_tasks
    ADD COLUMN provider_id VARCHAR(128) NULL AFTER agent;
ALTER TABLE chetter_tasks
    ADD COLUMN model_id VARCHAR(255) NULL AFTER provider_id;
ALTER TABLE chetter_tasks
    ADD COLUMN variant_id VARCHAR(128) NULL AFTER model_id;
ALTER TABLE chetter_tasks
    ADD COLUMN commit_author_name VARCHAR(128) NULL AFTER variant_id;
ALTER TABLE chetter_tasks
    ADD COLUMN commit_author_email VARCHAR(255) NULL AFTER commit_author_name;
ALTER TABLE chetter_tasks
    ADD COLUMN git_identity_id VARCHAR(64) NULL AFTER commit_author_email;
ALTER TABLE chetter_tasks
    ADD COLUMN skills JSON NULL AFTER max_attempts;
ALTER TABLE chetter_tasks
    ADD COLUMN mcp_endpoints JSON NULL AFTER skills;
ALTER TABLE chetter_tasks
    ADD COLUMN env JSON NULL AFTER mcp_endpoints;

UPDATE chetter_tasks t
JOIN chetter_agent_sessions s ON s.task_id = t.id
SET t.agent_image = s.agent_image,
    t.agent = s.agent,
    t.provider_id = s.provider_id,
    t.model_id = s.model_id,
    t.variant_id = s.variant_id,
    t.commit_author_name = s.commit_author_name,
    t.commit_author_email = s.commit_author_email,
    t.git_identity_id = s.git_identity_id,
    t.skills = s.skills,
    t.mcp_endpoints = s.mcp_endpoints,
    t.env = IF(s.harness IS NULL, s.env, JSON_SET(s.env, '$.__chetter_harness', s.harness))
WHERE s.sequence = (SELECT MAX(s2.sequence) FROM chetter_agent_sessions s2 WHERE s2.task_id = t.id);

ALTER TABLE chetter_tasks
    MODIFY COLUMN skills JSON NOT NULL,
    MODIFY COLUMN env JSON NOT NULL;

ALTER TABLE chetter_agent_sessions
    DROP COLUMN harness,
    DROP COLUMN skills,
    DROP COLUMN mcp_endpoints,
    DROP COLUMN env,
    DROP COLUMN commit_author_name,
    DROP COLUMN commit_author_email,
    DROP COLUMN git_identity_id;
