# Chetter Config Repo

Example git repository for Chetter runtime configuration. The MCP server reads from this repo when `DEFINITIONS_REPO` points to it. Never store secret values here — use environment variable names like `api_key_env: ANTHROPIC_API_KEY`.

## Structure

```
├── model-catalog.yaml      # AI model/provider registry
├── agents/                 # Agent definitions (*.md)
├── skills/                 # Skill definitions (SKILL.md)
├── triggers/               # Trigger definitions (*.yaml)
├── mcp-profiles/           # Global task MCP profiles (*.yaml)
└── task-templates/         # Reusable task templates (*.md)
```

## How it works

1. Point Chetter at your config repo with `DEFINITIONS_REPO=https://github.com/your-org/chetter-config`
2. Chetter clones the repo and syncs definitions into its database
3. Agents and skills are injected into runner containers at task time
4. Triggers are activated in the scheduler
5. MCP profile token values are configured on runners; the repo stores only environment variable names
6. Changes go through PRs — the git repo is the source of truth

## Validation

Copy or reference the schemas from the Chetter repo when editing definitions:

| File | Schema |
|---|---|
| `model-catalog.yaml` | `schemas/model-catalog.schema.json` |
| `triggers/*.yaml` | `schemas/trigger.schema.json` |
| `mcp-profiles/*.yaml` | `schemas/mcp-profile.schema.json` |
| Agent YAML frontmatter in `agents/*.md` | `schemas/agent-frontmatter.schema.json` |

Chetter validates these files during definitions sync and rejects the sync if a definition is malformed.

See `docs/CONFIGURATION.md` in the Chetter repo for full architecture details.
