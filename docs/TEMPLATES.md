# Generated Artifact Templates

This document defines the canonical MVP artifact formats for generated API projects. `PROCESS.md` owns the workflow; this document owns the file shapes the agent should produce.

The MVP artifact model is intentionally small:

```text
docs/VISION.md
docs/SPEC.md
api/openapi.yaml
Dockerfile
.dockerignore
SKILL.md
README.md
```

`docs/SPEC.md` is the single source of truth for generated behavior. Separate `REQUIREMENTS.md`, `MODEL.md`, and `DEPLOYMENT.md` files are not required for the MVP.

## General Rules

- Keep artifact filenames stable.
- Keep `docs/SPEC.md` human-readable first and parseable second.
- Use stable IDs only for acceptance criteria and invariants: `AC-{N}` and `INV-{N}`.
- Do not renumber existing IDs during changes; add a new ID when behavior changes materially.
- Every generated test that verifies approved behavior should include `// SPEC: AC-{N}` or `// SPEC: INV-{N}`.
- Heading-path `// SPEC:` references are not part of the simplified process.
- EARS-style wording is allowed, but not required.
- Markdown tables are allowed where they improve reviewability, but the builder should tolerate minor formatting variation.

## `docs/VISION.md`

Purpose: capture product intent before the API contract details.

Required template:

```markdown
# Vision

## Summary

{1-3 sentences describing the API and who it serves.}

## Users

| User | Goal | Notes |
|---|---|---|
| {actor} | {what they need to accomplish} | {constraints or context} |

## Goals

- {measurable or reviewable product goal}

## Non-Goals

- {explicitly out-of-scope behavior}

## Success Criteria

| Criterion | How It Will Be Verified |
|---|---|
| {observable success condition} | {test, smoke check, user review, deployment check} |

## Open Questions

| Question | Blocks | Status |
|---|---|---|
| {question} | spec / implementation / deployment | open / answered / deferred |
```

Rules:

- `Summary`, `Users`, `Goals`, `Non-Goals`, and `Success Criteria` are required.
- Open questions that would change API behavior must be answered or explicitly deferred before implementation.
- Vision should change rarely. Feature requests usually update `docs/SPEC.md`.

## `docs/SPEC.md`

Purpose: define the approved external API behavior and the domain rules the implementation must protect.

Required template:

```markdown
# Spec: {Project Name}

> Generated.
> Version: {version} | Date: {date}

## Summary

{1-3 sentence description of what this API does and who it serves.}

## Users And Ownership

| Actor | Owns / Can Access | Notes |
|---|---|---|
| {actor} | {resources or operations} | {authorization rule} |

## Entities

### {EntityName}

- Table: `{table_name}`
- Ownership: {who owns this record and how access is scoped}

| Field | Type | Required | Constraints | Description |
|---|---|---|---|---|
| id | uuid | yes | generated | Primary key |
| created_at | timestamp | yes | auto | Creation timestamp |
| updated_at | timestamp | yes | auto | Last update timestamp |

## Relationships

| From | To | Type | Constraint |
|---|---|---|---|
| Todo.user_id | User.id | many-to-one | required |

## State Machines

### {EntityName} State Machine

| From | To | Allowed? | Condition |
|---|---|---|---|
| draft | active | yes | {condition} |
| archived | active | no | archived records are terminal |

## Invariants

### INV-1: {Invariant title}

- Rule: {Entity.field} must {condition}.
- Test candidate: {property or table-driven test idea}

## API Operations

### {operationId} - {METHOD} {path}

- Summary: {one-line description}
- Auth: required / optional / none
- Input: `{SchemaName}`
- Output 200: `{SchemaName}`
- Errors:
  - 400 - `{ERROR_CODE}`: {condition}
  - 401 - `UNAUTHORIZED`: {condition}

## Acceptance Criteria

### AC-1: {Title}

- Operation: `{operationId}`
- Given: {preconditions}
- When: {HTTP request or event}
- Then: {status, response, side effects}

## Security

- Auth scheme: {Bearer JWT / API key / none}
- Token claims: {claims}
- Public endpoints: {paths}
- Ownership: {access control rule}

## Environment

| Variable | Default | Required | Secret? | Description |
|---|---|---|---|---|
| DATABASE_DSN | - | yes | yes | TiDB/MySQL connection string |
| JWT_SECRET | - | yes | yes | HMAC signing key |
| PORT | 8080 | no | no | HTTP listen port |
| LOG_LEVEL | info | no | no | debug, info, warn, error |
```

Rules:

- Every operation should have at least one happy-path `AC-{N}`.
- Every listed error should have an `AC-{N}` unless explicitly deferred in prose.
- Every important invariant should have an `INV-{N}`.
- Tests should reference acceptance criteria and invariants by stable ID: `// SPEC: AC-1` or `// SPEC: INV-1`.
- `operationId` uses kebab-case verb+noun, such as `create-refund`.
- Paths use `/api/v1/...` unless the user explicitly asks otherwise.
- Generated APIs target Go + Huma/Gin + TiDB first.

Allowed field types:

| Type | Meaning |
|---|---|
| `uuid` | Stable identifier stored as string unless the stack provides a native UUID type |
| `string` | Text |
| `int` | 32-bit integer |
| `int64` | 64-bit integer, money cents, counters |
| `float64` | Floating point value where acceptable |
| `bool` | Boolean |
| `timestamp` | Timestamp/date-time |
| `enum` | String enum with explicit values |
| `json` | Structured object where schema is documented |

## `.env.example`

Purpose: show runtime configuration without storing secrets.

Required shape:

```dotenv
DATABASE_DSN=
JWT_SECRET=
PORT=8080
LOG_LEVEL=info
```

Rules:

- Do not commit real secrets.
- Secret values should come from the builder's local project configuration, provider configuration, or deployment platform secrets.
- Deployment history belongs in builder state, not a generated Markdown document.

## `Dockerfile`

Purpose: provide a root, production-like container build as soon as the generated project is scaffolded.

Required shape:

```dockerfile
# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.23

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /
ENV PORT=8080
COPY --from=builder --chown=nonroot:nonroot /out/api /api
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]
```

Rules:

- Create `Dockerfile` in the repo root during initial workspace setup, before handler implementation.
- Keep the runtime stage distroless and non-root.
- Do not add shell-form runtime commands or curl-based health checks; distroless has no shell or curl.
- The default binary target is `./cmd/server`; change it only if the generated project intentionally uses a different server entrypoint.

## `.dockerignore`

Purpose: keep local state and secrets out of Docker build contexts.

Required shape:

```gitignore
.git
.deploy
bin
tmp
coverage.out
*.test
*.log
.env
.env.*
!.env.example
```

## `SKILL.md`

Purpose: give future agents project-specific conventions without rereading the full codebase.

Minimal template:

```markdown
# {Project Name} Skill

## Stack

- Go + Huma/Gin
- TiDB/MySQL-compatible SQL
- sqlc + Goose
- Docker

## Conventions

- Keep business logic in `internal/service/`.
- Keep Huma operation registration in `internal/api/operations/`.
- Use `// SPEC: AC-{N}` and `// SPEC: INV-{N}` comments in tests.
- Do not bypass public API behavior in MCP tools.

## Common Tasks

- Add an API operation by updating `docs/SPEC.md` first.
- Add a migration with Goose and update sqlc queries.
- Add tests mapped to the relevant spec IDs.
```

## Future Validation

The builder should eventually validate:

| Artifact | Validation |
|---|---|
| `VISION.md` | Required sections, no blocking open questions before implementation |
| `SPEC.md` | `AC-*` and `INV-*` IDs are unique, operations have acceptance criteria, tests map to stable IDs |
| `.env.example` | Required runtime variables exist and secrets are empty |
| `Dockerfile` | Exists at repo root, uses a multi-stage distroless non-root runtime, builds `./cmd/server` |
| `.dockerignore` | Excludes local state and secrets while keeping `.env.example` |
| `SKILL.md` | Stack and conventions are present |

The MVP should avoid strict Markdown table parsing unless the parser is resilient to harmless formatting differences.
