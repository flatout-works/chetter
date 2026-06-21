# Model Catalog

Chetter keeps provider and model definitions in a generic YAML catalog that
lives in a Git definitions repo. The server syncs it into TiDB and resolves
the harness-specific provider/model before a runner receives a task.

Runners do not receive or parse the full catalog. Claimed tasks contain the
resolved `harness`, `provider_id`, `model_id`, and provider metadata needed by
the selected harness to write its local config.

If no definitions repo is configured (`DEFINITIONS_REPO` env var), Chetter
uses a built-in default catalog with common providers (Synthetic, DeepSeek,
Z.ai, Anthropic, OpenCode Zen).

## Setup

1. Create a Git repo (or use an existing one) with a `model-catalog.yaml` at
   the root. The Flatout default repo is `github.com/flatout-works/chetter-config`.
2. Set `DEFINITIONS_REPO` (and optionally `DEFINITIONS_BRANCH`) on the MCP
   server.
3. Start (or restart) the server. It clones the repo, validates the catalog,
   and stores it as the active catalog in `chetter_model_catalogs`.
4. Chetter re-pulls the definitions repo every five minutes and updates the DB.
   To refresh immediately, call `chetter_sync_definitions` (admin only) or restart.

Example MCP server environment:

```bash
DEFINITIONS_REPO=git@github.com:flatout-works/chetter-config.git
DEFINITIONS_BRANCH=main
```

The catalog must not contain secret values; use env var names such as
`api_key_env: SYNTHETIC_API_KEY`.

## Shape

```yaml
version: 1
default_provider: synthetic
default_model: hf:zai-org/GLM-5.2

defaults:
  opencode:
    provider: synthetic
    model: hf:zai-org/GLM-5.2
  pi:
    provider: zai
    model: glm-5.2
  claude-code:
    provider: anthropic
    model: claude-sonnet-4-5

providers:
  synthetic:
    name: Synthetic
    kind: openai_compatible
    models:
      - id: hf:zai-org/GLM-5.2

  deepseek:
    name: DeepSeek
    kind: openai_compatible
    api_key_env: DEEPSEEK_API_KEY
    base_url: https://api.deepseek.com
    models:
      - id: deepseek-chat
```

`kind: openai_compatible` is enough for OpenCode provider rendering. Native
providers can still be listed for harnesses such as Claude Code or Pi without
OpenCode trying to render them as OpenAI-compatible endpoints.

Use provider or model `harnesses` overrides only when a harness needs a
different ID or should disable an entry.

## Viewing

Use `chetter_get_model_catalog` (no admin required) to see the active
catalog's default provider/model, provider count, model count, and source.
