# TiDB SQL Patterns Reference

## Connection DSN

```go
db, err := sql.Open("mysql", "root@tcp(127.0.0.1:4000)/flatout?parseTime=true")
```

Always include `parseTime=true` so `DATETIME`/`TIMESTAMP` columns map to `time.Time`.

## Common DDL Patterns

### UUID Primary Key Tables

```sql
CREATE TABLE things (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    status ENUM('active', 'inactive') NOT NULL DEFAULT 'active',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_things_user_id (user_id),
    INDEX idx_things_status (status)
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

### Foreign Keys (TiDB v6.6+)

```sql
CONSTRAINT fk_thing_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
```

### Unique Composite Indexes

```sql
UNIQUE INDEX idx_provider_uid (provider, provider_user_id)
```

## Transaction Pattern

```go
tx, err := db.BeginTx(ctx, nil)
if err != nil {
    return err
}
defer tx.Rollback()

qtx := queries.WithTx(tx)
if err := qtx.CreateThing(ctx, params); err != nil {
    return err
}
if err := qtx.CreateThingLog(ctx, logParams); err != nil {
    return err
}

return tx.Commit()
```

## Optimistic vs Pessimistic Transactions

TiDB defaults to pessimistic in v3.0+. For backends, use default pessimistic mode. Handle `COMMIT` failures by retrying at the application layer if needed.

## Diagnostic Queries

```sql
-- Check version
SELECT VERSION();  -- Should contain 'TiDB'

-- Explain a query
EXPLAIN FORMAT = "tidb_json" SELECT * FROM users WHERE email = 'test@example.com';

-- Check table regions (TiDB-specific)
SHOW TABLE users REGIONS;
```
