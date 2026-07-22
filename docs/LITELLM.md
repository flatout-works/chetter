# LiteLLM

LiteLLM can be used with Chetter in two related but separate ways:

1. As an LLM gateway for model requests.
2. As an MCP gateway for MCP servers and tools.

LiteLLM's virtual keys can combine model access, MCP access, budgets, rate limits, and expiry. Chetter currently supports the routing and direct MCP connection pieces, but does not yet provision or safely broker one LiteLLM key per Chetter agent or task.

## Virtual Keys

LiteLLM does not require one token per human user. It issues virtual keys. A key can optionally be associated with a `user_id`, `team_id`, organization, or agent.

A virtual key can carry:

- Allowed model groups through `models`.
- Model aliases.
- Expiry and rotation settings.
- Maximum spend and budget windows.
- Requests-per-minute, tokens-per-minute, and concurrency limits.
- MCP server and access-group permissions.
- Per-server MCP tool permissions.
- Per-server MCP request limits.

LiteLLM tracks spend at the key level and can also aggregate it for the associated user or team. The `user_id` and `team_id` are therefore useful for ownership and accounting, while the key is the bearer credential used by the client.

LiteLLM model access is based on model groups. For example, a key can be granted access to a configured model group named `coding-model` rather than directly to a provider deployment.

```yaml
model_list:
  - model_name: coding-model
    litellm_params:
      model: openai/gpt-4o
      api_key: os.environ/OPENAI_API_KEY
```

```json
{
  "models": ["coding-model"],
  "max_budget": 5,
  "duration": "1d",
  "rpm_limit": 30,
  "tpm_limit": 50000
}
```

## Per-Agent Keys

For Chetter, there are two possible key lifetimes:

### One key per persistent agent

Every task launched for the same Chetter agent uses the same LiteLLM key.

Advantages:

- Simple provisioning.
- Spend and limits aggregate naturally for the agent.
- Model and MCP permissions remain stable across runs.

Trade-off:

- A leaked key remains useful across multiple task runs until it expires or is revoked.

### One key per task

Chetter creates a short-lived LiteLLM key for each task and revokes or expires it when the task ends.

Advantages:

- Stronger isolation between task runs.
- Exact task-level spend and revocation.
- A stolen key has a shorter useful lifetime.

Trade-off:

- Requires Chetter to call LiteLLM key-management APIs for every task.
- Requires LiteLLM database-backed key management.
- More lifecycle and failure handling is needed.

For untrusted agent containers, a short-lived per-task key is the safer eventual design. A persistent per-agent key is reasonable when all runs of that agent intentionally share the same model and MCP permissions.

LiteLLM also has a separate Agent Gateway concept. Requests can include `x-litellm-agent-id`, and permissions for that LiteLLM agent participate in the permission intersection. This is not automatically connected to a Chetter task, Chetter agent definition, or task ID. Chetter would need to explicitly create that mapping and send the header.

## MCP Permissions

LiteLLM's MCP Gateway supports permissions at several levels:

- Virtual key.
- Team.
- End user.
- LiteLLM agent.
- Organization.

When multiple levels apply, LiteLLM generally intersects the allowed server and tool lists. The most restrictive result wins; organization permissions act as a ceiling.

A key can restrict MCP servers with `object_permission.mcp_servers` or MCP access groups:

```json
{
  "object_permission": {
    "mcp_servers": ["github_mcp", "docs_group"],
    "mcp_tool_permissions": {
      "github_mcp": ["search_code", "list_repositories"]
    }
  }
}
```

LiteLLM can also configure server-wide `allowed_tools` and `disallowed_tools`. Per-key or per-team `mcp_tool_permissions` are more appropriate when different agents need different subsets of the same server.

By default, LiteLLM documents MCP access as open when no permission level defines a server list. For restricted deployments, configure explicit key permissions and consider:

```yaml
general_settings:
  require_key_mcp_access_defined: true
```

Use the `no-mcp-servers` sentinel when a key must have no MCP access:

```json
{
  "object_permission": {
    "mcp_servers": ["no-mcp-servers"]
  }
}
```

## LiteLLM MCP Gateway

LiteLLM can front MCP servers configured with Streamable HTTP, SSE, or stdio transports. A client connects to LiteLLM over HTTP or SSE; LiteLLM handles the upstream MCP transport and authentication.

Common endpoint forms are:

```text
https://litellm.example.com/mcp
https://litellm.example.com/github_mcp/mcp
https://litellm.example.com/docs_group/mcp
```

The latter forms use LiteLLM URL namespacing for one server or an access group. LiteLLM also supports an `x-mcp-servers` header for selecting servers dynamically.

LiteLLM accepts its virtual key through either of these forms:

```text
Authorization: Bearer <litellm-key>
x-litellm-api-key: Bearer <litellm-key>
```

LiteLLM's recommended server-specific client credentials use headers such as:

```text
x-mcp-github-authorization: Bearer <github-token>
x-mcp-zapier-x-api-key: <zapier-token>
```

These are separate from the LiteLLM API key. LiteLLM uses them to authenticate to the selected upstream MCP server when configured with `extra_headers`.

## Chetter `mcp_endpoints`

Chetter's `mcp_endpoints` feature can connect a supported harness directly to the LiteLLM MCP Gateway.

Example:

```yaml
name: litellm-tools
transport: http
url: https://litellm.example.com/docs_group/mcp
auth:
  type: bearer
  token_env: LITELLM_MCP_KEY
```

For dynamic server selection, a static header can be configured:

```yaml
name: litellm-tools
transport: http
url: https://litellm.example.com/mcp
auth:
  type: bearer
  token_env: LITELLM_MCP_KEY
headers:
  x-mcp-servers: docs_group
```

Current Chetter behavior:

- `transport: http` is used for Streamable HTTP.
- `transport: sse` is used for SSE.
- `auth.token_env` produces `Authorization: Bearer ${TOKEN_ENV}` for harnesses that support environment references.
- Static headers such as `x-mcp-servers` are supported.
- Chetter resolves endpoint definitions by global/team scope and attaches them to the task.
- The endpoint is configured as a native MCP server in the agent harness.

Chetter does not currently call LiteLLM's `/v1/responses` or `/v1/chat/completions` MCP orchestration APIs. The agent's own MCP client connects to LiteLLM and performs MCP discovery and tool calls directly.

LiteLLM's upstream stdio support is therefore transparent to Chetter. Chetter only needs to support the HTTP or SSE connection to LiteLLM.

## Current Security Limitation

`mcp_endpoints` currently requires the configured `token_env` to exist on the runner and passes that environment variable into the task container. The Docker path uses an inherited environment entry:

```text
-e LITELLM_MCP_KEY
```

This means the agent can read and reuse the LiteLLM key from its environment. LiteLLM will still enforce the key's model and MCP permissions, but Chetter does not currently hide the key from the sandbox.

This is different from the desired credential-forwarder design:

```text
Chetter task
  -> receives a short-lived capability
  -> MCP/model forwarder validates the capability
  -> forwarder injects the LiteLLM key
  -> LiteLLM enforces model and MCP permissions
```

Until that broker exists, use short-lived keys with narrow permissions and small budgets. Do not put a LiteLLM master key in `mcp_endpoints`, task environment, or an agent image.

## Current Chetter Support Level

### Model routing

LiteLLM support is mature as a protocol adapter:

- OpenCode uses LiteLLM's OpenAI-compatible `/v1/chat/completions` contract.
- Claude Code uses LiteLLM's Anthropic-compatible `/v1/messages` contract.
- Codex can use LiteLLM's Responses-compatible `/v1/responses` contract.
- Pi can use LiteLLM's OpenAI-compatible endpoint.
- Catalog entries support provider aliases, per-harness base URLs, API protocol selection, and credential environment variables.

Chetter does not currently create one LiteLLM key per Chetter agent or task, enforce LiteLLM key permissions, or revoke LiteLLM keys at task completion.

### MCP gateway connection

Direct connection through `mcp_endpoints` is supported for LiteLLM HTTP and SSE endpoints. LiteLLM performs the actual MCP server access control, tool filtering, upstream authentication, and optional cost tracking.

Chetter currently provides the endpoint attachment and token environment wiring, but not per-agent LiteLLM identity or safe token isolation.

## Recommended Future Integration

The clean division of responsibility is:

```text
Chetter
  - owns task and agent identity
  - creates a per-task or per-agent LiteLLM key
  - selects the allowed model groups and MCP permissions
  - sets expiry, budget, and rate limits
  - revokes the key at task completion
  - keeps the key out of the sandbox

LiteLLM
  - authenticates the virtual key
  - enforces model access
  - enforces MCP server/tool permissions
  - routes model requests
  - routes MCP requests
  - tracks spend and rate limits
```

The first useful implementation would be a Chetter-managed short-lived per-task LiteLLM key combined with the credential forwarder. The key should contain both the allowed model groups and the allowed MCP server/tool set. Chetter should not rely on an agent-supplied `user_id` or `x-litellm-agent-id` for authorization.

## References

- [LiteLLM Virtual Keys](https://docs.litellm.ai/docs/proxy/virtual_keys)
- [LiteLLM Budgets and Rate Limits](https://docs.litellm.ai/docs/proxy/users)
- [LiteLLM Model Access](https://docs.litellm.ai/docs/proxy/model_access_guide)
- [LiteLLM MCP Overview](https://docs.litellm.ai/docs/mcp)
- [LiteLLM Using MCP](https://docs.litellm.ai/docs/mcp_usage)
- [LiteLLM MCP Permission Management](https://docs.litellm.ai/docs/mcp_control)
- [LiteLLM MCP REST API](https://docs.litellm.ai/docs/mcp_rest_api)
