# Chetter Backend — Design & Technical Choices

This article explains how the Chetter backend is built: the MCP server/control plane and
the containerized runner. It is a companion to the [Web UI article](WEBUI.md), which
covers the frontend. The goal here is to explain *why* the system is shaped the way it
is, with code examples that show the mechanisms in action.

Chetter is a control plane for a fleet of containerized coding agents. A client (an MCP
consumer such as an editor or CLI, or the web UI) submits a *task*; the server queues it;
a *runner* claims it, spins up an agent harness (OpenCode, Claude Code, Pi, CodeWhale,
Codex) inside a sandboxed container, streams events back, and the server records the
result.

There are two separately versioned binaries that matter, plus a third CLI:

| Binary | Module | Role |
|---|---|---|
| `chetter` | root `go.mod` | MCP server + web API + control plane |
| `runner` | `runner/go.mod` | Agent harness daemon, one per worker node |
| `chetterctl` | root `go.mod` | Token management CLI |

The root and `runner/` are **separate Go modules** (the runner imports the root module
only for the generated protobuf types, via a `replace` directive pointing at `..`).

---

## 1. System shape

```text
MCP client / editor        Web browser
        │                       │
        ▼                       ▼
   ┌─────────────────────────────────────────────┐
   │            chetter (control plane)          │
   │                                             │
   │  /mcp   MCP StreamableHTTP (go-sdk)         │
   │  /api   ConnectRPC web API (proto/api/v1)   │
   │  /      embedded static web UI              │
   │                                             │
   │  internal/service    business logic         │
   │  internal/data       dialect facade (sqlc)  │
   │  internal/store      schema + migrations    │
   └──────────────┬──────────────────────────────┘
                  │ ConnectRPC (proto/runner/v1)
                  ▼   ClaimTask / Heartbeat / ReportTaskEvents
   ┌─────────────────────────────────────────────┐
   │              runner (per node)              │
   │                                             │
   │  claimLoop → docker/kubernetes/local        │
   │  harness (opencode/claude-code/pi/...)      │
   │  gVisor (runsc) sandbox                     │
   └─────────────────────────────────────────────┘
```

The two halves communicate only through ConnectRPC. There is no message bus: the runner
**polls** the server with long-poll `ClaimTask` calls and pushes events back with
`ReportTaskEvents`, and the server pushes control commands back inside `Heartbeat`
responses. This keeps every interaction observable through ordinary HTTP and avoids a
second infrastructure dependency (no NATS/Redis).

---

## 2. The control plane in `main.go`

`main.go` is the composition root: it loads config, opens the database, constructs the
service, and mounts three HTTP surfaces on two listeners.

```go
// main.go (abridged)
func run() error {
	cfg := config.Load()
	// ... signal handling, store.Open, pingWithRetry, ApplySchema ...

	svc := service.New(cfg, st)
	// definitions manager, GitHub App manager ...

	eventBus := webapi.NewEventBus()
	repo := data.New(st.DB(), st.Dialect())
	runnerSvc := service.NewRunnerRPCService(repo, st.DB(), st.Dialect()).
		WithEventBus(eventBus).
		WithEventCallbacks(svc).
		WithGitHubActions(svc).
		WithSecurityAuditLogger(svc)
	svc.SetRunnerRPC(runnerSvc)
	svc.Start(ctx)

	// MCP surface (port HTTPAddr)
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "chetter", Version: "v0.1.0"}, nil)
	service.RegisterTools(mcpServer, svc)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, Logger: slog.Default()})

	mux := http.NewServeMux()
	mux.Handle("/mcp", authMiddleware(cfg.MCPAuthToken, st.DB(), mcpHandler))
	runnerPath, runnerHandler := runnerv1connect.NewRunnerServiceHandler(runnerSvc)
	mux.Handle(runnerPath, runnerRPCAuthMiddleware(cfg.RunnerRPCToken, runnerHandler))
	// metrics, healthz, readyz, github webhook ...

	// Web API + UI surface (port WebAddr)
	webMux := http.NewServeMux()
	webHandlers := webapi.NewHandlers(svc, eventBus)
	webapi.RegisterHandlers(webMux, webHandlers, cfg.MCPAuthToken, st.DB(), oidcAuth, repo)
	webMux.Handle("GET /api/server-info", /* ... */)
	webMux.Handle("/", webui.Handler())
	// ... two http.Server instances, graceful shutdown sequence ...
}
```

Three separate surfaces share one `Service`:

- **`/mcp`** — the Model Context Protocol endpoint, streamable HTTP, stateless.
- **`/api`** — the ConnectRPC web API consumed by the SPA.
- **`/`** — the embedded static UI.

They are split across two listeners (the MCP/runner port and the web port) so the
operator can firewall the control plane separately from the browser-facing API.

---

## 3. Configuration

Configuration is env-var driven and loaded once in `internal/config/config.go`. Key knobs
include `DATABASE_DSN`, `CHETTER_DB_DIALECT` (explicit `tidb`/`mysql`/`postgres`, else
auto-detected), `CHETTER_MCP_AUTH_TOKEN`, `CHETTER_RUNNER_RPC_TOKEN`, and
`DEFAULT_AGENT_IMAGE`. Optional subsystems — GitHub App, OIDC SSO, and Arcane — are only
registered when their env vars are present, so the default deployment is minimal.

```go
// config validation is explicit; optional features are gated on presence
if cfg.OIDCConfigured() {
    oidcAuth, err = auth.NewOIDCAuth(ctx, cfg.OIDCConfig())
    // ...
}
if cfg.GitHubAppConfigured() {
    githubManager, err = newGitHubManager(cfg)
    // ...
}
```

---

## 4. Data layer: one schema, three dialects, generated queries

Chetter runs on **TiDB, MySQL (incl. Aurora), and PostgreSQL**. The data layer is
designed around that constraint:

- **Schema** is defined both as idempotent bootstrap DDL in `internal/store/schema.go`
  (MySQL/TiDB) with a derived PostgreSQL variant in `schema_postgres.go`, and as ordered
  **Goose migrations** in `db/migrations/` and `db/postgres/migrations/`.
- **Queries** are written in sqlc `.sql` files, twice — once per dialect family — and
  compiled into two generated packages: `internal/repository/` (MySQL/TiDB, `?`
  placeholders) and `internal/repositorypostgres/` (PostgreSQL, `$1` placeholders).
- **`internal/data`** is a runtime facade: `data.New(db, dialect)` returns a
  `Repository` interface backed by whichever generated package matches the detected
  dialect. Business code depends only on the interface.

```go
// main.go — the facade selects the dialect-specific implementation at runtime
repo := data.New(st.DB(), st.Dialect())
runnerSvc := service.NewRunnerRPCService(repo, st.DB(), st.Dialect())
```

The dialect split surfaces in the query files themselves. The same logical upsert
(`UpsertRunnerHeartbeat`) is written once per dialect:

```sql
-- db/queries/runners.sql (MySQL/TiDB)
INSERT INTO runners (...) VALUES (...)
ON DUPLICATE KEY UPDATE
    status = VALUES(status),
    image_ref = VALUES(image_ref),
    -- ...
    started_at = COALESCE(VALUES(started_at), started_at),
    last_seen_at = VALUES(last_seen_at);
```

```sql
-- db/postgres/queries/runners.sql (PostgreSQL)
INSERT INTO runners (...) VALUES (...)
ON CONFLICT (id) DO UPDATE SET
    status = EXCLUDED.status,
    image_ref = EXCLUDED.image_ref,
    -- ...
    started_at = COALESCE(EXCLUDED.started_at, runners.started_at),
    last_seen_at = EXCLUDED.last_seen_at;
```

`make generate` runs both `buf generate` (protobuf) and `sqlc generate`, then a
`genfacade` tool regenerates the facade methods so the `data.Repository` interface stays
in lockstep with the sqlc output.

---

## 5. The MCP server

The MCP surface is defined in `internal/service/tools.go` with the
`modelcontextprotocol/go-sdk`. Each tool is a struct with `jsonschema` tags (which the
SDK turns into the tool's input JSON Schema) plus a handler method. The tool list is the
full control-plane surface — submit/status/list tasks, triggers, sessions, tokens,
teams, git identities, model catalog, audit, and more:

```go
// internal/service/tools.go
type SubmitTaskInput struct {
	TeamID     string `json:"team_id,omitempty" jsonschema:"Owning team ID..."`
	Prompt     string `json:"prompt" jsonschema:"Task prompt to run in the Chetter runner"`
	GitURL     string `json:"git_url,omitempty" jsonschema:"Repository URL to clone before running the task"`
	AgentImage string `json:"agent_image,omitempty" jsonschema:"Runner harness image override"`
	Agent      string `json:"agent,omitempty" jsonschema:"OpenCode agent to use for the task"`
	ProviderID string `json:"provider_id,omitempty" jsonschema:"OpenCode provider id for model selection"`
	ModelID    string `json:"model_id,omitempty" jsonschema:"OpenCode model id..."`
	Harness    string `json:"harness,omitempty" jsonschema:"Runner harness to use..."`
	Isolation  string `json:"isolation,omitempty" jsonschema:"Isolation requirement..."`
	// ...
}

func RegisterTools(server *mcp.Server, svc *Service) {
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_submit_task", Description: "Submit a development task to the Chetter runner fleet..."}, svc.submitTaskTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_task_status", Description: "Get current status and result details for a chetter task."}, svc.taskStatusTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_list_tasks", Description: "List recent chetter tasks, optionally filtered by status."}, svc.listTasksTool)
	// ... ~50 more tools: triggers, event callbacks, tokens, teams,
	//     git identities, model catalog, definitions, audit, artifacts ...
}
```

The MCP handlers return a stable `TaskToolRecord` shape — deliberately decoupled from the
raw store row so internal columns can be added without breaking existing MCP clients:

```go
// TaskToolRecord is the stable MCP task response shape. Store-level task
// records may grow internal audit fields without breaking existing MCP clients.
type TaskToolRecord struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Prompt      string `json:"prompt"`
	GitURL      string `json:"git_url,omitempty"`
	// ...
	TotalInputTokens int64 `json:"total_input_tokens"`
	CostCents        int64 `json:"cost_cents"`
}
```

---

## 6. The web API: ConnectRPC over `proto/api/v1`

The SPA (and any other API consumer) talks to the server through a ConnectRPC service
defined in `proto/api/v1/api.proto` and implemented in `internal/webapi/`. The service is
split into domain handlers — `Task`, `Event`, `Session`, `Trigger`, `EventCallback`,
`Fleet`, `Admin`, `Arcane`, `Catalog` — each a thin wrapper over the shared `Service`:

```go
// internal/webapi/server.go
type Handlers struct {
	Task          *taskHandler
	Event         *eventHandler
	Session       *sessionHandler
	Trigger       *triggerHandler
	EventCallback *eventCallbackHandler
	Fleet         *fleetHandler
	Admin         *adminHandler
	Arcane        *arcaneHandler
	Catalog       *catalogHandler
}

func RegisterHandlers(mux *http.ServeMux, h *Handlers, adminToken string, db *sql.DB, oidc *auth.OIDCAuth, teams TeamResolver) {
	interceptor := NewAuthInterceptor(adminToken, db, oidc)
	mux.Handle(apiv1connect.NewTaskServiceHandler(h.Task, connect.WithInterceptors(interceptor)))
	mux.Handle(apiv1connect.NewEventServiceHandler(h.Event, connect.WithInterceptors(interceptor)))
	// ... every service, with the same auth interceptor ...
}
```

The protobuf contract is the single source of truth for the UI: `TaskService`,
`EventService`, `FleetService`, etc. are the exact services the TypeScript client calls
(see the Web UI article). Server-streaming RPCs — `SubscribeTaskEvents` and
`SubscribeFleetUpdates` — power the live event log and fleet dashboard:

```proto
// proto/api/v1/api.proto
service TaskService {
  rpc SubmitTask(SubmitTaskRequest) returns (SubmitTaskResponse);
  rpc ListTasks(ListTasksRequest) returns (ListTasksResponse);
  rpc CancelTask(CancelTaskRequest) returns (CancelTaskResponse);
  rpc SubscribeTaskEvents(SubscribeTaskEventsRequest) returns (stream TaskEvent);
  // ...
}
```

### Auth interceptor

All ConnectRPC handlers share one interceptor that resolves a `Scope` from the request
and injects it into the context. Bearer tokens (the admin MCP token or a DB API token)
are tried first; a valid OIDC session cookie is accepted as a fallback:

```go
// internal/webapi/interceptor.go
func (a *authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		scope, ok := a.resolve(ctx, req.Header())
		if !ok {
			return nil, connect.NewError(connect.CodeUnauthenticated, nil)
		}
		return next(auth.WithScope(ctx, scope), req)
	}
}

func (a *authInterceptor) resolve(ctx context.Context, h http.Header) (auth.Scope, bool) {
	scope, ok := auth.ResolveToken(ctx, a.adminToken, a.db, bearerToken(h))
	if ok {
		return scope, true
	}
	if a.oidc != nil {
		if sessionScope, ok := a.oidc.ScopeFromCookie(h); ok {
			return sessionScope, true
		}
	}
	return auth.Scope{}, false
}
```

The `Scope` carries team membership, which the list queries use to filter results —
**team-scoped tokens only ever see their own team's tasks** because the handler passes the
scope-derived `team_id` down into the SQL. The admin token bypasses scoping entirely.

---

## 7. Runner RPC: the lease-based claim protocol

The server↔runner protocol is defined in `proto/runner/v1/runner.proto`:

```proto
service RunnerService {
  rpc RegisterRunner(RegisterRunnerRequest) returns (RegisterRunnerResponse) {}
  rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse) {}
  rpc ClaimTask(ClaimTaskRequest) returns (ClaimTaskResponse) {}
  rpc ReportTaskEvents(ReportTaskEventsRequest) returns (ReportTaskEventsResponse) {}
  rpc PruneWorkspaces(PruneWorkspacesRequest) returns (PruneWorkspacesResponse) {}
  // GitHub actions proxied from the sandboxed agent to the server
  rpc GitHubCreateIssue(GitHubCreateIssueRequest) returns (GitHubCreateIssueResponse) {}
  // ...
}
```

`ClaimTask` is a **long-poll with a lease**. The runner asks for a task and a lease
duration; the server atomically claims the next pending attempt (marking it with a fresh
`claim_id`, a `runner_id`, and a `lease_expires_at` timestamp) and returns it. The
critical part is the atomicity — two runners polling at the same time must never receive
the same task.

`claimOnce` does that with `SELECT ... FOR UPDATE SKIP LOCKED`, which locks a candidate
row and *skips* rows already locked by another in-flight claim:

```go
// internal/service/runner_rpc.go (abridged)
err = withTxRetry(ctx, s.rawDB, s.dialect, func(q data.Repository) error {
	queued, err := q.GetClaimableExecutionAttemptForUpdate(ctx,
		repository.GetClaimableExecutionAttemptForUpdateParams{
			RunnerID:         sql.NullString{String: runnerID, Valid: true},
			IsolationEnabled: isolationEnabled,
		})
	if errors.Is(err, sql.ErrNoRows) {
		return errNoClaimableTask
	}
	// ...
	claimID, _ := randomID("clm")
	now := time.Now().UTC()
	attemptRows, err := q.MarkExecutionAttemptClaimed(ctx,
		repository.MarkExecutionAttemptClaimedParams{
			RunnerID:       nullString(runnerID),
			ClaimID:        claimID,
			ClaimedAt:      sql.NullTime{Time: now, Valid: true},
			LeaseExpiresAt: sql.NullTime{Time: now.Add(lease), Valid: true},
			StartedAt:      sql.NullTime{Time: now, Valid: true},
			// ...
		})
	// ... MarkTaskRunning, insert a "task.claimed" event ...
})
```

Every subsequent mutation — heartbeat renewal, event reporting, GitHub RPC — is
**fenced** to the active `(task_id, execution_id, runner_id, claim_id)` tuple with an
unexpired lease, so a stale runner that lost its lease cannot write into a task that has
been handed to someone else.

The long-poll loop waits on a **notification channel** rather than busy-polling the
database:

```go
// internal/service/runner_rpc.go — ClaimTask wait loop
for {
	waitCh := s.claimNotify.waitCh()          // snapshot BEFORE polling
	claim, err := s.claimOnce(ctx, req.Msg.RunnerId, time.Duration(leaseSec)*time.Second)
	if err == nil {
		// ... hydrate resume state, resolve model/MCP endpoints ...
		return connect.NewResponse(&runnerv1.ClaimTaskResponse{Task: protoTask}), nil
	}
	if !errors.Is(err, errNoClaimableTask) {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// wait on the notifier, or a safety-net poll timer, whichever fires first
	select {
	case <-ctx.Done():
		return nil, connect.NewError(connect.CodeCanceled, ctx.Err())
	case <-waitCh:
	case <-timer.C:
	}
}
```

The `claimNotifier` (`internal/service/claim_notifier.go`) is a broadcast channel that
wakes in-flight long-polls the moment a task becomes claimable — on `SubmitTask`, webhook
triggers, schedules, `RecoverTask`/`RerunTask`, session resume, and the reaper's
requeue path. The snapshot-before-poll ordering guarantees no submission can be missed:
a poller that snapshots before the notify is woken by the channel close, and a poller
that snapshots after the notify runs its poll after the row committed. A 15s safety-net
poll catches work that bypassed the notifier (direct DB writes).

For multi-replica deployments, each server replica also polls a lightweight
`claim_notify_counter` table once per second; a counter change triggers the local
`claimNotifier` broadcast so runners wake within ~1s of a task submitted on another
replica. The coordination layer (`internal/service/coordination.go`) also provides
`trigger_locks` for cron deduplication, `admission_locks` for strict pending-admission,
and durable `runner_drain_requests` so drain commands survive across replicas. Task-event
streams poll the DB every second alongside the local event bus, and a per-replica
fleet-cursor poller (`internal/webapi/fleet_poller.go`) feeds fleet streams when task
activity lands from other replicas (every ~3s).

---

## 8. Heartbeats and the reaper

The runner sends a `Heartbeat` every 5 seconds, carrying its full `RunnerInfo` (running
tasks, resource usage, isolation capability, MCP relay rejection counters). The server
renews the lease on each heartbeat and returns any pending `RunnerCommand`s (cancel,
drain) in the response:

```go
// internal/service/runner_rpc.go
func (s *RunnerRPCService) Heartbeat(ctx context.Context, req *connect.Request[runnerv1.HeartbeatRequest]) (*connect.Response[runnerv1.HeartbeatResponse], error) {
	// ... upsertRunner, renew leases for active executions, audit relay rejections ...
	commands := s.runnerCommands(ctx, req.Msg.Runner)
	return connect.NewResponse(&runnerv1.HeartbeatResponse{Commands: commands}), nil
}
```

Heartbeat *events* are still recorded to `task_events` for observability, but throttled to
one row per task per minute (`heartbeatEventMinInterval`) so the event log is not flooded
while still showing when a runner went silent.

The **reaper** runs every 30s and reclaims expired leases. It selects expired execution
attempts, and for each one still under its attempt budget and not from a deliberately
drained runner, creates a fresh session/prompt/attempt and re-queues it:

```go
// internal/service/service.go
func (s *Service) reapExpiredLeases() {
	// ...
	err := withTxRetry(ctx, s.rawDB, s.dialect, func(q data.Repository) error {
		candidates, err := q.ListReclaimableExecutionAttemptsForUpdate(ctx, expiredBefore)
		// ...
		for _, task := range candidates {
			// skip if attempt budget exhausted, or runner was deliberately drained
			// ... MarkExecutionAttemptLost, create new session/attempt, notify ...
		}
	})
}
```

Failed leases beyond the attempt budget are marked failed for inspection. Auto-recovery
can be disabled with `DEFAULT_AUTO_RECOVERY=false`, in which case every expired lease is
failed rather than re-queued.

---

## 9. The runner

The runner (`runner/cmd/runner/main.go`) is intentionally small — it loads
`runner.yaml`, builds a `controller.Runner`, and starts it. The interesting logic is in
`runner/internal/controller/`.

### The claim loop

The runner reserves a concurrency slot *before* calling `ClaimTask`, so it never holds a
claimed task while waiting for a free slot (the semaphore has `MaxConcurrent+1` slots,
the extra one reserved for the poller itself):

```go
// runner/internal/controller/runner_rpc.go
func (r *Runner) claimLoop(ctx context.Context) {
	for {
		if r.draining.Load() { return }
		select {
		case r.sem <- struct{}{}:   // reserve a slot first
		case <-ctx.Done(): return
		}
		resp, err := r.claimClient.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
			RunnerId:     r.runnerID,
			WaitSeconds:  30,
			LeaseSeconds: 120,
		}))
		if err != nil {
			<-r.sem
			// backoff and retry
		}
		if resp.Msg.Task == nil || resp.Msg.Task.TaskId == "" {
			<-r.sem
			continue
		}
		// hand the task to runTask; its defer releases the slot
		go r.runTask(/* ... */)
	}
}
```

A single claim loop replaces the earlier design of "one long-polling goroutine per
concurrent slot", which had been issuing a DB transaction every second while idle and
dominating the fleet's query rate.

### Execution backends and harnesses

Once a task is claimed, `runTask` clones the repository (Git credentials stay in the
runner, never in the agent container), generates a harness config, and launches the
agent. The **execution backend** is pluggable:

| Backend | Runtime | Isolation | Use case |
|---|---|---|---|
| `local` | subprocess | none | development / CI smoke tests |
| `docker` | Docker CLI + runc | convenience only | trusted workloads |
| `docker` + gVisor | Docker CLI + `runsc` | userspace kernel sandbox | untrusted tasks |
| `kubernetes` + gVisor | Pod + runsc runtime class | userspace kernel sandbox | managed clusters |

The **harness** is selected per task (`opencode`, `claude-code`, `pi`, `codewhale`,
`codex`). Harnesses implement small interfaces — `ServeHarness` (interactive HTTP API),
`RPCHarness` (subprocess RPC), `SessionContinuable` (resume) — so the controller is
harness-agnostic:

```go
// runner/internal/controller/runner_task.go (abridged)
func (r *Runner) runTask(req task.TaskRequest) {
	h := r.harnessFor(req.Harness)
	// ...
	if rpcHarness, ok := h.(harness.RPCHarness); ok {
		// subprocess RPC mode (e.g. pi --mode rpc)
	}
	serveHarness, ok := h.(harness.ServeHarness)
	if !ok {
		// no supported execution mode for this harness
	}
	// generate config, start the harness server, stream progress/events back
}
```

### Isolation

Security isolation is **gVisor only**. A runner advertises `enforced_isolation` (gVisor
configured *and* `runsc` available) in its heartbeat; the server only admits
isolation-requiring tasks to capable runners and fails them fast otherwise. A runner that
receives an isolation-requiring task it cannot sandbox refuses it at claim time with
`error_category=isolation_unavailable` rather than running it unsandboxed. Trusted
single-tenant deployments opt out with `CHETTER_ALLOW_UNISOLATED=true` on both sides.

### MCP bridge

Each execution gets its own MCP server and a per-execution bearer token; a relay carries
the runner-wide Chetter MCP capabilities into the sandbox. Relay rejections are counted
in heartbeats, surface as system audit events, and feed the
`chetter_mcp_relay_rejected_requests` metric. Capabilities are revoked on close and never
logged.

---

## 10. Codegen

Both protobuf and SQL are code-generated, and both are owned by `make generate`:

```makefile
# Makefile
generate: tools
	$(BUF) dep update
	$(BUF) generate       # protobuf → gen/ (Go) + web/src/gen/ (TS)
	$(SQLC) generate      # sqlc → internal/repository + internal/repositorypostgres
	go generate ./internal/data   # regenerate the dialect facade
```

- **`buf generate`** produces Go and TypeScript from `proto/api/v1/api.proto` and
  `proto/runner/v1/runner.proto`, including the ConnectRPC clients/handlers and
  protovalidate validation.
- **`sqlc generate`** produces the two dialect-specific repository packages.
- **`go generate ./internal/data`** runs the `genfacade` tool to keep the
  `data.Repository` interface in sync with both generated packages.

Generated output (`gen/`, `internal/repository*/`, `web/src/gen/`) is never edited by
hand.

---

## 11. Observability

- **Prometheus metrics** at `/metrics` (`internal/metrics`), including aggregate MCP
  relay rejection counters and DB health.
- **Audit log** (`audit_log` table) recording server-side events — webhook receipts,
  trigger matches, task submissions, session resume, cancellation, queue clear, token
  create/delete, model catalog sync — queryable via `chetter_list_audit_events`.
- **Task artifacts** (`task_artifacts`) track GitHub issues/PRs/comments created by
  tasks, discovered passively via a `Task: task_XXX` footer signature.
- **`/healthz`** and **`/readyz`** for liveness/readiness (readiness pings the DB).

---

## 12. Summary of the key decisions

| Decision | Rationale |
|---|---|
| ConnectRPC everywhere (MCP + web + runner) | One transport, one proto contract, streaming, HTTP-observable |
| Long-poll `ClaimTask` with lease + `FOR UPDATE SKIP LOCKED` | Atomic queueing with no broker; leases make crashes safe |
| In-process `claimNotifier` + DB counter poll + safety-net poll | Zero-idle DB load for single replica; ~1s wake-up across replicas |
| sqlc dual-dialect + `data` facade | TiDB/MySQL/PostgreSQL support behind one interface |
| Handlers as thin wrappers over one `Service` | Business logic in one place; MCP/web/runner share it |
| Harness interface + pluggable backend | New agent CLIs and executors slot in without touching the controller |
| gVisor as the *only* sandbox boundary | Honest security model; plain Docker is explicitly not a boundary |
| Generated proto + sqlc + facade | Compile-time contract enforcement across Go, TS, and SQL |

The backend's central idea is **a small, typed control plane with one shared domain
model**, exposed over three protocols but implemented once — and a runner that is a
dumb-but-safe executor: it claims, sandboxes, runs, and reports, while every decision
about what may run and for whom stays on the server.
