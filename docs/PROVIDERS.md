# Provider Support Matrix

Each Chetter harness consumes a different API contract internally. The model
catalog's `kind` field signals which protocol the harness uses for a given
provider entry. This document maps every supported combination.

## Protocol Overview

| Protocol | What the harness sends | Typical endpoint |
|---|---|---|
| **Responses API** | `POST /v1/responses` | `https://api.openai.com/v1` |
| **Completions API** | `POST /v1/chat/completions` | `https://api.openai.com/v1` |
| **Anthropic API** | `POST /v1/messages` | `https://api.anthropic.com` |
| **Native / harness-specific** | Provider-resolved internally by the CLI | Harness-dependent |

## Catalog `kind` → Protocol Mapping

| `kind` | Protocol | Examples |
|---|---|---|
| `openai_compatible` | Completions API (OpenAI-compatible) | OpenCode Zen, DeepSeek, Groq, LiteLLM, any `/v1/chat/completions` endpoint |
| `native` | Harness-specific native protocol | Anthropic (Claude API), Z.ai, OpenAI (Responses API) |
| `aws_bedrock` | Responses API via AWS SigV4 | Amazon Bedrock |

When `kind` is omitted or empty, it defaults to `native`.

## Harness × Provider Matrix

### OpenCode

| API contract | Supported | Catalog `kind` | Notes |
|---|---|---|---|
| Responses API | ✗ | — | Not supported; OpenCode uses `/v1/chat/completions` |
| Completions API (OpenAI-compatible) | ✓ | `openai_compatible` | Provider rendered into `.opencode.json`. `base_url`, `env_key`, and models flow from catalog. |
| Anthropic API | ✗ | — | |
| AWS Bedrock (SigV4) | ✗ | — | |
| **LiteLLM** | ✓ | `openai_compatible` | Point `base_url` at your LiteLLM router. |

**Best practice:** Any `openai_compatible` provider in the catalog is automatically available to OpenCode.

### Claude Code

| API contract | Supported | Catalog `kind` | Notes |
|---|---|---|---|
| Responses API | ✗ | — | |
| Completions API (OpenAI-compatible) | ✗ | — | |
| Anthropic API | ✓ | `native` | Uses `ANTHROPIC_API_KEY` and `ANTHROPIC_BASE_URL` env vars. |
| Anthropic-compatible proxy | ✓ | `native` | Set `base_url` and `api_key_env` in catalog. Harness sets `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN`. Example: Synthetic. |
| **LiteLLM** | ✓ | `native` | Point `base_url` at `https://your-litellm/v1/messages` with Anthropic contract. |

**Current limitation:** Claude's Anthropic message contract is the sole wire protocol. Any provider aiming for Claude Code must speak it or be proxied through a compatible endpoint.

### Pi

| API contract | Supported | Catalog `kind` | Notes |
|---|---|---|---|
| Responses API | ✗ | — | |
| Completions API (OpenAI-compatible) | ✓ | — | Pi resolves its own provider from `PI_PROVIDER` / `PI_MODEL` env vars and its internal `models.json`. The catalog provides defaults only. |
| Anthropic API | ✓ | — | Same resolution path as above; Pi speaks multiple protocols natively. |
| **LiteLLM** | ✓ | `openai_compatible` | Pi accepts any OpenAI-compatible endpoint. Set `provider_id`/`model_id` on the task. |

**Current limitation:** Pi runs as a subprocess (no per-task Docker isolation). Provider breadth is the widest of all harnesses (30+), but Pi's own catalog powers the model list; Chetter only supplies defaults.

### CodeWhale

| API contract | Supported | Catalog `kind` | Notes |
|---|---|---|---|
| Responses API | ✗ | — | |
| Completions API (OpenAI-compatible) | ✓ | `native` | Uses `CODEWHALE_PROVIDER` and `CODEWHALE_MODEL` env vars. `base_url` flows to `CODEWHALE_BASE_URL`. |
| Anthropic API | ✓ | `native` | Native provider handling inside CodeWhale. |
| **LiteLLM** | ✓ | `native` | Point `base_url` at your LiteLLM router. |

**Current limitation:** Model routing and provider configuration are mostly handed off to CodeWhale's own resolver. Chetter sets the env vars and the MCP config.

### Codex

| API contract | Supported | Catalog `kind` | Notes |
|---|---|---|---|
| Responses API | ✓ | `native` | Provider rendered into `.codex/config.toml` as `model_provider` with `base_url`, `env_key`, and `wire_api = "responses"`. |
| AWS Bedrock (SigV4) | ✓ | `aws_bedrock` | Generates `[model_providers.amazon-bedrock.aws]` with `profile`/`region`. |
| Completions API (OpenAI-compatible) | ✗ | — | Codex requires the Responses API wire protocol. |
| Anthropic API | ✗ | — | |
| **LiteLLM** | ✓ | `native` | Point `base_url` at your LiteLLM router if it speaks the Responses API contract (`/v1/responses`). |

**Current limitation:** All non-Bedrock providers use `wire_api = "responses"`. The harness config always sets `model_provider = "chetter"` (a custom provider entry). Provide `OPENAI_API_KEY` (or the mapped `api_key_env` from the catalog) to authenticate.

## Quick Reference

```
                  │ OpenCode │ Claude │  Pi   │ CodeWhale │ Codex  │
──────────────────┼──────────┼────────┼───────┼───────────┼────────┤
OpenAI direct     │    ✓*    │   ✗    │   ✓   │     ✓     │   ✓    │
Anthropic direct  │    ✗     │   ✓    │   ✓   │     ✓     │   ✗    │
DeepSeek          │    ✓     │   ✗    │   ✓   │     ✓     │   ✓    │
Synthetic         │    ✓     │   ✓    │   ✓   │     ✓     │   ✓    │
Groq              │    ✓     │   ✗    │   ✓   │     ✓     │   ✗†   │
xAI / Grok        │    ✓     │   ✗    │   ✓   │     ✓     │   ✗†   │
AWS Bedrock       │    ✗     │   ✗    │   ✗   │     ✗     │   ✓    │
Google Gemini     │    ✓     │   ✗    │   ✓   │     ✓     │   ✗†   │
Z.ai / GLM        │    ✓     │   ✗    │   ✓   │     ✓     │   ✗†   │
LiteLLM           │    ✓     │   ✓‡   │   ✓   │     ✓     │   ✓‡   │
──────────────────┴──────────┴────────┴───────┴───────────┴────────┘

*  OpenCode uses Completions API, not Responses API.
†  Requires an OpenAI-compatible gateway or LiteLLM that speaks Codex's
   Responses API contract.
‡  Point at the correct LiteLLM contract endpoint: `/v1/messages` for Claude
   Code, `/v1/responses` for Codex, `/v1/chat/completions` for OpenCode/Pi.
```

## Adding a New Provider

1. Add it to the catalog in `model-catalog.yaml` (or the definitions repo equivalent).
2. Choose the `kind` that matches the harness(es) you want to use it with.
3. For `native` providers that need special handling (e.g., a new wire protocol), update the relevant harness `config.go` to generate appropriate credentials or config sections.
4. Update this document and `docs/CONFIGURATION.md` with the new provider.

### Provider Fields

```yaml
providers:
  my-provider:
    name: "Display name"          # Required
    kind: openai_compatible       # Required: openai_compatible, native, or aws_bedrock
    base_url: https://...         # Optional API base URL
    api_key_env: MY_API_KEY       # Optional env var containing the API key
    aws_profile: my-bedrock       # Optional, kind=aws_bedrock only: AWS SSO profile
    aws_region: us-west-2         # Optional, kind=aws_bedrock only: AWS region
    models:
      - id: model-slug
```

Per-harness overrides are available under `harnesses.<harness_name>` on both
providers and models. See `docs/CONFIGURATION.md` for the full catalog schema.
