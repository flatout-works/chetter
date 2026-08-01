# Repository Quality Review

**Date:** 2026-08-01  
**Scope:** Maintained Go server and runner code, SQL/data facade, Svelte frontend, tests, dependencies, and repository organization.

## Executive summary

The repository has good baseline discipline: clear package naming, substantial tests, generated-code boundaries, and passing root static analysis. The review nevertheless found several security issues, one reproducible runner test failure, meaningful dead code, and large orchestration areas that should be decomposed.

The recommended order is:

1. Address security and correctness issues.
2. Remove confirmed dead code.
3. Strengthen dialect and lifecycle testing.
4. Refactor the highest-complexity orchestration paths.
5. Simplify repeated frontend state and UI patterns.

## Implementation progress

Work started on the highest-priority, independently reviewable items:

- [x] Sanitize task summary and session export Markdown before `{@html}` rendering, with executable-HTML regression tests enforced by `make web-check`.
- [x] Add contained workspace-file writing that rejects traversal and symlink paths.
- [x] Fix Pi's explicit model/provider precedence and make the regression test independent of ambient Pi variables.
- [x] Update the frontend lockfile to resolve the PostCSS security advisory; `npm audit --omit=dev` reports no vulnerabilities.
- [ ] Authenticate and narrow per-task MCP listeners.
- [ ] Decide and implement PostgreSQL startup migration behavior.
- [x] Remove confirmed dead runner packages and stale compile-time placeholders.
- [x] Consolidate duplicated harness output logging into the shared harness package.
- [x] Make service shutdown idempotent, cancelable, and waiting for owned background loops.

## Immediate priorities

### 1. Sanitize rendered Markdown

**Files:**

- `web/src/lib/utils.svelte.ts`
- `web/src/routes/tasks/[id]/+page.svelte`

`marked` output is inserted using `{@html}` without sanitization:

```ts
export function renderMarkdown(text: string): string {
  return parser(lexer(text));
}
```

Both task summaries and session exports can contain agent-controlled content. A malicious repository or prompt could cause an agent to emit raw HTML containing scripts or event handlers. Because the UI stores authentication information in browser state, the impact could include token theft.

**Recommendations:**

- Sanitize rendered HTML with a maintained sanitizer such as DOMPurify.
- Alternatively, disable raw HTML in Markdown and render from a constrained token/component model.
- Centralize all Markdown conversion in one safe function; the task page currently also calls `marked.parse()` directly.
- Add tests containing `<script>`, `onerror`, `javascript:` URLs, SVG payloads, and malformed HTML.

### 2. Prevent runner extra-file path traversal

**File:** `runner/internal/controller/runner_task.go`

```go
filePath := filepath.Join(wsDir, filename)
```

`filename` comes from the runner RPC `extra_files` map. Names such as `../../somewhere` or absolute paths can escape the workspace. The server currently appears to use this primarily for recovery transcripts, but this is still a trust-boundary violation at the runner RPC layer.

**Recommendations:**

Create a shared secure path resolver that:

1. Rejects absolute paths.
2. Cleans the path.
3. Rejects `..` traversal.
4. Verifies the result remains beneath `wsDir` using `filepath.Rel`.
5. Rejects symlink traversal, or writes through an `openat`-style safe mechanism.

Add table-driven tests for absolute paths, nested traversal, platform separators, symlinks, and valid nested files.

### 3. Fix Pi model/provider precedence and environment-dependent tests

`cd runner && make check` currently fails:

```text
TestBuildRPCCommandProviderQualifiedModel
got provider "openai-codex", model "anthropic/claude-sonnet-4-5"
want provider "anthropic", model "claude-sonnet-4-5"
```

**Files:**

- `runner/harness/pi/rpc.go`
- `runner/harness/pi/pi_test.go`

`modelFields` loads `PI_PROVIDER` from the ambient process before interpreting an explicitly provider-qualified model. This is more than a test-isolation issue: an explicit `anthropic/model` should normally override an ambient default provider.

**Recommendations:**

Define and document precedence explicitly:

1. Explicit `ProviderID`.
2. Provider embedded in explicit `ModelID`.
3. Environment defaults.
4. Built-in defaults.

Use `t.Setenv("PI_PROVIDER", "")` and `t.Setenv("PI_MODEL", "")` in relevant tests so they cannot inherit developer or CI configuration.

### 4. Make PostgreSQL startup migration behavior consistent

**File:** `internal/store/store.go`

`ApplySchema` returns immediately for PostgreSQL after running `CREATE TABLE IF NOT EXISTS` statements:

```go
if s.IsPostgres() {
    return nil
}
```

All column/index upgrade helpers then run only for MySQL/TiDB. This conflicts with the repository guidance that startup schema application provides zero-downtime upgrades for existing deployments across dialects. For an existing PostgreSQL table, an updated `CREATE TABLE IF NOT EXISTS` does not add new columns.

**Recommendations:**

Choose and enforce one migration policy:

- Implement PostgreSQL equivalents for startup `ensure*` operations, or
- Explicitly require Goose migrations before startup and remove claims that startup auto-migration handles PostgreSQL.

Add upgrade tests that begin with an older schema and call `ApplySchema`, rather than testing only creation from an empty database.

### 5. Authenticate and narrow per-task MCP listeners

**File:** `runner/internal/mcp/server.go`

```go
net.Listen("tcp4", "0.0.0.0:0")
```

These servers expose GitHub mutation tools and do not appear to require per-task authentication. A random port is not an authorization mechanism. Depending on runner networking, sibling containers, local processes, or reachable hosts may be able to invoke a task's MCP tools.

**Recommendations:**

- Bind to the narrowest address that remains reachable through gVisor.
- Require a high-entropy per-task bearer token.
- Validate task/execution identity at each tool invocation.
- Ensure a task's MCP server is unreachable from sibling tasks.
- Add an isolation test involving two simultaneous task MCP servers.

## High-value structural refactorings

### 6. Remove confirmed unused runner packages

No maintained Go code imports either package:

- `runner/internal/tools/`
- `runner/internal/executil/`

Together these contain approximately:

- 916 lines of production code.
- 451 lines of tests.
- 41 production functions.

`runner/internal/tools` appears to be a legacy local deploy/git/workspace toolset superseded by the current runner MCP and controller architecture. Because these packages are under `internal/`, out-of-repository consumers cannot legally import them.

**Recommendation:** Delete both packages after one final check of build scripts and documentation.

### 7. Remove or narrow production APIs used only by tests

Dead-code analysis found several functions reachable only from tests. Strong candidates include:

- `store.RetryTransient`
- `store.RetryTransientWithMax`
- `store.retryTransient`
- `modelcatalog.ParseYAMLOrDefault`
- `opencode.GenerateConfigWithEnv`
- `opencode.LogMCPStatus`
- `agentenv.ShellQuoteArg`
- `tokenUsageAccumulator.snapshot`

`store.IsTransientError` is genuinely used and should remain.

Some internal test helpers may reasonably stay, but production-only wrappers with no callers create misleading APIs. Prefer testing real production entry points or move test-only helpers into `_test.go` files.

### 8. Replace reflection-based PostgreSQL facade conversion

**File:** `internal/data/data.go`

The PostgreSQL facade converts generated PostgreSQL models into MySQL repository models using reflection:

```go
func convert[T any](source any) T
func copyValue(destination, source reflect.Value)
```

This conversion silently leaves fields at zero values when names or types stop matching. Schema generation can therefore succeed while runtime data is dropped or misconverted. Coverage for `internal/data` is only 8.9%, making this particularly risky.

**Recommendations:**

Extend `internal/data/cmd/genfacade` to generate explicit, compile-time checked conversions. Ideally:

- Generate common domain models independently of either sqlc package.
- Generate adapters from both dialect packages to those models.
- Fail generation when fields cannot be mapped.
- Avoid treating the MySQL-generated package as the canonical cross-dialect domain model.

At minimum, add parity tests that populate every field and compare MySQL and PostgreSQL adapter results.

### 9. Consolidate duplicate task record types and conversions

`store.TaskRecord` and `service.TaskToolRecord` are almost identical but differ subtly; for example, `Harness` exists only in the service type. Multiple conversion paths exist:

- `taskToolRecord`
- `repoTaskToToolRecord`
- `repoTaskToStoreRecord`
- `taskToProto`
- `protoTask`
- Hand-built `TaskToolRecord` literals in `webapi/handlers.go`

This invites field omissions whenever a task field is added.

**Recommendations:**

Introduce one domain-level task representation, separate from:

- sqlc persistence models.
- MCP wire models.
- ConnectRPC protobuf models.

Generate or centralize conversions and eliminate hand-built copies in:

- `webapi.taskHandler.SubmitTask`
- `webapi.triggerHandler.RunTrigger`

Review the current `Harness` defaulting in `SubmitTask`: the handler computes a default local value but passes `req.Msg.Harness` to the service.

### 10. Decompose high-complexity orchestration functions

The complexity scan found these production hotspots:

- `RunnerRPCService.recordTaskEvent` — complexity 60.
- `Service.reapExpiredLeases` — 42.
- OpenCode `watchEvents` — 38.
- `Runner.runTask` — 38.
- `Runner.handleRPCEvent` — 37.
- `Service.SubmitTask` — 37.
- `Service.ResumeAgentSession` — 34.

#### `recordTaskEvent`

It currently handles validation, payload normalization, redaction, error classification, heartbeat throttling, execution-attempt updates, task updates, session transitions, event persistence, publication, and callbacks.

Extract a normalized event type and separate:

1. `normalizeRunnerEvent`.
2. `validateExecutionOwnership`.
3. `persistRunnerEvent`.
4. `applyTerminalSessionTransition`.
5. `publishPersistedEvent`.

A typed state-transition function would make terminal behavior significantly easier to test.

#### `Runner.runTask`

Break it into staged operations:

1. Initialize execution/session state.
2. Prepare workspace.
3. Clone and configure Git.
4. Write validated extra files.
5. Start the MCP server.
6. Generate harness configuration.
7. Select the execution backend.
8. Finalize, export, and clean up.

This should reduce duplicated error publication and make stages independently testable.

### 11. Split oversized files by responsibility

Several maintained files are too large to review safely:

- `internal/service/service.go` — 2,377 lines.
- `internal/service/tools.go` — 1,849 lines.
- `runner/internal/controller/runner_task.go` — 1,804 lines.
- `internal/service/api.go` — 1,595 lines.
- `internal/store/store.go` — 1,593 lines.
- `internal/service/runner_rpc.go` — 1,457 lines.
- `internal/webapi/handlers.go` — 1,228 lines.
- `internal/webhook/handler.go` — 1,108 lines.
- `web/src/routes/tasks/[id]/+page.svelte` — 965 lines.

Split by cohesive domain rather than creating abstraction-heavy micro-packages. A possible service layout is:

```text
internal/service/
  tasks.go
  sessions.go
  triggers.go
  reaper.go
  audit.go
  runner_events.go
  runner_claims.go
  tools_tasks.go
  tools_triggers.go
  tools_admin.go
```

For the task Svelte page, extract:

- Task summary/header.
- Session history.
- Execution attempts.
- Artifact list.
- Progress timeline.
- Resume/recover/extend modals.
- Export viewer.

## Duplication and simplification opportunities

### 12. Share harness process-output handling

The same `pipeOutput` implementation exists in:

- `runner/harness/claude/events.go`
- `runner/harness/codewhale/events.go`
- `runner/harness/codex/events.go`
- `runner/harness/pi/events.go`

OpenCode has a related implementation.

Move this to the parent `harness` package with parameters for harness name, maximum line length, and logger. Password/secret generation is also repeated across harnesses and proxy binaries. Create a shared helper that handles `crypto/rand` errors instead of ignoring them.

### 13. Generalize repeated schema-upgrade helpers

`internal/store/store.go` repeats the same patterns for:

- Checking and adding columns.
- Checking and adding indexes.
- Checking and replacing full-text indexes.

Task, session, artifact, and audit full-text setup is nearly identical.

Introduce helpers such as:

```go
ensureColumns(ctx, table, []columnMigration)
ensureIndex(ctx, indexMigration)
ensureFulltextIndex(ctx, table, index, column)
```

Keep dialect-specific DDL explicit in migration data rather than embedding it in generic logic. Consider moving schema-upgrade code into:

```text
schema_upgrade_mysql.go
schema_upgrade_postgres.go
```

### 14. Unify Arcane HTTP request methods

**File:** `internal/service/arcane.go`

`get` and `post` duplicate request execution, response reading, status validation, and error construction.

Replace them with:

```go
do(ctx, method, path string, body io.Reader) ([]byte, error)
```

Also:

- Limit error response bodies.
- Include method and path in wrapped errors.
- Set content type only when a body exists.
- Consider decoding directly into a generic response type.

### 15. Consolidate list-page URL, filter, and pagination behavior

The Svelte routes repeatedly implement `syncURL`, query parsing, pagination, and filter effects:

- Tasks.
- Sessions.
- Triggers.
- Event callbacks.
- Audit.
- Artifacts.

Create a reusable Svelte 5 query-state class or helper that owns:

- Safe initial parsing.
- `resolve()` and `goto()`.
- Reset-to-first-page behavior.
- Serialization of empty filters.
- Debounced search.
- Server-side filter values.

This would also address many Svelte autofixer warnings around state mutations inside `$effect`.

### 16. Replace legacy writable stores or rename their modules

Several `.svelte.ts` modules still use legacy `writable` stores:

- `auth.svelte.ts`
- `theme.svelte.ts`
- `tasks.svelte.ts`
- `filter.svelte.ts`
- `settings.svelte.ts`
- `taskDetail.svelte.ts`

Because SSR is disabled, this is not currently an SSR state-leak issue. Nevertheless, these modules mix Svelte 5 rune conventions with legacy store APIs and contribute to effect-heavy synchronization.

Either migrate them to rune-backed classes or keep writable stores but rename them to ordinary `.ts` modules so the intended style is clear. Do this incrementally rather than as a wholesale rewrite.

## Svelte and UI findings

### 17. Resolve concrete Svelte autofixer findings

`svelte-check` reports zero errors and warnings, but the Svelte autofixer found:

- `goto()` calls without `resolve()` in several list pages.
- Internal `href` values without `resolve()`.
- One unkeyed each block in `runners/+page.svelte`.
- A mutable built-in `Map` in `triggers/[name]/+page.svelte`.
- State assignment inside effects in `ConfirmDialog.svelte`.
- Extensive effect-driven synchronization in `+layout.svelte`.
- The unsafe `{@html}` calls described above.

Use `resolve()` consistently for base-path-safe navigation. Key runner cards by a stable identifier. Replace reactive mutable `Map` usage with `SvelteMap`.

### 18. Follow Flowbite component conventions consistently

Raw `<button>` elements exist in:

- `web/src/routes/+layout.svelte`
- `web/src/routes/admin/audit/+page.svelte`

These violate the repository's Flowbite-only UI policy. Replace them with `<Button>` while retaining icon-only accessibility labels and overlay behavior.

Some hand-built panel/status markup also remains in the task detail page. Not every nested `<div>` needs to become a Card, but status pills and top-level panels should consistently use established shared components.

### 19. Simplify `ConfirmDialog`

The autofixer identifies two state variables synchronized through an effect. The dialog state and open state can likely be derived directly from the confirmation store, with explicit handlers for accept and cancel. This removes bidirectional synchronization and avoids transient inconsistent states.

## Lifecycle and concurrency improvements

### 20. Make `Service.Start` and `Stop` failure-safe and idempotent

**File:** `internal/service/service.go`

`Start` starts cron before loading triggers. If `loadTriggers` fails, the caller returns without stopping the cron instance.

`Stop` directly closes `reaperStop`, so calling it twice panics. It also signals loops but does not wait for the reaper and definitions sync goroutines to terminate.

**Recommendations:**

- Load and validate triggers before starting background work.
- Use `sync.Once` for shutdown.
- Use an internal cancelable context.
- Track background goroutines with `sync.WaitGroup` or `errgroup`.
- Wait for all owned loops during `Stop`.
- Add start-failure, double-stop, and shutdown-under-load tests.

### 21. Track asynchronous callbacks as service-owned work

Several callbacks are launched using `go func()` and `context.Background()`, including runner event callbacks. They may continue after service shutdown and are not bounded by a worker pool.

Use a bounded dispatcher or service-owned `errgroup`:

- Tie contexts to service lifetime.
- Cap concurrency.
- Log dropped or backpressured callbacks.
- Wait during shutdown where appropriate.

Webhook post-response work may intentionally outlive an HTTP request, but it should still be tied to application lifetime.

## Cleanup and smaller quality fixes

### 22. Remove stale compile-time placeholders

**File:** `internal/webapi/server.go`

```go
var _ = context.Background
var _ = http.StatusNotFound
```

Remove the unused `context` import and both placeholder declarations.

**File:** `internal/webapi/handlers.go`

```go
var _ repository.ChetterTrigger
```

The adjacent comments describe a once-planned compile-time check that no longer has value. Remove the dummy import/check and stale comments.

### 23. Stop ignoring meaningful errors in production paths

Most ignored errors are harmless type assertions or test cleanup, but some should be handled:

- `crypto/rand.Read` in harness/proxy password generation.
- `json.Marshal` when creating protocol payloads.
- `io.ReadAll` for HTTP error handling.
- `flag.FlagSet.Parse` in `chetterctl`.
- JSON config parsing that silently returns empty configuration.

For CLI code, prefer `flag.ContinueOnError` and return parse errors. `flag.ExitOnError` makes command code hard to test and means `_ = fs.Parse(...)` is misleading.

### 24. Clarify or consolidate runner example/test configurations

There are four similar files:

- `runner/runner.yaml`
- `runner/runner.docker.yaml`
- `runner/test.runner.yaml`
- `runner/test_runner.yaml`

The two test files are similar but use different paths, ports, concurrency, and allowlists. Rename them according to their actual consumers or remove the obsolete one. Add comments or Makefile references indicating which file is authoritative.

### 25. Reduce stringly typed task and session states

Status values such as `pending`, `running`, `done`, `error`, `cancelled`, `recoverable`, and `paused` are distributed throughout service, runner, SQL, and UI code.

Introduce typed Go constants and centralized transition validation. The database and protobuf can remain strings, but domain logic should not depend on ad hoc literals. This is particularly valuable before decomposing reaper and runner-event logic.

## Testing and dependency health

### Check results at review time

| Check | Result |
|---|---|
| `make test` | Pass |
| `make vet` | Pass |
| `make lint` | Pass |
| `cd runner && make check` | **Fail:** Pi environment-dependent test |
| `cd web && npm run check` | Pass |
| Selected root tests with `-race` | Pass |
| Runner tests with `-race` | Same Pi failure |
| Svelte autofixer | Multiple routing/reactivity issues |
| `npm audit --omit=dev` | **2 high-severity vulnerabilities** |

The npm vulnerability is in PostCSS through Tailwind:

```text
GHSA-r28c-9q8g-f849
Path traversal / arbitrary source-map file disclosure
```

Evaluate `npm audit fix`, update the relevant Tailwind/PostCSS packages, run the frontend checks, and rebuild the embedded frontend.

### Coverage weaknesses

Notable package coverage:

- `internal/data`: 8.9%.
- `internal/webhook`: 18.0%.
- `internal/store`: 23.4%.
- `internal/metrics`: 29.3%.
- Runner controller: 33.1%.
- PostgreSQL generated repository: 0%.
- Runner MCP config package: 0%.

Generated package coverage itself is not important, but PostgreSQL behavior, data-facade conversion, webhook event permutations, and runner lifecycle transitions need stronger behavioral coverage.

### CI recommendations

Add explicit jobs for:

1. MySQL.
2. PostgreSQL.
3. TiDB when practical.
4. Root `-race` on non-integration packages.
5. Runner `-race`.
6. `npm run check`.
7. `npm audit`.
8. Svelte autofixer or equivalent lint rules.
9. Generated-code freshness: `make generate` followed by `git diff --exit-code`.

Database integration tests can skip when Docker is unavailable, which means CI may report green without exercising database behavior. Make test execution versus intentional skipping visible in CI summaries.

## Duplication that should remain

Some apparent duplication is intentional and should not be hand-refactored:

- `internal/repository/` and `internal/repositorypostgres/` are generated.
- MySQL and PostgreSQL query files must remain dialect-specific.
- `internal/webui/dist/` is intentionally tracked for Go embedding.
- Local binaries and build directories are correctly ignored.
- Dialect differences should not be hidden behind overly generic SQL builders.

Refactoring should focus on maintained source and generated adapters, not hand-editing generated sqlc or protobuf output.

## Proposed implementation order

### Phase 1: correctness and security

1. Sanitize Markdown.
2. Validate runner extra-file paths.
3. Fix Pi model/provider precedence and isolate tests from environment.
4. Upgrade PostCSS/Tailwind dependencies.
5. Authenticate and narrow task MCP listeners.
6. Decide and enforce PostgreSQL upgrade behavior.

### Phase 2: safe cleanup

1. Remove `runner/internal/tools`.
2. Remove `runner/internal/executil`.
3. Remove production-only test helpers and stale placeholders.
4. Consolidate harness output and secret helpers.
5. Clarify duplicate runner YAML files.

### Phase 3: architecture

1. Replace reflection-based data conversion.
2. Introduce a canonical task domain model.
3. Decompose `recordTaskEvent`.
4. Decompose `runTask`.
5. Make service lifecycle managed and idempotent.
6. Generalize schema-upgrade helpers.

### Phase 4: frontend maintainability

1. Extract task-detail components.
2. Build shared query/filter/pagination state.
3. Resolve Svelte autofixer findings.
4. Replace raw buttons with Flowbite components.
5. Gradually migrate legacy stores where beneficial.

## Recommended first change set

The first implementation change set should remain focused and independently reviewable:

1. Markdown sanitization and security tests.
2. Extra-file path containment and tests.
3. Pi provider/model precedence and deterministic tests.
4. PostCSS/Tailwind dependency audit fix.
5. Confirmed dead package removal.

Larger architecture changes should follow as separate pull requests to keep regression risk and review scope manageable.
