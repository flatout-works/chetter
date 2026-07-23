---
artifact_contract: ce-unified-plan/v1
artifact_readiness: implementation-ready
execution: operations
product_contract_source: "operator request to separate Chetter from flatoutdev"
title: "ops: move Chetter into its own logical database"
date: 2026-07-23
---

# Move Chetter Into Its Own Logical Database

## Goal

Move all Chetter state out of the shared `flatoutdev` database into a new
logical database named `chetter`, without copying unrelated Flatout tables.

The recommended approach is a rehearsed, quiesced logical export/import and a
short maintenance-window cutover. Chetter has no dual-write or change-capture
mechanism, so an online copy would risk losing task, event, token, or trigger
writes.

## Current Baseline

- `DATABASE_DSN` selects the database. Runtime SQL uses unqualified table names,
  so changing the DSN database name is sufficient; no query qualification
  change is required (`internal/config/config.go`, `internal/store/store.go`).
- The Kubernetes example already uses `/chetter`, but the production Secret or
  external secret is the source of truth and must be inspected separately
  (`deploy/k8s/secrets.yaml`). No `flatoutdev` reference is committed in this
  repository.
- Startup runs `ApplySchema`, which creates the current schema and performs
  compatibility repairs (`main.go:57-70`, `internal/store/store.go:348-415`).
- Goose migrations are historical and must be run on a fresh destination before
  starting Chetter. Starting Chetter first can create latest-schema objects and
  make older migrations fail on duplicate columns or tables.
- Paused and recoverable sessions store workspace/checkpoint paths in the
  database, but the actual workspace and checkpoint bytes live on runner-local
  storage. The database move alone does not move those bytes.

## Destination Allowlist

The new database must contain exactly these Chetter application tables:

### Execution and Runtime

- `chetter_tasks`
- `chetter_agent_sessions`
- `chetter_user_prompts`
- `chetter_execution_attempts`
- `chetter_agent_session_checkpoints`
- `chetter_task_events`
- `chetter_runners`
- `chetter_triggers`
- `chetter_trigger_runs`
- `chetter_event_callbacks`

### Authentication and Ownership

- `teams`
- `users`
- `api_tokens`
- `user_team_memberships`
- `api_token_teams`
- `git_identities`

### Definitions, Audit, and Artifacts

- `chetter_model_catalogs`
- `definition_sources`
- `definitions`
- `definition_sync_runs`
- `definition_change_proposals`
- `chetter_audit_log`
- `chetter_task_artifacts`

`goose_db_version` is migration metadata, not application data. It may exist in
the destination if Goose is used to initialize the schema and should not be
treated as a Flatout table.

Do not copy the entire database, wildcard all tables, or import views,
routines, events, or triggers from `flatoutdev`. The legacy names
`chetter_schedules`, `chetter_schedule_runs`, and `chetter_session_runs` are
not current runtime tables. If any contain rows, stop and reconcile the source
migration state before exporting; do not silently discard those rows.

## Decisions and Preconditions

1. Confirm the production engine and dialect before execution. The checked-in
   deployment examples target TiDB/MySQL, while the server also supports
   PostgreSQL. Use the matching migration directory and client for the actual
   engine.
2. Use `chetter` as the destination database name. Do not rename the tables or
   add cross-database qualifiers.
3. Pre-create the destination with an operations/admin account. The Chetter
   runtime account should not require permission to create databases.
4. Initially grant the runtime account access only to `chetter.*`. Because the
   current startup compatibility path can issue `CREATE`, `ALTER`, and index
   statements, retain schema DDL privileges scoped to `chetter.*` until that
   path is separated into a migration-only job.
5. Take a provider-native snapshot or full backup of the source before any
   migration work. Also retain the selected Chetter export, its checksum, and
   the exact table allowlist used.
6. Decide how to handle tasks that are still `pending`, `running`, or
   `resuming`. The default is to let running work finish, stop new claims, and
   preserve pending rows for the destination queue. Do not cut over with active
   writes.
7. Inventory paused/recoverable sessions. Keep the same runner hosts and
   persistent runner-data volumes, or separately copy and validate every
   referenced workspace/checkpoint before allowing resume after cutover.

## Implementation Phases

### Phase 0: Discover and Rehearse

Record the following using a privileged, audited connection. Never put a real
password in command history, this plan, or a ticket.

- Actual source host, engine, database name, server version, timezone, charset,
  and `CHETTER_DB_DIALECT`.
- The production source of `DATABASE_DSN`: Kubernetes Secret, ExternalSecret,
  Arcane/GitOps value, Compose environment, or another deployment system.
- Database users, grants, and whether any Flatout service also uses the
  Chetter tables or the shared `goose_db_version` table.
- All tables in `flatoutdev`, including table type, row count, and size.
- Views, routines, events, and triggers associated with `flatoutdev`.
- Goose status and schema drift against the current checkout.

For TiDB/MySQL, the inventory should include queries equivalent to:

```sql
SELECT DATABASE(), VERSION();

SELECT table_name, table_type, table_rows, data_length, index_length
FROM information_schema.tables
WHERE table_schema = 'flatoutdev'
ORDER BY table_name;

SELECT trigger_name, event_object_table
FROM information_schema.triggers
WHERE trigger_schema = 'flatoutdev';
```

Run the Chetter migration status against the source and record the result:

```bash
make migrate-status DB_DSN="$SOURCE_DSN" CHETTER_DB_DIALECT="$DIALECT"
```

The source must be at the current migration head and must not have pending
legacy migrations. If it is behind or contains legacy tables, take the backup,
obtain a DBA review, and apply the approved migrations before the export. Do
not use an unreviewed `ApplySchema` startup as a substitute for reconciling
migration history.

Create a disposable rehearsal database, initialize it with the current
migrations, import a selected-table export, start a temporary Chetter instance
against it, and exercise the validation checklist below. Measure dump/import
duration and destination size so the production maintenance window is known.

### Phase 1: Prepare `chetter`

1. Create the logical database with the same engine-compatible charset,
   collation, timezone, and server settings required by the source.
2. Create a dedicated Chetter database account. Grant it access only to
   `chetter.*`; explicitly verify it has no privileges on `flatoutdev` or other
   Flatout databases.
3. Initialize the empty destination with the current dialect migrations:

   ```bash
   make migrate DB_DSN="$DEST_DSN" CHETTER_DB_DIALECT="$DIALECT"
   make migrate-status DB_DSN="$DEST_DSN" CHETTER_DB_DIALECT="$DIALECT"
   ```

   Do this before any Chetter server starts. The destination must have the
   expected `goose_db_version` state and the current schema, including all
   compatibility columns and indexes.
4. Do not import the source schema wholesale. The destination schema comes
   from the repository's dialect-specific migrations; the export below carries
   only data for the allowlisted tables.

For PostgreSQL, use the same sequence with `db/postgres/migrations` and
`pg_dump`/`pg_restore`; use the database name `chetter` rather than treating a
PostgreSQL schema and database as interchangeable.

### Phase 2: Quiesce and Export

Use a maintenance window with external task submission and webhook delivery
controlled. The order is important:

1. Announce the window and stop or disable cron, issue, and PR-review trigger
   sources. Preserve their current enabled state for restoration after the
   cutover.
2. Stop new runner claims using the runner drain operation. Wait until all
   active executions have completed or have been explicitly handled. Confirm
   there are no unexpected `running` attempts and that runner-local files for
   paused sessions are preserved.
3. Stop the Chetter MCP/Web/API deployment after runners are drained. Keep the
   old database available but do not allow either application or maintenance
   jobs to write to it.
4. Capture final source counts and status counts. A final export must be made
   after the write freeze, even if a rehearsal export was already successful.
5. Export data from the explicit allowlist only. For TiDB/MySQL, the shape is:

   ```bash
   mysqldump \
     --single-transaction --quick --skip-lock-tables \
     --no-create-info --skip-triggers --complete-insert --hex-blob \
     --set-gtid-purged=OFF \
     flatoutdev \
     chetter_tasks chetter_agent_sessions chetter_user_prompts \
     chetter_execution_attempts chetter_agent_session_checkpoints \
     chetter_task_events chetter_runners chetter_triggers \
     chetter_trigger_runs chetter_event_callbacks teams users api_tokens \
     user_team_memberships api_token_teams git_identities chetter_model_catalogs \
     definition_sources definitions definition_sync_runs \
     definition_change_proposals chetter_audit_log chetter_task_artifacts \
     > chetter-data.sql
   ```

   Inspect the dump before import. It must contain inserts only for the
   allowlist and must not select or switch back to `flatoutdev`. If the client
   emits a database `USE` statement, remove it using a reviewed, deterministic
   transformation or use a client option that targets the destination; do not
   rely on an interactive session's current database.
6. Encrypt and checksum the export. Store the source snapshot, export, import
   log, source/destination counts, and operator identity according to the
   existing retention policy.

For PostgreSQL, produce a data-only custom-format dump with one `--table`
selector per allowlisted table and restore it into `chetter` with
`--data-only --exit-on-error`.

### Phase 3: Import and Cut Over

1. Import the data-only export into the already-migrated `chetter` database.
   Abort on the first error; do not use a best-effort import.
2. Run the row-count, range, checksum, and relationship checks in Phase 4.
   Resolve every mismatch before starting production traffic.
3. Update the real deployment secret or external secret so `DATABASE_DSN`
   points to the same host with `/chetter` as the selected database. Set
   `CHETTER_DB_DIALECT` explicitly when auto-detection is not sufficient.
4. Roll out the MCP deployment and verify `DATABASE()`/equivalent reports
   `chetter`. Confirm startup schema checks succeed and no process logs an
   `Unknown database`, missing table, or migration error.
5. Keep triggers disabled and runners stopped while the MCP/API service is
   smoke-tested. Then start the runners with their existing persistent data
   volumes and verify registration, heartbeat, claim, event, and terminal
   result behavior.
6. Restore trigger enabled states and reopen webhook/task ingress in a
   controlled order. Watch for duplicate trigger runs and webhook retries.

### Phase 4: Validation and Acceptance

The cutover is successful only when all of the following hold:

- The destination contains exactly the 23 allowlisted application tables plus
  migration metadata, and no unrelated Flatout table, view, routine, event, or
  trigger was imported.
- Every allowlisted table has matching source/destination row counts. For each
  table also compare primary-key or timestamp ranges and a deterministic
  checksum or chunked row hash for large tables.
- Relationship checks report no new orphans, including task to session/prompt,
  prompt to execution attempt, session to checkpoint, task to event/artifact,
  trigger to trigger-run, definition to source, proposal to source/task, and
  token/membership to user/team relationships.
- Counts match for pending/running/terminal tasks, active triggers, runners,
  paused/recoverable sessions, checkpoints, API tokens, teams, active model
  catalogs, definitions, audit events, and artifacts.
- Token authentication works with an existing non-admin token and the admin
  token. Team scoping returns the same results as before the move.
- The web/API health checks pass, existing task history and event history load,
  the scheduler loads, and a controlled task completes end to end.
- A runner registers with the expected identity, claims a task, renews its
  lease, reports events, and publishes a terminal result.
- A controlled resumable-session check passes, or every paused/recoverable
  session has been explicitly accepted as unavailable and repaired/recovered.
- Database grants show the Chetter account can access `chetter` but cannot
  access `flatoutdev`.
- After cutover, source-side Chetter tables show no writes. Application logs,
  audit events, and database monitoring show the destination receiving all
  expected activity.

### Phase 5: Retire Shared Tables

Do not drop anything from `flatoutdev` during the initial cutover. Keep the
source database read-only and retain the snapshot for the agreed rollback
window. Monitor for old-client access and unexpected source writes.

After the rollback window and business-owner approval:

1. Re-run the database inventory and verify no Flatout service uses any Chetter
   table.
2. Revoke the Chetter account's `flatoutdev` privileges if not already done.
3. Drop only the allowlisted Chetter application tables from `flatoutdev`, if
   retention and compliance requirements allow it. Treat legacy tables and the
   shared `goose_db_version` table separately; do not drop either without
   confirming ownership.
4. Retain the encrypted export/snapshot and a record of the final source table
   inventory.

## Rollback

Before destination traffic starts, rollback is straightforward: keep the old
deployment secret and point Chetter back to `flatoutdev`, then restore the
service and runners after verifying the source was not modified.

Once destination writes begin, rollback is not a simple DSN reversal because
the two databases can diverge. Define the rollback boundary before the window:

- Before destination writes: revert the secret and restart from the source.
- After destination writes: stop traffic, preserve the destination, and either
  repair/retry the destination cutover or restore/reconcile from the destination
  backup and an explicitly recorded post-cutover activity window.
- Never write to both databases opportunistically or switch back and forth
  without a reconciliation decision.

## Risks and Follow-ups

- **Unknown production engine:** A MySQL/TiDB dump cannot be restored with the
  PostgreSQL procedure. Confirm the engine first.
- **Schema drift:** Startup auto-repair can make the live schema newer than
  Goose history. Resolve this before export instead of losing columns or
  creating an untracked destination.
- **Large audit/session data:** Measure the rehearsal and use `--quick` or
  chunked exports. Do not extend the freeze without an operator decision.
- **Paused sessions:** Database metadata is insufficient without runner-local
  workspaces/checkpoints. Preserve volumes and runner IDs.
- **Secrets:** API token hashes and audit payloads may be sensitive. Encrypt
  exports and restrict access.
- **Runtime DDL privileges:** A follow-up hardening change should move schema
  creation/repair to a migration job and remove DDL privileges from the
  long-running application account.

## Definition of Done

- Production Chetter uses the `chetter` logical database.
- The destination contains only the allowlisted Chetter tables and migration
  metadata.
- Source and destination validation reports are complete with no unexplained
  mismatches.
- Chetter, runners, authentication, scheduling, task execution, history,
  artifacts, and resumable sessions are validated.
- The Chetter database account has no access to `flatoutdev`.
- The old Chetter tables remain available only for the approved rollback and
  retention period, then are removed through a separate reviewed operation.
