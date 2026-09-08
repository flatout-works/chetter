# Inbound webhooks

Generic inbound webhook endpoints (issue #120, epic #253 Phase 2) let external
systems — CI, monitoring, incidents, internal services — submit authenticated
JSON events that Chetter durably turns into exactly one configured action
(`create_task` in the first version).

GitHub's existing `/webhook/github` receiver and PR/issue trigger semantics are
unaffected.

## Configuration

Inbound endpoints are Git-managed in `chetter-config`:

```text
global/webhooks/inbound/*.yaml
groups/<team>/webhooks/inbound/*.yaml
```

- The filename stem must equal `name` and unknown YAML fields are rejected
  (strict schema: `schemas/inbound-webhook.schema.json`).
- Global endpoints must declare the fixed action team
  (`action.team_name`); team-scoped endpoints inherit their team from the
  directory and must not declare `team_name`. **Payload data can never select
  the team.**
- Secrets are referenced by environment variable name only
  (`auth.secret_env`) and are resolved from the server process environment at
  request time. Values are never stored, listed, logged, or returned. Set
  them on the server deployment, e.g. `CHETTER_WEBHOOK_CI_SECRET`.

Example (`examples/config-repo/global/webhooks/inbound/ci-build-events.yaml`):

```yaml
name: ci-build-events
enabled: true
auth:
  type: hmac_sha256
  secret_env: CHETTER_WEBHOOK_CI_SECRET
  signature_header: X-CI-Signature
  signature_prefix: sha256=
delivery_id_header: X-Delivery-ID
event_type_header: X-Event-Type
accepted_events: [build.completed, build.failed]
action:
  type: create_task
  prompt: Investigate CI event {{ .EventType }} for {{ .Payload.repository }}.
  agent: issue-triage
  timeout_sec: 900
  team_name: platform
```

Authentication options: `hmac_sha256` (HMAC-SHA256 over the raw body,
compared in constant time against `signature_prefix + hex(hmac)`) and
`bearer` (`Authorization: Bearer <token>`). Both compare in constant time
against the value of the referenced environment variable.

## Delivery lifecycle

1. A sync of the definitions repo materializes each definition into a
   `webhook_endpoints` row with an **opaque stable public id**. The id never
   changes while the source file keeps its path, so moving or renaming the
   file creates a new endpoint URL.
2. External systems send `POST /hooks/inbound/<public_id>` with
   `Content-Type: application/json` and the configured signature/bearer
   header. The receiver enforces method, content type, body size, per-endpoint
   rate, delivery backlog, and replay limits.
3. A **valid request returns `202 Accepted` only after the delivery row was
   durably inserted** into the `inbound_deliveries` inbox. Responses are
   idempotent: replaying the same `delivery_id_header` value returns 202
   without enqueueing a second action.
4. A leased multi-replica worker claims due deliveries atomically
   (`FOR UPDATE SKIP LOCKED`), recovers stale processing leases after a crash,
   renders the action prompt, and creates **exactly one task per delivery**.
   Task ids are derived deterministically from the delivery row, and the
   delivery/task correlation is persisted, so retries after a crash cannot
   create duplicate tasks.

Delivery statuses: `pending`, `processing`, `succeeded`, `retry_wait`,
`failed_permanent`, `dead_letter`.

## Sending events

```bash
SECRET=...   # must match the server's CHETTER_WEBHOOK_CI_SECRET

# Discover the endpoint's public URL (server-side):
#   chetter_list_inbound_endpoints

PUBLIC_ID=... # from the endpoint listing

BODY='{"repository":"acme/web","build_url":"https://ci.example.com/build/1"}'
SIG=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SECRET" | awk '{print "sha256="$2}')

curl -i -X POST "https://chetter.example.com/hooks/inbound/$PUBLIC_ID" \
  -H 'Content-Type: application/json' \
  -H "X-CI-Signature: $SIG" \
  -H 'X-Delivery-ID: build-2026-0001' \
  -H 'X-Event-Type: build.completed' \
  --data "$BODY"
```

The task prompt is a strict Go template over normalized metadata
(`{{ .EndpointName }}`, `{{ .EventType }}`, `{{ .DeliveryID }}`,
`{{ .SourceIP }}`) and the decoded JSON payload
(`{{ .Payload.<field> }}`, or `{{ .PayloadRaw }}` for the raw JSON text).
Raw request headers are never expanded into prompts or task environment
variables.

## Inspection and audit

- `chetter_list_inbound_endpoints` — endpoint URL, health (`enabled`), source
  path, auth type, secret availability (`secret_env` name and a
  `secret_configured` boolean — never the value), action, and team scope.
- `chetter_list_inbound_deliveries` — status, attempts, next attempt,
  linked task id, and error text (never payloads).
- Team-scoped tokens see only their own endpoints and deliveries.
- Audit events cover authentication failures, receipt, replay, processing,
  retries, completion, permanent failure, and dead-letter transitions without
  payload or secret leakage.
