---
name: chetter
description: Use Chetter to submit, track, and manage remote agent tasks: runner health, task status, schedules, and cancellation. Triggers on chetter-related workflow requests, slash commands (/chetter-*), task management, and fleet health checks.
---

# Chetter Remote Development Runner Fleet

Chetter is a self-hosted MCP server for running autonomous AI development agents. It gives your AI tooling a way to submit software development work to a fleet of containerized runners.

- **MCP endpoint:** `https://some.ip.name/mcp` (hosted) or your own instance
- **Source repo:** `https://github.com/flatout-works/chetter`

## Available MCP Tools

All tools are prefixed `chetter_` and available via the `chetter` MCP server.

### Fleet Health
| Tool | Purpose |
|---|---|
| `chetter_runner_health` | Runner fleet health, running/stale tasks, image versions, and latest task event age |
| `chetter_list_tasks` | List recent tasks, optional status filter |
| `chetter_list_schedules` | List cron task schedules |

### Task Lifecycle
| Tool | Purpose |
|---|---|
| `chetter_submit_task` | Submit a new task to the fleet |
| `chetter_task_status` | Get current status and result for a task |
| `chetter_task_progress` | Get distilled progress timeline |
| `chetter_task_events` | Get full event history |
| `chetter_task_latest_event` | Get most recent event |
| `chetter_cancel_task` | Cancel a pending or running task |
| `chetter_clear_queue` | Clear queued task messages (requires confirm) |

### Schedules
| Tool | Purpose |
|---|---|
| `chetter_schedule_task` | Create/activate a cron schedule |
| `chetter_update_schedule` | Update a schedule by name |
| `chetter_run_schedule` | Run a schedule immediately |
| `chetter_delete_schedule` | Delete a schedule by name |

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

### Manage Schedules
Schedules can be kept as YAML files in your repo for reviewability. Chetter does not read local YAML files directly; use the schedule tools to create or update each schedule.

```
Use chetter_schedule_task to create a schedule from schedules/nightly-changelog-update.yaml
```

## Working with Schedules

### Adding a New Schedule

1. Copy an existing sample from `schedules/` as a starting point.
2. Edit it with your repo details and prompt.
3. Create it in Chetter:
   ```
   Use chetter_schedule_task with the fields from schedules/nightly-changelog-update.yaml
   ```

### Customizing a Schedule

Each schedule YAML supports these fields:

| Field | Required | Description |
|---|---|---|
| `name` | yes | Unique schedule name (slug) |
| `enabled` | no | `true` to activate, `false` to pause (default false) |
| `cron_expr` | yes | Five-field cron or `@hourly`, `@daily` |
| `prompt` | yes | Task prompt run on each cron fire |
| `git_url` | yes | Repository URL to clone |
| `git_ref` | no | Branch/tag/commit (default main) |
| `agent_image` | no | Runner image override |
| `agent` | no | OpenCode agent to use |
| `provider_id` | no | LLM provider for model selection |
| `model_id` | no | Model ID |
| `variant_id` | no | Model variant |
| `skills` | no | List of skill names to load |
| `timeout_sec` | no | Task timeout in seconds |

### Tweaking an Existing Schedule

To change a schedule's cron expression, prompt, model, or other fields:

1. Edit the `schedules/*.yaml` file directly.
2. Update Chetter with the changed fields:
   ```
   Use chetter_update_schedule to change nightly-changelog-update's model to opencode/minimax-m3
   ```

### Pausing a Schedule
Set `enabled: false` in the YAML and sync, or:
```
Use chetter_update_schedule to disable nightly-issue-fixer
```

### Running a Schedule Manually
```
Use chetter_run_schedule to run the nightly-changelog-update schedule now
```

### Deleting a Schedule
```
Use chetter_delete_schedule to delete nightly-docs-update
```
Remove the corresponding YAML file from your repo if it is no longer part of your desired schedule set.

### Keeping Schedules in Your Repo

The recommended pattern is to store schedule YAMLs in your own repo (not in chetter's `schedules/` directory). When you set up your project:

1. Create a `schedules/` directory in your project repo.
2. Copy the samples from chetter's `schedules/` as starting points.
3. Customize for your project (repo URL, agent image, prompt details).
4. Apply each schedule with `chetter_schedule_task` or `chetter_update_schedule`.

This way your schedules are version-controlled alongside your code and can be reviewed in PRs.

## Safety Rules

- Never send secrets (API keys, tokens, passwords) in task prompts or env vars
- Task prompts must explicitly state whether file edits and PR creation are allowed
- Tell tasks to create branches and PRs rather than pushing to the default branch
- Use `timeout_sec` appropriate for the work (e.g., 600 for quick checks, 3600 for code changes)
- Chetter clones from Git; tasks cannot access uncommitted local changes
- For recurring schedules, check `schedules/` YAMLs into version control

## Model Selection

Common model choices for different task types:

| Task type | Suggested model | Notes |
|---|---|---|
| Docs/changelog | synthetic/kimi-k2.6 | Fast, cheap, good at summarizing |
| PR reviews | opencode-go/minimax-m3 | Thorough, structured output |
| Code quality / fixes | opencode/deepseek-v4-pro | Good at Go code analysis |
| Bugfixes | deepseek/deepseek-chat | Budget-friendly for simple fixes |

Prefer reliable, cost-effective models for scheduled maintenance tasks. Reserve large/expensive models for complex implementation work.
