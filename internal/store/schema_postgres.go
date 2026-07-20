package store

import "strings"

// postgresSchemaStatements is derived from the canonical MySQL/TiDB bootstrap
// schema. PostgreSQL uses native types and standalone indexes, while keeping
// table and column names identical for the application layer.
var postgresSchemaStatements = postgresSchema()

func postgresSchema() []string {
	statements := make([]string, 0, len(schemaStatements)+40)
	indexes := make([]string, 0, 40)
	for _, statement := range schemaStatements {
		lines := strings.Split(statement, "\n")
		if len(lines) < 3 {
			continue
		}
		header := lines[0]
		table := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(header, "CREATE TABLE IF NOT EXISTS ")), "("))
		fields := make([]string, 0, len(lines)-2)
		for _, line := range lines[1 : len(lines)-1] {
			field := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), ","))
			if table == "chetter_tasks" && strings.HasPrefix(field, "git_identity_id ") {
				// Keep SELECT * column order aligned with the PostgreSQL migration,
				// which adds this column after the original baseline table.
				continue
			}
			switch {
			case strings.HasPrefix(field, "FULLTEXT INDEX "):
				// PostgreSQL full-text indexes are added below as GIN expression indexes.
				continue
			case strings.HasPrefix(field, "KEY "):
				parts := strings.SplitN(strings.TrimPrefix(field, "KEY "), " ", 2)
				if len(parts) == 2 {
					indexes = append(indexes, "CREATE INDEX IF NOT EXISTS "+parts[0]+" ON "+table+" "+parts[1])
				}
				continue
			case strings.HasPrefix(field, "UNIQUE KEY "):
				field = "UNIQUE " + strings.SplitN(strings.TrimPrefix(field, "UNIQUE KEY "), " ", 2)[1]
			}
			field = strings.ReplaceAll(field, "DATETIME(6)", "TIMESTAMPTZ")
			field = strings.ReplaceAll(field, "MEDIUMTEXT", "TEXT")
			field = strings.ReplaceAll(field, " JSON", " JSONB")
			fields = append(fields, field)
			if (table == "chetter_tasks" || table == "chetter_triggers") && field == "id VARCHAR(64) NOT NULL" {
				fields = append(fields, "team_id VARCHAR(64) NULL")
			}
		}
		statements = append(statements, header+"\n\t"+strings.Join(fields, ",\n\t")+"\n)")
	}
	statements = append(statements, indexes...)
	statements = append(statements, "ALTER TABLE chetter_tasks ADD COLUMN IF NOT EXISTS git_identity_id VARCHAR(64) NULL")
	statements = append(statements,
		"CREATE INDEX IF NOT EXISTS idx_tasks_search ON chetter_tasks USING GIN (to_tsvector('simple', COALESCE(search_text, '')))",
		"CREATE INDEX IF NOT EXISTS idx_sessions_search ON chetter_agent_sessions USING GIN (to_tsvector('simple', COALESCE(search_text, '')))",
		"CREATE INDEX IF NOT EXISTS idx_audit_search ON chetter_audit_log USING GIN (to_tsvector('simple', COALESCE(search_text, '')))",
		"CREATE INDEX IF NOT EXISTS idx_artifacts_search ON chetter_task_artifacts USING GIN (to_tsvector('simple', COALESCE(search_text, '')))",
	)
	return statements
}
