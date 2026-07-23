# TiDB on Wowbagger

This guide moves Chetter's MySQL-compatible state from TiDB Cloud to a
self-managed TiDB cluster on wowbagger. It is intentionally a maintenance-window
procedure: Chetter has no dual-write or change-capture path.

## 1. Bootstrap TiDB

The bootstrap script uses TiUP Cluster with one PD, one TiDB server, and three
TiKV processes on wowbagger. This provides a persistent TiDB deployment, but it
does not provide host-level high availability because every process is on the
same machine.

Install a MySQL-compatible client first. Then, on wowbagger:

```bash
cd ~/chetter-src
chmod +x ops/tidb-bootstrap.sh ops/tidb-cloud-migrate.sh
```

Choose the address advertised by TiDB and the address Chetter can reach. If
Chetter runs in Docker, `127.0.0.1` inside the Chetter container is not the
wowbagger host. Use wowbagger's private address or place both services on a
private Docker network.

Example bootstrap configuration:

```bash
export TIDB_NODE_HOST=10.0.0.10
export TIDB_CONNECT_HOST=10.0.0.10
export TIDB_SSH_USER=root
export TIDB_VERSION=v8.5.1
export TIDB_DATABASE=chetter
export TIDB_APP_USER=chetter
export TIDB_APP_PASSWORD='use-a-random-password-from-a-secret-manager'
./ops/tidb-bootstrap.sh
```

For an existing root password, set `TIDB_ADMIN_PASSWORD`. On a new cluster,
TiUP's standard start leaves root without a password initially; set an admin
password or use TiUP's safe-start procedure before exposing port 4000 beyond a
private interface.

The generated topology is retained at:

```text
~/.config/chetter/chetter-tidb.yaml
```

Review it before deployment. The script does not overwrite an existing topology.
Use `TIDB_APPLY_CHECK=1` only after reviewing the precheck output.

## 2. Prepare migration credentials

Use a mode-0600 environment file on wowbagger or another trusted migration
machine. Do not put passwords in command arguments, shell history, Git, or this
document.

```bash
umask 077
cat >/tmp/chetter-tidb-migration.env <<'EOF'
SOURCE_HOST=gateway.<region>.prod.aws.tidbcloud.com
SOURCE_PORT=4000
SOURCE_USER=<tidb-cloud-user>
SOURCE_PASSWORD=<tidb-cloud-password>
SOURCE_DATABASE=flatoutdev
SOURCE_SSL_MODE=VERIFY_IDENTITY
SOURCE_SSL_CA=/etc/chetter/tidb-cloud-ca.pem

LOCAL_HOST=10.0.0.10
LOCAL_PORT=4000
LOCAL_USER=chetter
LOCAL_PASSWORD=<local-chetter-password>
LOCAL_DATABASE=chetter
LOCAL_DSN=chetter:<url-encoded-password>@tcp(10.0.0.10:4000)/chetter?parseTime=true

CHETTER_REPO_ROOT=/home/gokr/chetter-src
EXPORT_DIR=/var/lib/chetter-migration
EOF
chmod 600 /tmp/chetter-tidb-migration.env
source /tmp/chetter-tidb-migration.env
```

Use the CA certificate and endpoint details provided by TiDB Cloud. Do not use
`SOURCE_SSL_MODE=REQUIRED` as a substitute for identity verification unless the
network and risk have been explicitly reviewed.

## 3. Rehearse export and import

Run this against a disposable local database first:

```bash
./ops/tidb-cloud-migrate.sh export
./ops/tidb-cloud-migrate.sh prepare
./ops/tidb-cloud-migrate.sh import
./ops/tidb-cloud-migrate.sh verify
```

The export is data-only and limited to Chetter's application tables. The
destination schema comes from the repository's current TiDB migrations. The
script rejects database/schema DDL in the dump and removes only the expected
source `USE` statement before import.

The source database is not modified by these commands.

## 4. Production cutover

1. Take a TiDB Cloud backup/snapshot.
2. Disable cron, PR-review, issue, and external webhook triggers.
3. Drain runners and wait for active tasks to finish. Preserve runner-local
   workspaces and checkpoint data for paused or recoverable sessions.
4. Stop the Chetter server and any reaper/migration jobs so the source is frozen.
5. Run a final `export`, then `prepare`, `import`, and `verify`.
6. Check task, event, trigger, token, artifact, and session counts and inspect
   the import log.
7. Point Chetter at the local database with an explicit dialect:

   ```text
   DATABASE_DSN=chetter:<password>@tcp(10.0.0.10:4000)/chetter?parseTime=true
   CHETTER_DB_DIALECT=tidb
   ```

8. Start Chetter with triggers and runners still disabled. Verify health, login,
   task history, event history, and scheduler loading.
9. Start runners and execute one controlled task, including heartbeat and
   terminal-result checks.
10. Restore trigger states and reopen ingress.

The application account should have access to `chetter.*` only. Port 4000,
TiDB Dashboard, and Grafana should be restricted by firewall or private
networking. Configure backups outside wowbagger; a second disk on the same host
is not an adequate disaster-recovery backup.

## 5. Rollback boundary

Before any destination writes, restore the old deployment secret and restart
against TiDB Cloud. After destination writes begin, do not switch back by merely
changing the DSN: the databases have diverged. Stop traffic and make an explicit
reconciliation or restore decision.
