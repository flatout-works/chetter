---
name: chetter
description: Use Chetter to submit, track, recover, and manage remote agent tasks, sessions, triggers, definitions, fleet health, and task-created GitHub artifacts.
---

# Chetter Remote Development Runner Fleet

Chetter is a self-hosted MCP control plane for running autonomous development agents in isolated task containers.

- Source: `https://github.com/flatout-works/chetter`
- Hosted MCP endpoint: `https://some.ip.name/mcp`, or the endpoint for your own instance

## Delegate Requests Addressed to Chetter

When a user clearly addresses an instruction to "Chetter", delegate it to the runner fleet instead of performing it locally.

1. Turn the request into a self-contained task prompt with the desired outcome, repository, constraints, verification, and whether edits or GitHub artifacts are allowed.
2. Determine `git_url` and `git_ref` from the request or current repository context. Ask only when the target is genuinely ambiguous.
3. Submit with `chetter_submit_task`.
4. Return the task ID and, when useful, offer to track it with `chetter_task_progress` or `chetter_task_status`.

Use an agent image containing the selected harness, such as `ghcr.io/flatout-works/chetter-agent-base:main` or `ghcr.io/flatout-works/chetter-agent:golang`. Never use `chetter-runner`: it is the fleet daemon image and does not contain the harness CLIs needed by tasks. Omit `agent_image` when the Chetter instance has a suitable default.

For recurring work, create or change a Chetter `cron` trigger. Do not use a local machine scheduler as a substitute.

## Tool Contexts

### Control-plane clients

Interactive MCP clients may expose the full Chetter API, subject to token scope and optional server integrations. Major tool groups are:

| Area | Tools |
|---|---|
| Tasks | `chetter_submit_task`, `chetter_list_tasks`, `chetter_task_status`, `chetter_task_progress`, `chetter_task_events`, `chetter_task_latest_event`, `chetter_task_export`, `chetter_cancel_task`, `chetter_recover_task` |
| Sessions | `chetter_list_agent_sessions`, `chetter_agent_session_status`, `chetter_resume_agent_session` |
| Fleet | `chetter_runner_health`, `chetter_drain_runner`, `chetter_clear_queue` |
| Triggers | `chetter_create_trigger`, `chetter_update_trigger`, `chetter_list_triggers`, `chetter_delete_trigger`, `chetter_run_trigger`, `chetter_list_trigger_runs` |
| Event callbacks | `chetter_create_event_callback`, `chetter_update_event_callback`, `chetter_list_event_callbacks`, `chetter_delete_event_callback` |
| Definitions | `chetter_list_definition_sources`, `chetter_get_definition_source`, `chetter_sync_definition_source`, `chetter_sync_definitions`, `chetter_list_definitions`, `chetter_get_definition`, `chetter_create_definition_proposal`, `chetter_list_definition_proposals`, `chetter_get_definition_proposal` |
| Git identities | `chetter_create_git_identity`, `chetter_list_git_identities`, `chetter_update_git_identity`, `chetter_delete_git_identity`, `chetter_set_git_identity_default` |
| Administration | `chetter_create_token`, `chetter_list_tokens`, `chetter_delete_token`, `chetter_create_team`, `chetter_list_teams`, `chetter_delete_team`, `chetter_list_users` |
| Observability | `chetter_list_audit_events`, `chetter_list_task_artifacts`, `chetter_usage_summary`, `chetter_get_model_catalog` |
| GitHub artifacts | `chetter_create_issue`, `chetter_issue_comment`, `chetter_create_pr`, `chetter_pr_review` |

Arcane image-scanning tools are present only when the server has Arcane configured: `chetter_arcane_scanner_status`, `chetter_arcane_environment_summary`, `chetter_arcane_list_images`, `chetter_arcane_image_summary`, and `chetter_arcane_list_vulnerabilities`.

Admin-only or unavailable tools may not be visible to team-scoped clients. Use the tools actually exposed by the current MCP connection.

### Agents running inside tasks

OpenCode task agents receive a restricted subset of the remote Chetter tools. They can inspect tasks, sessions, fleet health, triggers, definitions, audit/usage data, and Arcane data; submit/recover/resume tasks; create definition proposals; and create GitHub artifacts. They cannot administer teams, tokens, identities, triggers, callbacks, the queue, or runners.

Task agents also receive runner-bridge versions of these artifact tools, with task context injected automatically:

- `chetter_create_issue`
- `chetter_issue_comment`
- `chetter_create_pr`
- `chetter_pr_review`

Use these tools instead of `gh issue create`, `gh issue comment`, `gh pr create`, or `gh pr review`. They add the canonical Chetter signature and record artifact/audit metadata. Standard harness file tools handle workspace reads and edits; the runner bridge does not provide `workspace_*` tools.

## Submit Tasks

Useful `chetter_submit_task` fields include:

- `prompt`: clear, scoped instructions
- `git_url`, `git_ref`: committed Git state to clone
- `agent_image`: optional agent image override, never the runner daemon image
- `harness`: `opencode`, `claude-code`, `pi`, `codewhale`, or `codex`
- `agent`, `provider_id`, `model_id`, `variant_id`
- `skills`, `mcp_endpoints`: names resolved from applicable Git-backed definitions
- `env`: non-secret environment values only
- `timeout_sec`
- `session_mode: resumable`, `pause_reason`, `ttl_hours`: retain an eligible gVisor task container for follow-up work

Chetter clones committed Git state. It cannot see local uncommitted changes. Put all required context in the prompt or repository, and never put secrets in prompts or `env`.

## Track, Recover, and Resume

- Use `chetter_task_progress` for a concise timeline and `chetter_task_events` for full diagnostics.
- Use `chetter_task_export` to inspect a completed task transcript.
- Use `chetter_recover_task` after a failed task when a fresh task should receive the previous session export as workspace context.
- Use `chetter_resume_agent_session` only for paused or recoverable sessions. Resumable sessions require gVisor and expire according to their TTL.
- Use `chetter_runner_health` to distinguish task failures from stale runners or old heartbeats.

## Triggers

Supported trigger types are:

- `cron`: requires `cron_expr`
- `pr_review`: requires `repo`; `event` can narrow webhook actions
- `issue`: requires `repo`; supports `event` and `match_labels`

Trigger run configuration can include the same harness, model, image, skills, timeout, and resumable-session fields as task submission.

Git-backed trigger definitions are the durable source of truth. Store them under the applicable scope:

- `global/triggers/*.yaml`
- `groups/<team>/triggers/*.yaml`
- `repos/<owner>/<repo>/triggers/*.yaml`

After merging changes, call `chetter_sync_definition_source` (or the legacy default-source `chetter_sync_definitions`) to materialize them. Trigger names must be unique across the instance; scope does not namespace the trigger table. Use direct create/update/delete tools for explicit operational changes, not as a replacement for reviewed definition files.

## Definitions

Chetter materializes these Git-backed definition types: `agent`, `skill`, `trigger`, `task_template`, and `mcp_endpoint`. Definitions resolve by global, team, and repository scope, with the most specific applicable definition winning.

Use `chetter_create_definition_proposal` for durable changes from a task agent. It accepts complete replacement file contents, creates a branch and pull request, and records the proposal. Track it with `chetter_list_definition_proposals` and `chetter_get_definition_proposal`.

Agents that declare an `identity` require a matching server-managed Git identity. MCP endpoints and skills referenced by a task or agent must exist in an applicable scope; missing skill definitions are not useful hints and should be removed or added properly.

## Event Callbacks

Event callbacks react to task event patterns such as `task.completed`, `task.failed.*`, or `task.failed.model_error`. Supported actions are `create_task`, `webhook`, and `slack`. Callback action configuration is JSON. Keep webhook credentials in server-managed configuration, never in definition files or task prompts.

## Safety

- State whether edits, commits, pushes, issues, comments, reviews, or pull requests are allowed.
- Prefer a branch and reviewed PR over direct pushes to a default branch.
- Do not submit secrets in prompts or task `env`.
- Check for an existing issue or PR before scheduled automation creates another.
- Use realistic timeouts: about 600 seconds for quick inspection and 3600 or more for implementation.
- Treat Arcane, GitHub App, managed identities, and resumable sessions as optional deployment capabilities; handle their absence explicitly.

## Model Selection

Use `chetter_get_model_catalog` as the authority for available providers, models, harness defaults, and variants. Do not hard-code model recommendations in automation guidance: the catalog changes independently and explicit trigger/task selections override defaults.
