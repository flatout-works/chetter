#!/usr/bin/env bash

tidb_die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

tidb_need_cmd() {
  command -v "$1" >/dev/null 2>&1 || tidb_die "required command not found: $1"
}

tidb_valid_identifier() {
  [[ "$1" =~ ^[A-Za-z][A-Za-z0-9_-]*$ ]]
}

tidb_sql_identifier() {
  local value=$1
  tidb_valid_identifier "$value" || return 1
  printf '`%s`' "$value"
}

tidb_sql_string() {
  local value=$1
  value=${value//\\/\\\\}
  value=${value//\'/\'\'}
  value=${value//$'\n'/\\n}
  value=${value//$'\r'/\\r}
  printf "'%s'" "$value"
}

# Write a mode-0600 MySQL client defaults file and print its path.
# Passwords are kept out of process arguments and shell command output.
tidb_mysql_defaults() {
  local prefix=$1 host=$2 port=$3 user=$4 password=$5 ssl_mode=$6 ssl_ca=$7
  local file
  umask 077
  file=$(mktemp "${TMPDIR:-/tmp}/${prefix}.XXXXXX") || return 1
  {
    printf '[client]\n'
    printf 'host=%s\n' "$host"
    printf 'port=%s\n' "$port"
    printf 'user=%s\n' "$user"
    if [[ -n "$password" ]]; then
      printf 'password=%s\n' "$password"
    fi
    if [[ -n "$ssl_mode" ]]; then
      printf 'ssl-mode=%s\n' "$ssl_mode"
    fi
    if [[ -n "$ssl_ca" ]]; then
      printf 'ssl-ca=%s\n' "$ssl_ca"
    fi
  } >"$file" || {
    rm -f "$file"
    return 1
  }
  printf '%s\n' "$file"
}
