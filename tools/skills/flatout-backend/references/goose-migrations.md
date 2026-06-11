# Goose Migration Reference

## Migration File Naming

Use sequential numbering: `001_create_users.sql`, `002_create_projects.sql`

Create new migration: `make migrate-create` (prompts for name)
Or manually: `~/go/bin/goose -dir db/migrations -s create <name> sql`

## Annotated Migration Template

```sql
-- +goose Up
CREATE TABLE IF NOT EXISTS example (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS example;
```

## TiDB-Specific Notes

- TiDB supports `FOREIGN KEY` from v6.6.0+ (use with care, performance impact)
- TiDB does NOT support `PROCEDURE`, `TRIGGER`, `EVENT`, `FUNCTION`
- Views are read-only in TiDB
- Prefer `AUTO_RANDOM` over `AUTO_INCREMENT` for distributed writes (if using BIGINT PK)
- `ON UPDATE CURRENT_TIMESTAMP` works on TiDB v5.3+

## Running Migrations

```bash
make migrate      # goose up
make migrate-down # goose down (rollback one)
make migrate-status
```

DSN format: `root@tcp(127.0.0.1:4000)/flatout?parseTime=true`

## In Docker Compose

The `migrate` service in `docker-compose.yml` runs all `.sql` files sequentially via `mysql` client. For production, use `goose` binary with proper version tracking (`goose_db_version` table).
