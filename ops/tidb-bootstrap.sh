#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=tidb-common.sh
source "${SCRIPT_DIR}/tidb-common.sh"

CLUSTER_NAME=${TIDB_CLUSTER_NAME:-chetter-tidb}
TIDB_VERSION=${TIDB_VERSION:-v8.5.1}
TIDB_NODE_HOST=${TIDB_NODE_HOST:-127.0.0.1}
TIDB_CONNECT_HOST=${TIDB_CONNECT_HOST:-${TIDB_NODE_HOST}}
TIDB_SSH_USER=${TIDB_SSH_USER:-root}
TIDB_SSH_KEY=${TIDB_SSH_KEY:-}
TIDB_SERVICE_USER=${TIDB_SERVICE_USER:-tidb}
TIDB_DEPLOY_DIR=${TIDB_DEPLOY_DIR:-/tidb-deploy}
TIDB_DATA_DIR=${TIDB_DATA_DIR:-/tidb-data}
TIDB_DATABASE=${TIDB_DATABASE:-chetter}
TIDB_APP_USER=${TIDB_APP_USER:-chetter}
TIDB_APP_HOST=${TIDB_APP_HOST:-%}
TIDB_PORT=${TIDB_PORT:-4000}
TIDB_TOPOLOGY=${TIDB_TOPOLOGY:-${HOME}/.config/chetter/${CLUSTER_NAME}.yaml}
TIDB_INSTALL_TIUP=${TIDB_INSTALL_TIUP:-1}
if [[ "${TIDB_SKIP_TIUP:-0}" == 1 ]]; then
  TIDB_INSTALL_TIUP=0
fi
TIDB_APPLY_CHECK=${TIDB_APPLY_CHECK:-0}
TIDB_ADMIN_PASSWORD=${TIDB_ADMIN_PASSWORD:-}
TIDB_APP_PASSWORD=${TIDB_APP_PASSWORD:-}

usage() {
  cat <<'EOF'
Usage: tidb-bootstrap.sh [--dry-run|--generate-only]

Deploy or start a persistent, single-host TiDB cluster using TiUP Cluster,
then create the configured application database. Existing TiUP clusters are
not redeployed.

Important environment variables:
  TIDB_NODE_HOST       Host/IP advertised by TiDB (default: 127.0.0.1)
  TIDB_CONNECT_HOST    Host Chetter uses to connect (default: node host)
  TIDB_SSH_USER        SSH user TiUP uses (default: root)
  TIDB_SSH_KEY         Optional SSH private key passed to TiUP
  TIDB_ADMIN_PASSWORD  Existing TiDB admin password, if configured
  TIDB_APP_PASSWORD    If set, create/update the chetter application user
  TIDB_APPLY_CHECK=1   Run tiup cluster check --apply before deployment
  TIDB_SKIP_TIUP=1     Do not install/update TiUP components

  --generate-only      Write the topology and stop before requiring TiUP

The default topology runs one PD, one TiDB, and three TiKV processes on one
host. It is not host-level HA. Use TIDB_NODE_HOST as a private address when
Chetter runs outside the host network, and firewall port 4000 accordingly.
EOF
}

DRY_RUN=0
GENERATE_ONLY=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) DRY_RUN=1; shift ;;
    --generate-only) GENERATE_ONLY=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) tidb_die "unknown argument: $1" ;;
  esac
done

run() {
  printf '+ '
  printf '%q ' "$@"
  printf '\n'
  if [[ "$DRY_RUN" == 0 ]]; then
    "$@"
  fi
}

if ! tidb_valid_identifier "$TIDB_DATABASE"; then
  tidb_die "invalid TIDB_DATABASE: $TIDB_DATABASE"
fi
if ! tidb_valid_identifier "$TIDB_APP_USER"; then
  tidb_die "invalid TIDB_APP_USER: $TIDB_APP_USER"
fi
if [[ "$TIDB_NODE_HOST" == "127.0.0.1" && "$TIDB_CONNECT_HOST" != "127.0.0.1" ]]; then
  printf 'NOTE: TiDB advertises %s but Chetter connects to %s.\n' "$TIDB_NODE_HOST" "$TIDB_CONNECT_HOST"
fi

write_topology() {
  mkdir -p "$(dirname -- "$TIDB_TOPOLOGY")"
  if [[ ! -e "$TIDB_TOPOLOGY" ]]; then
    cat >"$TIDB_TOPOLOGY" <<EOF
global:
  user: "${TIDB_SERVICE_USER}"
  ssh_port: 22
  deploy_dir: "${TIDB_DEPLOY_DIR}"
  data_dir: "${TIDB_DATA_DIR}"

pd_servers:
  - host: "${TIDB_NODE_HOST}"
    client_port: 2379
    peer_port: 2380

tidb_servers:
  - host: "${TIDB_NODE_HOST}"
    port: ${TIDB_PORT}
    status_port: 10080

tikv_servers:
  - host: "${TIDB_NODE_HOST}"
    port: 20160
    status_port: 20180
  - host: "${TIDB_NODE_HOST}"
    port: 20161
    status_port: 20181
  - host: "${TIDB_NODE_HOST}"
    port: 20162
    status_port: 20182

monitoring_servers:
  - host: "${TIDB_NODE_HOST}"
    port: 9090

grafana_servers:
  - host: "${TIDB_NODE_HOST}"
    port: 3000
EOF
    chmod 600 "$TIDB_TOPOLOGY"
    printf 'Created topology: %s\n' "$TIDB_TOPOLOGY"
  else
    printf 'Using existing topology: %s\n' "$TIDB_TOPOLOGY"
  fi
}

if [[ "$GENERATE_ONLY" == 1 ]]; then
  write_topology
  printf 'Review %s, then rerun without --generate-only to deploy.\n' "$TIDB_TOPOLOGY"
  exit 0
fi

tidb_need_cmd curl
tidb_need_cmd mysql

if [[ "$TIDB_INSTALL_TIUP" != 0 ]] && ! command -v tiup >/dev/null 2>&1; then
  if [[ "$DRY_RUN" == 1 ]]; then
    printf 'Would install TiUP from the official PingCAP installer.\n'
  else
    printf 'Installing TiUP from the official PingCAP installer.\n'
    curl --proto '=https' --tlsv1.2 -sSf https://tiup-mirrors.pingcap.com/install.sh | sh
  fi
fi
export PATH="${HOME}/.tiup/bin:${PATH}"
if [[ "$DRY_RUN" == 0 ]]; then
  tidb_need_cmd tiup
fi

if [[ "$TIDB_INSTALL_TIUP" != 0 ]]; then
  run tiup update --self
  run tiup update cluster
fi

TIUP_AUTH=()
if [[ -n "$TIDB_SSH_KEY" ]]; then
  TIUP_AUTH+=(--identity_file "$TIDB_SSH_KEY")
fi

if [[ "$DRY_RUN" == 1 ]]; then
  printf 'Would check/deploy/start cluster %s at %s using %s.\n' "$CLUSTER_NAME" "$TIDB_NODE_HOST" "$TIDB_TOPOLOGY"
  exit 0
fi

if tiup cluster display "$CLUSTER_NAME" >/dev/null 2>&1; then
  printf 'TiDB cluster %s already exists; skipping deploy.\n' "$CLUSTER_NAME"
else
  CHECK_ARGS=(tiup cluster check "$TIDB_TOPOLOGY" --user "$TIDB_SSH_USER")
  if [[ "${TIDB_APPLY_CHECK}" == 1 ]]; then
    CHECK_ARGS+=(--apply)
  fi
  CHECK_ARGS+=("${TIUP_AUTH[@]}")
  run "${CHECK_ARGS[@]}"

  DEPLOY_ARGS=(tiup cluster deploy "$CLUSTER_NAME" "$TIDB_VERSION" "$TIDB_TOPOLOGY" --user "$TIDB_SSH_USER")
  DEPLOY_ARGS+=("${TIUP_AUTH[@]}")
  run "${DEPLOY_ARGS[@]}"
fi

run tiup cluster start "$CLUSTER_NAME"
run tiup cluster display "$CLUSTER_NAME"

MYSQL_DEFAULTS=$(tidb_mysql_defaults "chetter-tidb-admin" "$TIDB_CONNECT_HOST" "$TIDB_PORT" root "$TIDB_ADMIN_PASSWORD" "" "")
trap 'rm -f -- "$MYSQL_DEFAULTS"' EXIT

MYSQL=(mysql --defaults-extra-file="$MYSQL_DEFAULTS" --protocol=tcp)
if ! "${MYSQL[@]}" --batch --skip-column-names -e 'SELECT VERSION()' >/dev/null; then
  tidb_die "could not connect to TiDB at ${TIDB_CONNECT_HOST}:${TIDB_PORT}; set TIDB_ADMIN_PASSWORD if root is password-protected"
fi

APP_USER_QUOTED=$(tidb_sql_string "$TIDB_APP_USER")
APP_HOST_QUOTED=$(tidb_sql_string "$TIDB_APP_HOST")
DATABASE_IDENTIFIER=$(tidb_sql_identifier "$TIDB_DATABASE")
SQL="CREATE DATABASE IF NOT EXISTS ${DATABASE_IDENTIFIER} CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;"
if [[ -n "$TIDB_APP_PASSWORD" ]]; then
  APP_PASSWORD_QUOTED=$(tidb_sql_string "$TIDB_APP_PASSWORD")
  SQL+=" CREATE USER IF NOT EXISTS ${APP_USER_QUOTED}@${APP_HOST_QUOTED} IDENTIFIED BY ${APP_PASSWORD_QUOTED};"
  SQL+=" ALTER USER ${APP_USER_QUOTED}@${APP_HOST_QUOTED} IDENTIFIED BY ${APP_PASSWORD_QUOTED};"
  SQL+=" GRANT ALL PRIVILEGES ON ${DATABASE_IDENTIFIER}.* TO ${APP_USER_QUOTED}@${APP_HOST_QUOTED};"
fi
printf '%s\n' "$SQL" | "${MYSQL[@]}"

printf '\nTiDB is ready.\n'
printf '  Cluster: %s\n' "$CLUSTER_NAME"
printf '  Endpoint: %s:%s\n' "$TIDB_CONNECT_HOST" "$TIDB_PORT"
printf '  Database: %s\n' "$TIDB_DATABASE"
if [[ -n "$TIDB_APP_PASSWORD" ]]; then
  printf '  Runtime user: %s@%s\n' "$TIDB_APP_USER" "$TIDB_APP_HOST"
else
  printf '  Runtime user: not created; set TIDB_APP_PASSWORD and rerun to create it\n'
fi
