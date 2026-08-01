# GitHub Multi-Installation Support Plan

## Summary

Chetter currently binds all GitHub App operations to one statically configured
`GITHUB_INSTALLATION_ID`. That works while every automated repository belongs
to one GitHub App installation, but it cannot support repositories owned by
different accounts, such as `flatout-works/chetter` and `gokr/buddydrive`, at
the same time.

The target design treats the GitHub App as the configured identity and resolves
the correct installation from repository or webhook context. Webhook-triggered
work uses the signed payload's `installation.id`. Manual and scheduled work
resolves an installation through GitHub's repository installation endpoint.
Installation access tokens are cached separately and are never persisted in
task or session data.

This work also closes a related credential gap. Webhook handlers currently add
an installation token to task environment input, but task persistence redacts
that value and runners replace it with their static `GITHUB_TOKEN`. The final
design uses an execution-fenced credential broker so private clones, read-only
`gh` commands, and Git pushes can obtain short-lived credentials without
storing them in the database.

## Goals

- Support one GitHub App installed on multiple organizations and user accounts.
- Route every GitHub API operation through the installation authorized for the
  target repository.
- Keep installation tokens out of persistent task, session, event, and audit
  payloads.
- Support long-running tasks whose first installation token expires before a
  later Git push or `gh` read.
- Bind task-created GitHub artifacts to the task's authorized repository.
- Preserve existing `flatout-works` automation during a staged rollout.
- Work with TiDB/MySQL and PostgreSQL.

## Non-Goals

- Supporting multiple GitHub Apps with different App IDs and private keys.
- Turning the definitions repository into a secret store.
- Allowing tasks to select arbitrary installation IDs.
- Replacing the GitHub App with user OAuth credentials.
- Expanding the set of direct GitHub writes allowed through the `gh` wrapper.

## Current State

### Static installation

`internal/config.Config` has one `GitHubInstallationID`, loaded from
`GITHUB_INSTALLATION_ID`. `GitHubAppConfigured` requires this ID, and `main.go`
creates installation-bound clients for both the service and webhook handler.

`internal/webhook.Client` contains one installation ID and one token cache.
Every installation token exchange uses:

```text
POST /app/installations/{configured-installation-id}/access_tokens
```

### Webhook installation is discarded

GitHub includes `installation.id` in supported webhook payloads, but the event
types in `internal/webhook/events.go` do not model it. JSON unmarshalling
therefore discards it for:

- `pull_request`
- `issues`
- `issue_comment`
- `pull_request_review`
- `pull_request_review_comment`

### Runner token mismatch

Webhook task construction sets `GITHUB_TOKEN`, but `sanitizeTaskEnv` redacts
secret-looking environment values before persistence. The runner also reserves
`GITHUB_TOKEN` as runner-owned and replaces task-provided values with its
static process environment. The installation token created for a webhook task
does not reach the task container.

### Artifact authorization gap

Runner GitHub RPC methods accept a repository string from the task and execute
the operation with the global installation client. They verify task/execution
hierarchy for signatures and artifact records, but they do not bind the
requested repository to immutable task repository metadata or select an
installation per repository.

## Design

### GitHub App manager

Introduce a single process-wide GitHub App manager responsible for:

- parsing App credentials once;
- generating App JWTs;
- resolving a repository to an installation;
- resolving an event installation ID to an installation client;
- issuing installation access tokens;
- caching tokens independently by installation, repository, and permission
  profile;
- caching repository-to-installation mappings with a bounded TTL;
- caching the App bot login, which is App-scoped rather than
  installation-scoped;
- invalidating a cached token and retrying once after an authentication
  failure;
- exposing a configurable GitHub API base URL for tests.

The manager must never mutate an installation ID on a shared client. A token
cache associated with one installation must not be reusable by another
installation.

Cache concurrency uses per-key singleflight (one in-flight token exchange per
cache key), not a single global mutex around the network call: a refresh for
installation A must not stall installations B and C. Caches are bounded in
size (LRU) so unexpected signed installation IDs cannot grow memory without
limit.

Recommended API shape:

```go
type Manager struct {
    // App credentials, HTTP client, bounded caches.
}

func (m *Manager) ClientForInstallation(ctx context.Context, installationID int64) (*Client, error)
func (m *Manager) ClientForRepo(ctx context.Context, repo string) (*Client, error)
func (m *Manager) CredentialForRepo(ctx context.Context, repo string, profile PermissionProfile) (Credential, error)
func (m *Manager) AppLogin(ctx context.Context) (string, error)
```

The exact package can remain under `internal/webhook` initially, but a neutral
package such as `internal/githubapp` better reflects that the manager serves
webhooks, runner RPCs, definition proposals, and task credentials.

### Repository-restricted tokens

When GitHub permits it, installation token creation should request access only
to the task's target repository. Token cache keys must include:

```text
installation_id + repository + permission_profile
```

Initial permission profiles should cover:

| Profile | Purpose |
|---|---|
| `webhook` | Repository metadata, collaborator checks, issues, PRs, labels, comments |
| `artifact-write` | Create issues, comments, PRs, and reviews through server RPCs |
| `task-git` | Clone and push repository contents plus read-only issue/PR inspection |
| `definition-proposal` | Branch creation, contents updates, PR creation, checks read |

If GitHub rejects a requested permission that the installation does not have,
return a clear capability error rather than silently requesting broader
permissions.

Installation tokens are valid for roughly one hour, and every token exchange
costs a GitHub API call. Reuse cached tokens across tasks that share the same
installation, repository, and profile so a burst of tasks for one repository
does not trigger secondary rate limits. Repository restriction is a hardening
measure; if it complicates Phase 1 materially, land per-installation caching
first and add repository/profile restriction with the broker in Phase 5.

### Installation selection

Use the following precedence:

1. Signed webhook event: use `installation.id` from the payload.
2. Task with pinned installation metadata: use the pinned installation.
3. Repository known but installation unknown: resolve through
   `GET /repos/{owner}/{repo}/installation` using an App JWT, then pin the
   result where appropriate.
4. Public repository without an installation: allow anonymous clone/read where
   possible, but reject GitHub writes with a clear error.

Do not silently use a default installation for a webhook from another account.

### Fork pull requests

For `pr_review` tasks, `git_url` is the PR **head** clone URL, which points at
a fork for fork PRs, while every GitHub API operation (review, labels,
comments, write-access checks) targets the **base** repository. Therefore:

- `github_repo` on a webhook-submitted task is always the webhook payload's
  repository (the base repo), never derived from the head clone URL.
- Repository-restricted credentials are requested for the base repository.
- Fork head clones cannot use the App (it is not installed on the fork): they
  proceed anonymously for public forks, or via the static runner-token
  fallback. This fallback is acceptable because the fork head is read-only for
  the agent; all writes happen server-side against the base repo.

### Task repository metadata

Add immutable metadata to `chetter_tasks`:

```text
github_repo VARCHAR/TEXT NULL
github_installation_id BIGINT NULL
```

Webhook submissions populate both values. Manual and cron submissions derive
`github_repo` from `git_url`; installation resolution may remain lazy until a
credential or server-side GitHub operation is required.

The server, not task input, owns `github_installation_id`. It must not be a
normal user-supplied MCP field.

Repository identity should be normalized to lowercase `owner/repo` for
comparison while preserving the canonical value for display and API calls.
Support HTTPS and SSH GitHub URLs using one shared parser rather than the
current duplicated helpers.

Lazy installation resolution may race when several executions of the same task
(or resumable-session prompts) resolve concurrently. Pinning is idempotent:
an `UPDATE ... WHERE github_installation_id IS NULL` write wins; a concurrent
second write must compare the existing value and fail loudly only on an actual
conflict.

### Webhook routing

Add a shared installation payload type to `internal/webhook/events.go` and
include it in every supported top-level event structure.

Each event handler resolves its installation client once and threads that
client through all related operations, including:

- write-access checks;
- PR lookup;
- acknowledgement comments;
- label reads and writes;
- failure comments;
- task submission metadata;
- session feedback resume handling where GitHub API access is needed.

Missing or zero installation IDs on live supported events should fail closed
and mark durable deliveries failed for retry/dead-letter processing. Test
fixtures must include installation IDs explicitly.

### Server-side artifact operations

Replace the global `GitHubClient() *webhook.Client` contract used by
`runner_github_rpc.go` with repository-aware resolution.

Before an artifact operation, the server must:

1. authenticate the runner RPC;
2. validate task and execution hierarchy;
3. validate that the execution is active and leased to the calling runner;
4. load the task's repository and installation metadata;
5. require the requested repository to match the task repository;
6. resolve a repository-bound installation client;
7. perform the GitHub operation;
8. record the artifact and audit metadata.

Tasks intentionally operating without a cloned repository must receive an
explicit server-owned `github_repo` at submission time before artifact writes
are allowed. This avoids retaining the current ability to target any repository
accessible to the App.

Definition proposals resolve their installation from the definition source
repository rather than from a default installation.

### Execution-fenced credential broker

Do not persist access tokens in task environment JSON. Add a runner RPC that
returns a current repository-restricted installation credential only after
validating active execution ownership.

Proposed protocol:

```protobuf
message GetGitHubCredentialRequest {
  string runner_id = 1;
  string task_id = 2;
  string execution_id = 3;
  string repo = 4;
}

message GetGitHubCredentialResponse {
  string token = 1;
  string expires_at = 2;
}
```

The server must verify:

- task and execution IDs match;
- the execution is active;
- the lease belongs to `runner_id`;
- the repository matches task metadata;
- the requested installation matches the task's pinned or resolved
  installation.

The runner uses the broker in three places:

1. Before cloning a private GitHub repository.
2. Through a dynamic Git askpass helper for later fetches and pushes.
3. Through the existing `gh` wrapper before allowed read-only commands.

The helper must refresh credentials as needed so tasks longer than one hour do
not fail near completion. The token must not appear in command-line arguments,
logs, events, checkpoints, or workspace files.

#### Broker transport inside the task container

The askpass script and `gh` wrapper execute **inside** the task container,
which does not and must not hold the runner ConnectRPC token (and under gVisor
must not need a direct control-plane connection). The historical Unix-socket
MCP bridge was removed before this work and is not portable to Kubernetes PVCs
or the current gVisor transport. The broker therefore uses a private, non-MCP
endpoint on the existing per-execution runner HTTP server:

1. The runner creates a random 256-bit bearer capability for the execution.
2. The askpass/`gh` helper calls `POST /internal/github-credential` with that
   capability. The endpoint accepts no task-controlled identity fields.
3. The runner attaches the task, execution, runner, and repository identities
   captured by the endpoint closure.
4. The runner calls the server's `GetGitHubCredential` ConnectRPC and returns
   only the current token to the helper with `Cache-Control: no-store`.

The no-secret askpass helper is written by the runner into the workspace. The
capability is supplied through runner-managed environment variables, never in
the endpoint URL or workspace file. The Git credential username for
installation tokens remains `x-access-token`.

The static runner `GITHUB_TOKEN` remains a temporary compatibility fallback
for tasks that cannot resolve a GitHub App installation (including fork head
clones). App credentials take precedence for base-repository Git operations
with a known installation.

## Implementation Phases

### Sequencing and dependencies

The phases below are not strictly ordered. Three constraints matter:

- **Phase 3 (schema columns) is independent and should land first or alongside
  Phase 1.** It is additive, nullable, and behavior-preserving, and Phase 2
  step 5 (persisting task metadata) depends on it.
- **Phase 1 is a behavior-preserving refactor** (single installation,
  same call sites) and can ship alone with its tests.
- Phase 2 splits naturally: 2a (parse installation, route clients) ships after
  Phase 1; 2b (persist task metadata) ships after Phase 3.

### Phase 1: App manager and configuration split

1. Add the process-wide App manager and keyed token caches.
2. Refactor GitHub API request creation to obtain credentials from the manager.
3. Split configuration predicates into App credentials and webhook readiness.
4. Make `GITHUB_INSTALLATION_ID` an optional legacy fallback.
5. Wire one manager instance into the service and webhook handler.
6. Add manager tests using a fake GitHub API server.

Primary files:

- `internal/config/config.go`
- `internal/config/config_test.go`
- `internal/webhook/github.go`
- `internal/webhook/github_test.go`
- `main.go`
- `main_test.go`

### Phase 2: Webhook installation routing

1. Parse `installation.id` for every supported event.
2. Resolve one installation client per event.
3. Thread the client through all helper functions.
4. Remove webhook installation tokens from submitted task environment maps.
5. Attach repository and installation metadata to task submissions.
6. Include installation metadata in webhook delivery/audit diagnostics.
7. Verify retries reselect the same installation from retained payloads.

Primary files:

- `internal/webhook/events.go`
- `internal/webhook/handler.go`
- `internal/webhook/submitter.go`
- `internal/webhook/handler_test.go`
- `internal/webhook/handler_audit_test.go`
- `internal/service/webhook_deliveries.go`

### Phase 3: Persistent task repository identity

1. Add `github_repo` and `github_installation_id` to startup schemas.
2. Add zero-downtime ensure-column handling for existing deployments.
3. Add MySQL/TiDB and PostgreSQL Goose migrations.
4. Update dual-dialect task queries.
5. Run sqlc and regenerate the data facade.
6. Add metadata to internal task submission and retrieval models.
7. Add shared GitHub repository URL parsing and normalization.
8. Add dialect integration tests.

Required schema order:

1. Update `internal/store/schema.go` and PostgreSQL schema equivalents.
2. Update ensure-column migration logic.
3. Add migrations under both migration trees.
4. Update queries for both dialects.
5. Run `make generate`.
6. Run `cd internal/data && go run ./cmd/genfacade` if generated facade
   signatures change.

Primary files:

- `internal/store/schema.go`
- `internal/store/schema_postgres.go`
- `internal/store/store.go`
- `db/migrations/*.sql`
- `db/postgres/migrations/*.sql`
- `db/queries/tasks.sql`
- `db/postgres/queries/tasks.sql`
- generated repository packages
- `internal/data/queries_gen.go`
- `internal/service/service.go`
- `internal/service/tools.go`

### Phase 4: Repository-bound server operations

1. Resolve artifact clients by task repository and installation.
2. **Audit existing triggers first.** Every enabled trigger that calls artifact
   tools must target the repository it clones (or declare an explicit
   `github_repo`). Current chetter-config triggers are consistent, but this
   check must be repeated against live data before enforcement.
3. Enforce requested-repository equality behind a warn-only rollout flag
   (log/audit mismatches without rejecting) for one release, then enforce.
4. Enforce active execution lease and runner ownership.
5. Route definition proposal operations by source repository.
6. Record installation ID in artifact/audit diagnostics.
7. Add tests for cross-repository and stale-execution rejection.

Primary files:

- `internal/service/runner_github_rpc.go`
- `internal/service/github_tools.go`
- `internal/service/definition_proposal_tools.go`
- `internal/service/service.go`
- related integration tests

### Phase 5: Credential broker and runner integration

1. Add the credential RPC to `proto/runner/v1/runner.proto`.
2. Regenerate protobuf and ConnectRPC code.
3. Implement server-side execution fencing and credential issuance.
4. Fetch credentials for GitHub clone operations.
5. Replace static-token-only askpass behavior with a refreshable no-secret
   helper calling the authenticated, non-MCP endpoint on the per-task HTTP
   server.
6. Update the `gh` wrapper to obtain a current token before allowed reads.
7. Ensure task containers receive no persisted installation token.
8. **Update chetter-config prompts.** Global agent definitions and trigger
   prompts currently promise `$GITHUB_TOKEN` in the task environment; revise
   them to describe broker-provided credentials once the broker ships.
9. Add Docker, local, and Kubernetes execution-path tests.

Primary files:

- `proto/runner/v1/runner.proto`
- generated protobuf code
- `internal/service/runner_rpc.go`
- `runner/internal/controller/runner_rpc.go`
- `runner/internal/controller/runner_task.go`
- `runner/internal/controller/docker_args.go`
- `runner/internal/controller/kubernetes_executor.go`
- `runner/internal/agentenv/environment.go`
- `runner/scripts/gh`
- agent base image packaging

### Phase 6: Documentation and compatibility cleanup

1. Document installing the same App on multiple accounts.
2. Document repository and permission requirements.
3. Remove the `GITHUB_INSTALLATION_ID` requirement entirely. After Phase 4
   there are no repository-less operations left that need it: the App manager
   resolves installations from repositories or webhook payloads, and
   `GetAppLogin` uses only the App JWT. Accept-but-ignore the variable for
   one release with a startup deprecation warning, then delete it.
4. Document static runner-token fallback and removal procedure.
5. Update deployment examples.
6. After a stable rollout, remove the static GitHub token from runners.

Primary files:

- `docs/MANUAL.md`
- `docs/REVIEWS.md`
- `docs/EKS.md`
- `AGENTS.md`
- `compose.yaml`
- `deploy/compose.yaml`

## Testing Strategy

### App manager tests

- Installation 111 exchanges at `/app/installations/111/access_tokens`.
- Installation 222 exchanges at `/app/installations/222/access_tokens`.
- Tokens are cached independently.
- Concurrent requests do not cross-contaminate tokens.
- Repository discovery returns and caches the correct installation.
- Repository-restricted token payloads contain the expected repository and
  permissions.
- Near-expiry credentials refresh.
- A 401 invalidates and retries once.
- Unknown or inaccessible repositories fail clearly.

### Webhook tests

- Every supported event parses `installation.id`.
- Missing and zero IDs fail according to policy.
- PR, issue, and comment handlers use the event installation for every API
  operation.
- Submitted tasks contain repository and installation metadata but no token.
- Durable delivery retry selects the original installation.
- Bot-login filtering remains App-wide and works across installations.

### Task and database tests

- Repository and installation metadata persist in MySQL/TiDB and PostgreSQL.
- Rerun, recovery, resume, and callback-created tasks preserve or correctly
  resolve repository metadata.
- Legacy task rows with null metadata remain readable.
- Repository URL parsing handles HTTPS, SSH, `.git`, and invalid/non-GitHub
  URLs.

### Artifact security tests

- Correct task repository succeeds.
- Another repository on the same installation is rejected.
- Another installation is rejected.
- Mismatched task and execution IDs are rejected.
- Stale execution attempts are rejected.
- A runner that does not own the lease is rejected.
- Successful remote creation records artifact and audit metadata.

### Runner credential tests

- Private clone receives a brokered credential.
- Public clone without installation can proceed anonymously.
- Askpass refreshes an expired credential before push.
- Allowed `gh` reads receive a current credential.
- Blocked `gh` writes remain blocked.
- Tokens never appear in persisted task environment, process diagnostics,
  events, or session exports.
- Docker, local, and Kubernetes modes apply the same rules.

### Verification commands

```bash
make generate
cd internal/data && go run ./cmd/genfacade
make check
cd runner && make check
```

Database integration tests should run against TiDB/MySQL and PostgreSQL as
described in `AGENTS.md`.

## Rollout

1. Install `chetterbot` on the `gokr` account with access to `buddydrive` and
   `buddydrive-relay` and the required contents, issues, pull request, and
   metadata permissions.
2. Record the new installation ID operationally for diagnostics, but do not add
   it as another static Chetter environment variable.
3. Deploy Phase 3 (columns) and Phase 1 (manager refactor) independently; both
   are behavior-preserving.
4. Deploy Phase 2 (webhook routing + metadata persistence) and Phase 4 with
   repository-match enforcement in warn-only mode; audit live triggers for
   mismatches, fix any, then switch to enforce.
5. Verify one PR review and one issue-triggered task under both
   `flatout-works` and `gokr`, including one fork PR if available.
6. Deploy the credential broker and updated chetter-config prompts.
7. Verify private clone, branch push, issue comment, PR creation, and PR review.
8. Remove static `GITHUB_TOKEN` from runners after all GitHub task paths use the
   broker.
9. Remove `GITHUB_INSTALLATION_ID` from configuration and docs after the
   deprecation window.

## Security Invariants

- A webhook operation uses only the installation ID from its signed payload.
- A repository-discovered installation is verified through the App JWT.
- Installation tokens are never persisted.
- Token caches are isolated by installation and repository.
- Task artifact operations cannot target a repository unrelated to the task.
- Credential issuance requires an active execution lease owned by the caller.
- GitHub writes remain mediated by Chetter artifact tools.
- Logs and error messages must redact GitHub credentials and authenticated
  clone URLs.

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Token from one installation is reused for another | Immutable per-installation cache keys and concurrency tests |
| Long task token expires before push | On-demand execution-fenced credential broker |
| Webhook fixture lacks installation ID | Update all fixtures and fail closed in production handlers |
| Existing tasks have no repository metadata | Nullable columns and explicit legacy resolution path |
| App lacks a requested permission | Capability-specific errors and documented App permissions |
| Repository mapping changes after App installation update | Bounded mapping TTL and invalidation on authentication/not-found errors |
| Cross-repository task writes | Persist repository identity and enforce it in runner RPC handlers |
| Fork PR pinned to fork clone URL breaks artifact ops | Pin `github_repo` from the webhook payload repository, never from head clone URL |
| Task container lacks a safe broker transport | Use an authenticated non-MCP endpoint on the per-execution HTTP bridge; never give containers the runner RPC token |
| Phase 4 rejects a legitimate existing automation | Warn-only rollout mode plus a live trigger audit before enforcement |
| Token-exchange burst hits GitHub rate limits | Per-key singleflight and cross-task token reuse by installation/repo/profile |
| Deployment regression for `flatout-works` | Staged rollout with legacy static-token fallback |

## Acceptance Criteria

- One Chetter instance handles GitHub repositories owned by both
  `flatout-works` and `gokr`.
- Webhook API calls always use the payload's installation.
- Manual and cron tasks discover installation by repository when needed.
- Tokens from one installation are never used against another installation.
- GitHub installation tokens do not appear in persistent storage.
- Long-running tasks can refresh credentials before Git operations.
- Runner artifact RPCs cannot target repositories unrelated to their task.
- Definition proposals use the installation for their source repository.
- Existing `flatout-works/chetter` automation continues to work throughout the
  migration.
- Root and runner checks pass for both supported database dialects.
