# AGENTS.md

Repo-local guidance for OpenCode sessions working on Chetter.

## Project Structure

- **Root** (`main.go`, `internal/*`): The MCP server and control plane.
- **Runner** (`runner/`): The containerized agent harness. Separate Go module with its own `go.mod` and `Makefile`.
- **Runner Images** (`runner/images/`): Dockerfile variants for agent execution environments (golang, python, node, rust, minimal). All inherit from `Dockerfile.chetter-base` except `minimal` which builds from `debian:bookworm-slim`.
- **CLI** (`cmd/chetterctl/`): Token management CLI.
- **DB** (`db/`): Goose migrations and sqlc query files.
- **Proto** (`proto/`): ConnectRPC service between server and runner.

Two binaries: `chetter` (server) and `chetterctl` (token CLI).

## Developer Commands

```bash
# Root (server)
make build          # go build chetter + chetterctl
make test           # go test ./... (fast, skips runner tests)
make vet            # go vet ./...
make lint           # staticcheck ./...
make check          # test + vet + lint + runner-check
make generate       # buf generate + sqlc generate (requires bin/buf, bin/sqlc)
make migrate        # goose up against DB_DSN
make migrate-status # goose status

# Runner (cd runner/)
make test           # go test ./...
make vet            # go vet ./...
make lint           # staticcheck ./...
make check          # test + vet + lint
make local          # build runner + mcp-bridge binaries

# Docker images (from repo root)
make docker-build-mcp            # build MCP server image
make docker-build-runner-base    # build heavy base image (Go, opencode, claude, tools)
make docker-build-runner         # build default runner image (base + app layer)
make docker-build-golang         # build golang variant
make docker-build-python         # build python variant
make docker-build-node           # build node variant
make docker-build-rust           # build rust variant
make docker-build-minimal        # build minimal variant (no language toolchain)
```

**Order matters for schema changes:** `sqlc generate` reads from `db/migrations/`, so always update migrations **before** running `make generate`.

## Testing

- Unit tests: `go test ./internal/config/`, `go test ./internal/store/` — fast, no DB.
- Integration tests: `go test ./internal/service/` — require a **real TiDB**. The test harness (`internal/testdb`) auto-spins a Docker container if `CHETTER_TEST_DSN` is not set.
- To skip integration tests: `go test ./internal/config/ ./internal/store/`
- To run with an existing TiDB: `CHETTER_TEST_DSN="root@tcp(127.0.0.1:4000)/?parseTime=true" go test ./...`
- `CHETTER_TEST_LOCAL_TIDB` (host:port) is also supported as a fallback.

**CI note:** If Docker is unavailable, integration tests skip automatically with a message. Do not treat skips as failures.

## Schema & Codegen

**Two sources of truth for schema:**

1. **`internal/store/schema.go`** — `schemaStatements` + `ensureTaskMetadataColumns` / `ensureScheduleMetadataColumns` / `ensureRunnerMetadataColumns`. These run on every server startup via `ApplySchema()`. They provide **zero-downtime auto-migration** for new columns.
2. **`db/migrations/*.sql`** — Used by Goose for explicit migrations and by **sqlc** to infer the schema for Go code generation.

**When adding a column:**
1. Add it to the `CREATE TABLE` in `schema.go`.
2. Add an `ensure*Columns` entry in `store.go` so existing deployments auto-migrate.
3. Create a Goose migration in `db/migrations/` if the change needs to be tracked.
4. Update the `.sql` query files in `db/queries/`.
5. Run `make generate` to regenerate sqlc models.

**Important:** sqlc reads all Goose migration files to build its schema. Migration `002_drop_nats_columns.sql` drops columns (`listen_subject`, `result_subject`) that no longer exist in the base `CREATE TABLE`. This would break sqlc **unless** those columns are also present in `001_create_chetter_core.sql` (they were added retroactively for sqlc compatibility).

## Auth Model

- **Admin token:** `CHETTER_MCP_AUTH_TOKEN` env var. Single static bearer token. Bypasses all scoping.
- **Team tokens:** Stored in `api_tokens` table. SHA-256 hashed. Each token belongs to a user in a team.
- Tasks/schedules get `team_id` auto-stamped from the auth context.
- Team-scoped tokens see only their team's work via `team_id` filtering on list queries.

## Multi-Package Monorepo

The root and `runner/` are **separate Go modules**.

```
chetter/
  go.mod          # server
  runner/go.mod   # runner harness
```

- Root depends on runner proto (generated from `proto/runner/v1/runner.proto`).
- Runner depends on root proto for the ConnectRPC client.
- Do not add cross-module circular imports.
- The `bin/` directory holds tool binaries (buf, sqlc) shared by both modules.

## Key Architecture Notes

- **Runner communication** uses ConnectRPC over HTTP (not NATS). The runner polls `ClaimTask` with a lease-based claim. Leases expire after 60s and are renewed on heartbeat.
- **Task claiming** uses `SELECT ... FOR UPDATE SKIP LOCKED` for atomic pending-task claiming.
- **Reaper** runs every 30s to reclaim expired leases and mark stale tasks. `reaperHealthMaxEventSec = 120`.
- **GitHub webhook** is optional. If `GITHUB_APP_ID`, `GITHUB_INSTALLATION_ID`, `GITHUB_APP_PRIVATE_KEY_B64`, and `GITHUB_WEBHOOK_SECRET` are set, the webhook handler is registered.
- **Arcane tools** are conditionally registered only if `ARCANE_SERVER_URL` and `ARCANE_API_KEY` are configured.

## Environment & Config

Config is loaded from env vars in `internal/config/config.go`.

Key env vars for local dev:
- `DATABASE_DSN` — MySQL/TiDB connection string.
- `CHETTER_MCP_AUTH_TOKEN` — Admin bearer token.
- `DEFAULT_AGENT_IMAGE` — Default runner image.
- `DEFAULT_TASK_TIMEOUT_SEC` — Task timeout (default 600).
- GitHub App fields for PR review automation.

## Gotchas

- `make generate` installs `buf` and `sqlc` into `bin/`. The first run may take a minute to download.
- `internal/repository/` is **generated by sqlc** — do not edit by hand.
- `gen/` is **generated by buf** — do not edit by hand.
- The root `test` target does **not** run runner tests. Use `make check` for full coverage.
- When adding MCP tools, update both `internal/service/tools.go` (Go handler) and the `.opencode/opencode.json` (client-side commands).
