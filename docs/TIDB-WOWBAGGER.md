# TiDB on Wowbagger

Chetter's production database is a self-managed TiDB cluster on wowbagger.
It was migrated from TiDB Cloud (`flatoutdev` on `gateway01.eu-central-1`)
to a local TiDB on 2026-08-04.

## Deployment shape

A TiUP-managed single-host cluster named `chetter-tidb` (v8.5.3), deployed
with `ops/tidb-bootstrap.sh` and the topology retained at
`~/.config/chetter/chetter-tidb.yaml` on wowbagger:

- 1 PD (2379/2380), 1 TiDB server (4000/10080), 3 TiKV (20160-20162),
  Prometheus (9090), Grafana (3000)
- All processes advertise `159.195.108.207` (wowbagger's public Netcup IP)
- Hard memory caps via systemd (`resource_control`): PD 512M, TiDB 2.5G,
  each TiKV 2.5G; TiKV block cache 512MB, unified readpool 4 threads
- Deploy dir `/tidb-deploy`, data dir `/tidb-data`, service user `tidb`

This is not host-level HA: every process is on the same machine. Port 4000
and the dashboards should stay restricted to private networking.

## Credentials

- TiDB `root` password: `~/chetter-migration/.admin-password` (0600)
- App user `chetter` password: `~/chetter-migration/.app-password` (0600)
  - Granted `ALL` on `chetter.*` only (and `chetter_rehearsal.*` during the
    migration)
- Migration env file: `/tmp/chetter-tidb-migration.env` (0600) holds source
  (TiDB Cloud) and local connection details for `ops/tidb-cloud-migrate.sh`

Never put these in command arguments, shell history, or Git.

## DSN routing

Only the MCP server container carries `DATABASE_DSN`; runners and agents
reach Chetter purely over ConnectRPC and never connect to the database:

```text
DATABASE_DSN=chetter:<password>@tcp(159.195.108.207:4000)/chetter?parseTime=true
CHETTER_DB_DIALECT=tidb
```

The DSN lives in the Arcane-managed compose
(`/opt/arcane/projects/chetter/compose.yaml` on wowbagger, root-owned),
synced from `deploy/compose.yaml` in this repo by the CI
`arcane-build-deploy` job.

## Time zone: TiDB must run in UTC

Chetter stores all timestamps as UTC and computes ages with
`TIMESTAMPDIFF(..., NOW())` (runner presence window is 60s, reaper stale
detection, task heartbeat age). If TiDB's session time zone is not UTC,
every such age is inflated by the host offset and the fleet breaks in ways
that look unrelated:

- wowbagger runs `Europe/Vienna` (+02:00), so a fresh TiDB defaulting to
  `@@time_zone = SYSTEM` shifted every age by exactly 7200s
- All runners exceeded the 60s presence window, were marked stale, and
  disappeared from the web UI and fleet health, even while heartbeats were
  arriving fine
- Running/stale task detection skewed the same way

Fix applied 2026-08-04:

```sql
SET GLOBAL time_zone = '+00:00';
```

`SET GLOBAL` persists across TiDB restarts; `ops/tidb-bootstrap.sh` also
writes `time-zone: "UTC"` into `server_configs.tidb` so fresh deployments
start in UTC. The host time zone is intentionally left alone — only the
database must be UTC. Verify with:

```sql
SELECT NOW(), UTC_TIMESTAMP(), @@time_zone, @@global.time_zone;
```

`NOW()` and `UTC_TIMESTAMP()` should match.

## Startup preflight guard (issue #316)

The server no longer relies on the database default alone. Since issue #316:

- Every TiDB/MySQL connection is forced to UTC by injecting
  `time_zone='+00:00'` into the DSN (the driver runs
  `SET time_zone = '+00:00'` on connect). An explicit non-UTC `time_zone`
  in the DSN is left alone so the preflight can refuse it.
- Startup verifies the effective session time zone before serving; a
  TiDB/MySQL database that rejects or ignores the setting refuses to start
  with a clear error naming the offending host. PostgreSQL is exempt: its
  timestamps are TIMESTAMPTZ and age queries use NOW()/interval arithmetic,
  so the session zone is irrelevant for correctness — the zone is captured
  for server-info observability only.
- Dialect auto-detection is fail-closed: a failed `SELECT VERSION()` probe
  aborts startup instead of silently defaulting to TiDB.
- The verified session/global time zones are logged at startup and exposed
  via `/readyz` (HTTP 503 when a TiDB/MySQL session is not UTC) and
  `/api/server-info` (`dbSessionTimeZone`, `dbGlobalTimeZone`,
  `dbTimeZoneUTC`).

The TiDB-side pinning above remains the right thing to do — the preflight
is a second line of defense, not a replacement.

## Migration history (TiDB Cloud -> local)

Data was moved with `ops/tidb-cloud-migrate.sh` (`export`, `prepare`,
`import`, `verify`) against the 24-table application allowlist:

- `export`: data-only `mysqldump` from TiDB Cloud over TLS
  (`VERIFY_IDENTITY` with the system CA bundle). `--single-transaction` is
  not used: TiDB Cloud rejects the SAVEPOINT it implies (Error 1305), and
  cutover exports run with writes frozen.
- `prepare`: applies `db/migrations` to the empty target via a prebuilt
  `chetter-migrate` binary (wowbagger has no Go toolchain).
- `import`/`verify`: loads the dump and compares per-table row counts.

Notable gotchas from the migration:

- **Schema drift between migrations and `ensure*` upgrades.** The TiDB
  Cloud source schema evolved through the startup `ensure*` path (add
  only), so it still contained `chetter_runners.listen_subject` and
  `result_subject`, which migration 002 drops. The `PRE_IMPORT_SQL` /
  `POST_IMPORT_SQL` hooks add the two columns before import and drop them
  after, so the final schema matches the code. Any future source with
  `ensure*`-only history may need the same treatment; compare
  `INFORMATION_SCHEMA.COLUMNS` before import.
- **Row counts on a live source.** `task_events` grows constantly
  (runner heartbeats), so verify against `MAX(created_at)` of the dump, not
  the current source total. At cutover, writes are frozen and counts are
  exact.
- The final export/import landed 24 tables, ~290MB of SQL, with all row
  counts matching.

## Task claiming and the TiDB planner bug

Migration 051 (isolation admission) initially broke all task claiming:
the `SELECT ... FOR UPDATE SKIP LOCKED` claim query joins four tables, and
TiDB's planner fails with `Error 1105 ... Can't find column
chetter_agent_sessions.id` on that shape. Fixed by rewriting the admission
check as a `NOT EXISTS` subquery (commit `b3d5a0a`). When touching the
claim query, validate against a real TiDB — the integration suite does not
catch planner-level failures.

## Isolation requirement

Tasks run under gVisor; the server rejects isolation-requiring tasks
unless a runner advertises enforcement. Advertising requires `runsc` on
the runner's PATH (probe in `runner/internal/controller/isolation.go`), so
the compose bind-mounts the host binary read-only
(`/usr/bin/runsc:/usr/bin/runsc:ro`) into the runner containers, and the
host Docker daemon must have the `runsc` runtime registered (see
`docs/DEPLOYMENT.md`). After changing either side, verify with
`chetter_run_self_test` (profile `quick`).

## Operations

```bash
# Cluster status
~/.tiup/bin/tiup cluster display chetter-tidb

# Config changes (edit topology, then rolling restart)
~/.tiup/bin/tiup cluster edit-config chetter-tidb
~/.tiup/bin/tiup cluster reload chetter-tidb

# Fleet + claiming sanity (from any MCP client)
chetter_runner_health
chetter_run_self_test quick
```

Backups: none are configured yet. A same-host dump is not disaster
recovery; set up exports to storage outside wowbagger before considering
this production-complete.
