---
name: flatout-backend
description: Backend development for generated APIs using Go, Huma, Gin, OpenAPI 3.1, sqlc, goose, MySQL/TiDB, JWT + bcrypt, Staticcheck, Docker, and a generated MCP server for API exploration. Use when generating or modifying backend APIs, Huma operation definitions, Gin handlers, sqlc queries, goose migrations, MySQL schemas, auth flows, static analysis, or MCP API tools.
---

# Backend (Go + Huma + Gin + OpenAPI + MySQL + sqlc + MCP)

Senior Go backend developer specializing in the generated API stack: Huma for OpenAPI generation and validation, Gin for HTTP routing and middleware, OpenAPI 3.1 as the mechanically-derived contract, sqlc for type-safe database queries, goose for migrations, MySQL/TiDB as the datastore, JWT + bcrypt for auth, Staticcheck for dead-code and correctness linting, Docker for deployment, and a generated MCP server so agents can explore and call the API.

The product loop is:

```text
Chat -> Vision -> docs/SPEC.md -> Huma Structs -> OpenAPI Spec -> Verified Code -> MCP Server
```

`docs/SPEC.md` is the approved intended behavior. Huma structs produce the executable API contract, `api/openapi.yaml`, from the same definitions that power the handlers, so runtime validation and OpenAPI stay mechanically aligned.

Use this skill for generated customer APIs. ConnectRPC can still be used for internal control plane, but generated user-facing APIs should default to Huma + Gin because OpenAPI correctness is guaranteed by construction.

## Why Huma + Gin (Not Pure Gin)

Pure Gin requires writing OpenAPI YAML by hand, then building Gin handlers to match. These inevitably diverge — a handler returns a field the spec omits, or a new endpoint is added without updating the spec. The skill warns about this, but warnings don't prevent bugs.

Huma eliminates the problem mechanically. You write Go structs with Huma tags. Huma generates the OpenAPI spec from those structs. Huma validates incoming requests against the same structs. The spec and the code are the same source — change one, and the other updates automatically.

```
┌─────────────────────────────────────────────┐
│  Huma-annotated Go structs                  │
│  (single source of truth for the API)       │
├─────────────────────────────────────────────┤
│  ┌─────────────┐  ┌───────────────┐         │
│  │ OpenAPI 3.1 │  │  Gin handlers │         │
│  │ (generated) │  │  (implemented)│         │
│  └─────────────┘  └───────────────┘         │
│         │                 │                 │
│         ▼                 ▼                 │
│  Contract tests    Running HTTP API         │
│  validate that     serves requests          │
│  the API matches   using the same           │
│  the spec          structs                  │
└─────────────────────────────────────────────┘
```

Gin is not replaced — it is the HTTP router. Huma wraps Gin via the `humagin` adapter. Gin handles routing, middleware, and the request/response lifecycle. Huma handles OpenAPI generation, JSON Schema validation, interactive docs, and client SDK generation.

## Specialist Skills

Load these when the context matches:

| Specialist Skill | Load When |
|-----------------|-----------|
| `openapi` | Reviewing or refining the generated OpenAPI 3.1 spec, schemas, responses, security schemes, pagination, and extensions |
| `golang-pro` | Go project structure, idioms, interfaces, errors, concurrency, benchmarks, and table-driven tests |
| `mysql` | MySQL/InnoDB schema design, indexing, transactions, query tuning, and operational constraints |
| `tidb-sql` | TiDB-specific SQL compatibility, distributed SQL behavior, vector indexes, and TiDB Cloud concerns |
| `sqlc` | sqlc config, query annotations, generated code workflow, and null handling |
| `go-mcp-server-generator` | Creating the generated MCP server that exposes API operations as tools |

## Core Workflow

The spec-driven workflow for every generated API:

1. **Vision, spec, and deployable skeleton** — Produce `docs/VISION.md` and `docs/SPEC.md` from the user's requirements, then immediately create root deployment artifacts: `Dockerfile`, `.dockerignore`, and `Makefile`. `docs/SPEC.md` contains domain entities, invariants, API operations, security, environment, and given/when/then acceptance criteria.
2. **Early containerization** — Copy or adapt `.flatout/templates/go-huma-gin/Dockerfile` when present. The root `Dockerfile` must be a multi-stage distroless build targeting `./cmd/server` and should exist before handler implementation starts.
3. **API spec approval** — The user must review and approve `docs/SPEC.md` before implementation code is written.
4. **Spec Validation** — Run validation checks against `docs/SPEC.md`: structural completeness (all references resolve), semantic consistency (auth, error codes, pagination), and coverage (persisted resources have CRUD unless explicitly omitted, every operation has acceptance criteria). Fix issues before proceeding.
5. **Database schema** — Write goose migrations from `docs/SPEC.md`. Add indexes for query patterns.
6. **sqlc queries** — Write type-safe SQL queries in `db/queries/` for all CRUD and business operations defined in `docs/SPEC.md`.
7. **API contract (Huma skeleton)** — Write Go structs with Huma tags derived from the API Operations section of `docs/SPEC.md`. Input/output types match the model definitions. This is the spec review stage — no handler implementation yet.
8. **OpenAPI review** — Generate `api/openapi.yaml` from the Huma skeleton. The user reviews and approves the machine-readable contract before implementation. The OpenAPI spec can also be browsed interactively at `/docs` (Scalar UI).
9. **Handler implementation** — Implement the Huma operation handlers using the approved structs. Business logic goes in `internal/service/`, not in handlers.
10. **Test generation** — Generate tests from `docs/SPEC.md` using the `generate_tests_from_spec` and `generate_integration_tests` prompts. Every test function includes a `// SPEC: AC-*` or `// SPEC: INV-*` comment linking it to a stable spec ID. Uses standard Go `testing` package only.
11. **MCP server** — Generate an MCP server that exposes API operations as tools, OpenAPI spec + docs + skill as resources, and common tasks as prompts.
12. **Verification** — Run `go test ./...` to execute all tests. The spec renderer maps results back to `docs/SPEC.md` sections showing pass/fail/untested indicators.
13. **Build & verify** — Run `make generate`, `go build ./...`, `go vet ./...`, `staticcheck ./...`, `docker build -t devfleet-api:latest .`, and all tests.

## Default Generated Stack

| Layer | Tool | Purpose |
|-------|------|---------|
| API contract + validation | Huma v2 | OpenAPI 3.1 generation, JSON Schema validation, interactive docs |
| HTTP router | Gin (via `humagin` adapter) | Request routing, middleware, serving |
| Database | MySQL or TiDB | MySQL-compatible default datastore |
| DB access | sqlc | Type-safe Go generated from SQL |
| Migrations | goose | Versioned SQL migrations |
| Auth | JWT + bcrypt | Access tokens and password hashing |
| Config | env vars + `.env` in dev | Twelve-factor deployment |
| Logging | slog | Structured JSON logs |
| Static analysis | Staticcheck | Dead-code, correctness, simplification, and performance linting |
| Deployment | Docker | Portable generated API runtime |
| Agent interface | MCP server | Explore and call generated API operations from agents |

## Project Layout

```text
cmd/
  server/
    main.go                 # Gin + Huma HTTP server entrypoint
  migrate/
    main.go                 # Optional migration runner
  mcp/
    main.go                 # MCP server exposing API operations as tools

internal/
  api/
    router.go               # Gin router setup + humagin adapter
    middleware.go            # auth, recovery, request IDs, logging
    operations/
      things.go              # Huma operation registrations (one file per resource)
  config/
    config.go               # env-based config
  repository/
    db.go                   # sqlc DBTX interface
    models.go               # sqlc models
    *.sql.go                # sqlc generated query methods
  service/
    things.go               # Business logic between handlers and repositories
  auth/
    jwt.go                  # JWT issue/verify
    password.go             # bcrypt hash/verify
  mcp/
    tools.go                # MCP tool registrations and API client wrappers

api/
  openapi.yaml              # Generated OpenAPI 3.1 contract (mechanical output from Huma structs)

docs/
  VISION.md                 # Product goal, users, non-goals, success criteria
  SPEC.md                   # Human-readable implementation contract
  .env.example              # Runtime config template, no secrets

db/
  migrations/
    001_create_*.sql        # goose migrations
  queries/
    *.sql                   # sqlc named queries

Dockerfile                   # Multi-stage build (required)
.dockerignore                # Docker build ignore file (required)
docker-compose.yml           # Local orchestration (required)
Makefile                     # Standard targets: build, test, lint, build-docker, run-docker (required)
sqlc.yaml
go.mod
```

## Step 1: Spec Workspace and Deployable Skeleton - Before Any Code

Before writing database migrations, SQL queries, or Go structs, produce the required spec workspace under `docs/` and the root deployment skeleton:

- `docs/VISION.md`: product goal, users, non-goals, and success criteria.
- `docs/SPEC.md`: the implementation contract that feeds code generation, including entities, invariants, operations, security, environment, and acceptance criteria.
- `.env.example`: runtime config template with empty secret values.
- `Dockerfile`: root multi-stage distroless container build. Use `.flatout/templates/go-huma-gin/Dockerfile` when present.
- `.dockerignore`: root Docker build ignore file. Use `.flatout/templates/go-huma-gin/.dockerignore` when present.
- `Makefile`: standard build, test, generate, and Docker targets.

Follow the generated artifact templates. If `.flatout/templates/generated-docs.md` exists in the workspace, treat it as the canonical local copy:

- `docs/VISION.md` must include Summary, Users, Goals, Non-Goals, Success Criteria, and Open Questions.
- `docs/SPEC.md` must use stable `AC-*` and `INV-*` IDs. Every operation has auth, input, output, errors, and at least one happy-path acceptance criterion.
- `.env.example` must list runtime variables and never contain real secrets.
- `Dockerfile` must be created early, even if `./cmd/server` is only a planned build target at this point. Keep it at repo root so `docker build -t devfleet-api:latest .` is the canonical container build.

`docs/SPEC.md` follows the API spec template and must include these sections:

- **Summary**: 1-3 sentence description of what the API does
- **Entities**: For each entity — field table (name, type, required, constraints, description), state machine transitions (from → to → allowed), testable invariants, and relationships
- **API Operations**: For each operation — operationId, method, path, auth, input/output types, error list with status codes
- **Acceptance Criteria**: Given/When/Then format, at least one happy-path per operation plus error cases
- **Security**: Auth scheme, token claims, public endpoints, ownership model
- **Environment**: Required env vars with defaults

The spec must follow a fixed set of allowed types: `uuid`, `string`, `int`, `int64`, `float64`, `bool`, `timestamp`, `enum` (with enumerated values).

Example entity definition from the Entities section of `docs/SPEC.md`:

```markdown
### `Order` {StateMachine: pending → [confirmed, shipped, delivered, cancelled]}

| Field | Type | Required | Constraints | Description |
|-------|------|----------|-------------|-------------|
| id | uuid | yes | auto-generated | Primary key |
| customer_id | uuid | yes | FK → customers.id | Customer |
| status | enum | yes | Values: pending, confirmed, shipped, delivered, cancelled | Current status |
| total_cents | int64 | yes | minimum: 0 | Total in cents |
| created_at | timestamp | yes | auto | Creation time |
| updated_at | timestamp | yes | auto | Last update time |

**State Transitions**:
| From | To | Allowed? |
|------|----|----------|
| pending | confirmed | yes |
| pending | cancelled | yes |
| confirmed | shipped | yes |
| confirmed | cancelled | yes |
| shipped | delivered | yes |
| delivered | * | no |
| cancelled | * | no |

**Invariants**:
- `total_cents` must be >= 0 _(property: non-negative total)_
- `customer_id` must reference an existing customer _(property: valid foreign key)_
- Cancelled orders must be immutable _(property: no mutations on cancelled)_
```

`docs/SPEC.md` is the **human-readable implementation contract** and `api/openapi.yaml` is the **machine-readable implementation contract**. Together they drive:
- Migrations: columns, types, indexes derived from field tables
- sqlc queries: CRUD operations from entity definitions
- Huma structs: input/output types from API Operations + entity fields
- State machine: transition table → service layer enforcement
- Tests: invariants → property tests, acceptance criteria → golden-path tests

The user must review and approve the spec workspace before implementation code is written. This is the **intent approval gate** - it confirms the AI understood the requirements correctly.

### Step 1a: Spec Validation

Before writing code, validate the spec with these prompts:

- **`validate_spec`**: Structural completeness — all field references resolve, all state transitions reference valid states, all paths are unique, all acceptance criteria reference real operations
- **`validate_spec_consistency`**: Semantic consistency — auth is consistent, error codes are uniform, timestamps are `created_at`/`updated_at` everywhere, field names match across sections
- **`validate_spec_coverage`**: Coverage — persisted resources have CRUD unless explicitly omitted, every operation has acceptance criteria, every error condition has a test case, every state transition is tested

Fix all validation failures before proceeding to Step 2.

## Step 2: Go Module Setup

```bash
go mod init github.com/username/project-name
go get github.com/danielgtaylor/huma/v2
go get github.com/danielgtaylor/huma/v2/adapters/humagin
go get github.com/gin-gonic/gin
```

The `go.mod` should include `huma/v2` (for operation definitions, validation, OpenAPI), `humagin` (Gin adapter), and `gin` (HTTP router).

## Step 3: Huma Operation Pattern (Spec Review Stage)

Write Huma operations as skeletons first — structs with tags but no implementation. This is the spec review phase. The user reviews the generated OpenAPI before any handler code is written.

```go
package operations

import (
    "context"
    "time"

    "github.com/danielgtaylor/huma/v2"
)

// --- Request / Response types (these ARE the API contract) ---

type Thing struct {
    ID        string    `json:"id" example:"550e8400-e29b-41d4-a716-446655440000" doc:"Unique identifier"`
    UserID    string    `json:"user_id" doc:"Owner ID"`
    Name      string    `json:"name" example:"My Thing" doc:"Thing name"`
    Status    string    `json:"status" example:"active" enum:"draft,active,archived" doc:"Current status"`
    CreatedAt time.Time `json:"created_at" doc:"Creation timestamp"`
    UpdatedAt time.Time `json:"updated_at" doc:"Last update timestamp"`
}

type CreateThingInput struct {
    Body struct {
        Name   string `json:"name" minLength:"1" maxLength:"255" example:"My Thing" doc:"Thing name"`
        UserID string `json:"user_id" format:"uuid" doc:"Owner ID"`
    }
}

type GetThingInput struct {
    ID string `path:"id" format:"uuid" doc:"Thing ID"`
}

type ListThingsInput struct {
    UserID    string `query:"user_id" format:"uuid" doc:"Filter by owner"`
    PageToken string `query:"page_token" doc:"Opaque pagination cursor"`
    PageSize  int    `query:"page_size" minimum:"1" maximum:"100" default:"20" doc:"Items per page"`
}

type UpdateThingInput struct {
    ID   string `path:"id" format:"uuid" doc:"Thing ID"`
    Body struct {
        Name   *string `json:"name,omitempty" minLength:"1" maxLength:"255" doc:"Thing name"`
        Status *string `json:"status,omitempty" enum:"draft,active,archived" doc:"Current status"`
    }
}

type DeleteThingInput struct {
    ID string `path:"id" format:"uuid" doc:"Thing ID"`
}

type ListThingsOutput struct {
    Body struct {
        Items         []Thing `json:"items"`
        NextPageToken string  `json:"next_page_token,omitempty" doc:"Present if more results exist"`
        TotalCount    int     `json:"total_count" doc:"Total number of results"`
    }
}

// --- Error response (standard for all endpoints) ---

type ErrorDetail struct {
    Field string `json:"field,omitempty" doc:"Field that caused the error"`
    Issue string `json:"issue" doc:"Description of the issue"`
}

type ErrorResponse struct {
    Code    string        `json:"code" example:"NOT_FOUND" doc:"Machine-readable error code"`
    Message string        `json:"message" example:"Thing not found" doc:"Human-readable error message"`
    Details []ErrorDetail `json:"details,omitempty" doc:"Per-field error details"`
}
```

Huma struct tags map directly to OpenAPI schema properties:
- `json:"name"` → property name
- `doc:"description"` → property description
- `example:"value"` → example value in docs
- `enum:"a,b,c"` → allowed values
- `format:"uuid"` → UUID type
- `minLength`, `maxLength`, `minimum`, `maximum` → validation constraints
- `required` is determined by whether the field is a pointer or not
- `path:"id"` / `query:"page_size"` / `header:"Authorization"` → parameter source
- Omitempty pointers (e.g. `*string`) become optional fields in OpenAPI

## Step 4: Huma Operation Registration (Full Implementation)

After the spec is approved, implement the handlers. Each operation is registered with `huma.Register` and a handler function that calls the service layer:

```go
package operations

import (
    "context"
    "errors"
    "net/http"

    "github.com/danielgtaylor/huma/v2"
)

type ThingOperations struct {
    svc *service.ThingService
}

func NewThingOperations(svc *service.ThingService) *ThingOperations {
    return &ThingOperations{svc: svc}
}

func (o *ThingOperations) Register(api huma.API) {
    huma.Register(api, huma.Operation{
        OperationID: "list-things",
        Method:      http.MethodGet,
        Path:        "/api/v1/things",
        Summary:     "List things",
        Description: "Returns a paginated list of things.",
        Tags:        []string{"Things"},
        Security:    []map[string][]string{{"BearerAuth": {}}},
        Errors:      []int{http.StatusUnauthorized},
    }, o.ListThings)

    huma.Register(api, huma.Operation{
        OperationID: "create-thing",
        Method:      http.MethodPost,
        Path:        "/api/v1/things",
        Summary:     "Create a thing",
        Description: "Creates a new thing.",
        Tags:        []string{"Things"},
        Security:    []map[string][]string{{"BearerAuth": {}}},
        Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized},
    }, o.CreateThing)

    huma.Register(api, huma.Operation{
        OperationID: "get-thing",
        Method:      http.MethodGet,
        Path:        "/api/v1/things/{id}",
        Summary:     "Get a thing",
        Description: "Returns a single thing by ID.",
        Tags:        []string{"Things"},
        Security:    []map[string][]string{{"BearerAuth": {}}},
        Errors:      []int{http.StatusNotFound, http.StatusUnauthorized},
    }, o.GetThing)

    huma.Register(api, huma.Operation{
        OperationID: "update-thing",
        Method:      http.MethodPut,
        Path:        "/api/v1/things/{id}",
        Summary:     "Update a thing",
        Description: "Updates a thing's name or status.",
        Tags:        []string{"Things"},
        Security:    []map[string][]string{{"BearerAuth": {}}},
        Errors:      []int{http.StatusBadRequest, http.StatusNotFound, http.StatusUnauthorized},
    }, o.UpdateThing)

    huma.Register(api, huma.Operation{
        OperationID: "delete-thing",
        Method:      http.MethodDelete,
        Path:        "/api/v1/things/{id}",
        Summary:     "Delete a thing",
        Description: "Deletes a thing permanently.",
        Tags:        []string{"Things"},
        Security:    []map[string][]string{{"BearerAuth": {}}},
        Errors:      []int{http.StatusNoContent, http.StatusNotFound, http.StatusUnauthorized},
    }, o.DeleteThing)
}

func (o *ThingOperations) ListThings(ctx context.Context, input *ListThingsInput) (*ListThingsOutput, error) {
    things, nextToken, total, err := o.svc.ListThings(ctx, input.UserID, input.PageSize, input.PageToken)
    if err != nil {
        return nil, huma.Error500InternalServerError("failed to list things", err)
    }
    return &ListThingsOutput{
        Body: struct {
            Items         []Thing `json:"items"`
            NextPageToken string  `json:"next_page_token,omitempty"`
            TotalCount    int     `json:"total_count"`
        }{Items: things, NextPageToken: nextToken, TotalCount: total},
    }, nil
}

func (o *ThingOperations) CreateThing(ctx context.Context, input *CreateThingInput) (*struct{ Body Thing }, error) {
    thing, err := o.svc.CreateThing(ctx, input.Body.Name, input.Body.UserID)
    if err != nil {
        if errors.Is(err, service.ErrInvalidInput) {
            return nil, huma.Error400BadRequest("invalid input", err)
        }
        return nil, huma.Error500InternalServerError("failed to create thing", err)
    }
    return &struct{ Body Thing }{Body: *thing}, nil
}

func (o *ThingOperations) GetThing(ctx context.Context, input *GetThingInput) (*struct{ Body Thing }, error) {
    thing, err := o.svc.GetThing(ctx, input.ID)
    if err != nil {
        if errors.Is(err, service.ErrNotFound) {
            return nil, huma.Error404NotFound("thing not found")
        }
        return nil, huma.Error500InternalServerError("failed to get thing", err)
    }
    return &struct{ Body Thing }{Body: *thing}, nil
}

func (o *ThingOperations) UpdateThing(ctx context.Context, input *UpdateThingInput) (*struct{ Body Thing }, error) {
    thing, err := o.svc.UpdateThing(ctx, input.ID, input.Body.Name, input.Body.Status)
    if err != nil {
        if errors.Is(err, service.ErrNotFound) {
            return nil, huma.Error404NotFound("thing not found")
        }
        if errors.Is(err, service.ErrInvalidTransition) {
            return nil, huma.Error409Conflict("invalid state transition")
        }
        return nil, huma.Error500InternalServerError("failed to update thing", err)
    }
    return &struct{ Body Thing }{Body: *thing}, nil
}

func (o *ThingOperations) DeleteThing(ctx context.Context, input *DeleteThingInput) (*struct{}, error) {
    if err := o.svc.DeleteThing(ctx, input.ID); err != nil {
        if errors.Is(err, service.ErrNotFound) {
            return nil, huma.Error404NotFound("thing not found")
        }
        return nil, huma.Error500InternalServerError("failed to delete thing", err)
    }
    return nil, nil
}
```

Key points:
- `huma.Register` takes the API instance, an `huma.Operation` descriptor, and a handler function
- The handler's input type drives parameter parsing and validation (path, query, body, header)
- The output type drives response serialization and OpenAPI response schema
- Huma validates all inputs against the struct tags before the handler runs
- `huma.Error404NotFound`, `huma.Error400BadRequest`, etc. produce standard error responses
- Business logic lives in `internal/service/`, not in operation handlers

## Test Generation from `docs/SPEC.md`

Every generated test must include a `// SPEC:` comment linking it to the stable acceptance criterion or invariant it validates. Use stable IDs such as `AC-1` and `INV-2`; heading-path mappings are not part of the simplified process. This enables the builder to render pass/fail/untested status in the spec viewer.

### Test-to-Spec Mapping Convention

```go
// SPEC: INV-1
func TestOrder_Invariant_TotalCentsNeverNegative(t *testing.T) { ... }

// SPEC: AC-3
func TestOrder_StateMachine_CancelledIsTerminal(t *testing.T) { ... }

// SPEC: AC-1
func TestOrder_CreateOrder_HappyPath(t *testing.T) { ... }

// SPEC: AC-3
func TestOrder_StateMachine_RejectsCancelledModification(t *testing.T) { ... }

// SPEC: AC-4
func TestOrder_CreateOrder_EmptyName_Returns400(t *testing.T) { ... }
```

Mapping rules:
- Use stable IDs only: `SPEC: AC-1` or `SPEC: INV-2`.
- `AC-*` maps to an acceptance criterion in `docs/SPEC.md`.
- `INV-*` maps to an invariant in `docs/SPEC.md`.
- Multiple `// SPEC:` comments on one test are allowed (test covers multiple spec sections)

### Test File Layout

Tests are placed alongside the code they test using Go's convention:

```
internal/
├── service/
│   ├── orders.go              # Business logic
│   └── orders_test.go         # Property + state machine tests (SPEC: INV-* or AC-*)
├── api/
│   └── operations/
│       ├── orders.go          # Huma operation registrations
│       └── orders_test.go     # Golden-path + contract tests (SPEC: AC-*)
```

### Test Categories Generated from `docs/SPEC.md`

| Category | Generated From | File Location | SPEC ID |
|----------|---------------|---------------|----------------|
| **Property tests** | Invariants | `internal/service/{entity}_test.go` | `INV-*` |
| **State machine tests** | Acceptance criteria or invariants | `internal/service/{entity}_test.go` | `AC-*` or `INV-*` |
| **Golden-path tests** | Acceptance criteria | `internal/api/operations/{entity}_test.go` | `AC-*` |
| **Contract tests** | Acceptance criteria / operations | `internal/api/operations/{entity}_test.go` | `AC-*` |
| **Integration tests** | Multi-step acceptance criteria | `internal/api/operations/{entity}_test.go` | `AC-*` |

All tests use the standard Go `testing` package — no external test framework required. Assertions use `t.Errorf` with descriptive messages. HTTP tests use `net/http/httptest` with the Gin router.

### Test Generation Prompts

The MCP server exposes these prompts for test generation. Each reads `docs/SPEC.md` and produces Go test files:

- **`generate_tests_from_spec`**: Reads `docs/SPEC.md` and generates property tests, state machine tests, golden-path tests, and contract tests in one pass
- **`generate_integration_tests`**: Generates end-to-end integration tests that exercise the full API through the Gin router

## Step 5: Router Setup (humagin Adapter)

```go
package api

import (
    "net/http"

    "github.com/danielgtaylor/huma/v2"
    "github.com/danielgtaylor/huma/v2/adapters/humagin"
    "github.com/gin-gonic/gin"

    "github.com/username/project-name/internal/api/operations"
    "github.com/username/project-name/internal/config"
    "github.com/username/project-name/internal/service"
)

func NewRouter(cfg *config.Config, services Services) http.Handler {
    r := gin.New()
    r.Use(gin.Recovery())
    r.Use(RequestIDMiddleware())
    r.Use(LoggingMiddleware())

    // Huma wraps Gin router. The config determines the OpenAPI info.
    humaConfig := huma.DefaultConfig(cfg.APIName, cfg.APIVersion)
    humaConfig.Servers = []*huma.Server{
        {URL: cfg.ServerURL, Description: cfg.Environment},
    }
    humaConfig.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
        "BearerAuth": {
            Type:        "http",
            Scheme:      "bearer",
            BearerFormat: "JWT",
        },
    }

    api := humagin.New(r, humaConfig)

    // Health check (no auth)
    r.GET("/health", func(c *gin.Context) {
        c.JSON(http.StatusOK, gin.H{"status": "ok"})
    })

    // API group — auth middleware applied
    authorized := r.Group("/api/v1")
    authorized.Use(AuthMiddleware(services.Auth))

    // Wrap the authorized group with Huma for versioned API
    apiV1 := humagin.New(authorized, humaConfig)

    // Register operations
    things := operations.NewThingOperations(services.ThingService)
    things.Register(apiV1)

    return r
}
```

Key points:
- `humagin.New(r, config)` wraps the Gin router (or a Gin route group)
- The `humaConfig` sets API metadata (title, version, servers, security schemes)
- Gin middleware (auth, logging, recovery) applies as usual
- Huma operations are registered on the `huma.API` instance
- Huma automatically serves `/openapi.yaml`, `/openapi.json`, and interactive docs at `/docs`

## Step 6: Main Entrypoint

```go
package main

import (
    "log"
    "net/http"

    "github.com/username/project-name/internal/api"
    "github.com/username/project-name/internal/config"
    "github.com/username/project-name/internal/repository"
    "github.com/username/project-name/internal/service"
)

func main() {
    cfg := config.Load()

    db := repository.MustConnect(cfg.DatabaseURL)
    defer db.Close()

    repo := repository.New(db)

    services := api.Services{
        Auth:         auth.NewJWTAuth(cfg.JWTSecret),
        ThingService: service.NewThingService(repo),
    }

    handler := api.NewRouter(cfg, services)

    addr := ":" + cfg.Port
    log.Printf("Listening on %s", addr)
    if err := http.ListenAndServe(addr, handler); err != nil {
        log.Fatalf("server error: %v", err)
    }
}
```

## Step 7: OpenAPI Generation

Huma generates the OpenAPI spec from the registered operations — no manual YAML writing needed:

```go
// cmd/gen-openapi/main.go — utility to write openapi.yaml to disk
package main

import (
    "os"

    "gopkg.in/yaml.v3"

    // Import the router package so operations are registered
    "github.com/username/project-name/internal/api"
    "github.com/username/project-name/internal/config"
)

func main() {
    cfg := config.Load()
    // Build router (registers all Huma operations)
    // Extract the Huma API instance to call api.OpenAPI()
    // Marshal to YAML and write to api/openapi.yaml
    b, _ := yaml.Marshal(api.OpenAPISpec)
    os.WriteFile("api/openapi.yaml", b, 0644)
}
```

The generated OpenAPI is the mechanically correct contract. Save it to `api/openapi.yaml` for the MCP server to serve as a resource. The same spec is available at runtime at `/openapi.yaml`.

## Step 1a: Early Containerization (Required)

Every generated project must include a root `Dockerfile`, `.dockerignore`, `docker-compose.yml`, and a `Makefile` with standard targets. This is a required step, not optional. Create the root `Dockerfile` during initial workspace setup before implementing handlers. The file may point at the planned `./cmd/server` package before that package exists.

### Dockerfile (Multi-Stage, Distroless)

Create `Dockerfile` in the repo root. If `.flatout/templates/go-huma-gin/Dockerfile` exists, copy it first and adapt only the binary path or exposed port if the project intentionally differs:

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

- Keep the `Dockerfile` at repo root. Do not put the only Dockerfile under `docker/`, `deploy/`, or `.flatout/deploy/`.
- Use a named builder stage and `gcr.io/distroless/static-debian12:nonroot` runtime stage.
- Build with `CGO_ENABLED=0`, `-trimpath`, and stripped linker flags.
- Run as `nonroot:nonroot`.
- Do not add shell-form `RUN`, `CMD`, or health checks to the distroless runtime stage. Distroless has no shell or curl. Add health checks at the orchestrator level unless the generated binary explicitly supports a probe subcommand.
- Verify with `docker build -t devfleet-api:latest .` before marking the project complete.

Create `.dockerignore` in the repo root:

```gitignore
.git
.flatout
bin
tmp
coverage.out
*.test
*.log
.env
.env.*
!.env.example
```

### docker-compose.yml

Create `docker-compose.yml` for local testing with the user's chosen database:

```yaml
services:
  db:
    image: mysql:8.0
    environment:
      MYSQL_ROOT_PASSWORD: localdev
      MYSQL_DATABASE: flatout_db
    ports:
      - "3306:3306"
  api:
    build: .
    ports:
      - "8080:8080"
    environment:
      - DATABASE_URL=root:localdev@tcp(db:3306)/flatout_db?parseTime=true
      - PORT=8080
    depends_on:
      - db
```

### Makefile

Create `Makefile` with these targets (agent and builder both rely on them):

```makefile
.PHONY: generate build test migrate build-docker run-docker

generate:
	go generate ./...
	go run ./cmd/gen-openapi

build:
	go build -o bin/api ./cmd/server

test:
	go test ./...

migrate:
	go run ./cmd/migrate

build-docker:
	docker build -t $(IMAGE):latest .

run-docker: build-docker
	docker run -d --name devfleet-api -p 8080:8080 $(IMAGE):latest

IMAGE ?= devfleet-api
```

The `build-docker` target must work from the repo root and produce a runnable image.

---

## SQL: sqlc Query Pattern

Queries live in `db/queries/<domain>.sql`.

```sql
-- name: GetThingByID :one
SELECT id, user_id, name, status, created_at, updated_at
FROM things
WHERE id = ?;

-- name: ListThingsByUser :many
SELECT id, user_id, name, status, created_at, updated_at
FROM things
WHERE user_id = ?
ORDER BY created_at DESC
LIMIT ?;

-- name: CreateThing :execresult
INSERT INTO things (id, user_id, name, status, created_at, updated_at)
VALUES (?, ?, ?, 'draft', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);

-- name: UpdateThing :exec
UPDATE things
SET name = COALESCE(?, name),
    status = COALESCE(?, status),
    updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND user_id = ?;
```

Use `?` placeholders for MySQL/TiDB. After editing queries, run `make generate`.

## Goose Migration Pattern

```sql
-- +goose Up
CREATE TABLE IF NOT EXISTS things (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'draft',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_things_user_id (user_id),
    INDEX idx_things_status (status),
    CONSTRAINT fk_things_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS things;
```

For write-heavy tables, consider `BIGINT UNSIGNED AUTO_INCREMENT` internal primary keys with UUID public IDs in a unique secondary column. Load `mysql` before making that decision.

## MCP Server Pattern

Every generated API should include an MCP server, usually under `cmd/mcp`, that exposes three primitives: **tools**, **resources**, and **prompts**.

### Tools — One per Huma operation

MCP tools correspond to Huma operations. Parse `api/openapi.yaml` to discover operations:

| Huma OperationID | MCP tool |
|------------------|----------|
| `list-things` | `things_list` |
| `get-thing` | `things_get` |
| `create-thing` | `things_create` |
| `update-thing` | `things_update` |
| `delete-thing` | `things_delete` |

MCP tools call the generated API over HTTP rather than duplicating business logic. This keeps Huma handlers, OpenAPI tests, and MCP exploration on the same contract path. Tool inputs mirror OpenAPI request schemas. Tool outputs mirror OpenAPI response schemas. Auth uses `MCP_AUTH_TOKEN` env var.

### Resources — OpenAPI spec + domain skill

| URI | Description |
|-----|-------------|
| `resources://openapi/spec` | `api/openapi.yaml` — the mechanically-derived API contract |
| `resources://skill/flatout-backend` | This skill — conventions, patterns, guardrails |

The MCP server reads `api/openapi.yaml` (generated by Huma) and `SKILL.md` at request time. Resources let AI agents discover the API shape and project conventions by reading the MCP server, without hunting for files on disk.

### Prompts — Common extension + validation tasks

| Prompt | Description |
|--------|-------------|
| `extend_api` | Add a new CRUD endpoint for an entity, following existing Huma patterns |
| `add_auth` | Add JWT auth to unprotected endpoints |
| `add_migration` | Create a new database migration |
| `add_validation` | Add property tests and contract compliance checks |
| `validate_spec` | Run structural completeness validation against `docs/SPEC.md` |
| `validate_spec_consistency` | Run semantic consistency validation against `docs/SPEC.md` |
| `validate_spec_coverage` | Run coverage validation against `docs/SPEC.md` |
| `generate_tests_from_spec` | Read `docs/SPEC.md` and generate all test files with `// SPEC:` comments |
| `generate_integration_tests` | Generate end-to-end integration tests from `docs/SPEC.md` |

Prompts encode the skill's domain knowledge as actionable templates. Load `go-mcp-server-generator` for the full MCP server implementation.

## Verification Commands

After any code change:

```bash
go build ./...
go vet ./...
go test ./...
```

After Huma operation, SQL, or migration changes:

```bash
make generate          # sqlc + goose
make generate-openapi  # Huma → api/openapi.yaml (if using a standalone gen command)
go build ./...
go test ./...
```

Before considering generated code complete, ensure all four test layers pass:

```bash
go test ./internal/service/...         # Property tests + state machine tests
go test ./internal/api/operations/...   # Golden-path tests + contract tests
go test ./... -run Integration          # Integration tests (end-to-end)
```

The spec renderer maps `go test -json ./...` output back to `docs/SPEC.md` sections using the `// SPEC:` comments, showing pass, fail, or untested status.

## Guardrails

- Always produce `docs/VISION.md` and `docs/SPEC.md` before implementation code. The user must approve `docs/SPEC.md`. Never skip the spec.
- Do not add operations to Gin without registering them with Huma. If Huma doesn't know about it, it won't appear in OpenAPI and won't be validated.
- Do not make operation handlers contain business logic; put domain behavior in `internal/service`.
- Do not let MCP tools bypass the HTTP API; MCP should exercise the public contract.
- Do not return raw database errors to clients.
- Do not generate hidden endpoints that are missing from OpenAPI (Huma prevents this by construction).
- Review the generated `api/openapi.yaml` before implementing handlers. This is the spec approval gate.
- Run spec validation prompts (`validate_spec`) before writing any database schema or Go code. Fix all validation failures.
- Every test function must have a `// SPEC: AC-*` or `// SPEC: INV-*` comment linking it to a stable spec ID.
- All tests use the standard Go `testing` package — no external test framework unless the user explicitly requests one.
- Prefer boring, familiar HTTP JSON APIs over exotic protocols for generated projects.
- Use `huma.Error4xx` and `huma.Error5xx` helpers for consistent error responses.
- Huma introduces a dependency: `github.com/danielgtaylor/huma/v2`. Always include `github.com/danielgtaylor/huma/v2/adapters/humagin` for the Gin adapter.
- The spec template enforces a fixed set of types: `uuid`, `string`, `int`, `int64`, `float64`, `bool`, `timestamp`, `enum`. Do not invent new type names.
- Field names in input/output types must match entity field names exactly — typos break the spec-to-code mapping.
- Acceptance criteria IDs (AC-N) are stable references. Changing them breaks the test-to-spec mapping.

## Huma → OpenAPI OperationID Mapping Convention

Huma `OperationID` uses kebab-case (e.g. `list-things`). The MCP tool name flips to `{resource}_{verb}`:

| Huma OperationID | OpenAPI operationId | MCP tool name |
|------------------|---------------------|---------------|
| `list-things` | `list-things` | `things_list` |
| `get-thing` | `get-thing` | `things_get` |
| `create-thing` | `create-thing` | `things_create` |
| `update-thing` | `update-thing` | `things_update` |
| `delete-thing` | `delete-thing` | `things_delete` |

The MCP server can parse the OperationID to derive the tool name: split on `-`, reverse, join with `_`.

## Full Workflow Summary

```
User describes requirements
         │
         ▼
[1] docs/VISION.md + docs/SPEC.md + root Dockerfile
    Product intent, domain model, operations, acceptance criteria, deployable skeleton
    User reviews & approves ← INTENT APPROVAL GATE
         │
         ▼
[2] docs/SPEC.md (API Spec)
    Operations, acceptance criteria, security, environment
    User reviews & approves ← API SPEC APPROVAL GATE
         │
         ▼
[2a] Spec Validation
    validate_spec (completeness)
    validate_spec_consistency (semantic consistency)
    validate_spec_coverage (coverage)
    Confirm Dockerfile and .dockerignore already exist at repo root
         │
         ▼
[3] Database Schema
    goose migrations + sqlc queries
         │
         ▼
[4] Huma Skeleton (OpenAPI Review)
    Struct definitions with tags, no handler implementation
    User reviews generated api/openapi.yaml ← OPENAPI APPROVAL GATE
    Optional: browse at /docs (Scalar UI)
         │
         ▼
[5] Service Layer + Handlers
    Business logic in internal/service/
    Handler implementation in internal/api/operations/
         │
         ▼
[6] Test Generation
    generate_tests_from_spec → property + state machine + golden-path + contract
    generate_integration_tests → end-to-end
    All tests use standard Go testing package with // SPEC: comments
         │
          ▼
[7] MCP Server Generation
     Tools, resources, prompts (including validation & test gen prompts)
          │
          ▼
[7a] Containerization Completion
     docker-compose.yml, final Makefile Docker targets, final Dockerfile review
     Must build and run with `make build-docker` before verification
          │
          ▼
[8] Verification
     go test ./... -> spec renderer shows pass/fail/untested in docs/SPEC.md viewer
     docker build -t devfleet-api:latest . → image builds and `docker run` starts the API
          │
          ▼
[9] Build & Deploy
     docker build/push, deploy to provider (Preview Deploy or production)
```
