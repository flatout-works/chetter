---
name: chetter
description: Use Chetter to submit, track, and manage remote agent tasks: runner health, task status, schedules, and cancellation. Triggers on chetter-related workflow requests, slash commands (/chetter-*), task management, and fleet health checks.
---

# Chetter Remote Development Runner Fleet

Chetter is a self-hosted MCP server for running autonomous AI development agents. It gives your AI tooling a way to submit software development work to a fleet of containerized runners.

- **MCP endpoint:** `https://some.ip.name/mcp` (hosted) or your own instance
- **Source repo:** `https://github.com/flatout-works/chetter`

## Invocation Rule: When the User Addresses "Chetter"

If the user directs an instruction at Chetter (for example, messages that start with "Chetter, ...", "Hey Chetter, ...", or any sentence clearly addressed to Chetter), **do not solve the request yourself**. Treat it as a request to delegate the work to the Chetter runner fleet.

1. Rephrase the user's instruction into a clear, self-contained task prompt.
2. Determine the target repository and branch (`git_url` and `git_ref`). If the user did not specify them, use the current repository context or ask for them.
3. Submit the task with `chetter_submit_task`.
4. Tell the user the resulting task ID so they can track it.

For example, if the user says "Chetter, can you create an issue about xxx", do **not** call `gh issue create` yourself. Submit a Chetter task whose prompt instructs the remote agent to create the issue, including the desired title, body, and repository details.

Use a sensible default runner image such as `ghcr.io/flatout-works/chetter-runner:main` if none is specified. If the request implies a schedule or recurring task, use the schedule tools instead.

## Available MCP Tools

All tools are prefixed `chetter_` and available via the `chetter` MCP server.

### Fleet Health
| Tool | Purpose |
|---|---|---|
| `chetter_runner_health` | Runner fleet health, running/stale tasks, image versions, and latest task event age |
| `chetter_list_tasks` | List recent tasks, optional `status` and `trigger_name` filters |
| `chetter_list_triggers` | List triggers (cron schedules and PR review configs) |

### Task Lifecycle
| Tool | Purpose |
|---|---|
| `chetter_submit_task` | Submit a new task to the fleet |
| `chetter_task_status` | Get current status and result for a task |
| `chetter_task_progress` | Get distilled progress timeline |
| `chetter_task_events` | Get full event history |
| `chetter_task_latest_event` | Get most recent event |
| `chetter_cancel_task` | Cancel a pending or running task |
| `chetter_clear_queue` | Clear queued task messages (admin only; requires confirm) |

### Tokens
| Tool | Purpose |
|---|---|---|
| `chetter_create_token` | Create a new API token for a team and user (admin only) |
| `chetter_list_tokens` | List all API tokens with user and team info (admin only) |
| `chetter_delete_token` | Delete an API token by name (admin only) |

### Triggers
| Tool | Purpose |
|---|---|
| `chetter_create_trigger` | Create a trigger (cron schedule or PR review) |
| `chetter_update_trigger` | Update a trigger by name |
| `chetter_list_triggers` | List triggers, optionally by type |
| `chetter_delete_trigger` | Delete a trigger by name |
| `chetter_run_trigger` | Run a cron trigger immediately |

### Definitions
| Tool | Purpose |
|---|---|
| `chetter_get_model_catalog` | Get the active model/provider catalog |
| `chetter_sync_definitions` | Sync the configured definitions repo and reload indexed configs (admin only) |
| `chetter_list_definition_sources` | List Git-backed definition sources |
| `chetter_get_definition_source` | Get a definition source by ID or name |
| `chetter_sync_definition_source` | Sync a definition source (admin only) |
| `chetter_list_definitions` | List active materialized definitions, optionally by type/source |
| `chetter_get_definition` | Get a materialized definition by type and name |
| `chetter_create_definition_proposal` | Create a PR proposing definition file changes |
| `chetter_list_definition_proposals` | List definition change proposals created by Chetter |
| `chetter_get_definition_proposal` | Get proposal details and live PR status when available |

### Arcane (Vulnerability Scanning, Optional)
| Tool | Purpose |
|---|---|
| `chetter_arcane_list_images` | List Docker images in an Arcane environment |
| `chetter_arcane_image_summary` | Vulnerability summary for a specific image |
| `chetter_arcane_environment_summary` | Aggregated vulnerability counts across all images |
| `chetter_arcane_list_vulnerabilities` | Detailed vulnerability list with filtering |
| `chetter_arcane_scanner_status` | Scanner availability and version |

## Common Workflows

### Check Fleet Status
Use `/chetter-status` or ask:
```
Tell me about the ongoing tasks
```
The agent will call `chetter_list_tasks` and `chetter_runner_health` to show what's running, what's done, what's stale, and what failed.

Tasks created by cron triggers, PR reviews, or issue webhooks are stamped with `trigger_name` and `trigger_type` attribution. You can filter by trigger:
```
Show me all tasks from the nightly-docs-update trigger
```
The agent will use `chetter_list_tasks` with `trigger_name="nightly-docs-update"`.

### Submit a Task
Use `/chetter-submit` or ask explicitly. When submitting, specify:
- `git_url`: your repository URL
- `git_ref`: usually `main`
- `agent_image`: your runner image, such as `ghcr.io/your-org/chetter-runner:main`
- `prompt`: clear, scoped instructions
- Optional: `agent`, `provider_id`, `model_id`, `variant_id`, `skills`, `timeout_sec`

### Track a Task
```
Show progress for task task_<id>
Show the latest event for task task_<id>
```

### Diagnose Stale Tasks
A running task is stale in fleet health when `last_event_sec > 600`. Check its events and progress to understand what step it is stuck on. Consider canceling and resubmitting.

### Manage Triggers
Triggers (cron schedules and PR review configs) can be kept as YAML files in your repo for reviewability. Chetter does not read local YAML files directly; use the trigger tools to create or update each trigger.

```
Use chetter_create_trigger with trigger_type=cron to create a trigger from triggers/nightly-changelog-update.yaml
```

### Inspect Definitions
Definitions are indexed from the configured `DEFINITIONS_REPO` into TiDB. To inspect the current registry:
```
List definition sources and show active agent definitions
```
The agent will call `chetter_list_definition_sources` and `chetter_list_definitions` with `definition_type="agent"`.

To refresh the configured source:
```
Sync the default definition source
```
The agent will use `chetter_sync_definition_source`; admin access is required.

To propose durable definition changes, use `chetter_create_definition_proposal` with complete replacement file contents. The tool creates a branch, writes the files, opens a GitHub pull request, and records the proposal. Use `chetter_list_definition_proposals` and `chetter_get_definition_proposal` to track review status.

## Working with Triggers

### Adding a New Cron Trigger

1. Copy an existing sample from `triggers/` as a starting point.
2. Edit it with your repo details and prompt.
3. Create it in Chetter:
   ```
   Use chetter_create_trigger with trigger_type=cron and the fields from triggers/nightly-changelog-update.yaml
   ```

### Adding a New PR Review Trigger

PR review triggers watch a GitHub repository for new pull requests:

```
Use chetter_create_trigger to create a pr_review trigger for flatout-works/chetter
with agent=pr-reviewer, model=opencode/minimax-m3, and prompt "You are performing a deep code review..."
```

### Customizing a Trigger

Each trigger supports these fields:

| Field | Required | Description |
|---|---|---|
| `name` | yes | Unique trigger name (slug) |
| `trigger_type` | yes | `cron` or `pr_review` |
| `enabled` | no | `true` to activate, `false` to pause (default `true`) |
| `cron_expr` | for `cron` | Five-field cron or `@hourly`, `@daily` |
| `repo` | for `pr_review` | Repository to watch (e.g. `flatout-works/chetter`) |
| `prompt` | yes | Task prompt run on each trigger fire |
| `git_url` | no | Repository URL to clone |
| `git_ref` | no | Branch/tag/commit (default main) |
| `agent_image` | no | Runner image override |
| `agent` | no | OpenCode agent to use |
| `provider_id` | no | LLM provider for model selection |
| `model_id` | no | Model ID |
| `variant_id` | no | Model variant |
| `skills` | no | List of skill names to load |
| `timeout_sec` | no | Task timeout in seconds |

### Tweaking an Existing Trigger

To change a trigger's cron expression, prompt, model, or other fields:

1. Edit the `triggers/*.yaml` file directly.
2. Update Chetter with the changed fields:
   ```
   Use chetter_update_trigger to change nightly-changelog-update's model to opencode/minimax-m3
   ```

### Pausing a Trigger
Set `enabled: false` in the YAML and sync, or:
```
Use chetter_update_trigger to disable nightly-issue-fixer
```

### Running a Cron Trigger Manually
```
Use chetter_run_trigger to run the nightly-changelog-update trigger now
```

### Deleting a Trigger
```
Use chetter_delete_trigger to delete nightly-docs-update
```
Remove the corresponding YAML file from your repo if it is no longer part of your desired trigger set.

### Keeping Triggers in Your Repo

The recommended pattern is to store trigger YAMLs in your own repo (not in chetter's `triggers/` directory). When you set up your project:

1. Create a `triggers/` directory in your project repo.
2. Copy the samples from chetter's `triggers/` as starting points.
3. Customize for your project (repo URL, agent image, prompt details).
4. Apply each trigger with `chetter_create_trigger`, or update with `chetter_update_trigger`.

This way your triggers are version-controlled alongside your code and can be reviewed in PRs.

## Safety Rules

- Never send secrets (API keys, tokens, passwords) in task prompts or env vars
- Task prompts must explicitly state whether file edits and PR creation are allowed
- Tell tasks to create branches and PRs rather than pushing to the default branch
- Use `timeout_sec` appropriate for the work (e.g., 600 for quick checks, 3600 for code changes)
- Chetter clones from Git; tasks cannot access uncommitted local changes
- For recurring schedules, check `triggers/` YAMLs into version control

## Model Selection

The provider/model catalog is loaded from a Git definitions repo (`DEFINITIONS_REPO` env var) on server startup and refreshed with `chetter_sync_definitions`. The catalog file `model-catalog.yaml` is generic across harnesses and defines per-harness defaults for OpenCode, Claude Code, Pi, and future harnesses. Catalog entries should reference secret environment variable names (for example `api_key_env: SYNTHETIC_API_KEY`), not secret values. If no definitions repo is configured, Chetter uses a built-in default catalog.

View the current catalog with `chetter_get_model_catalog`.

Common model choices for different task types:

| Task type | Suggested model | Notes |
|---|---|---|
| Docs/changelog | synthetic/kimi-k2.6 | Fast, cheap, good at summarizing |
| PR reviews | opencode/minimax-m3 | Thorough, structured output |
| Code quality / fixes | opencode/deepseek-v4-pro | Good at Go code analysis |
| Bugfixes | deepseek/deepseek-chat | Budget-friendly for simple fixes |

Prefer reliable, cost-effective models for scheduled maintenance tasks. Reserve large/expensive models for complex implementation work.
