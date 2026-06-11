---
name: go-mcp-server-generator
description: 'Generate a complete Go MCP server project that exposes API operations as tools, OpenAPI specs and domain skills as resources, and common extension tasks as prompts. Uses the github.com/mark3labs/mcp-go library.'
---

# Go MCP Server Project Generator

Generate a complete, production-ready Model Context Protocol (MCP) server project in Go. The generated server exposes three MCP primitives:

| Primitive | Purpose | Maps to |
|-----------|---------|---------|
| **Tools** | Callable functions | API endpoints (one tool per operation) |
| **Resources** | Readable data | OpenAPI spec, spec docs, domain skill |
| **Prompts** | Reusable prompt templates | Common extension tasks |

## Project Requirements

You will create a Go MCP server with:

1. **Project Structure**: Proper Go module layout with tools, resources, and prompts packages
2. **Dependencies**: Official MCP SDK and necessary packages
3. **Server Setup**: Configured MCP server with all three capability types and transports
4. **Tools**: One tool per API endpoint, calling the HTTP API over the network
5. **Resources**: OpenAPI spec, `docs/SPEC.md`, and domain skill served as readable resources
6. **Prompts**: Pre-built prompt templates for common extension tasks
7. **Error Handling**: Proper error handling and context usage
8. **Documentation**: README with setup and usage instructions
9. **Testing**: Basic test structure

## Template Structure

```
myserver/
├── go.mod
├── go.sum
├── main.go
├── tools/
│   ├── registry.go          # RegisterTools — registers all endpoint tools
│   └── example_entity.go    # Tools for one API resource
├── resources/
│   ├── registry.go          # RegisterResources — registers OpenAPI + docs + skill
│   ├── openapi.go           # Serves openapi.yaml as resource
│   ├── spec.go              # Serves docs/SPEC.md as resource
│   └── skill.go             # Serves SKILL.md as resource
├── prompts/
│   ├── registry.go               # RegisterPrompts — registers all prompt templates
│   ├── extend_api.go             # "extend_api" prompt
│   ├── add_auth.go               # "add_auth" prompt
│   ├── add_migration.go          # "add_migration" prompt
│   ├── add_validation.go         # "add_validation" prompt
│   ├── validate_spec.go          # "validate_spec" prompt
│   ├── validate_spec_consistency.go  # "validate_spec_consistency" prompt
│   ├── validate_spec_coverage.go     # "validate_spec_coverage" prompt
│   ├── generate_tests_from_spec.go   # "generate_tests_from_spec" prompt
│   └── generate_integration_tests.go # "generate_integration_tests" prompt
├── config/
│   └── config.go
├── README.md
└── main_test.go
```

## go.mod Template

```go
module github.com/yourusername/{{PROJECT_NAME}}

go 1.23

require (
    github.com/mark3labs/mcp-go v0.54.0
)
```

## main.go Template

```go
package main

import (
    "context"
    "log"
    "net/http"
    "os"
    "os/signal"
    "syscall"

    "github.com/mark3labs/mcp-go/server"
    "github.com/yourusername/{{PROJECT_NAME}}/config"
    "github.com/yourusername/{{PROJECT_NAME}}/tools"
    "github.com/yourusername/{{PROJECT_NAME}}/resources"
    "github.com/yourusername/{{PROJECT_NAME}}/prompts"
)

func main() {
    cfg := config.Load()

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
    go func() {
        <-sigCh
        log.Println("Shutting down...")
        cancel()
    }()

    s := server.NewMCPServer(
        cfg.ServerName,
        cfg.Version,
        server.WithInstructions("API exploration and extension tools"),
    )

    tools.RegisterTools(s, cfg.APIBaseURL)
    resources.RegisterResources(s, cfg.SkillName)
    prompts.RegisterPrompts(s)

    sse := server.NewSSEServer(s)
    http.Handle("GET /sse", sse.SSEHandler())
    http.Handle("POST /message", sse.MessageHandler())

    addr := ":" + cfg.Port
    log.Printf("MCP server listening on %s", addr)
    srv := &http.Server{Addr: addr, Handler: nil}

    go func() {
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("Server error: %v", err)
        }
    }()

    <-ctx.Done()
    srv.Shutdown(context.Background())
}
```

## tools/example_entity.go Template

```go
package tools

import (
    "context"
    "fmt"
    "io"
    "net/http"

    "github.com/mark3labs/mcp-go/mcp"
)

var apiBaseURL string
var authToken string

func ListEntitiesHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    pageSize := req.GetString("page_size", "10")
    pageToken := req.GetString("page_token", "")

    url := fmt.Sprintf("%s/api/v1/entities?page_size=%s&page_token=%s",
        apiBaseURL, pageSize, pageToken)

    httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
    if err != nil {
        return mcp.NewToolResultError(fmt.Sprintf("create request: %s", err)), nil
    }
    if authToken != "" {
        httpReq.Header.Set("Authorization", "Bearer "+authToken)
    }

    resp, err := http.DefaultClient.Do(httpReq)
    if err != nil {
        return mcp.NewToolResultError(fmt.Sprintf("execute request: %s", err)), nil
    }
    defer resp.Body.Close()

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return mcp.NewToolResultError(fmt.Sprintf("read response: %s", err)), nil
    }

    return mcp.NewToolResultText(string(body)), nil
}

func RegisterEntityTools(s *server.MCPServer) {
    s.AddTool(
        mcp.NewTool("entities_list",
            mcp.WithDescription("List all entities with optional pagination"),
            mcp.WithString("page_size", mcp.Description("Number of items per page")),
            mcp.WithString("page_token", mcp.Description("Opaque cursor for pagination")),
        ),
        ListEntitiesHandler,
    )
}
```

## tools/registry.go Template

```go
package tools

import (
    "os"

    "github.com/mark3labs/mcp-go/server"
)

func RegisterTools(s *server.MCPServer, baseURL string) {
    apiBaseURL = baseURL
    authToken = os.Getenv("MCP_AUTH_TOKEN")

    RegisterEntityTools(s)
}
```

## resources/openapi.go Template

```go
package resources

import (
    "context"
    "fmt"
    "os"

    "github.com/mark3labs/mcp-go/mcp"
)

func RegisterOpenAPISpec(s *server.MCPServer) {
    s.AddResource(
        mcp.NewResource("resources://openapi/spec", "OpenAPI Specification",
            mcp.WithResourceDescription("The OpenAPI 3.1 specification for the API"),
            mcp.WithMIMEType("application/yaml"),
        ),
        func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
            data, err := os.ReadFile("api/openapi.yaml")
            if err != nil {
                return nil, fmt.Errorf("read openapi spec: %w", err)
            }
            return []mcp.ResourceContents{
                mcp.TextResourceContents{
                    URI:      "resources://openapi/spec",
                    MIMEType: "application/yaml",
                    Text:     string(data),
                },
            }, nil
        },
    )
}
```

## resources/spec.go Template

```go
package resources

import (
    "context"
    "fmt"
    "os"

    "github.com/mark3labs/mcp-go/mcp"
)

func RegisterSpec(s *server.MCPServer) {
    s.AddResource(
        mcp.NewResource("resources://spec", "Specification Document",
            mcp.WithResourceDescription("The structured Markdown spec (docs/SPEC.md) — API operations and acceptance criteria"),
            mcp.WithMIMEType("text/markdown"),
        ),
        func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
            data, err := os.ReadFile("docs/SPEC.md")
            if err != nil {
                return nil, fmt.Errorf("read spec: %w", err)
            }
            return []mcp.ResourceContents{
                mcp.TextResourceContents{
                    URI:      "resources://spec",
                    MIMEType: "text/markdown",
                    Text:     string(data),
                },
            }, nil
        },
    )
}
```

## resources/skill.go Template

```go
package resources

import (
    "context"
    "fmt"
    "os"

    "github.com/mark3labs/mcp-go/mcp"
)

var skillName string

func RegisterSkill(s *server.MCPServer, name string) {
    skillName = name
    s.AddResource(
        mcp.NewResource(fmt.Sprintf("resources://skill/%s", skillName), "Domain Skill",
            mcp.WithResourceDescription("Conventions, patterns, and guardrails for extending this API"),
            mcp.WithMIMEType("text/markdown"),
        ),
        func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
            data, err := os.ReadFile("SKILL.md")
            if err != nil {
                return nil, fmt.Errorf("read skill: %w", err)
            }
            return []mcp.ResourceContents{
                mcp.TextResourceContents{
                    URI:      fmt.Sprintf("resources://skill/%s", skillName),
                    MIMEType: "text/markdown",
                    Text:     string(data),
                },
            }, nil
        },
    )
}
```

## resources/registry.go Template

```go
package resources

import "github.com/mark3labs/mcp-go/server"

func RegisterResources(s *server.MCPServer, skillName string) {
    RegisterOpenAPISpec(s)
    RegisterSpec(s)
    RegisterSkill(s, skillName)
}
```

## prompts/extend_api.go Template

```go
package prompts

import (
    "context"
    "fmt"

    "github.com/mark3labs/mcp-go/mcp"
)

func RegisterExtendAPIPrompt(s *server.MCPServer) {
    s.AddPrompt(
        mcp.NewPrompt("extend_api",
            mcp.WithPromptDescription("Add a new CRUD endpoint for an entity"),
            mcp.WithArgument("entity_name", mcp.Description("Name of the new entity/resource"), mcp.RequiredArgument()),
            mcp.WithArgument("fields", mcp.Description("Comma-separated field definitions"), mcp.RequiredArgument()),
        ),
        func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
            entityName := req.GetArgument("entity_name")
            fields := req.GetArgument("fields")

            prompt := fmt.Sprintf(`Add a new entity "%s" with fields: %s.

Follow the spec-driven workflow:
1. Update docs/SPEC.md — entity fields, state machine (if any), invariants, and relationships
2. Update docs/SPEC.md — API operations: list, create, get, update, delete for the new entity
3. Update docs/SPEC.md — acceptance criteria with stable AC-* IDs and invariants with INV-* IDs
4. Create a goose migration in db/migrations/ for the new table
5. Write sqlc queries in db/queries/%[1]s.sql
6. Write Huma input/output structs in internal/api/operations/%[1]s.go
7. Implement Huma operation registration with huma.Register
8. Add service methods in internal/service/%[1]s.go
9. Generate tests with // SPEC: comments from the acceptance criteria
10. Register MCP tools for each endpoint
11. Run go build ./..., go test ./...

Use the conventions from docs/SPEC.md and the existing code as your guide.
Every test must include a // SPEC: comment linking it to a stable spec ID such as AC-1 or INV-2.`, entityName, fields)

            return mcp.NewGetPromptResult("Extend API with new CRUD", []mcp.PromptMessage{
                mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(prompt)),
            }), nil
        },
    )
}
```

## prompts/add_auth.go Template

```go
package prompts

import (
    "context"
    "fmt"

    "github.com/mark3labs/mcp-go/mcp"
)

func RegisterAddAuthPrompt(s *server.MCPServer) {
    s.AddPrompt(
        mcp.NewPrompt("add_auth",
            mcp.WithPromptDescription("Add JWT authentication to unprotected endpoints"),
            mcp.WithArgument("endpoints", mcp.Description("Comma-separated endpoint paths to protect"), mcp.RequiredArgument()),
        ),
        func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
            endpoints := req.GetArgument("endpoints")

            prompt := fmt.Sprintf(`Add JWT authentication to these endpoints: %s.

Follow the existing auth patterns in the project:
1. Add BearerAuth security scheme to api/openapi.yaml if not present
2. Add security: [BearerAuth: []] to the specified operations
3. Ensure internal/auth/jwt.go has proper verification
4. Apply AuthMiddleware to the specified route group
5. Add 401 Unauthorized to the response schemas
6. Run make generate, go build ./..., go test ./...`, endpoints)

            return mcp.NewGetPromptResult("Add JWT auth to specified endpoints", []mcp.PromptMessage{
                mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(prompt)),
            }), nil
        },
    )
}
```

## prompts/add_migration.go Template

```go
package prompts

import (
    "context"
    "fmt"

    "github.com/mark3labs/mcp-go/mcp"
)

func RegisterAddMigrationPrompt(s *server.MCPServer) {
    s.AddPrompt(
        mcp.NewPrompt("add_migration",
            mcp.WithPromptDescription("Create a new database migration for an additional table"),
            mcp.WithArgument("table_name", mcp.Description("Name of the new table"), mcp.RequiredArgument()),
            mcp.WithArgument("columns", mcp.Description("Column definitions (e.g. 'id:varchar(36):pk, name:varchar(255):notnull, created_at:datetime:notnull')"), mcp.RequiredArgument()),
        ),
        func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
            tableName := req.GetArgument("table_name")
            columns := req.GetArgument("columns")

            prompt := fmt.Sprintf(`Create a new database migration for table "%s" with columns: %s.

Follow the existing migration patterns:
1. Create a new goose migration in db/migrations/ with the next sequence number
2. Include both Up (CREATE TABLE) and Down (DROP TABLE) sections
3. Add appropriate indexes (foreign keys, commonly queried columns)
4. Use utf8mb4 charset and unicode collation
5. Write corresponding sqlc queries in db/queries/
6. Run make generate to regenerate the repository code
7. Run go build ./..., go test ./...`, tableName, columns)

            return mcp.NewGetPromptResult(fmt.Sprintf("Create a migration for the %s table", tableName), []mcp.PromptMessage{
                mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(prompt)),
            }), nil
        },
    )
}
```

## prompts/add_validation.go Template

```go
package prompts

import (
    "context"
    "fmt"

    "github.com/mark3labs/mcp-go/mcp"
)

func RegisterAddValidationPrompt(s *server.MCPServer) {
    s.AddPrompt(
        mcp.NewPrompt("add_validation",
            mcp.WithPromptDescription("Add property tests, contract compliance checks, state machine tests, and golden-path tests for one or more entities"),
            mcp.WithArgument("entity_name", mcp.Description("Name of the entity to add validation for"), mcp.RequiredArgument()),
        ),
        func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
            entityName := req.GetArgument("entity_name")

            prompt := fmt.Sprintf(`Add comprehensive validation tests for the "%s" entity.

Follow the test-to-spec mapping convention:
1. Read docs/SPEC.md sections for the "%[1]s" entity:
   - INV-* invariants — generate property tests
   - AC-* state-transition criteria — generate state machine tests
   - AC-* operation criteria — generate golden-path and contract tests
2. Create tests in:
   - internal/service/%[1]s_test.go — property tests + state machine tests
   - internal/api/operations/%[1]s_test.go — golden-path tests + contract tests
3. Every test function MUST have a // SPEC: comment using a stable ID:
   // SPEC: INV-1
   // SPEC: AC-3
4. Use only the standard Go testing package (no testify, no ginkgo)
5. Use net/http/httptest for HTTP handler tests
6. Each test function follows naming: Test{Entity}_{Category}_{Description}
7. Use t.Errorf with descriptive messages on failure
8. Run go test ./... to verify all tests pass

Test categories to generate:
- Property tests: one per invariant, random inputs where applicable
- State machine tests: one per transition (valid = expects success, invalid = expects error)
- Golden-path tests: one per acceptance criterion happy path
- Contract tests: one per API operation, verify status codes and response schemas`, entityName)

            return mcp.NewGetPromptResult("Add validation tests with SPEC: comments", []mcp.PromptMessage{
                mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(prompt)),
            }), nil
        },
    )
}
```

## prompts/validate_spec.go Template

```go
package prompts

import (
    "context"

    "github.com/mark3labs/mcp-go/mcp"
)

func RegisterValidateSpecPrompt(s *server.MCPServer) {
    s.AddPrompt(
        mcp.NewPrompt("validate_spec",
            mcp.WithPromptDescription("Run structural completeness validation against docs/SPEC.md"),
        ),
        func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
            prompt := `Validate docs/SPEC.md for structural completeness.

Read docs/SPEC.md and check:
1. All entity fields referenced in API Operations are defined in the Entities section
2. All state machine transition From/To values reference defined states
3. All relationship foreign keys reference existing entity tables
4. No duplicate operation paths or operationIds exist
5. All acceptance criteria Given clauses reference defined operations
6. All invariants are written in a testable format (clear conditions with field names)
7. All field types are from the allowed set: uuid, string, int, int64, float64, bool, timestamp, enum

For each failure, report:
- The section or stable ID involved (e.g. "AC-3" or "create-order input")
- The specific problem (e.g. "Field 'customer_email' not defined on Customer in docs/SPEC.md")
- The fix required

If no failures found, report: "docs/SPEC.md is structurally complete."

Do NOT modify any code — this is a read-only validation step.`

            return mcp.NewGetPromptResult("Validate docs/SPEC.md structure", []mcp.PromptMessage{
                mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(prompt)),
            }), nil
        },
    )
}
```

## prompts/validate_spec_consistency.go Template

```go
package prompts

import (
    "context"

    "github.com/mark3labs/mcp-go/mcp"
)

func RegisterValidateSpecConsistencyPrompt(s *server.MCPServer) {
    s.AddPrompt(
        mcp.NewPrompt("validate_spec_consistency",
            mcp.WithPromptDescription("Run semantic consistency validation against docs/SPEC.md"),
        ),
        func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
            prompt := `Validate docs/SPEC.md for semantic consistency.

Read docs/SPEC.md and check:
1. Auth requirements are consistent — if one endpoint requires auth, document why others don't
2. Error codes are consistently applied — same condition → same error code across operations
3. Pagination parameters (page_size, page_token) are consistent across all list operations
4. All entities use created_at and updated_at with timestamp type
5. Enum values used in API Operations match the entity field Constraints definitions
6. Field names in Input/Output match entity field names exactly — no typos, no invented names
7. Security section auth scheme matches what operations reference
8. Environment variables are referenced consistently (no variables used in code but missing from env table)

For each inconsistency, report:
- The two conflicting sections
- The inconsistency (e.g. "create-order uses error code 'INVALID_INPUT' but update-order uses 'BAD_REQUEST' for the same condition")
- Resolution recommendation

If no inconsistencies found, report: "docs/SPEC.md is semantically consistent."

Do NOT modify any code — this is a read-only validation step.`

            return mcp.NewGetPromptResult("Validate docs/SPEC.md consistency", []mcp.PromptMessage{
                mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(prompt)),
            }), nil
        },
    )
}
```

## prompts/validate_spec_coverage.go Template

```go
package prompts

import (
    "context"

    "github.com/mark3labs/mcp-go/mcp"
)

func RegisterValidateSpecCoveragePrompt(s *server.MCPServer) {
    s.AddPrompt(
        mcp.NewPrompt("validate_spec_coverage",
            mcp.WithPromptDescription("Run coverage completeness validation against docs/SPEC.md"),
        ),
        func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
            prompt := `Validate docs/SPEC.md for coverage completeness.

Read docs/SPEC.md and check:
1. Every persisted resource has full CRUD operations (list, create, get, update, delete) unless explicitly omitted — list any persisted resource with missing operations
2. Every operation has at least one acceptance criterion (happy path required)
3. Every error condition listed in an operation has a corresponding acceptance criterion
4. Every state machine transition has coverage:
   - Every valid transition has an AC that tests it succeeds
   - Every invalid transition has an AC that tests it returns an error
5. Every invariant has a corresponding acceptance criterion or property test description
6. Authentication edge cases are covered: no token (401), expired token (401), wrong user (403)
7. Pagination edge cases: empty list, single item, exactly page_size items, beyond last page

For each gap, report:
- What's missing (e.g. "Entity 'Customer' has no delete operation")
- Priority: must-fix or nice-to-have

If coverage is complete, report: "docs/SPEC.md has complete coverage."

Do NOT modify any code — this is a read-only validation step.`

            return mcp.NewGetPromptResult("Validate docs/SPEC.md coverage", []mcp.PromptMessage{
                mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(prompt)),
            }), nil
        },
    )
}
```

## prompts/generate_tests_from_spec.go Template

```go
package prompts

import (
    "context"

    "github.com/mark3labs/mcp-go/mcp"
)

func RegisterGenerateTestsFromSpecPrompt(s *server.MCPServer) {
    s.AddPrompt(
        mcp.NewPrompt("generate_tests_from_spec",
            mcp.WithPromptDescription("Read docs/SPEC.md and generate all test files with // SPEC: comments"),
        ),
        func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
            prompt := `Read docs/SPEC.md and generate all test files for every entity and operation.

For each entity in docs/SPEC.md, generate:

1. internal/service/{entity}_test.go:
   - Property tests from INV-* invariants
   - State machine tests from AC-* or INV-* state-transition rules
   - Use table-driven tests with t.Run

2. internal/api/operations/{entity}_test.go:
   - Golden-path tests from AC-* acceptance criteria for {entity}
   - Contract tests from AC-* criteria that cover status codes and error cases
   - Use net/http/httptest with the Gin router

CRITICAL: Every test function MUST start with a // SPEC: comment using a stable ID:
  // SPEC: INV-1
  // SPEC: AC-1
  // SPEC: AC-4

Naming convention: Test{Entity}_{Category}_{Description}
  TestOrder_Invariant_TotalCentsNeverNegative
  TestOrder_StateMachine_ArchivedIsTerminal
  TestOrder_CreateOrder_HappyPath
  TestOrder_CreateOrder_EmptyNameReturns400

Use only the standard Go testing package. No testify, no ginkgo.
All tests must compile and pass when run with go test ./...

After generating, run: go test -json ./... > test-results.json`

            return mcp.NewGetPromptResult("Generate all tests from docs/SPEC.md", []mcp.PromptMessage{
                mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(prompt)),
            }), nil
        },
    )
}
```

## prompts/generate_integration_tests.go Template

```go
package prompts

import (
    "context"

    "github.com/mark3labs/mcp-go/mcp"
)

func RegisterGenerateIntegrationTestsPrompt(s *server.MCPServer) {
    s.AddPrompt(
        mcp.NewPrompt("generate_integration_tests",
            mcp.WithPromptDescription("Generate end-to-end integration tests that exercise full API flows through the Gin router"),
        ),
        func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
            prompt := `Read docs/SPEC.md and generate end-to-end integration tests.

Integration tests exercise the full API through the Gin router:
- Start a test server with httptest.NewServer
- Make real HTTP requests
- Verify response status codes, body schemas, and error formats
- Chain multiple operations (create → get → update → delete)

For each entity, generate at least one full lifecycle test:

// SPEC: AC-1
// SPEC: AC-5
// SPEC: AC-8
func TestOrder_Lifecycle_CreateGetDelete(t *testing.T) { ... }

And tests for cross-entity relationships:

// SPEC: AC-9
// SPEC: AC-10
func TestCustomerOrder_Relationship_TenantIsolation(t *testing.T) { ... }

Requirements:
1. Use the standard Go testing package only
2. Use net/http/httptest for HTTP requests
3. Each test starts with a // SPEC: comment
4. Use a test database or transaction rollback for isolation
5. Test auth: no token → 401, expired token → 401, wrong user → 403
6. Test pagination: empty, single, multiple pages
7. Test error paths: not found, validation failures, state machine violations

After generating, run: go test ./... -run Integration -v`

            return mcp.NewGetPromptResult("Generate integration tests from docs/SPEC.md", []mcp.PromptMessage{
                mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(prompt)),
            }), nil
        },
    )
}
```

## prompts/registry.go Template

```go
package prompts

import "github.com/mark3labs/mcp-go/server"

func RegisterPrompts(s *server.MCPServer) {
    // Extension prompts
    RegisterExtendAPIPrompt(s)
    RegisterAddAuthPrompt(s)
    RegisterAddMigrationPrompt(s)
    RegisterAddValidationPrompt(s)

    // Validation prompts (read-only, run before code generation)
    RegisterValidateSpecPrompt(s)
    RegisterValidateSpecConsistencyPrompt(s)
    RegisterValidateSpecCoveragePrompt(s)

    // Test generation prompts (produce Go test files from docs/SPEC.md)
    RegisterGenerateTestsFromSpecPrompt(s)
    RegisterGenerateIntegrationTestsPrompt(s)
}
```

## config/config.go Template

```go
package config

import "os"

type Config struct {
    ServerName string
    Version    string
    LogLevel   string
    Port       string
    APIBaseURL string
    SkillName  string
}

func Load() *Config {
    return &Config{
        ServerName: getEnv("SERVER_NAME", "{{PROJECT_NAME}}-mcp"),
        Version:    getEnv("VERSION", "v1.0.0"),
        LogLevel:   getEnv("LOG_LEVEL", "info"),
        Port:       getEnv("PORT", "8080"),
        APIBaseURL: getEnv("API_BASE_URL", "http://localhost:8080"),
        SkillName:  getEnv("SKILL_NAME", "{{PROJECT_NAME}}"),
    }
}

func getEnv(key, defaultValue string) string {
    if value := os.Getenv(key); value != "" {
        return value
    }
    return defaultValue
}
```

## main_test.go Template

```go
package main

import (
    "context"
    "testing"

    "github.com/mark3labs/mcp-go/mcp"
    "github.com/yourusername/{{PROJECT_NAME}}/tools"
)

func TestListEntitiesHandler(t *testing.T) {
    ctx := context.Background()

    result, err := tools.ListEntitiesHandler(ctx, mcp.CallToolRequest{})
    if err != nil {
        t.Fatalf("ListEntitiesHandler failed: %v", err)
    }

    if result == nil {
        t.Error("Expected non-nil result")
    }
}
```

## README.md Template

```markdown
# {{PROJECT_NAME}} — MCP Server

A Model Context Protocol (MCP) server that exposes the {{PROJECT_NAME}} API to AI agents.

## MCP Capabilities

### Tools (API Operations)

One tool per API endpoint. Each tool calls the HTTP API.

| Tool | API Endpoint |
|------|-------------|
| `entities_list` | `GET /api/v1/entities` |
| `entities_get` | `GET /api/v1/entities/{id}` |
| `entities_create` | `POST /api/v1/entities` |
| `entities_update` | `PUT /api/v1/entities/{id}` |
| `entities_delete` | `DELETE /api/v1/entities/{id}` |

### Resources (Readable Data)

| URI | Description |
|-----|-------------|
| `resources://openapi/spec` | The OpenAPI 3.1 specification |
| `resources://spec` | The structured Markdown spec (`docs/SPEC.md`) |
| `resources://skill/{{PROJECT_NAME}}` | Domain skill with conventions and patterns |

### Prompts (Extension + Validation + Test Generation)

Extension prompts:
| Prompt | Description |
|--------|-------------|
| `extend_api` | Add a new CRUD endpoint |
| `add_auth` | Add JWT auth to endpoints |
| `add_migration` | Create a new DB migration |
| `add_validation` | Add property tests + contract compliance checks |

Validation prompts (read-only, run before code generation):
| Prompt | Description |
|--------|-------------|
| `validate_spec` | Structural completeness check against `docs/SPEC.md` |
| `validate_spec_consistency` | Semantic consistency check against `docs/SPEC.md` |
| `validate_spec_coverage` | Coverage completeness check against `docs/SPEC.md` |

Test generation prompts (read `docs/SPEC.md`, produce Go test files):
| Prompt | Description |
|--------|-------------|
| `generate_tests_from_spec` | Generate all 4 test layers with // SPEC: comments |
| `generate_integration_tests` | Generate end-to-end integration tests |

## Installation

```bash
go mod download
go build -o {{PROJECT_NAME}}-mcp ./cmd/mcp
```

## Usage

Run with stdio transport (for agent process management):

```bash
./{{PROJECT_NAME}}-mcp
```

## Configuration

| Env Var | Default | Description |
|---------|---------|-------------|
| `API_BASE_URL` | `http://localhost:8080` | Base URL of the HTTP API |
| `MCP_AUTH_TOKEN` | | Bearer token for authenticated endpoints |
| `SERVER_NAME` | `{{PROJECT_NAME}}-mcp` | Server name in MCP handshake |
| `VERSION` | `v1.0.0` | Server version |

## License

MIT
```

## How Spec-Driven Prompts Work

The validation and test generation prompts follow a read-spec-docs -> produce-code pattern. They do not execute code themselves - they return structured instructions that the AI agent follows:

```
docs/SPEC.md + api/openapi.yaml (implementation source of truth)
    │
    ├── validate_spec ──────────▶ Error list or "structurally complete"
    ├── validate_spec_consistency ─▶ Error list or "semantically consistent"
    ├── validate_spec_coverage ──▶ Gap list or "coverage complete"
    │
    ├── generate_tests_from_spec ──▶ *_test.go files with // SPEC: comments
    └── generate_integration_tests ─▶ Integration test file
```

The validation prompts are read-only - they inspect `docs/SPEC.md` and report problems. They never modify files. Run them before writing any code to catch spec errors early.

The test generation prompts read `docs/SPEC.md` and produce Go test files. They use the `// SPEC:` comment convention with stable IDs such as `AC-1` and `INV-2`, so the builder's spec renderer can map test results back to spec sections.

## Generation Instructions

When generating a Go MCP server for a generated API:

1. **Read spec docs first** — Understand `docs/VISION.md` and `docs/SPEC.md` before generating tools
2. **Read the OpenAPI spec** — Parse `api/openapi.yaml` (generated by Huma) to discover all operations
3. **Generate one tool per operation** — Tool name is `{resource}_{verb}`, tool calls HTTP API
4. **Generate resources** — OpenAPI spec (`resources://openapi/spec`) + `docs/SPEC.md` (`resources://spec`) + domain skill (`resources://skill/{name}`)
5. **Generate extension prompts** — `extend_api`, `add_auth`, `add_migration`, `add_validation`
6. **Generate validation prompts** — `validate_spec`, `validate_spec_consistency`, `validate_spec_coverage`
7. **Generate test generation prompts** — `generate_tests_from_spec`, `generate_integration_tests`
8. **Tools call HTTP, not DB** — MCP tools exercise the public API contract
9. **Resources read from disk** — `api/openapi.yaml`, `docs/SPEC.md`, and `SKILL.md` are read at request time
10. **Prompts include context** — Each prompt references the project's `docs/SPEC.md` conventions and patterns
11. **Type Safety** — Use structs with JSON schema tags for all tool/prompt inputs
12. **Error Handling** — Validate inputs, check context, wrap errors
13. **Graceful Shutdown** — Handle signals properly

## Best Practices

- Keep tools focused: one tool per API endpoint
- Tools must call the HTTP API, not bypass handlers
- Resources serve the OpenAPI spec, `docs/SPEC.md`, and skill as the single source of truth
- Validation prompts are read-only - they inspect but never modify
- Test generation prompts reference `docs/SPEC.md`, not hardcoded test data
- Prompts should be actionable and reference specific project conventions from `docs/SPEC.md`
- Use descriptive names: `entities_list` not `list`, `extend_api` not `extend`
- Include JSON schema documentation in struct tags
- Always respect context cancellation
- Return descriptive errors
- Keep main.go minimal, logic in packages
- Read resource files at request time, not at startup (allows updates without restart)
- Serve `docs/SPEC.md` as a resource (`resources://spec`) alongside `resources://openapi/spec` and `resources://skill/{name}`
