#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=tidb-common.sh
source "${SCRIPT_DIR}/tidb-common.sh"

SOURCE_HOST=${SOURCE_HOST:-}
SOURCE_PORT=${SOURCE_PORT:-4000}
SOURCE_USER=${SOURCE_USER:-}
SOURCE_PASSWORD=${SOURCE_PASSWORD:-}
SOURCE_DATABASE=${SOURCE_DATABASE:-flatoutdev}
SOURCE_SSL_MODE=${SOURCE_SSL_MODE:-VERIFY_IDENTITY}
SOURCE_SSL_CA=${SOURCE_SSL_CA:-}

LOCAL_HOST=${LOCAL_HOST:-127.0.0.1}
LOCAL_PORT=${LOCAL_PORT:-4000}
LOCAL_USER=${LOCAL_USER:-chetter}
LOCAL_PASSWORD=${LOCAL_PASSWORD:-}
LOCAL_DATABASE=${LOCAL_DATABASE:-chetter}
LOCAL_DSN=${LOCAL_DSN:-}
EXPORT_DIR=${EXPORT_DIR:-${HOME}/chetter-migration}
DUMP_FILE=${DUMP_FILE:-${EXPORT_DIR}/chetter-data.sql}
CHETTER_REPO_ROOT=${CHETTER_REPO_ROOT:-${SCRIPT_DIR}/..}

TABLES=(
  chetter_tasks
  chetter_agent_sessions
  chetter_user_prompts
  chetter_execution_attempts
  chetter_agent_session_checkpoints
  chetter_task_events
  chetter_runners
  chetter_triggers
  chetter_trigger_runs
  chetter_event_callbacks
  teams
  users
  api_tokens
  user_team_memberships
  api_token_teams
  git_identities
  chetter_model_catalogs
  definition_sources
  definitions
  definition_sync_runs
  definition_change_proposals
  chetter_audit_log
  chetter_task_artifacts
  chetter_webhook_deliveries
)

usage() {
  cat <<'EOF'
Usage: tidb-cloud-migrate.sh <command>

Commands:
  export   Export the allowlisted Chetter tables from TiDB Cloud.
  prepare  Apply the repository's TiDB migrations to the local database.
  import   Import the data-only export into the prepared local database.
  verify   Compare row counts between source and local databases.

Source variables:
  SOURCE_HOST, SOURCE_USER, SOURCE_PASSWORD, SOURCE_DATABASE=flatoutdev
  SOURCE_PORT=4000, SOURCE_SSL_MODE=VERIFY_IDENTITY, SOURCE_SSL_CA

Local variables:
  LOCAL_HOST=127.0.0.1, LOCAL_PORT=4000, LOCAL_USER=chetter
  LOCAL_PASSWORD, LOCAL_DATABASE=chetter, LOCAL_DSN
  EXPORT_DIR=${HOME}/chetter-migration, DUMP_FILE=.../chetter-data.sql
  CHETTER_REPO_ROOT (required for prepare; defaults to repository root)

Passwords should be supplied through a mode-0600 environment file, not command
arguments. This script does not stop Chetter, drain runners, disable triggers,
or change deployment secrets; perform those cutover steps separately.
EOF
}

COMMAND=${1:-}
case "$COMMAND" in
  export|prepare|import|verify) ;;
  -h|--help) usage; exit 0 ;;
  *) usage >&2; exit 2 ;;
esac

case "$COMMAND" in
  export)
    tidb_need_cmd mysql
    tidb_need_cmd mysqldump
    ;;
  import|verify)
    tidb_need_cmd mysql
    ;;
esac

if ! tidb_valid_identifier "$SOURCE_DATABASE"; then
  tidb_die "invalid SOURCE_DATABASE: $SOURCE_DATABASE"
fi
if ! tidb_valid_identifier "$LOCAL_DATABASE"; then
  tidb_die "invalid LOCAL_DATABASE: $LOCAL_DATABASE"
fi

SOURCE_DEFAULTS=''
LOCAL_DEFAULTS=''
cleanup() {
  [[ -z "$SOURCE_DEFAULTS" ]] || rm -f -- "$SOURCE_DEFAULTS"
  [[ -z "$LOCAL_DEFAULTS" ]] || rm -f -- "$LOCAL_DEFAULTS"
}
trap cleanup EXIT

make_source_defaults() {
  [[ -n "$SOURCE_HOST" ]] || tidb_die 'SOURCE_HOST is required'
  [[ -n "$SOURCE_USER" ]] || tidb_die 'SOURCE_USER is required'
  if [[ "$SOURCE_SSL_MODE" == VERIFY_IDENTITY && -z "$SOURCE_SSL_CA" ]]; then
    tidb_die 'SOURCE_SSL_CA is required with SOURCE_SSL_MODE=VERIFY_IDENTITY'
  fi
  SOURCE_DEFAULTS=$(tidb_mysql_defaults "chetter-tidb-source" "$SOURCE_HOST" "$SOURCE_PORT" "$SOURCE_USER" "$SOURCE_PASSWORD" "$SOURCE_SSL_MODE" "$SOURCE_SSL_CA")
}

make_local_defaults() {
  LOCAL_DEFAULTS=$(tidb_mysql_defaults "chetter-tidb-local" "$LOCAL_HOST" "$LOCAL_PORT" "$LOCAL_USER" "$LOCAL_PASSWORD" "" "")
}

mysql_source() {
  "${MYSQL_SOURCE[@]}" --database="$SOURCE_DATABASE" "$@"
}

mysql_local() {
  "${MYSQL_LOCAL[@]}" --database="$LOCAL_DATABASE" "$@"
}

sanitize_dump() {
  local line
  while IFS= read -r line; do
    case "$line" in
      "USE \\`${SOURCE_DATABASE}\\`;"|"USE ${SOURCE_DATABASE};") ;;
      *) tidb_die "unexpected database selection in dump: $line" ;;
    esac
  done < <(grep -E '^USE ' "$DUMP_FILE" || true)

  if grep -Eiq '^(CREATE|DROP) DATABASE|^(CREATE|DROP|ALTER) TABLE' "$DUMP_FILE"; then
    tidb_die 'dump contains schema DDL; expected data-only export'
  fi

  if grep -Eq '^USE ' "$DUMP_FILE"; then
    local sanitized
    sanitized=$(mktemp "${DUMP_FILE}.XXXXXX")
    sed '/^USE /d' "$DUMP_FILE" >"$sanitized"
    chmod 600 "$sanitized"
    mv -f "$sanitized" "$DUMP_FILE"
  fi
}

prepare_local_schema() {
  [[ -n "$LOCAL_DSN" ]] || tidb_die 'LOCAL_DSN is required for prepare'
  [[ -d "$CHETTER_REPO_ROOT/db/migrations" ]] || tidb_die "migration directory not found: $CHETTER_REPO_ROOT/db/migrations"
  printf 'Applying current TiDB migrations to %s.\n' "$LOCAL_DATABASE"
  (
    cd "$CHETTER_REPO_ROOT"
    DATABASE_DSN="$LOCAL_DSN" go run ./cmd/chetter-migrate -dir db/migrations -driver tidb
  )
}

case "$COMMAND" in
  export)
    make_source_defaults
    mkdir -p "$EXPORT_DIR"
    chmod 700 "$EXPORT_DIR"
    MYSQL_SOURCE=(mysqldump --defaults-extra-file="$SOURCE_DEFAULTS" --protocol=tcp)
    printf 'Exporting %d Chetter tables from %s into %s.\n' "${#TABLES[@]}" "$SOURCE_DATABASE" "$DUMP_FILE"
    "${MYSQL_SOURCE[@]}" \
      --single-transaction --quick --skip-lock-tables \
      --no-create-info --no-create-db --skip-triggers \
      --complete-insert --hex-blob --set-gtid-purged=OFF \
      --result-file="$DUMP_FILE" "$SOURCE_DATABASE" "${TABLES[@]}"
    sanitize_dump
    sha256sum "$DUMP_FILE" | tee "${DUMP_FILE}.sha256"
    printf 'Export complete. Inspect and retain the dump before import.\n'
    ;;
  prepare)
    prepare_local_schema
    ;;
  import)
    [[ -f "$DUMP_FILE" ]] || tidb_die "dump not found: $DUMP_FILE"
    make_local_defaults
    MYSQL_LOCAL=(mysql --defaults-extra-file="$LOCAL_DEFAULTS" --protocol=tcp)
    sanitize_dump
    printf 'Importing %s into %s.\n' "$DUMP_FILE" "$LOCAL_DATABASE"
    mysql_local <"$DUMP_FILE"
    printf 'Import complete.\n'
    ;;
  verify)
    make_source_defaults
    make_local_defaults
    MYSQL_SOURCE=(mysql --defaults-extra-file="$SOURCE_DEFAULTS" --protocol=tcp --batch --skip-column-names)
    MYSQL_LOCAL=(mysql --defaults-extra-file="$LOCAL_DEFAULTS" --protocol=tcp --batch --skip-column-names)
    mismatch=0
    printf '%-42s %12s %12s\n' table source local
    for table in "${TABLES[@]}"; do
      source_count=$(mysql_source --execute "SELECT COUNT(*) FROM \\`${table}\\`;")
      local_count=$(mysql_local --execute "SELECT COUNT(*) FROM \\`${table}\\`;")
      printf '%-42s %12s %12s\n' "$table" "$source_count" "$local_count"
      if [[ "$source_count" != "$local_count" ]]; then
        mismatch=1
      fi
    done
    if [[ "$mismatch" != 0 ]]; then
      tidb_die 'row-count mismatch; do not cut over'
    fi
    printf 'All allowlisted row counts match.\n'
    ;;
esac
