#!/bin/sh
set -eu

if [ -n "${DATABASE_DSN:-}" ]; then
    # goose does not register the application's custom tls=tidb config.
    goose_dsn=$(printf '%s' "$DATABASE_DSN" | sed 's/tls=tidb/tls=true/g')
    case "${CHETTER_DB_DIALECT:-}" in
        postgres|postgresql)
            goose_driver=postgres
            goose_dir=/migrations-postgres
            ;;
        tidb)
            goose_driver=tidb
            goose_dir=/migrations
            ;;
        mysql)
            goose_driver=mysql
            goose_dir=/migrations
            ;;
        *)
            case "$goose_dsn" in
                postgres://*|postgresql://*)
                    goose_driver=postgres
                    goose_dir=/migrations-postgres
                    ;;
                *tidbcloud.com*)
                    goose_driver=tidb
                    goose_dir=/migrations
                    ;;
                *)
                    goose_driver=mysql
                    goose_dir=/migrations
                    ;;
            esac
            ;;
    esac
    /usr/local/bin/chetter-migrate -dir "$goose_dir" -driver "$goose_driver" -dsn "$goose_dsn"
fi

exec /chetter "$@"
