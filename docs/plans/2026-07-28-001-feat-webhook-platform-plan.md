---
artifact_contract: ce-unified-plan/v1
artifact_readiness: implementation-ready
execution: code
product_contract_source: "GitHub issue #253 and operator request for UI-managed and Git-managed inbound/outbound webhooks"
title: "feat: unified inbound and outbound webhook platform"
date: 2026-07-28
---

# Unified Webhook Platform

## Goal

Give Chetter one coherent webhook area that supports:

1. inbound endpoints that let external systems submit authenticated events to Chetter;
2. outbound subscriptions that deliver Chetter task events to external systems;
3. a complete Web UI for configuration, delivery history, retries, and diagnostics;
4. Git-backed desired state in `chetter-config`, with UI edits proposed through pull requests.

The design should be flexible enough for CI systems, Slack-compatible endpoints,
PagerDuty-style receivers, and internal services without turning Chetter into a
general workflow engine, secret vault, or API gateway.

Tracking issue: [#253](https://github.com/flatout-works/chetter/issues/253).
The existing generic inbound proposal [#120](https://github.com/flatout-works/chetter/issues/120)
is the Phase 2 child issue.

## Product Model

Use **Webhooks** as the UI and documentation umbrella, but retain two typed
runtime resources:

- **Inbound Endpoint**: receives an external HTTP request and performs one
  configured Chetter action.
- **Outbound Subscription**: matches Chetter task events and sends one HTTP
  request to an external destination.

Do not use one polymorphic table or YAML shape for both directions. Their
authentication, delivery state, retries, and security boundaries are different.

The existing `create_task` event callback remains an **Automation**. Existing
`webhook` and `slack` callbacks migrate to outbound subscriptions. This keeps
internal event chaining separate from external HTTP delivery.

## Source Of Truth

`chetter-config` is the desired-state source for new webhook resources.

- Direct Git edits and normal pull requests remain supported.
- The Web UI provides structured forms, renders canonical YAML, validates it,
  and opens a definition proposal PR.
- Git-managed resources cannot be mutated or deleted through direct CRUD APIs.
- Runtime delivery state, generated endpoint URLs, health, and history remain in
  Chetter's database and are never written back to Git.
- Existing database-managed callbacks remain visible during migration and get a
  **Move to config** action. New direct database-managed webhook creation is not
  added to the replacement UI.

This avoids competing UI and Git configuration while still making the UI a
first-class management experience.

## Configuration Layout

Add these scanner-supported paths:

```text
global/webhooks/inbound/*.yaml
global/webhooks/outbound/*.yaml
groups/<team>/webhooks/inbound/*.yaml
groups/<team>/webhooks/outbound/*.yaml
```

Repository-scoped webhooks are deferred. Team and global scopes cover the
current ownership model without introducing ambiguous repository event routing.

Use separate strict schemas:

```text
schemas/inbound-webhook.schema.json
schemas/outbound-webhook.schema.json
```

The filename stem must equal `name`. Unknown YAML fields fail validation.

### Inbound Example

```yaml
# yaml-language-server: $schema=../../../../../chetter/schemas/inbound-webhook.schema.json
name: ci-build-events
enabled: true

auth:
  type: hmac_sha256
  secret_env: CHETTER_WEBHOOK_CI_SECRET
  signature_header: X-CI-Signature
  signature_prefix: sha256=

delivery_id_header: X-Delivery-ID
event_type_header: X-Event-Type
accepted_events:
  - build.completed
  - build.failed

action:
  type: create_task
  prompt: |
    Investigate CI event {{ .EventType }} for {{ .Payload.repository }}.
    Build: {{ .Payload.build_url }}
  agent: issue-triage
  timeout_sec: 900
```

Inbound endpoints support one action in the first version. Multiple effects use
multiple endpoints or a downstream automation. Supported authentication:

- `hmac_sha256` with a secret environment reference;
- `bearer` with a secret environment reference.

An inbound global definition must specify a fixed `action.team_name`. A
team-scoped definition inherits its team from the directory. Payload data can
never select the target team.

### Outbound Example

```yaml
# yaml-language-server: $schema=../../../../../chetter/schemas/outbound-webhook.schema.json
name: notify-ci
enabled: true

events:
  - task.completed
  - task.failed.*

destination:
  url: https://ci.example.com/api/chetter/events
  auth:
    type: bearer
    secret_env: CHETTER_WEBHOOK_CI_TOKEN

payload:
  mode: default

delivery:
  timeout_sec: 10
  max_attempts: 5
```

Outbound subscriptions are always HTTP `POST` in the first version. Avoid
arbitrary methods and proxy behavior. Payload modes:

- `default`: stable versioned Chetter event envelope;
- `template`: an optional strict Go template for systems needing another JSON
  shape.

Slack is a UI preset that creates an outbound subscription with a Slack-shaped
template. It is not a separate delivery engine.

## Stable Event Contract

Outbound requests use a versioned envelope:

```json
{
  "version": "1",
  "id": "evt_123",
  "type": "task.completed",
  "created_at": "2026-07-28T10:00:00Z",
  "team_id": "team_123",
  "subject": {
    "type": "task",
    "id": "task_123"
  },
  "data": {
    "status": "done",
    "summary": "Completed",
    "error": "",
    "error_category": "",
    "trigger_name": "nightly",
    "trigger_type": "cron"
  }
}
```

Additive fields are allowed within version 1. Breaking changes require a new
version. Do not expose task environment variables, prompts, credentials, or raw
session exports in the default envelope.

## Runtime Architecture

### Inbound Flow

```text
HTTP request
  -> resolve endpoint by public ID
  -> enforce size/content-type/auth/rate limits
  -> durably insert inbound delivery
  -> return 202
  -> leased worker validates event and renders action
  -> create task with deterministic idempotency key
  -> mark completed, retryable failure, permanent failure, or dead-letter
```

Endpoint URLs use an opaque stable public ID generated at first materialization:

```text
POST /hooks/inbound/<public_id>
```

The ID remains stable across content and name changes at the same source path.
Moving the file creates a new endpoint unless an explicit migration is supplied.
Do not put mutable team names or secret material in the URL.

### Outbound Flow

```text
task event transaction
  -> match active subscriptions
  -> insert one outbound delivery per event/subscription
  -> leased worker claims delivery
  -> resolve secret reference and safe destination
  -> send request with delivery and signature headers
  -> mark success, retryable failure, permanent failure, or dead-letter
```

Use a unique `(subscription_id, event_id)` constraint. Retries reuse the same
delivery ID and idempotency header. A process crash cannot lose or duplicate the
logical delivery.

Do not use audit rows as an event bus. Audit records are derived observability;
delivery rows are the delivery source of truth.

## Data Model

### `chetter_webhook_endpoints`

Materialized inbound configuration:

- `id`, `public_id`, `team_id`, `name`, `enabled`;
- `auth_type`, `secret_ref`, non-secret auth options;
- accepted event configuration;
- typed action configuration;
- `source_id`, `source_path`, `source_commit`, `content_hash`;
- timestamps and optional runtime suspension metadata.

### `chetter_webhook_subscriptions`

Materialized outbound configuration:

- `id`, `team_id`, `name`, `enabled`;
- event matchers;
- destination URL and non-secret authentication configuration;
- payload mode/template and bounded delivery policy;
- `source_id`, `source_path`, `source_commit`, `content_hash`;
- timestamps and optional runtime suspension metadata.

### `chetter_webhook_inbound_deliveries`

- endpoint and external delivery identity;
- event type, body hash, bounded payload, and selected headers;
- status, attempts, lease owner/expiry, retry time, and error category;
- resulting task ID when applicable;
- correlation ID and timestamps.

Unique key: `(endpoint_id, external_delivery_id)`. If no delivery header is
configured, use a body hash scoped to the endpoint and retention window.

### `chetter_webhook_outbound_deliveries`

- subscription ID and source task event ID;
- destination host, request version, status, attempts, lease and retry state;
- response status, bounded response preview, latency, and error category;
- correlation ID and timestamps.

Unique key: `(subscription_id, event_id)`.

Keep inbound and outbound deliveries separate. Their columns and retry semantics
are materially different, and combining them would produce nullable, ambiguous
state.

The existing `chetter_webhook_deliveries` table becomes legacy GitHub inbox data
and is migrated into the inbound delivery model after the generic receiver is
stable.

## Delivery State Machine

Use the same small state vocabulary in both delivery tables:

```text
pending -> processing -> succeeded
                      -> retry_wait -> processing
                      -> failed_permanent
                      -> dead_letter
```

Workers atomically claim rows with a lease. Expired `processing` leases return
to `retry_wait`. Multiple server replicas must not process the same delivery
concurrently.

Retry policy:

- retry network failures, timeouts, `408`, `425`, `429`, and `5xx`;
- treat other `4xx` responses as permanent;
- use bounded exponential backoff with jitter;
- enforce configured attempts within safe server limits;
- allow an authorized manual retry from the UI without resetting history.

## Security

### Secrets

- Git and database configuration store only `secret_env` references.
- Secret values are resolved at request time inside the control plane.
- List/get APIs return the reference and availability state, never the value.
- Templates, audit rows, errors, delivery previews, and logs must redact auth
  headers, signatures, cookies, and URL query strings.
- A missing secret marks the resource unhealthy and fails closed.

This phase does not introduce an encrypted database secret vault. UI secret
creation and rotation can be added later when Chetter has a managed secret
provider.

### Outbound SSRF Protection

Use a dedicated HTTP transport that:

- permits HTTPS only, except explicit loopback development mode;
- rejects userinfo and URL fragments;
- does not read proxy environment variables;
- does not follow redirects;
- rejects loopback, private, link-local, multicast, unspecified, and metadata
  addresses unless allowed by explicit operator policy;
- resolves and validates every dialed IP to prevent DNS rebinding;
- applies connect, header, body, idle, and total timeouts;
- limits request and response sizes.

### Inbound Protection

- constant-time HMAC and bearer comparison;
- maximum request size and JSON content-type enforcement;
- per-endpoint request and concurrency limits;
- replay protection through delivery IDs/body hashes;
- configurable payload retention with prompt cleanup;
- team-scoped delivery queries and authorization;
- no payload-driven team, agent image, secret, or destination selection.

## Definition Sync And Ownership

Add `WebhookEndpoint` and `WebhookSubscription` definition types to
`pkg/definitions` and the materialized `definitions` registry.

During sync:

1. pull and validate every file before changing runtime state;
2. resolve team directories to team IDs;
3. upsert resources by stable `(source_id, direction, source_path)` identity,
   with a separate uniqueness rule for `(direction, scope, team_id, name)`;
4. preserve runtime IDs and delivery history;
5. disable resources removed from Git instead of deleting history;
6. reject collisions with legacy database-managed resources and report an
   actionable migration error;
7. commit definitions, resources, and sync-run status transactionally.

Server-side update/delete methods must reject Git-managed resources. UI control
disabling is not an authorization boundary.

A GitHub push to the configured definitions repository should request a sync.
Retain the periodic sync as reconciliation and startup recovery.

## Proposal Workflow

Extract definition proposal operations from MCP-handler-shaped code into normal
service methods shared by MCP and ConnectRPC.

Add proposal support for:

- create file;
- replace file;
- delete file;
- validate without opening a PR;
- return canonical YAML and validation diagnostics;
- inspect PR/check/merge state.

The Web UI flow is:

```text
structured form -> server validation -> YAML/diff preview -> create PR
  -> proposal status -> merge -> definition sync -> active resource
```

The UI never commits directly to `chetter-config` and never mutates a
Git-managed runtime row.

## Web API

Add one `WebhookService` with resource-oriented RPCs:

- `List/GetInboundEndpoints`
- `List/GetOutboundSubscriptions`
- `List/GetInboundDeliveries`
- `List/GetOutboundDeliveries`
- `RetryDelivery`
- `SuspendWebhook` / `ResumeWebhook`
- `ValidateWebhookDefinition`
- `CreateWebhookProposal`
- `GetWebhookProposal`

List responses include source ID/path/commit, desired enabled state, runtime
state, secret availability, health summary, and last delivery. They never expose
resolved secrets or full sensitive payloads.

Keep the existing callback RPCs during migration, mark them deprecated, and
remove them only after all webhook/slack callbacks have moved.

## Web UI

Replace the current raw-JSON Event Callbacks page with `/webhooks`.

### Overview

- inbound/outbound resource counts;
- unhealthy and suspended resources;
- recent success/failure rate;
- dead-letter count;
- latest definition sync/proposal state.

### Inbound Tab

- name, scope/team, public URL, auth mode, accepted events, desired/runtime
  state, source, and recent health;
- copy URL, inspect, propose edit, suspend/resume;
- structured endpoint form with authentication and task-action sections.

### Outbound Tab

- name, scope/team, event matchers, destination domain, auth mode, source,
  desired/runtime state, and recent health;
- destination URL query strings and credentials are not displayed;
- structured subscription form and payload preview.

### Deliveries Tab

- direction, resource, team, event, status, date, and correlation filters;
- delivery timeline with attempts, latency, response status, and redacted error;
- authorized retry for failed/dead-letter deliveries;
- payload metadata by default, with raw payload access restricted and audited.

### Proposals

- YAML and diff preview before submission;
- PR link, checks, changed files, merge status, and resulting sync commit;
- clear distinction between desired Git state and live runtime state.

Use Flowbite-Svelte components throughout. Split list, detail, editor, delivery,
and proposal views into focused route components instead of extending the
current single large page.

## Implementation Phases

### Phase 0: Correctness And Security Foundation

Before adding more webhook traffic:

1. fix inbound error propagation so failed handling cannot be marked completed;
2. make inbox claiming atomic and multi-replica safe;
3. recover stale `received` and `processing` deliveries;
4. add deterministic per-trigger/action idempotency;
5. implement the SSRF-safe outbound transport;
6. add payload retention and delivery admin/team authorization;
7. add callback provenance/depth limits for `create_task` automations.

### Phase 1: Typed Outbound Subscriptions

1. add schemas, migrations, dual-dialect queries, and generated facade methods;
2. define and validate outbound YAML;
3. materialize subscriptions during definition sync;
4. centralize task-event persistence and transactionally enqueue delivery rows
   from every task event producer;
5. add the leased outbound worker, retries, audit, and metrics;
6. migrate existing `webhook` and `slack` callbacks;
7. retain `create_task` callbacks as Automations.

### Phase 2: Generic Inbound Endpoints

1. define and validate inbound YAML;
2. materialize endpoint identity and public URLs;
3. add the provider-neutral authenticated receiver;
4. implement the leased inbound worker and idempotent task action;
5. move GitHub receipt onto the generic inbox while keeping GitHub-specific
   parsing, enrichment, and trigger behavior;
6. migrate existing GitHub delivery history where practical.

### Phase 3: Proposal API And UI

1. extract reusable definition proposal services;
2. add validation/create/replace/delete proposal RPCs;
3. add WebhookService resource and delivery RPCs;
4. build overview, inbound, outbound, delivery, detail, and proposal routes;
5. add structured editors and Git source links;
6. add **Move to config** for legacy callbacks.

### Phase 4: Operational Completion

1. add manual retry, suspension, metrics, and alerts;
2. trigger definition sync after relevant GitHub pushes;
3. enforce delivery retention and payload cleanup;
4. remove migrated callback HTTP actions and deprecated APIs;
5. update `chetter-config` README, schemas, examples, and CI validation.

## Testing

Required automated coverage:

- strict YAML parsing, filename identity, scope resolution, and sync rollback;
- Git-managed mutation rejection and proposal authorization;
- inbound HMAC/bearer success, failure, replay, size, and rate limits;
- endpoint/task team attribution cannot be changed by payload data;
- atomic claims, stale lease recovery, retry classification, jitter bounds, and
  dead-letter transitions on MySQL/TiDB and PostgreSQL;
- crash recovery between receive, claim, side effect, and completion;
- idempotent task creation and outbound dispatch across retries;
- exact and wildcard task-event matching;
- SSRF cases: private IPs, redirects, proxy variables, alternate IP spellings,
  DNS rebinding, metadata endpoints, and oversized responses;
- secret references never appear in API responses, audit, logs, payload
  previews, or templates;
- Web API team scoping for resources and deliveries;
- proposal create/replace/delete, validation errors, PR status, and post-merge
  sync;
- UI component tests for structured forms, source ownership, redaction,
  delivery filters, retry confirmation, and responsive layouts.

## Migration And Compatibility

- Keep `/webhook/github` as a compatibility route until GitHub uses the generic
  receiver internally.
- Existing task/issue/PR trigger definitions remain unchanged.
- Existing callback rows remain active during migration.
- Convert webhook/slack callback rows only after destination and event matcher
  validation; preserve their IDs in migration metadata where possible.
- Do not silently migrate plaintext headers into Git. Require explicit secret
  references before promotion.
- Removed Git definitions disable delivery but retain historical rows.

## Non-Goals

- arbitrary workflow graphs or multiple actions per resource;
- arbitrary outbound HTTP methods or a general HTTP proxy;
- user-supplied JavaScript, CEL, JSONPath, or executable transforms;
- payload-driven team or credential selection;
- a database secret vault in the first implementation;
- repository-scoped webhook resources;
- exactly-once external side effects. The guarantee is durable at-least-once
  delivery with stable idempotency keys.

## Definition Of Done

- Inbound requests are authenticated, durably accepted, leased, retried, and
  idempotent.
- Outbound task events are transactionally converted into durable independent
  deliveries.
- Destination policy prevents SSRF and secrets never leave trusted resolution
  paths.
- Webhook resources are defined and reconciled from `chetter-config`.
- The UI can inspect all resources and deliveries and propose create/edit/delete
  changes through pull requests without raw JSON editing.
- Team scope is enforced for configuration, delivery history, retries, and
  proposals.
- GitHub webhook behavior and existing trigger semantics continue to work.
- MySQL/TiDB and PostgreSQL behavior is covered by equivalent queries and tests.
- Deprecated callback webhook/slack actions are migrated or explicitly retained
  with a documented owner.
