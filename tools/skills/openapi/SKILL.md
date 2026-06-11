---
name: openapi
description: OpenAPI 3.1 specification design, schema authoring, and API contract generation. Use when creating, editing, or reviewing OpenAPI specs, designing REST API contracts, defining request/response schemas, or writing API documentation from specs. Triggers on OpenAPI YAML/JSON, path definitions, component schemas, security schemes, or API design discussions.
---

# OpenAPI 3.1 Specification Design

Expert in designing and writing OpenAPI 3.1 specifications for production REST APIs. The OpenAPI spec is the **first-class contract** that drives code generation, automatic verification (property tests, contract compliance, state-machine validation), and MCP server exposure.

## Core Loop

```
Chat → Specs → Verified Code → MCP Server
```

The OpenAPI spec is the machine-readable contract between the AI planner and the code generator. Every generated API endpoint, request schema, response schema, error format, and security scheme is defined here first.

## OpenAPI 3.1 Document Structure

```yaml
openapi: "3.1.0"
info:
  title: My API
  version: "1.0.0"
  description: Description of the API.

servers:
  - url: http://localhost:8080
    description: Local development

paths:
  /things:
    get:
      operationId: ListThings
      summary: List all things
      parameters: []
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: "#/components/schemas/Thing"
    post:
      operationId: CreateThing
      summary: Create a new thing
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/CreateThingRequest"
      responses:
        "201":
          description: Created
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Thing"
        "400":
          $ref: "#/components/responses/BadRequest"

  /things/{id}:
    parameters:
      - name: id
        in: path
        required: true
        schema:
          type: string
          format: uuid
    get:
      operationId: GetThing
      summary: Get a thing by ID
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Thing"
        "404":
          $ref: "#/components/responses/NotFound"
    put:
      operationId: UpdateThing
      summary: Update a thing
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/UpdateThingRequest"
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Thing"
    delete:
      operationId: DeleteThing
      summary: Delete a thing
      responses:
        "204":
          description: Deleted
        "404":
          $ref: "#/components/responses/NotFound"

components:
  schemas:
    Thing:
      type: object
      required: [id, name, created_at]
      properties:
        id:
          type: string
          format: uuid
          description: Unique identifier
        name:
          type: string
          minLength: 1
          maxLength: 255
        created_at:
          type: string
          format: date-time
        updated_at:
          type: string
          format: date-time

    CreateThingRequest:
      type: object
      required: [name]
      properties:
        name:
          type: string
          minLength: 1
          maxLength: 255

    UpdateThingRequest:
      type: object
      properties:
        name:
          type: string
          minLength: 1
          maxLength: 255

    Error:
      type: object
      required: [code, message]
      properties:
        code:
          type: string
          description: Machine-readable error code (e.g. "NOT_FOUND")
        message:
          type: string
          description: Human-readable error message

  responses:
    BadRequest:
      description: Bad request
      content:
        application/json:
          schema:
            $ref: "#/components/schemas/Error"
    NotFound:
      description: Not found
      content:
        application/json:
          schema:
            $ref: "#/components/schemas/Error"
    Unauthorized:
      description: Unauthorized
      content:
        application/json:
          schema:
            $ref: "#/components/schemas/Error"

  securitySchemes:
    BearerAuth:
      type: http
      scheme: bearer
      bearerFormat: JWT

security:
  - BearerAuth: []
```

## Naming Conventions

| Element | Convention | Example |
|---------|-----------|---------|
| Operation ID | PascalCase verb + noun | `ListThings`, `GetThing`, `CreateThing`, `UpdateThing`, `DeleteThing` |
| Schema names | PascalCase noun | `Thing`, `CreateThingRequest`, `UpdateThingRequest` |
| Path segments | kebab-case | `/thing-tags`, `/user-settings` |
| Parameter names | snake_case | `page_size`, `created_at` |
| Property names | snake_case | `created_at`, `user_id` |
| Enum values | UPPER_SNAKE_CASE | `ACTIVE`, `PENDING_VERIFICATION` |
| Error codes | UPPER_SNAKE_CASE string | `"NOT_FOUND"`, `"INVALID_ARGUMENT"` |

## Schema Design Principles

### 1. Separate request and response schemas

Never reuse the same schema for request and response. Request schemas omit server-generated fields (`id`, `created_at`). Response schemas include all fields.

### 2. Use specific types and formats

Prefer `type: string, format: uuid` over bare `type: string`. Use `format: date-time` for timestamps. Use `format: uri` for URLs.

### 3. Add constraints

```yaml
name:
  type: string
  minLength: 1
  maxLength: 255
  pattern: "^[a-zA-Z0-9 _-]+$"

email:
  type: string
  format: email
  maxLength: 320

page_size:
  type: integer
  minimum: 1
  maximum: 100
  default: 20
```

### 4. Use $ref for reusable components

Extract common responses (`BadRequest`, `NotFound`, `Unauthorized`, `Forbidden`, `Conflict`) and schemas (`Error`, `Pagination`, `Sort`) into `components/responses` and `components/schemas`.

### 5. Always define a standard error schema

Every API should return errors in a consistent format:

```yaml
Error:
  type: object
  required: [code, message]
  properties:
    code: { type: string }
    message: { type: string }
    details:
      type: array
      items:
        type: object
        properties:
          field: { type: string }
          issue: { type: string }
```

### 6. Use operationId for every operation

`operationId` is required for code generation. It maps to the function/method name.

## Pagination Pattern

```yaml
parameters:
  - name: page_size
    in: query
    schema:
      type: integer
      minimum: 1
      maximum: 100
      default: 20
  - name: page_token
    in: query
    schema:
      type: string
      description: Opaque cursor for cursor-based pagination

responses:
  "200":
    description: Paginated list
    content:
      application/json:
        schema:
          type: object
          required: [items]
          properties:
            items:
              type: array
              items:
                $ref: "#/components/schemas/Thing"
            next_page_token:
              type: string
              description: Present if there are more results
            total_count:
              type: integer
              description: Total number of results (optional)
```

## Security Pattern

```yaml
# Define auth scheme
components:
  securitySchemes:
    BearerAuth:
      type: http
      scheme: bearer
      bearerFormat: JWT
    ApiKeyAuth:
      type: apiKey
      in: header
      name: X-API-Key

# Apply globally (all endpoints require auth)
security:
  - BearerAuth: []

# Override per-operation (public endpoint)
paths:
  /health:
    get:
      security: []  # No auth required
      responses:
        "200":
          description: OK
```

## File Upload Pattern

```yaml
requestBody:
  content:
    multipart/form-data:
      schema:
        type: object
        required: [file]
        properties:
          file:
            type: string
            format: binary
          metadata:
            type: string
            description: JSON-encoded metadata
```

## Webhook Pattern

```yaml
webhooks:
  thingCreated:
    post:
      summary: Thing was created
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/Thing"
      responses:
        "200":
          description: Acknowledged
```

## State Machine

Specs include a state machine definition for automatic state-transition validation:

```yaml
x-state-machine:
  entity: Thing
  initial: draft
  states:
    - name: draft
      transitions: [review, archived]
    - name: review
      transitions: [published, draft]
    - name: published
      transitions: [archived]
    - name: archived
      transitions: []
```

State machine transitions are verified during code generation — the generated code enforces only valid transitions.

## Verification Checks

The spec verification engine runs these checks:

| Check | What it verifies |
|-------|-----------------|
| **Schema validity** | Every `$ref` resolves, every operation has an `operationId`, every response schema is complete |
| **Contract compliance** | Generated endpoints return correct status codes, schemas, and error formats |
| **State machine** | Only valid transitions are allowed; invalid transitions return 409 Conflict |
| **Property tests** | Random input generation against schema constraints, round-trip JSON serialization |
| **Golden-path tests** | Happy-path flows produce expected responses |

## Tips

- Start with the paths and operations, then drill into schemas
- Use `allOf` for composition: `CreateThingRequest` inherits from a base schema and adds required fields
- Use `oneOf` for discriminated unions (e.g., different event types)
- Always include `description` on schemas and properties — they appear in generated docs
- Keep the spec under 1000 lines; split into multiple files if larger
- Run `openapi-generator validate` or equivalent before handing off to code generation
