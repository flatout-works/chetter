# Chetter Config Repo

Example git repository for Chetter runtime configuration. The MCP server reads from this repo when `DEFINITIONS_REPO` points to it. Never store secret values here — use environment variable names like `api_key_env: ANTHROPIC_API_KEY`.

## Structure

```
├── model-catalog.yaml      # AI model/provider registry
├── agents/                 # Agent definitions (*.md), including PR review orchestration agents
├── skills/                 # Skill definitions (SKILL.md), including shared review workflow instructions
├── triggers/               # Trigger definitions (*.yaml)
├── mcp-profiles/           # MCP profile definitions (*.yaml)
└── task-templates/         # Reusable task templates (*.md)
```

## How it works

1. Point Chetter at your config repo with `DEFINITIONS_REPO=https://github.com/your-org/chetter-config`
2. Chetter clones the repo and syncs definitions into its database
3. Agents, skills, and selected MCP profiles are injected into runner containers at task time
4. Triggers are activated in the scheduler
5. Changes go through PRs — the git repo is the source of truth

See `docs/CONFIG_IN_GIT.md` in the Chetter repo for full architecture details.

## PR Review Orchestration Example

This example repository includes a trusted self-hosted multi-agent PR review workflow:

- `agents/review-orchestrator.md`
- `agents/standard-pr-reviewer.md`
- `agents/adversarial-pr-reviewer.md`
- `agents/review-synthesizer.md`
- `skills/pr-review-workflow/SKILL.md`
- `mcp-profiles/chetter-orchestration.yaml`
- `triggers/chetter-pr-review-orchestrator.yaml`

The trigger is disabled by default. Enable it only after installing the GitHub App on the target repository and making `CHETTER_MCP_AUTH_TOKEN` available to trusted runners. The `chetter-orchestration` profile grants the full Chetter MCP tool surface to the trusted orchestrator task. Reviewer child tasks inherit read-only GitHub auth for cloning and `gh` inspection, but not GitHub write authorization. The synthesizer receives child exports as workspace files without Chetter MCP profiles or GitHub write credentials; the orchestrator verifies the head SHA and posts the final body through `chetter_pr_review`. Until scoped MCP tokens or proxy-side enforcement exists, this workflow is for trusted self-hosted deployments.
