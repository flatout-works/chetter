# Chetter Database Schema

Current schema of the `chetter` database, as of migration 053 (2026-08-14,
which added event-callback provenance columns to `tasks`; migration 052
dropped the historical `chetter_` table prefix).
The schema is dialect-agnostic (TiDB / MySQL / PostgreSQL) and uses **no
foreign-key constraints** — relationships below are logical, enforced by the
application. All timestamps are UTC (`datetime(6)`). IDs are prefixed random
strings (`task_`, `sess_`, `prompt_`, `exec_`, `evt_`, ...).

## Core task model

```mermaid
erDiagram
    tasks {
        string id PK
        string team_id FK
        string status
        text prompt
        text git_url
        string git_ref
        string github_repo
        bigint github_installation_id
        string trigger_name FK
        string trigger_type
        string submission_source
        string self_test_run_id
        string self_test_profile
        string self_test_check
        string self_test_nonce
        string callback_parent_task_id
        int callback_depth
        int max_attempts
        text summary
        text error
        string error_category
        string failure_message
        string failure_category
        text search_text
        datetime created_at
        datetime updated_at
        datetime ended_at
    }
    agent_sessions {
        string id PK
        string task_id FK
        int sequence
        string team_id FK
        string status
        string resume_mode
        bool isolation_required
        string pinned_runner_id FK
        string pinned_runner_name
        string checkpoint_id FK
        text workspace_path
        string container_name
        string harness_session_id
        text git_url
        string git_ref
        string agent_image
        string agent
        string provider_id
        string model_id
        string variant_id
        string harness
        json skills
        json mcp_endpoints
        json env
        string commit_author_name
        string commit_author_email
        string git_identity_id FK
        string pause_reason
        text summary
        text error
        text search_text
        datetime created_at
        datetime updated_at
        datetime started_at
        datetime ended_at
        datetime paused_at
        datetime expires_at
    }
    user_prompts {
        string id PK
        string agent_session_id FK
        string task_id FK
        int sequence
        string status
        text prompt
        string source_user_prompt_id FK
        text summary
        text error
        text session_export
        datetime created_at
        datetime updated_at
        datetime started_at
        datetime ended_at
    }
    execution_attempts {
        string id PK
        string user_prompt_id FK
        int sequence
        string status
        string runner_id FK
        string claim_id
        string required_runner_id
        datetime claimed_at
        datetime lease_expires_at
        int timeout_sec
        datetime last_event_at
        datetime started_at
        datetime ended_at
        text workspace_path
        string container_name
        string harness_execution_id
        string runner_image_digest
        text summary
        text error
        string error_category
        text session_export
        bigint total_input_tokens
        bigint total_output_tokens
        bigint total_cache_read_tokens
        bigint total_cache_write_tokens
        bigint total_reasoning_tokens
        bigint cost_cents
        datetime created_at
        datetime updated_at
    }
    task_events {
        string id PK
        string task_id FK
        string agent_session_id FK
        string user_prompt_id FK
        string execution_attempt_id FK
        string subject
        string status
        string event_type
        json payload
        datetime created_at
    }
    agent_session_checkpoints {
        string id PK
        string agent_session_id FK
        string user_prompt_id FK
        string runner_id FK
        text checkpoint_path
        text workspace_path
        string container_name
        string runsc_version
        string agent_image
        bigint size_bytes
        string status
        text error
        datetime created_at
        datetime updated_at
        datetime expires_at
    }
    task_artifacts {
        string id PK
        string task_id FK
        string agent_session_id FK
        string user_prompt_id FK
        string execution_attempt_id FK
        string artifact_type
        string repo
        int number
        text url
        string ref
        string sha
        string discovery_source
        text search_text
        datetime created_at
        datetime discovered_at
    }
    agent_sessions }o--|| tasks : "task_id"
    user_prompts }o--|| agent_sessions : "agent_session_id"
    user_prompts }o--|| tasks : "task_id"
    execution_attempts }o--|| user_prompts : "user_prompt_id"
    task_events }o--|| tasks : "task_id"
    agent_session_checkpoints }o--|| agent_sessions : "agent_session_id"
    task_artifacts }o--|| tasks : "task_id"
```

A **task** is a unit of work (prompt + repo context). Each task gets one or
more **agent sessions** (resumable conversations); each session receives one
or more **user prompts**; each prompt is executed by one or more **execution
attempts** (lease-based claims on a runner). **Task events** are the append-
only event log; **checkpoints** persist resumable gVisor checkpoints;
**artifacts** tracks GitHub issues/PRs/comments created by a task.

## Runner fleet

```mermaid
erDiagram
    runners {
        string id PK
        string status
        string image_ref
        string image_digest
        string version
        int max_concurrent
        int running_tasks
        int available_slots
        bool isolation_enabled
        bigint total_started
        bigint total_completed
        bigint total_errors
        datetime started_at
        datetime first_seen_at
        datetime last_seen_at
        datetime updated_at
        json metadata
    }
    execution_attempts {
        string id PK
        string user_prompt_id FK
        string runner_id FK
        string status
    }
    agent_session_checkpoints {
        string id PK
        string runner_id FK
        string agent_session_id FK
    }
    execution_attempts }o--o| runners : "runner_id"
    agent_session_checkpoints }o--|| runners : "runner_id"
```

Runners register and heartbeat via ConnectRPC (upserting this row);
`last_seen_at` older than 60s makes a runner stale. `isolation_enabled`
reflects the runner's advertised gVisor enforcement probe and gates
isolation-requiring tasks at claim time.

## Triggers and automation

```mermaid
erDiagram
    triggers {
        string id PK
        string team_id FK
        string name
        string trigger_type
        json trigger_config
        string cron_expr
        text prompt
        text git_url
        string git_ref
        string agent_image
        string agent
        string provider_id
        string model_id
        string variant_id
        string harness
        json skills
        int timeout_sec
        bool enabled
        string source_id
        datetime created_at
        datetime updated_at
        datetime last_run_at
        datetime next_run_at
    }
    trigger_runs {
        string id PK
        string trigger_id FK
        string team_id FK
        string task_id FK
        string status
        datetime triggered_at
        datetime created_at
    }
    event_callbacks {
        string id PK
        string team_id FK
        string name
        string event_type
        string action_type
        json action_config
        bool enabled
        datetime created_at
        datetime updated_at
    }
    webhook_deliveries {
        string id PK
        string delivery_id
        string event_type
        string event_action
        text payload
        string status
        int attempts
        int max_attempts
        text error
        datetime created_at
        datetime updated_at
        datetime next_attempt_at
        datetime processed_at
    }
    trigger_runs }o--|| triggers : "trigger_id"
    trigger_runs }o--|| tasks : "task_id"
```

**Triggers** fire tasks on cron, PR review, or issue events; each firing is
a **trigger run**. **Event callbacks** react to task lifecycle events with
create_task/webhook/slack actions. **Webhook deliveries** persist inbound
GitHub deliveries for retry/dead-letter handling.

## Teams and auth

```mermaid
erDiagram
    teams {
        string id PK
        string name
        string okta_group_id
        string okta_group_name
        datetime created_at
        datetime updated_at
    }
    users {
        string id PK
        string name
        string team_id FK
        datetime created_at
        datetime updated_at
    }
    user_team_memberships {
        string user_id PK, FK
        string team_id PK, FK
        string source
        datetime created_at
        datetime updated_at
    }
    api_tokens {
        string id PK
        string name
        string token_hash
        string user_id FK
        datetime expires_at
        datetime created_at
        datetime updated_at
    }
    api_token_teams {
        string token_id PK, FK
        string team_id PK, FK
        datetime created_at
    }
    git_identities {
        string id PK
        string team_id FK
        string name
        string git_author_name
        string git_author_email
        string credential_type
        bool is_default
        datetime created_at
        datetime updated_at
    }
    user_team_memberships }o--|| teams : "team_id"
    user_team_memberships }o--|| users : "user_id"
    users }o--|| teams : "team_id"
    api_tokens }o--|| users : "user_id"
    api_token_teams }o--|| api_tokens : "token_id"
    api_token_teams }o--|| teams : "team_id"
    git_identities }o--o| teams : "team_id"
```

API tokens are stored SHA-256 hashed (`token_hash`); tokens belong to one
user and any number of teams (`api_token_teams`). Tasks, triggers,
definitions and callbacks are team-scoped via `team_id`. **Git identities**
are managed author identities for agent commits.

## Definitions and model catalog

```mermaid
erDiagram
    definition_sources {
        string id PK
        string name
        string scope
        string team_id FK
        string repo
        text repo_url
        string branch
        string path
        bool enabled
        datetime last_sync_at
        datetime created_at
        datetime updated_at
    }
    definitions {
        string id PK
        string source_id FK
        string definition_type
        string name
        string scope
        string team_id FK
        string repo
        string path
        string source_commit
        string content_hash
        text content
        json metadata
        bool active
        datetime created_at
        datetime updated_at
    }
    definition_sync_runs {
        string id PK
        string source_id FK
        string status
        string source_commit
        int definitions_count
        text error
        datetime started_at
        datetime ended_at
        datetime created_at
    }
    definition_change_proposals {
        string id PK
        string source_id FK
        string task_id FK
        string repo
        string branch
        string base_branch
        int pr_number
        text pr_url
        string title
        text body
        json files
        string status
        datetime created_at
        datetime updated_at
    }
    model_catalogs {
        string id PK
        string name
        bool active
        string source
        string checksum
        text yaml
        datetime created_at
        datetime updated_at
    }
    definitions }o--|| definition_sources : "source_id"
    definition_sync_runs }o--|| definition_sources : "source_id"
    definition_change_proposals }o--|| definition_sources : "source_id"
```

Git-backed **definition sources** sync agent/skill/trigger/task-template/
MCP-endpoint **definitions** into the database; **sync runs** log each pull;
**change proposals** are task-created PRs against a definition source. The
**model catalog** holds the active provider/model YAML (checksum-gated).

## Audit log

```mermaid
erDiagram
    audit_log {
        string id PK
        string event_type
        datetime created_at
        string source_type
        string source_id
        string target_type
        string target_id
        string repo
        string github_event
        string github_action
        string github_delivery_id
        string parent_event_id
        string token_id FK
        string token_name
        text detail
        text search_text
        json payload
    }
```

Append-only record of server-side events: webhook receipts, trigger matches,
task submissions, session resumes, cancellations, token and trigger changes,
model catalog syncs. `parent_event_id` links causally related events.

## Notes

- **No FK constraints**: deleting/repointing rows is an application-level
  operation; the reaper and sync loops own lifecycle transitions.
- **`search_text` columns** back the Full-Text Search path (`FTS_MATCH_WORD`
  on TiDB, `MATCH ... AGAINST` on MySQL, `LIKE` fallback).
- **`metadata`/`payload`/`config` JSON columns** hold per-row schemaless
  detail (runner resource stats, event payloads, trigger configs).
- **`goose_db_version`** tracks applied migrations (`db/migrations/` for
  MySQL/TiDB, `db/postgres/migrations/` for PostgreSQL). On MySQL/TiDB the
  startup `ensure*` path also adds columns idempotently, so a live schema
  may briefly lead the migration files (see `docs/TIDB-WOWBAGGER.md`).
- All `*_sec` ages in TiDB/MySQL queries use `TIMESTAMPDIFF(SECOND, col,
  NOW())`, so the TiDB/MySQL database session must be in UTC. PostgreSQL
  queries use TIMESTAMPTZ/`NOW()` interval arithmetic and are exempt from
  the UTC session requirement (see the time-zone section of
  `docs/TIDB-WOWBAGGER.md`).
