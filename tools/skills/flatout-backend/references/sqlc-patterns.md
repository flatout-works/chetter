# sqlc Configuration Reference

## sqlc.yaml (MySQL engine)

```yaml
version: "2"
sql:
  - engine: "mysql"
    queries: "db/queries"
    schema: "db/migrations"
    gen:
      go:
        package: "repository"
        out: "internal/repository"
        emit_json_tags: true
        emit_empty_slices: true
        emit_pointers_for_null_types: true
```

## Query Comment Annotations

| Annotation | Returns | Use for |
|-----------|---------|---------|
| `:one` | single struct | SELECT with guaranteed single row (by PK or UNIQUE) |
| `:many` | `[]Struct` | SELECT returning multiple rows |
| `:exec` | `error` only | INSERT/UPDATE/DELETE without returning data |
| `:execresult` | `sql.Result` | INSERT where you need `LastInsertId()` |
| `:copyfrom` | `error` | Bulk COPY (not MySQL, use multi-row INSERT) |

## Parameter Types

sqlc infers Go types from TiDB/MySQL column definitions:
- `VARCHAR` / `TEXT` → `string`
- `DATETIME` / `TIMESTAMP` → `time.Time`
- `INT` / `BIGINT` → `int32` / `int64`
- `DECIMAL` → `string` (unless overridden)
- Nullable columns with `emit_pointers_for_null_types: true` → `*type`

## Null Handling

With `emit_pointers_for_null_types: true`, always check for nil before dereferencing:

```go
if thing.Description != nil {
    desc = *thing.Description
}
```

## Re-generation Workflow

1. Edit `db/queries/*.sql` or `db/migrations/*.sql`
2. Run `make generate`
3. Verify `go build ./...` compiles
4. Do NOT manually edit files in `internal/repository/*`
