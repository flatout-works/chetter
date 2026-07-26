package testdb

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// TestPostgresSchemaParityWithMigrations verifies that the runtime bootstrap
// schema (schema.go → schema_postgres.go) produces the same tables, columns,
// indexes, and constraints as the Goose migrations (db/postgres/migrations/*).
//
// This prevents drift where a developer updates one path but not the other when
// adding or modifying database objects.
func TestPostgresSchemaParityWithMigrations(t *testing.T) {
	t.Parallel()

	bootstrapDB, gooseDB, cleanup := PostgresSchemaParityDBs(t)
	if bootstrapDB == nil {
		return // skipped
	}
	defer cleanup()

	ctx := context.Background()

	// Compare columns.
	bootstrapCols := queryColumns(t, bootstrapDB, ctx)
	gooseCols := queryColumns(t, gooseDB, ctx)

	colDiffs := diffColumns(bootstrapCols, gooseCols)
	if len(colDiffs) > 0 {
		t.Errorf("column differences between bootstrap and goose schemas:\n%s",
			strings.Join(colDiffs, "\n"))
	}

	// Compare indexes.
	bootstrapIdxs := queryIndexes(t, bootstrapDB, ctx)
	gooseIdxs := queryIndexes(t, gooseDB, ctx)

	idxDiffs := diffIndexes(bootstrapIdxs, gooseIdxs)
	if len(idxDiffs) > 0 {
		t.Errorf("index differences between bootstrap and goose schemas:\n%s",
			strings.Join(idxDiffs, "\n"))
	}

	// Compare constraints.
	bootstrapConstr := queryConstraints(t, bootstrapDB, ctx)
	gooseConstr := queryConstraints(t, gooseDB, ctx)

	constrDiffs := diffConstraints(bootstrapConstr, gooseConstr)
	if len(constrDiffs) > 0 {
		t.Errorf("constraint differences between bootstrap and goose schemas:\n%s",
			strings.Join(constrDiffs, "\n"))
	}
}

// columnInfo holds the metadata about one column for comparison.
type columnInfo struct {
	Table    string
	Column   string
	Type     string
	Nullable string
	Default  string
}

func (c columnInfo) key() string {
	return c.Table + "." + c.Column
}

func queryColumns(t *testing.T, db *TestDB, ctx context.Context) []columnInfo {
	t.Helper()

	rows, err := db.DB.QueryContext(ctx, `
		SELECT table_name, column_name, data_type, is_nullable,
		       COALESCE(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = $1
		ORDER BY table_name, ordinal_position
	`, db.DatabaseName())
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	defer rows.Close()

	var cols []columnInfo
	for rows.Next() {
		var c columnInfo
		if err := rows.Scan(&c.Table, &c.Column, &c.Type, &c.Nullable, &c.Default); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		c.Default = normalizeDefault(c.Default)
		c.Type = normalizeType(c.Type)
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return cols
}

// normalizeDefault strips noise from column defaults so comparisons are stable.
func normalizeDefault(d string) string {
	if d == "" {
		return ""
	}
	// Strip ::type casts from defaults (bootstrap and goose may differ on cast style).
	d = strings.ReplaceAll(d, "::text", "")
	d = strings.ReplaceAll(d, "::character varying", "")
	d = strings.ReplaceAll(d, "::jsonb", "")
	d = strings.ReplaceAll(d, "::integer", "")
	d = strings.ReplaceAll(d, "::bigint", "")
	d = strings.ReplaceAll(d, "::boolean", "")
	d = strings.ReplaceAll(d, "::timestamp with time zone", "")
	// Normalize quoting.
	d = strings.ReplaceAll(d, "'::text'", "''")
	// Strip extra spaces.
	d = strings.TrimSpace(d)
	return d
}

// normalizeType normalizes data type names for comparison.
func normalizeType(typ string) string {
	typ = strings.TrimSpace(typ)
	switch typ {
	case "timestamp with time zone":
		return "TIMESTAMPTZ"
	case "timestamp without time zone":
		return "TIMESTAMP"
	case "character varying":
		return "VARCHAR"
	case "boolean":
		return "BOOL"
	case "double precision":
		return "FLOAT8"
	}
	return typ
}

// indexInfo holds metadata about one index.
type indexInfo struct {
	Table string
	Name  string
	Def   string
}

func (i indexInfo) key() string {
	return i.Table + "." + i.Def
}

func queryIndexes(t *testing.T, db *TestDB, ctx context.Context) []indexInfo {
	t.Helper()

	rows, err := db.DB.QueryContext(ctx, `
		SELECT tablename, indexname,
		       indexdef
		FROM pg_indexes
		WHERE schemaname = $1
		  AND tablename <> 'goose_db_version'
		ORDER BY tablename, indexname
	`, "public")
	if err != nil {
		t.Fatalf("query indexes: %v", err)
	}
	defer rows.Close()

	var idxs []indexInfo
	for rows.Next() {
		var i indexInfo
		if err := rows.Scan(&i.Table, &i.Name, &i.Def); err != nil {
			t.Fatalf("scan index: %v", err)
		}
		i.Def = normalizeIndexDef(i.Def)
		idxs = append(idxs, i)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return idxs
}

// normalizeIndexDef normalizes index definitions for comparison.
func normalizeIndexDef(def string) string {
	fields := strings.Fields(def)
	if len(fields) >= 4 && fields[0] == "CREATE" {
		nameIndex := 2
		if fields[1] == "UNIQUE" {
			nameIndex = 3
		}
		if (fields[1] == "INDEX" || fields[1] == "UNIQUE") && nameIndex < len(fields) {
			fields = append(fields[:nameIndex], fields[nameIndex+1:]...)
		}
	}
	return strings.Join(fields, " ")
}

// constraintInfo holds metadata about one constraint.
type constraintInfo struct {
	Table   string
	Name    string
	Type    string // PRIMARY KEY, UNIQUE
	Columns string // comma-separated column list
}

func (c constraintInfo) setKey() string {
	return c.Table + "|" + c.Type + "|" + c.Columns
}

func queryConstraints(t *testing.T, db *TestDB, ctx context.Context) []constraintInfo {
	t.Helper()

	rows, err := db.DB.QueryContext(ctx, `
		SELECT tc.table_name, tc.constraint_name, tc.constraint_type,
		       COALESCE(string_agg(kcu.column_name, ',' ORDER BY kcu.ordinal_position), '')
		FROM information_schema.table_constraints tc
		LEFT JOIN information_schema.key_column_usage kcu
			ON tc.constraint_name = kcu.constraint_name
			AND tc.table_schema = kcu.constraint_schema
			AND tc.table_name = kcu.table_name
		WHERE tc.table_schema = $1
		  AND tc.constraint_type IN ('PRIMARY KEY', 'UNIQUE')
		GROUP BY tc.table_name, tc.constraint_name, tc.constraint_type
		ORDER BY tc.table_name, tc.constraint_name
	`, db.DatabaseName())
	if err != nil {
		t.Fatalf("query constraints: %v", err)
	}
	defer rows.Close()

	var constrs []constraintInfo
	for rows.Next() {
		var c constraintInfo
		if err := rows.Scan(&c.Table, &c.Name, &c.Type, &c.Columns); err != nil {
			t.Fatalf("scan constraint: %v", err)
		}
		constrs = append(constrs, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return constrs
}

// ----- diff functions -----

func diffColumns(bootstrap, goose []columnInfo) []string {
	bmap := make(map[string]columnInfo, len(bootstrap))
	for _, c := range bootstrap {
		bmap[c.key()] = c
	}
	gmap := make(map[string]columnInfo, len(goose))
	for _, c := range goose {
		gmap[c.key()] = c
	}

	var diffs []string
	// Columns in bootstrap but not goose.
	for _, c := range bootstrap {
		if _, ok := gmap[c.key()]; !ok {
			diffs = append(diffs, fmt.Sprintf("  - missing in goose: %s.%s (%s)", c.Table, c.Column, c.Type))
		}
	}
	// Columns in goose but not bootstrap.
	for _, c := range goose {
		if _, ok := bmap[c.key()]; !ok {
			diffs = append(diffs, fmt.Sprintf("  + extra in goose:   %s.%s (%s)", c.Table, c.Column, c.Type))
		}
	}
	// Type/nullable/default mismatches.
	for _, bc := range bootstrap {
		gc, ok := gmap[bc.key()]
		if !ok {
			continue
		}
		if bc.Type != gc.Type {
			diffs = append(diffs, fmt.Sprintf("  ~ type mismatch:    %s.%s bootstrap=%s goose=%s", bc.Table, bc.Column, bc.Type, gc.Type))
		}
		if bc.Nullable != gc.Nullable {
			diffs = append(diffs, fmt.Sprintf("  ~ nullable mismatch: %s.%s bootstrap=%s goose=%s", bc.Table, bc.Column, bc.Nullable, gc.Nullable))
		}
		if bc.Default != gc.Default {
			diffs = append(diffs, fmt.Sprintf("  ~ default mismatch: %s.%s bootstrap=%q goose=%q", bc.Table, bc.Column, bc.Default, gc.Default))
		}
	}
	sort.Strings(diffs)
	return diffs
}

func diffIndexes(bootstrap, goose []indexInfo) []string {
	bmap := make(map[string]indexInfo, len(bootstrap))
	for _, i := range bootstrap {
		bmap[i.key()] = i
	}
	gmap := make(map[string]indexInfo, len(goose))
	for _, i := range goose {
		gmap[i.key()] = i
	}

	var diffs []string
	for _, i := range bootstrap {
		if _, ok := gmap[i.key()]; !ok {
			diffs = append(diffs, fmt.Sprintf("  - missing in goose: %s.%s", i.Table, i.Name))
		}
	}
	for _, i := range goose {
		if _, ok := bmap[i.key()]; !ok {
			diffs = append(diffs, fmt.Sprintf("  + extra in goose:   %s.%s", i.Table, i.Name))
		}
	}
	sort.Strings(diffs)
	return diffs
}

func diffConstraints(bootstrap, goose []constraintInfo) []string {
	// Constraints are matched by (table, type, columns) because PostgreSQL may
	// auto-generate constraint names differently.
	bmap := make(map[string]constraintInfo, len(bootstrap))
	for _, c := range bootstrap {
		bmap[c.setKey()] = c
	}
	gmap := make(map[string]constraintInfo, len(goose))
	for _, c := range goose {
		gmap[c.setKey()] = c
	}

	var diffs []string
	for _, c := range bootstrap {
		if _, ok := gmap[c.setKey()]; !ok {
			diffs = append(diffs, fmt.Sprintf("  - missing in goose: %s %s (%s)", c.Table, c.Type, c.Columns))
		}
	}
	for _, c := range goose {
		if _, ok := bmap[c.setKey()]; !ok {
			diffs = append(diffs, fmt.Sprintf("  + extra in goose:   %s %s (%s)", c.Table, c.Type, c.Columns))
		}
	}
	sort.Strings(diffs)
	return diffs
}
