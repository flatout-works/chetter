# Niffler harness

Select `harness: "niffler"` with any standard agent image. The shared base
includes the full Niffler installation alongside the other harnesses:

```json
{
  "prompt": "Implement the change, run the tests, and open a PR.",
  "git_url": "https://github.com/my-org/my-repo",
  "harness": "niffler",
  "agent_image": "ghcr.io/flatout-works/chetter-agent:golang",
  "provider_id": "deepseek",
  "model_id": "deepseek-v4-flash",
  "timeout_sec": 1800
}
```

The API, triggers, agent definitions, runner configuration, model catalog and
web task form accept Niffler just like the other harnesses. The built-in
catalog default is DeepSeek `deepseek-chat`; the instance configuration can
choose a newer model. The resolved provider base URL and API-key environment
variable are required. OpenAI chat-completions and Anthropic messages are
supported; API-key Responses and Bedrock are explicitly refused rather than
being mistaken for Niffler's ChatGPT OAuth adapter. Thinking effort uses
`variant_id` (`low`, `medium`, `high`, `max`) when supported by the provider.

## Execution and state

`niffler-serve-proxy` retains Chetter's authenticated HTTP ServeHarness
interface and owns the task-local Niffler stack. Each turn is delegated to
Niffler's native `cli run` NDJSON driver, which handles MCP bootstrap,
streaming, cancellation and complete canonical JSONL export. Chetter only
renders/redacts the exported transcript and maps the final accounting.
Docker/gVisor and Kubernetes execution
use the existing resource, network, cancellation and teardown policies.
Local execution is explicitly unisolated, as with the other harnesses.

The image's pinned `/opt/niffler` installation is an immutable template.
Each task owns `.niffler/runtime`, including a SQLite store, isolated NATS
bus, component processes and session runner. No installation `.env`, database
or persisted spawn records are copied. Fresh tasks discard checkout-provided
runtime state; a preserved resumable workspace retains its store and native
conversation ID. Standard `session_mode: "resumable"` uses Chetter's existing
same-runner workspace preservation/restore contract, not cross-runner state
migration. MCP capabilities are refreshed before resumed work.

Agent definitions augment Niffler's product/system prompt through its existing
`AGENTS.override.md` seam; repository instructions are still read from the
conversation workspace. Definition archives are extracted into a task-owned
skills directory exposed through Niffler's standard `.agents/skills` seam.
The prompt/tool snapshot remains frozen for the native conversation lifetime;
continuation appends content rather than rebuilding that prefix.

## MCP, progress and results

The native driver registers the authenticated runner bridge, claim-scoped Chetter
relay, and selected HTTP/SSE MCP endpoints before the first turn freezes its
tool set. Endpoint bearer credentials use `${ENV_NAME}` indirection; capability
files are mode `0600`. Local Niffler tasks also use a private claim relay,
never a persisted runner-wide MCP token. Niffler control variables are reserved
against task env/endpoint-token overrides.

Live Niffler text and thinking deltas are batched into progress messages
(up to one batch every three seconds during generation), not individual token
rows. Assistant, tool, terminal and driver-exit boundaries flush pending text;
canonical assistant frames fill missing trailing deltas without repeating text.
Tool-call events remain distinct and usage accounting is unchanged.
Completion requires exactly one native `result` with a successful outcome;
non-success outcomes and `turnError` fail the task. Cancellation sends SIGTERM
to the native driver, which uses the steering control and settles/exports
within a bounded grace. Exports contain the complete
canonical transcript, following every store cursor, rather than only the
possibly compacted provider context. Known task capability/provider secrets
are redacted from exported text, summaries and progress.

Usage comes directly from Niffler's authoritative per-turn `usage` object
([#123](https://github.com/gokr/niffler/issues/123)); no historical subtraction
or summing status frames is needed. The terminal event, final reply and abort
readback are deduplicated by `turnId`. Prompt/output, cache-read, cache-write
and reasoning counters are mapped when reported. Unknown counters remain
absent in the native payload; the numeric task rollup represents unavailable
values as zero. Descendant spend is explicitly excluded, and cost is not
invented. The native driver introduced by
[#124](https://github.com/gokr/niffler/issues/124) now replaces Chetter's custom
turn wait, MCP bootstrap and store-page export loops.

## Development verification

Build the proxy with `make -C runner local`. For local development only,
`CHETTER_NIFFLER_HOME` can select a built Niffler checkout instead of
`/opt/niffler`. Do not point it at a shared writable runtime.

```bash
cd runner
# Narrow contract tests (private NATS; no paid provider calls):
go test -race ./harness/niffler ./cmd/niffler-serve-proxy
# Actual isolated Niffler stack, fake streaming provider, real MCP tool,
# full process restart and durable conversation continuation:
go build -o ../bin/niffler-serve-proxy ./cmd/niffler-serve-proxy
CHETTER_NIFFLER_TEST_HOME="$HOME/git/niffler" \
CHETTER_NIFFLER_TEST_PROXY="$PWD/../bin/niffler-serve-proxy" \
  go test ./cmd/niffler-serve-proxy -run TestLiveNiffler -count=1
```

The base image is source-built at a pinned Niffler revision. Updating it
requires rebuilding the base and downstream variants, then running the live
contract test against that revision. The fleet `harnesses` and `full` self-test
profiles include Niffler using the default agent image.
