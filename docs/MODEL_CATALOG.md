# Model Catalog

Chetter keeps provider and model definitions in a generic YAML catalog. Admins can import the catalog into the control-plane database, and runners receive the active catalog when they claim tasks.

The catalog is shared by all harnesses. Harness-specific behavior belongs in optional `defaults` or `harnesses` sections, not in Go code for a specific provider or model.

## Import

Use the admin MCP tool:

```json
{
  "name": "default",
  "file_path": "config/model-catalog.yaml",
  "activate": true
}
```

Or pass inline YAML via the `yaml` field. The catalog must not contain secret values; use env var names such as `api_key_env: SYNTHETIC_API_KEY`.

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

`kind: openai_compatible` is enough for OpenCode provider rendering. Native providers can still be listed for harnesses such as Claude Code or Pi without OpenCode trying to render them as OpenAI-compatible endpoints.

Use provider or model `harnesses` overrides only when a harness needs a different ID or should disable an entry.
