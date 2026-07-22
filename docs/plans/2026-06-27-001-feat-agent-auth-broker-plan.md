---
artifact_contract: ce-unified-plan/v1
artifact_readiness: implementation-ready
execution: code
product_contract_source: "issue #118 and PR #119 design review"
title: "feat: keep operator model credentials out of agent sandboxes"
date: 2026-06-27
updated: 2026-07-22
---

# Keep operator model credentials out of agent sandboxes

## Goal

An agent task must be able to call its configured model provider without receiving the real provider credential.

The first milestone is deliberately narrow:

- Protect operator-managed API keys already present in the trusted runner environment.
- Support direct HTTP model providers through the Claude Code and OpenCode harnesses, starting with Anthropic- and OpenAI-compatible APIs.
- Keep real keys out of the task proto, container environment, Docker arguments, workspace, logs, and audit events.
- Do not add a credential vault, BYOK, OAuth, Bedrock translation, or transparent TLS interception.

The runner host and control plane remain trusted. The task container and agent-generated code are untrusted.

## Plain-Language Design

Today the runner copies model keys from its environment into task containers. The agent can read and exfiltrate them.

The new flow is:

```text
control plane claims task
  -> creates a short-lived claim capability (stores only its hash)
  -> task receives the capability and the forwarder URL
  -> agent sends its model request to the forwarder
  -> forwarder validates the live claim with the control plane
  -> forwarder reads the real key from the trusted runner environment
  -> forwarder sends the request to a fixed provider endpoint
  -> response streams back to the agent
```

The capability is not described as worthless: it is a temporary bearer capability that can spend the bound credential through the forwarder. It is random, short-lived, scoped to one claim attempt, unusable at the provider, and reachable only through the private forwarder endpoint.

No real provider credential is stored in the Chetter database in this milestone. Existing operator keys remain in the runner environment, but only the trusted forwarder process may read them.

## Current Problem

There are multiple paths that expose model credentials today:

1. Harness configuration reads `ProviderAPIKeyEnv` and writes the real value into agent configuration or environment. Relevant paths include `runner/harness/claude/resolve.go` and `runner/harness/opencode/config.go`.
2. `runnerOwnedEnvKeys` copies all populated model-provider variables into every task, independent of the selected provider. It is used by local, Docker, resume, and RPC execution paths in `runner/internal/controller/runner_task.go`.
3. OpenCode copies runner-side `auth.json` state into Docker workspaces, which may expose provider credentials independently of environment handling.

Protecting only a selected provider or only one harness is insufficient. A task could choose another path and still receive runner-held keys.

## Security Invariants

### I1. No model credential enters an untrusted task

All model credential variables are removed from generic runner-to-task environment propagation. Harnesses receive only the claim capability and forwarder URL.

This is global for Docker/gVisor execution, not conditional on provider `auth_mode`. There is no legacy model-key injection fallback.

Provider auth-state files are not copied into container workspaces. File- or OAuth-authenticated provider modes are rejected for forwarder-protected tasks.

Local execution is a trusted development mode that shares the runner host and is outside this security boundary. Forwarder-required tasks are rejected in local mode rather than claiming the sandbox-isolation guarantee there.

`GITHUB_TOKEN`, MCP endpoint tokens, and other non-model credentials are unchanged and explicitly out of scope.

### I2. Every capability belongs to one claim attempt

The control plane generates a 32-byte CSPRNG capability for each credential slot. The database stores only `SHA-256(capability)`.

A binding includes:

```text
task_id, attempt, claim_id, runner_id, slot,
provider_id, backend_id, credential_env, expires_at, revoked_at
```

`claim_id` is a new random identifier. Reusing a task ID does not reuse a claim. A resumed or reclaimed task receives a new claim and capability.

Every runner-originated task mutation carries `claim_id`, including events, terminal status, lease renewal, heartbeat task references, checkpoint operations, and session pause/resume state. Database updates match `task_id + runner_id + claim_id`; a stale process cannot renew or terminate a newer claim on the same runner.

### I3. Claim and binding are atomic

Model resolution, backend validation, capability generation, binding insertion, task claim, and attempt increment happen in the same `claimOnce` transaction.

Claim-time failures have explicit outcomes so a bad task cannot block the FIFO queue:

- Invalid task/provider/harness configuration is marked terminal `configuration` without creating a binding or consuming a runner attempt.
- A runner that lacks `credential_forwarder_v1` or the required provider credential is ineligible for that task; eligibility filtering skips it before selecting/locking a task.
- A transient control-plane, database, or catalog-read failure returns a claim error without mutating the task.
- For an eligible valid task, model resolution, binding insertion, task claim, and attempt increment commit atomically.

The current post-commit `resolveTaskModel` flow, which cannot return an error to the claim transaction, must not be used for credential binding.

### I4. All model providers fail closed

An invalid active catalog, missing backend policy, missing binding, expired lease, revoked claim, unavailable validation service, or missing runner credential never causes direct credential injection.

Runtime provider or forwarder failures return an error to the model client. They do not change the authentication mode. A runner startup or environment change that makes an advertised credential unavailable is reported as a terminal runner configuration error rather than retried until `max_attempts` is exhausted.

### I5. The agent cannot choose the real upstream

The capability binds to a server-resolved `backend_id`. The forwarder constructs the upstream request from that backend policy and ignores any inbound scheme, authority, host, or absolute URL.

The agent may choose only request data allowed by the provider adapter, such as the message body and approved provider headers.

### I6. Revocation follows the exact claim lifecycle

Completion, cancellation, lease reclaim, runner loss, and resume revoke the exact `claim_id`. Validation requires all of the following:

- capability hash matches the binding;
- task is currently running;
- task `runner_id`, `attempt`, and active `claim_id` match;
- lease has not expired;
- binding is not expired or revoked.

Revocation and lease checks happen on every forwarded request. No positive validation cache is required in the first version.

## Components

### 1. Backend policy in the model catalog

Add a provider backend configuration rather than overloading `Provider.Kind`:

```yaml
providers:
  anthropic:
    kind: anthropic
    backend:
      kind: direct
      url: https://api.anthropic.com
      credential_env: ANTHROPIC_API_KEY
      auth_header: x-api-key
      allowed_paths: [/v1/messages]
```

Validation rules:

- `kind` is `direct` in U1.
- URL is an absolute HTTPS URL with no userinfo, query, or fragment.
- Host and port are operator-controlled catalog values, never task input.
- `credential_env` must be a valid environment-variable name.
- `auth_header` is one exact supported header, never `*`.
- Methods and path prefixes are an explicit open list.
- An invalid active catalog is an error; it does not silently fall back to another catalog. The built-in catalog remains valid only when no active catalog has been configured.

### 2. Claim-scoped binding store

Add `chetter_model_credential_bindings` with no plaintext capability and no provider key. Apply the schema through both startup schema management and a Goose migration, then generate sqlc code.

The claim transaction returns the plaintext capability once in the `Task` response. It is never persisted or logged.

Add `claim_id` to the task claim state and all relevant runner proto messages. Heartbeats identify current tasks by `{task_id, claim_id}` rather than bare task IDs. The runner sends the binding fields required by the harness, but not `credential_env`, the upstream URL, or the real key.

### 3. Runner credential forwarder

Add a small runner-side reverse proxy, preferably as a package in the runner process unless process isolation provides a concrete operational benefit.

For each request it:

1. Extracts the capability from the one expected inbound auth field.
2. Hashes it and calls a narrow binding-validation RPC over the runner's existing authenticated control-plane channel.
3. Receives the fixed backend policy and credential environment reference after the server verifies the live claim and runner ID.
4. Reads the real credential from the runner environment only after successful validation.
5. Builds a new upstream request; it does not mutate and reuse the inbound URL.
6. Removes `Authorization`, `Proxy-Authorization`, cookies, forwarding headers, and known provider auth headers before inserting the configured auth header.
7. Passes only approved business headers, for example `content-type`, `accept`, `anthropic-version`, and `anthropic-beta` where required.
8. Streams the response and records a token-, key-, header-, URL-query-, and prompt-free audit event.

The validation RPC accepts a token hash, `runner_id`, and request metadata. It returns no key. Reusing the runner's authenticated channel is acceptable for U1 because a compromised runner is outside this threat model; per-runner credentials remain future hardening.

The forwarder does not cache a second copy of the real key beyond the runner environment. Request-local values are discarded after the request, and credentials are never written to files or child-process environments.

### 4. Safe upstream transport

Each direct backend gets a dedicated transport that:

- ignores `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY`;
- does not follow redirects;
- resolves and validates every dialed IP;
- rejects loopback, private, link-local, multicast, unspecified, and cloud-metadata destinations;
- rechecks DNS results when connecting, preventing DNS rebinding from bypassing validation;
- uses the configured TLS server name and normal certificate verification;
- applies request, response-header, idle-stream, and total connection limits.

The forwarder is not a general HTTP proxy.

### 5. Private forwarder endpoint

Docker/gVisor tasks receive an HTTPS forwarder URL using a normal service certificate trusted by the runner image. This is an explicit endpoint, not a forged CA or transparent MITM.

Use an operator-configured internal hostname, for example `credential-forwarder.internal`, mapped to the runner's task-network IP. The certificate SAN must contain that hostname. `CREDENTIAL_FORWARDER_CERT_FILE` and `CREDENTIAL_FORWARDER_KEY_FILE` provide the endpoint certificate; the issuing root is installed in the runner image's normal trust store. The runner fails startup if the key pair, SAN, listener, or trust probe is invalid. Rotation occurs by replacing the files and draining/restarting the runner.

The listener binds only to the runner's private task network and is not published on a host or public interface. Plain HTTP is allowed only on loopback in explicit local-development mode.

The capability remains the authorization mechanism; network placement is defense in depth. Add conservative per-binding concurrency and request-rate limits to reduce the impact of a stolen live capability.

### 6. Harness integration

Harnesses stop reading real model keys:

- Claude receives `ANTHROPIC_BASE_URL=<forwarder>` and the capability in the auth field expected by Claude Code.
- OpenCode provider configuration uses the forwarder URL and capability, never `os.Getenv(ProviderAPIKeyEnv)`.
- OpenCode provider auth-state copying is disabled for containerized tasks; only non-secret model/cache state may be copied.
- Pi and any other harness that cannot currently set both the provider base URL and capability are rejected as unsupported in U1; they do not fall back to direct key injection.

Capabilities may appear in ephemeral task configuration. They must be redacted from logs and exports. Persisted workspaces and resumed sessions cannot reuse them because the old claim is revoked and a new claim is minted.

## Implementation Order

### U1. Direct Anthropic/OpenAI-compatible forwarding

1. Add validated backend policy fields to `pkg/modelcatalog`.
2. Add `claim_id`, binding schema/queries, and the validation/audit RPC.
3. Move model resolution and binding creation into `claimOnce`.
4. Add the runner forwarder and safe transport.
5. Update Claude and OpenCode to use the forwarder; disable container auth-state copying and reject Pi/unsupported harnesses.
6. Remove every model credential and custom `ProviderAPIKeyEnv` from generic task environment propagation.
7. Require `claim_id` on every runner mutation and revoke bindings on every terminal, reclaim, and resume transition.

These changes ship as one security boundary. There is no mixed mode in which a failed forwarder silently uses the old injection path.

### U2. Bedrock gateway backend

Bedrock remains a separate follow-up after U1 is operating successfully. Select one maintained gateway and define ownership, deployment, health checks, model/region mapping, streaming behavior, and SLO before implementation.

The forwarder will authenticate to that gateway with a runner-side credential. AWS credentials and gateway credentials remain outside the sandbox. Native SigV4 in the forwarder stays deferred.

## Rollout

Chetter's deployment already drains runners. Use a coordinated rollout:

1. Deploy the control-plane schema/proto changes.
2. Drain old runners.
3. Deploy runners that advertise `credential_forwarder_v1` plus supported provider IDs and pass startup checks for the private listener, TLS configuration, and catalog credential references.
4. Permit model tasks to claim only on capable runners.
5. Verify canary tasks, then remove the temporary compatibility gate in a follow-up release.

Old runners must never receive a task containing a forwarder binding and must not remain eligible once forwarder-only mode is enabled.

## Verification

Required automated tests:

- Search every Docker, gVisor, resume, and RPC argument builder: no real model credential value or `ProviderAPIKeyEnv` value reaches the task.
- Claude and OpenCode configuration contains only the capability and forwarder URL; provider `auth.json` state is absent.
- Claim failure rolls back task status, attempt, and binding insertion.
- Invalid tasks become terminal without blocking later tasks; runner-incompatible tasks are skipped by eligibility filtering.
- Reclaim and resume mint a new `claim_id`; the previous capability returns 401/403.
- Events and heartbeats carrying an old or missing `claim_id` cannot mutate or renew the current attempt.
- A capability for another task, attempt, runner, provider, or slot is rejected.
- Expired lease, cancellation, terminal status, revoked binding, and control-plane outage fail closed.
- Incoming absolute URLs, forged `Host`, alternate auth headers, redirects, proxy environment variables, DNS rebinding, and private/special destination IPs cannot redirect a credential.
- Provider streaming works without buffering and preserves required approved headers.
- Audit records contain task, claim, provider, status, and timing, but no capability, key, prompt, body, auth header, or query string.
- Missing runner credentials produce one clear configuration failure and no fallback.

Manual verification in a canary container:

```text
/proc/self/environ       contains no real model key
docker inspect           contains no real model key
workspace/config files   contain no real model key
task proto/event/export  contains no real model key
provider receives        the configured real auth header
```

## Out of Scope

- Team or user BYOK and credential-selection policy.
- A database credential vault, KMS/HSM integration, key rotation, and per-team runner identities.
- Subscription OAuth, Claude setup tokens, Codex PKCE, and persisted provider auth files.
- Local execution mode and Pi/other harnesses until they support explicit forwarder configuration.
- Bedrock in U1, native SigV4, and customer-operated gateway integrations.
- Git, MCP, tool, and arbitrary application secrets.
- General network egress isolation. The forwarder protects the model credential even when a sandbox can reach the public provider directly because the sandbox never receives that credential.

These need separate threat models. In particular, BYOK requires authenticated owner identity, consent/delegation, billing attribution, automation principals, and an encrypted vault; it should not be hidden inside this transport change.

## Definition of Done

U1 is complete when every supported untrusted model task uses a claim-scoped capability through the private forwarder; no real model credential can be found in its proto, environment, process arguments, workspace, logs, or exports; all current model-key injection paths are removed; claim and revocation behavior is attempt-safe; upstream routing is fixed and SSRF-resistant; and every failure remains forwarder-only and fails closed.

Bedrock and BYOK are not required for U1 completion.
