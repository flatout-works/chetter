# Chetter Config Repo

Example git repository for Chetter runtime configuration. The MCP server reads from this repo when `DEFINITIONS_REPO` points to it. Never store secret values here — use environment variable names like `api_key_env: ANTHROPIC_API_KEY`.

## Structure

```
├── model-catalog.yaml      # AI model/provider registry
├── agents/                 # Agent definitions (*.md)
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

## Trusted MCP Profile Example

This example repository includes a trusted self-hosted MCP profile example:

- `mcp-profiles/chetter-orchestration.yaml`
- `triggers/example-daily-review.yaml` with the profile commented out

The trigger is disabled by default. Enable privileged MCP profiles only after installing the GitHub App on the target repository and making `CHETTER_MCP_AUTH_TOKEN` available to trusted runners. Chetter MCP is not mounted into all tasks by default; attach this profile explicitly with `mcp_profiles`. Credentialed profiles are trusted/admin-only in this MVP; `tool_allowlist` only generates OpenCode permission hints and is not server-side security enforcement. Resolved credentials are written into task-readable harness config files. Until scoped MCP tokens or proxy-side enforcement exists, do not use privileged Chetter MCP profiles for untrusted or multi-tenant tasks.
