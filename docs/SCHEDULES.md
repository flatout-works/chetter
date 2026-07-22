# Chetter Schedules

Chetter supports cron-backed schedules (a `cron`-type trigger) that automatically submit tasks at recurring intervals. Schedules are created and managed using the same trigger tools used for PR review webhooks.

---

## What Is a Schedule?

A schedule is a **persisted task template** with a cron expression. On each cron fire, Chetter:

1. Creates a new task from the template (same prompt, git_url, git_ref, agent_image, etc.)
2. Stamps it with the team's `team_id` (if the schedule was created with a scoped token)
3. Queues it as a pending task for runners to claim
4. Records the run in `chetter_schedule_runs`

---

## Schedule Fields

| Field | Required | Default | Description |
|---|---|---|---|
| `name` | Yes | — | Unique schedule name. Used as the stable identifier for updates and deletion. |
| `cron_expr` | Yes | — | 5-field cron expression (e.g. `0 4 * * *`) or descriptor (`@hourly`, `@daily`). Parsed by `robfig/cron` in UTC. |
| `prompt` | Yes | — | The task prompt. Executed by the runner's configured harness on each fire. |
| `git_url` | No | — | Repository URL to clone before running the task. |
| `git_ref` | No | `main` | Branch, tag, or commit to check out. |
| `agent_image` | Yes | — | Runner Docker image. Falls back to `DEFAULT_AGENT_IMAGE` if omitted and configured. |
| `agent` | No | — | Agent definition name (e.g. `changelog-maintainer`). |
| `harness` | No | — | Runner harness: `opencode`, `claude-code`, `pi`, `codewhale`, or `codex`. Defaults to the runner's `execution.harness` config. |
| `provider_id` | No | — | LLM provider (e.g. \`opencode\`). |
| `model_id` | No | — | LLM model (e.g. `deepseek-v4-pro`). |
| `variant_id` | No | — | Model variant (e.g. `high`, `minimal`). |
| `skills` | No | `[]` | Array of skill names passed to the runner. |
| `timeout_sec` | No | `600` | Task timeout in seconds. Defaults to `DEFAULT_TASK_TIMEOUT_SEC`. |
| `enabled` | No | `true` | Whether the schedule is active. Disabled schedules do not fire. |

---

## Managing Schedules via Trigger Tools

### Create a Schedule (Cron Trigger)

**Tool:** `chetter_create_trigger` (with `trigger_type: cron`)

Example input:

```json
{
  "name": "nightly-docs-update",
  "trigger_type": "cron",
  "cron_expr": "0 4 * * *",
  "prompt": "Review recent repository changes and update documentation...",
  "git_url": "https://github.com/flatout-works/chetter",
  "git_ref": "main",
  "agent_image": "ghcr.io/flatout-works/chetter-runner:main",
  "agent": "docs-maintainer",
  "timeout_sec": 3600
}
```

### List Schedules (Cron Triggers)

**Tool:** `chetter_list_triggers` (with `trigger_type: cron`)

- `trigger_type: "cron"` — returns only cron schedules
- `enabled_only: true` — returns only enabled triggers

### Run a Schedule Immediately

**Tool:** `chetter_run_trigger`

```json
{"name": "nightly-docs-update"}
```

This submits one task from the cron trigger right now, without waiting for the cron expression.

### Update a Schedule

**Tool:** `chetter_update_trigger`

```json
{
  "name": "nightly-docs-update",
  "cron_expr": "0 5 * * *",
  "enabled": false
}
```

Only provided fields are changed. The trigger is re-registered in the cron runner after update.

### Delete a Schedule

**Tool:** `chetter_delete_trigger`

```json
{"name": "nightly-docs-update"}
```

---

## Production Schedules

Trigger definitions live as YAML files under `global/triggers/`, `groups/<team-name>/triggers/`, or `repos/<owner>/<repo>/triggers/` in the config repo set via `DEFINITIONS_REPO`. They are auto-synced into the database on startup and on `chetter_sync_definitions`. See [CONFIGURATION.md](CONFIGURATION.md) for the full architecture.

### Included Schedules

| Schedule | Cron | Purpose |
|---|---|---|
| `chetter-nightly-vulnerability-scan` | `0 1 * * *` | Scan Go deps + Docker images for vulnerabilities, create PR with safe fixes |
| `chetter-nightly-changelog-update` | `0 3 * * *` | Update `CHANGELOG.md` from recent commits |
| `chetter-nightly-docs-update` | `0 4 * * *` | Update project documentation to match implementation |
| `chetter-nightly-website-presentation-update` | `0 5 * * *` | Update marketing website and architecture presentation |
| `next-feature-creator` | `*/30 * * * *` | Analyze repo and create GitHub issue for next feature/fix |

### Vulnerability Scan Schedule

The vulnerability scan is the most detailed schedule. It:

1. Checks for existing open security PRs (avoids duplicates)
2. Calls Arcane MCP tools to scan Docker images
3. Runs `govulncheck` and `osv-scanner` on all Go modules
4. Applies minimal safe fixes via `go get` + `go mod tidy`
5. Runs `make -C server check` and `make -C runner check`
6. Creates a PR with findings summary

Requires the `vuln-scan` skill.

---

## Schedule YAML Format

```yaml
name: my-schedule
enabled: true
cron_expr: "0 4 * * *"
git_url: https://github.com/org/repo
git_ref: main
agent_image: ghcr.io/flatout-works/chetter-runner:main
harness: opencode
provider_id: anthropic
model_id: claude-sonnet-4
skills:
  - docs-update
timeout_sec: 3600
prompt: |-
  Your detailed prompt here...
  Multi-line is supported.
```

---

## Cron Expression Reference

Schedules run in **UTC**. The parser supports standard 5-field cron and descriptors:

| Descriptor | Meaning |
|---|---|
| `@yearly` / `@annually` | Once per year, midnight Jan 1 |
| `@monthly` | Once per month, midnight first day |
| `@weekly` | Once per week, midnight Sunday |
| `@daily` / `@midnight` | Once per day, midnight |
| `@hourly` | Once per hour, minute 0 |

Standard 5-field format: `min hour dom month dow`

Examples:
- `0 4 * * *` — daily at 04:00 UTC
- `0 */6 * * *` — every 6 hours
- `30 8 * * 1` — Monday at 08:30 UTC

---

## Schedule Runs

Each time a schedule fires, a row is created in `chetter_schedule_runs`:

| Column | Description |
|---|---|
| `schedule_id` | The parent schedule |
| `task_id` | The task created by this run |
| `status` | Run status (submitted, etc.) |
| `scheduled_for` | The nominal cron fire time |

This lets you trace which task was created by which schedule fire.

---

## Tips

- Use `enabled: false` to pause a schedule without deleting it.
- Update `cron_expr` to change the schedule without losing the rest of the template.
- Schedules created with a team-scoped token only appear in `chetter_list_triggers` for that team.
- The `next_run_at` field is computed and updated automatically after each activation.
- If a schedule's name already exists, `chetter_create_trigger` will fail — use `chetter_update_trigger` instead.
