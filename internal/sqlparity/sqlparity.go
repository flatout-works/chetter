// Package sqlparity verifies that the MySQL/TiDB and PostgreSQL query sets
// stay in sync. Chetter maintains every SQL query twice — db/queries/*.sql
// (MySQL/TiDB, `?` placeholders) and db/postgres/queries/*.sql (PostgreSQL,
// `$N` placeholders) — and generates two sqlc packages behind the
// internal/data facade. Drift between the two dialects or use of
// dialect-wrong constructs only surfaces as a runtime/production bug, so
// this package provides a static check that fails the build early.
//
// Run it via `make check-sql-parity` (also part of `make check`) or directly
// with `go test ./internal/sqlparity/`.
package sqlparity

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Violation describes a single dialect-parity problem.
type Violation struct {
	File    string
	Message string
}

func (v Violation) String() string {
	if v.File == "" {
		return v.Message
	}
	return v.File + ": " + v.Message
}

// MySQL-only constructs that must never appear in a PostgreSQL query file.
var (
	reInsertIgnore   = regexp.MustCompile(`(?i)\bINSERT\s+IGNORE\b`)
	reOnDuplicateKey = regexp.MustCompile(`(?i)\bON\s+DUPLICATE\s+KEY\b`)
	// MySQL upsert target: VALUES(col) with a bare column name (as opposed to
	// a normal INSERT ... VALUES ($1, $2, ...) clause, which contains
	// placeholders and is legal in both dialects).
	reValuesUpsert = regexp.MustCompile(`(?i)\bVALUES\s*\(\s*[A-Za-z_][A-Za-z0-9_]*\s*\)`)
	// A bare `?` placeholder that is not part of a sqlc.arg/narg/slice call.
	// PostgreSQL query files must use $N placeholders; any other `?` is
	// MySQL/TiDB syntax. (Detected by scanBareQuestion, not a regexp: Go's
	// RE2 engine has no lookbehind.)
)

// PostgreSQL-only constructs that must never appear in a MySQL/TiDB query file.
var (
	reOnConflict = regexp.MustCompile(`(?i)\bON\s+CONFLICT\b`)
	reExcluded   = regexp.MustCompile(`(?i)\bEXCLUDED\.`)
	// PostgreSQL positional placeholder $N. MySQL JSON paths (->>'$.key')
	// use `$` followed by `.`, so `$` directly followed by a digit is the
	// placeholder form.
	reDollarPlaceholder = regexp.MustCompile(`\$[0-9]`)
)

// Check scans the dialect query directories and the generated sqlc code under
// root (the repository root) and returns every parity violation found. An
// empty result means the tree is healthy.
func Check(root string) []Violation {
	var violations []Violation

	mysqlDir := filepath.Join(root, "db", "queries")
	postgresDir := filepath.Join(root, "db", "postgres", "queries")

	mysqlFiles, err := filepath.Glob(filepath.Join(mysqlDir, "*.sql"))
	if err != nil {
		return []Violation{{File: mysqlDir, Message: fmt.Sprintf("glob: %v", err)}}
	}
	postgresFiles, err := filepath.Glob(filepath.Join(postgresDir, "*.sql"))
	if err != nil {
		return []Violation{{File: postgresDir, Message: fmt.Sprintf("glob: %v", err)}}
	}

	// File-level parity: every query file must exist in both dialects.
	mysqlBase := make(map[string]bool)
	for _, f := range mysqlFiles {
		mysqlBase[filepath.Base(f)] = true
	}
	postgresBase := make(map[string]bool)
	for _, f := range postgresFiles {
		postgresBase[filepath.Base(f)] = true
	}
	for _, f := range mysqlFiles {
		if !postgresBase[filepath.Base(f)] {
			violations = append(violations, Violation{File: rel(root, f),
				Message: "query file has no PostgreSQL counterpart in db/postgres/queries/"})
		}
	}
	for _, f := range postgresFiles {
		if !mysqlBase[filepath.Base(f)] {
			violations = append(violations, Violation{File: rel(root, f),
				Message: "query file has no MySQL/TiDB counterpart in db/queries/"})
		}
	}

	// Construct checks: per file, scan for dialect-wrong syntax.
	for _, f := range postgresFiles {
		violations = append(violations, checkMySQLOnlyConstructs(f, rel(root, f))...)
	}
	for _, f := range mysqlFiles {
		violations = append(violations, checkPostgresOnlyConstructs(f, rel(root, f))...)
	}

	// Query-name parity: every -- name: declaration must exist in both
	// dialects with the same command type, and must have been regenerated
	// into both sqlc packages and the internal/data facade.
	mysqlQueries := collectQueries(mysqlFiles)
	postgresQueries := collectQueries(postgresFiles)
	for name, cmd := range mysqlQueries {
		pc, ok := postgresQueries[name]
		if !ok {
			violations = append(violations, Violation{File: "db/queries", Message: fmt.Sprintf(
				"query %q has no PostgreSQL counterpart (add it to db/postgres/queries/ and run `make generate`)", name)})
			continue
		}
		if pc != cmd {
			violations = append(violations, Violation{File: "db/queries", Message: fmt.Sprintf(
				"query %q command type mismatch: MySQL/TiDB is %q but PostgreSQL is %q", name, cmd, pc)})
		}
	}
	for name := range postgresQueries {
		if _, ok := mysqlQueries[name]; !ok {
			violations = append(violations, Violation{File: "db/postgres/queries", Message: fmt.Sprintf(
				"query %q has no MySQL/TiDB counterpart (add it to db/queries/ and run `make generate`)", name)})
		}
	}

	// Generated-code sync: each query must be present as a Queries method in
	// both sqlc packages and the internal/data facade. A missing method means
	// `make generate` / `go generate ./internal/data` was not re-run.
	generated := generatedQueryNames(root)
	allNames := make(map[string]bool)
	for name := range mysqlQueries {
		allNames[name] = true
	}
	for name := range postgresQueries {
		allNames[name] = true
	}
	for name := range allNames {
		if !generated.mysql[name] {
			violations = append(violations, Violation{File: "internal/repository", Message: fmt.Sprintf(
				"query %q is missing from the generated MySQL/TiDB package (run `make generate`)", name)})
		}
		if !generated.postgres[name] {
			violations = append(violations, Violation{File: "internal/repositorypostgres", Message: fmt.Sprintf(
				"query %q is missing from the generated PostgreSQL package (run `make generate`)", name)})
		}
		if !generated.facade[name] {
			violations = append(violations, Violation{File: "internal/data", Message: fmt.Sprintf(
				"query %q is missing from the internal/data facade (run `go generate ./internal/data`)", name)})
		}
	}

	return violations
}

type generatedQueries struct {
	mysql    map[string]bool
	postgres map[string]bool
	facade   map[string]bool
}

// generatedQueryNames extracts the Queries methods from the generated sqlc
// packages and the facade by scanning for `func (q *Queries) Name(`. The
// facade scan covers both queries_gen.go (generated) and data.go (the
// hand-written SearchTasks/ListAuditLog/ListTaskArtifacts implementations).
func generatedQueryNames(root string) generatedQueries {
	g := generatedQueries{
		mysql:    scanGenerated(filepath.Join(root, "internal", "repository")),
		postgres: scanGenerated(filepath.Join(root, "internal", "repositorypostgres")),
		facade:   scanGenerated(filepath.Join(root, "internal", "data", "queries_gen.go")),
	}
	for name := range scanGenerated(filepath.Join(root, "internal", "data", "data.go")) {
		g.facade[name] = true
	}
	return g
}

func scanGenerated(path string) map[string]bool {
	names := make(map[string]bool)
	var files []string
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		files = []string{path}
	} else {
		var err error
		files, err = filepath.Glob(filepath.Join(path, "*.go"))
		if err != nil {
			return names
		}
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "func (q *Queries) ") {
				continue
			}
			rest := strings.TrimPrefix(line, "func (q *Queries) ")
			name, _, ok := strings.Cut(rest, "(")
			if ok && name != "" {
				names[name] = true
			}
		}
	}
	return names
}

// collectQueries returns the query name → command type map for a set of
// query files, parsed from `-- name: QueryName :command` lines.
func collectQueries(files []string) map[string]string {
	queries := make(map[string]string)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "-- name:") {
				continue
			}
			rest := strings.TrimSpace(strings.TrimPrefix(line, "-- name:"))
			fields := strings.Fields(rest)
			if len(fields) == 0 {
				continue
			}
			name := fields[0]
			cmd := ""
			if len(fields) > 1 {
				cmd = strings.TrimPrefix(fields[1], ":")
			}
			queries[name] = cmd
		}
	}
	return queries
}

// checkMySQLOnlyConstructs scans a single PostgreSQL query file for
// MySQL/TiDB-only syntax. file is the path to read; display is the path used
// in violation messages (relative to the repository root).
func checkMySQLOnlyConstructs(file, display string) []Violation {
	violations := scanConstructs(file, display, []constructRule{
		{"INSERT IGNORE", reInsertIgnore, "MySQL-only construct; use INSERT ... ON CONFLICT (...) DO NOTHING in PostgreSQL"},
		{"ON DUPLICATE KEY", reOnDuplicateKey, "MySQL-only construct; use INSERT ... ON CONFLICT (...) DO UPDATE in PostgreSQL"},
		{"VALUES(col) upsert target", reValuesUpsert, "MySQL-only upsert target; use EXCLUDED.col in PostgreSQL"},
	})
	// Bare `?` placeholders are detected per line (RE2 has no lookbehind).
	data, err := os.ReadFile(file)
	if err != nil {
		return append(violations, Violation{File: display, Message: fmt.Sprintf("read: %v", err)})
	}
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		if n := scanBareQuestion(trimmed); n > 0 {
			violations = append(violations, Violation{File: display, Message: fmt.Sprintf(
				"line %d: %d bare '?' placeholder(s) (MySQL-only; use $N or sqlc.arg(...) in PostgreSQL)", i+1, n)})
		}
	}
	return violations
}

// checkPostgresOnlyConstructs scans a single MySQL/TiDB query file for
// PostgreSQL-only syntax. file is the path to read; display is the path used
// in violation messages (relative to the repository root).
func checkPostgresOnlyConstructs(file, display string) []Violation {
	return scanConstructs(file, display, []constructRule{
		{"ON CONFLICT", reOnConflict, "PostgreSQL-only construct; use INSERT ... ON DUPLICATE KEY UPDATE in MySQL/TiDB"},
		{"EXCLUDED.", reExcluded, "PostgreSQL-only upsert reference; use VALUES(col) in MySQL/TiDB"},
		{"'$N' placeholder", reDollarPlaceholder, "PostgreSQL-only placeholder; use ? or sqlc.arg(...) in MySQL/TiDB"},
	})
}

type constructRule struct {
	label   string
	pattern *regexp.Regexp
	message string
}

// scanConstructs runs the rules against a file, skipping `--` comment lines
// so comments explaining dialect differences do not trip the check. The bare
// `?` placeholder rule is applied separately because it needs lookahead of
// the preceding token (RE2 has no lookbehind).
func scanConstructs(file, display string, rules []constructRule) []Violation {
	data, err := os.ReadFile(file)
	if err != nil {
		return []Violation{{File: display, Message: fmt.Sprintf("read: %v", err)}}
	}
	var violations []Violation
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		for _, rule := range rules {
			if rule.pattern.MatchString(trimmed) {
				violations = append(violations, Violation{File: display, Message: fmt.Sprintf(
					"line %d: %s (%s)", i+1, rule.message, rule.label)})
			}
		}
	}
	return violations
}

// scanBareQuestion returns the count of `?` characters in line that are not
// part of a sqlc.arg/narg/slice call (i.e. not immediately preceded by
// "sqlc."). PostgreSQL query files must use $N placeholders; a bare `?` is
// MySQL/TiDB placeholder syntax.
func scanBareQuestion(line string) int {
	count := 0
	for i := 0; i < len(line); i++ {
		if line[i] != '?' {
			continue
		}
		if i >= 5 && line[i-5:i] == "sqlc." {
			continue
		}
		count++
	}
	return count
}

// rel returns the file path relative to root for a stable, short display.
func rel(root, path string) string {
	r, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return r
}

// FormatViolations renders violations as a stable, sorted multi-line string
// for test failure output and error messages.
func FormatViolations(violations []Violation) string {
	if len(violations) == 0 {
		return ""
	}
	lines := make([]string, 0, len(violations))
	for _, v := range violations {
		lines = append(lines, v.String())
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
