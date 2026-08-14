package sqlparity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTemp writes content to a temp file and returns its path.
func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestCheckMySQLOnlyConstructs(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int // number of violations
	}{
		{
			name: "clean postgres query",
			content: `-- name: InsertThing :exec
INSERT INTO things (id, name)
VALUES ($1, $2)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name;
`,
			want: 0,
		},
		{
			name: "INSERT IGNORE",
			content: `-- name: InsertThing :exec
INSERT IGNORE INTO things (id) VALUES ($1);
`,
			want: 1,
		},
		{
			name: "ON DUPLICATE KEY",
			content: `-- name: InsertThing :exec
INSERT INTO things (id) VALUES ($1)
ON DUPLICATE KEY UPDATE name = VALUES(name);
`,
			want: 2, // ON DUPLICATE KEY + VALUES(col)
		},
		{
			name: "VALUES col upsert target",
			content: `-- name: UpsertThing :exec
INSERT INTO things (id) VALUES ($1)
ON CONFLICT (id) DO UPDATE SET name = VALUES(name);
`,
			want: 1,
		},
		{
			name: "bare question placeholder",
			content: `-- name: GetThing :one
SELECT * FROM things WHERE id = ? AND name = ?;
`,
			want: 1, // both bare `?` on the same line collapse into one per-line violation
		},
		{
			name: "sqlc named args are fine",
			content: `-- name: ListThings :many
SELECT * FROM things
WHERE team_id IN (sqlc.slice(team_ids))
  AND name = sqlc.narg(name)
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);
`,
			want: 0,
		},
		{
			name: "comment explaining dialect differences is ignored",
			content: `-- MySQL would use: INSERT IGNORE / ON DUPLICATE KEY UPDATE col = VALUES(col)
-- name: InsertThing :exec
INSERT INTO things (id) VALUES ($1);
`,
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			file := writeTemp(t, dir, "things.sql", tt.content)
			got := checkMySQLOnlyConstructs(file, file)
			if len(got) != tt.want {
				t.Fatalf("checkMySQLOnlyConstructs() got %d violations, want %d:\n%s",
					len(got), tt.want, FormatViolations(got))
			}
		})
	}
}

func TestCheckPostgresOnlyConstructs(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
	}{
		{
			name: "clean mysql query",
			content: `-- name: InsertThing :exec
INSERT INTO things (id, name) VALUES (?, ?)
ON DUPLICATE KEY UPDATE name = VALUES(name);
`,
			want: 0,
		},
		{
			name: "ON CONFLICT",
			content: `-- name: InsertThing :exec
INSERT INTO things (id) VALUES (?)
ON CONFLICT (id) DO NOTHING;
`,
			want: 1,
		},
		{
			name: "EXCLUDED reference",
			content: `-- name: UpsertThing :exec
INSERT INTO things (id) VALUES (?)
ON DUPLICATE KEY UPDATE name = EXCLUDED.name;
`,
			want: 1,
		},
		{
			name: "dollar placeholder",
			content: `-- name: GetThing :one
SELECT * FROM things WHERE id = $1;
`,
			want: 1,
		},
		{
			name: "json path is not a placeholder",
			content: `-- name: ListThings :many
SELECT * FROM triggers
WHERE trigger_config->>'$.repo' = sqlc.arg(repo);
`,
			want: 0,
		},
		{
			name: "comment explaining dialect differences is ignored",
			content: `-- PostgreSQL would use: ON CONFLICT (...) DO UPDATE SET col = EXCLUDED.col
-- name: InsertThing :exec
INSERT INTO things (id) VALUES (?);
`,
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			file := writeTemp(t, dir, "things.sql", tt.content)
			got := checkPostgresOnlyConstructs(file, file)
			if len(got) != tt.want {
				t.Fatalf("checkPostgresOnlyConstructs() got %d violations, want %d:\n%s",
					len(got), tt.want, FormatViolations(got))
			}
		})
	}
}

// TestCheckCleanRepo asserts the live repository currently passes the parity
// check. This is the guard that makes `make check` fail on drift.
func TestCheckCleanRepo(t *testing.T) {
	root := filepath.Join("..", "..")
	violations := Check(root)
	if len(violations) > 0 {
		t.Fatalf("SQL dialect parity check found violations:\n%s", FormatViolations(violations))
	}
}

// buildFixture creates a minimal two-dialect query tree under dir that
// mirrors the repository layout used by Check.
func buildFixture(t *testing.T, dir string, mysql, postgres map[string]string) {
	t.Helper()
	for _, d := range []string{filepath.Join(dir, "db", "queries"), filepath.Join(dir, "db", "postgres", "queries")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	for name, content := range mysql {
		writeTemp(t, filepath.Join(dir, "db", "queries"), name, content)
	}
	for name, content := range postgres {
		writeTemp(t, filepath.Join(dir, "db", "postgres", "queries"), name, content)
	}
}

func TestCheckQueryNameParity(t *testing.T) {
	dir := t.TempDir()
	buildFixture(t, dir,
		map[string]string{
			"things.sql": "-- name: InsertThing :exec\nINSERT INTO things (id) VALUES (?);\n",
		},
		map[string]string{
			"things.sql": "-- name: InsertThing :exec\nINSERT INTO things (id) VALUES ($1);\n",
		})

	t.Run("missing postgres counterpart", func(t *testing.T) {
		root := t.TempDir()
		buildFixture(t, root,
			map[string]string{
				"things.sql":      "-- name: InsertThing :exec\nINSERT INTO things (id) VALUES (?);\n",
				"extra_mysql.sql": "-- name: InsertExtra :exec\nINSERT INTO extra (id) VALUES (?);\n",
			},
			map[string]string{
				"things.sql": "-- name: InsertThing :exec\nINSERT INTO things (id) VALUES ($1);\n",
			})
		violations := Check(root)
		if !containsViolation(violations, "extra_mysql.sql", "PostgreSQL counterpart") {
			t.Fatalf("expected missing-file violation, got:\n%s", FormatViolations(violations))
		}
		if !containsViolation(violations, "InsertExtra", "no PostgreSQL counterpart") {
			t.Fatalf("expected missing-query violation for InsertExtra, got:\n%s", FormatViolations(violations))
		}
	})

	t.Run("missing mysql counterpart", func(t *testing.T) {
		root := t.TempDir()
		buildFixture(t, root,
			map[string]string{
				"things.sql": "-- name: InsertThing :exec\nINSERT INTO things (id) VALUES (?);\n",
			},
			map[string]string{
				"things.sql":         "-- name: InsertThing :exec\nINSERT INTO things (id) VALUES ($1);\n",
				"extra_postgres.sql": "-- name: InsertExtra :exec\nINSERT INTO extra (id) VALUES ($1);\n",
			})
		violations := Check(root)
		if !containsViolation(violations, "extra_postgres.sql", "MySQL/TiDB counterpart") {
			t.Fatalf("expected missing-file violation, got:\n%s", FormatViolations(violations))
		}
		if !containsViolation(violations, "InsertExtra", "no MySQL/TiDB counterpart") {
			t.Fatalf("expected missing-query violation for InsertExtra, got:\n%s", FormatViolations(violations))
		}
	})

	t.Run("command type mismatch", func(t *testing.T) {
		root := t.TempDir()
		buildFixture(t, root,
			map[string]string{
				"things.sql": "-- name: InsertThing :exec\nINSERT INTO things (id) VALUES (?);\n",
			},
			map[string]string{
				"things.sql": "-- name: InsertThing :one\nSELECT * FROM things WHERE id = $1;\n",
			})
		violations := Check(root)
		if !containsViolation(violations, "InsertThing", "command type mismatch") {
			t.Fatalf("expected command-type-mismatch violation, got:\n%s", FormatViolations(violations))
		}
	})

	t.Run("fixture with generated code is clean", func(t *testing.T) {
		// A clean fixture that also generates the expected method names.
		writeGenerated(t, dir)
		violations := Check(dir)
		if len(violations) > 0 {
			t.Fatalf("clean fixture reported violations:\n%s", FormatViolations(violations))
		}
	})
}

// writeGenerated plants generated sqlc code and facade methods for the
// fixture queries used in TestCheckQueryNameParity.
func writeGenerated(t *testing.T, dir string) {
	t.Helper()
	repo := filepath.Join(dir, "internal", "repository")
	postgres := filepath.Join(dir, "internal", "repositorypostgres")
	facade := filepath.Join(dir, "internal", "data")
	for _, d := range []string{repo, postgres, facade} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	gen := "// Code generated by sqlc. DO NOT EDIT.\n\npackage repository\n\nfunc (q *Queries) InsertThing(ctx context.Context, arg InsertThingParams) error { return nil }\n"
	if err := os.WriteFile(filepath.Join(repo, "things.sql.go"), []byte(gen), 0o644); err != nil {
		t.Fatalf("write generated: %v", err)
	}
	genPostgres := "// Code generated by sqlc. DO NOT EDIT.\n\npackage repositorypostgres\n\nfunc (q *Queries) InsertThing(ctx context.Context, arg InsertThingParams) error { return nil }\n"
	if err := os.WriteFile(filepath.Join(postgres, "things.sql.go"), []byte(genPostgres), 0o644); err != nil {
		t.Fatalf("write generated: %v", err)
	}
	facadeGen := "// Code generated by go generate; DO NOT EDIT.\n\npackage data\n\nfunc (q *Queries) InsertThing(ctx context.Context, arg repository.InsertThingParams) error { return nil }\n"
	if err := os.WriteFile(filepath.Join(facade, "queries_gen.go"), []byte(facadeGen), 0o644); err != nil {
		t.Fatalf("write facade: %v", err)
	}
}

func TestCheckGeneratedSync(t *testing.T) {
	t.Run("missing generated method is reported", func(t *testing.T) {
		root := t.TempDir()
		buildFixture(t, root,
			map[string]string{
				"things.sql": "-- name: InsertThing :exec\nINSERT INTO things (id) VALUES (?);\n",
			},
			map[string]string{
				"things.sql": "-- name: InsertThing :exec\nINSERT INTO things (id) VALUES ($1);\n",
			})
		// No generated code planted: every method check must fire.
		violations := Check(root)
		if !containsViolation(violations, "internal/repository", "missing from the generated MySQL/TiDB package") {
			t.Fatalf("expected generated-mysql violation, got:\n%s", FormatViolations(violations))
		}
		if !containsViolation(violations, "internal/repositorypostgres", "missing from the generated PostgreSQL package") {
			t.Fatalf("expected generated-postgres violation, got:\n%s", FormatViolations(violations))
		}
		if !containsViolation(violations, "internal/data", "missing from the internal/data facade") {
			t.Fatalf("expected facade violation, got:\n%s", FormatViolations(violations))
		}
	})

	t.Run("stale generated code missing only facade", func(t *testing.T) {
		root := t.TempDir()
		buildFixture(t, root,
			map[string]string{
				"things.sql": "-- name: InsertThing :exec\nINSERT INTO things (id) VALUES (?);\n",
			},
			map[string]string{
				"things.sql": "-- name: InsertThing :exec\nINSERT INTO things (id) VALUES ($1);\n",
			})
		writeGenerated(t, root)
		// Remove the facade method: sqlc was regenerated but genfacade was not.
		gen := "// Code generated by go generate; DO NOT EDIT.\n\npackage data\n\n"
		if err := os.WriteFile(filepath.Join(root, "internal", "data", "queries_gen.go"), []byte(gen), 0o644); err != nil {
			t.Fatalf("write facade: %v", err)
		}
		violations := Check(root)
		if !containsViolation(violations, "internal/data", "missing from the internal/data facade") {
			t.Fatalf("expected facade violation, got:\n%s", FormatViolations(violations))
		}
		if len(violations) != 1 {
			t.Fatalf("expected exactly one violation, got:\n%s", FormatViolations(violations))
		}
	})
}

func containsViolation(violations []Violation, file, message string) bool {
	for _, v := range violations {
		if strings.Contains(v.File+v.Message, file) && strings.Contains(v.Message, message) {
			return true
		}
	}
	return false
}
