package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/stdlib"
)

const postgresDriverName = "chetter-postgres"

var registerPostgresDriver sync.Once

// DriverName returns the database/sql driver name for a dialect.
func DriverName(dialect Dialect) string {
	if dialect == DialectPostgres {
		registerPostgresDriverOnce()
		return postgresDriverName
	}
	return "mysql"
}

// DBTX is the subset of database/sql used by sqlc-generated repositories.
type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	PrepareContext(context.Context, string) (*sql.Stmt, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// RebindDB returns a database handle suitable for sqlc-generated queries.
// PostgreSQL rewriting occurs in the registered database/sql driver, which
// also covers callers that use the raw *sql.DB directly.
func RebindDB(db *sql.DB, _ Dialect) DBTX { return db }

// RebindTx returns a transaction suitable for sqlc-generated queries.
func RebindTx(tx *sql.Tx, _ Dialect) DBTX { return tx }

func registerPostgresDriverOnce() {
	registerPostgresDriver.Do(func() {
		sql.Register(postgresDriverName, postgresDriver{Driver: stdlib.GetDefaultDriver()})
	})
}

type postgresDriver struct{ driver.Driver }

func (d postgresDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return postgresConn{Conn: conn}, nil
}

type postgresConn struct{ driver.Conn }

func (c postgresConn) Prepare(query string) (driver.Stmt, error) {
	return c.Conn.Prepare(postgresQuery(query))
}

func (c postgresConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if conn, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return conn.PrepareContext(ctx, postgresQuery(query))
	}
	return c.Prepare(query)
}

func (c postgresConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if conn, ok := c.Conn.(driver.ConnBeginTx); ok {
		return conn.BeginTx(ctx, opts)
	}
	return c.Conn.Begin()
}

func (c postgresConn) Ping(ctx context.Context) error {
	if conn, ok := c.Conn.(driver.Pinger); ok {
		return conn.Ping(ctx)
	}
	return nil
}

func (c postgresConn) CheckNamedValue(value *driver.NamedValue) error {
	if conn, ok := c.Conn.(driver.NamedValueChecker); ok {
		return conn.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

func (c postgresConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if conn, ok := c.Conn.(driver.ExecerContext); ok {
		return conn.ExecContext(ctx, postgresQuery(query), args)
	}
	return nil, driver.ErrSkip
}

func (c postgresConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if conn, ok := c.Conn.(driver.QueryerContext); ok {
		return conn.QueryContext(ctx, postgresQuery(query), args)
	}
	return nil, driver.ErrSkip
}

func postgresQuery(query string) string {
	query = strings.TrimSpace(query)
	if special, ok := postgresSessionQuery(query); ok {
		query = special
	}
	query = strings.ReplaceAll(query, "<=>", "IS NOT DISTINCT FROM")
	query = strings.ReplaceAll(query, "trigger_config->>'$.repo'", "trigger_config->>'repo'")
	query = strings.ReplaceAll(query, "GROUP_CONCAT(ttm.name ORDER BY ttm.name SEPARATOR ',')", "STRING_AGG(ttm.name, ',' ORDER BY ttm.name)")
	query = rebindPostgresPlaceholders(query)
	query = castTimestampNullParameters(query)
	query = strings.ReplaceAll(query, "DATE_SUB(NOW(), INTERVAL $", "NOW() - (")
	query = replaceIntervalSeconds(query)
	query = postgresUpsert(query)
	if strings.Contains(query, "INSERT IGNORE INTO ") {
		query = strings.Replace(query, "INSERT IGNORE INTO ", "INSERT INTO ", 1)
		query = strings.TrimSuffix(strings.TrimSpace(query), ";") + " ON CONFLICT DO NOTHING"
	}
	return query
}

func castTimestampNullParameters(query string) string {
	for offset := 0; ; {
		idx := strings.Index(query[offset:], " IS NULL")
		if idx < 0 {
			return query
		}
		idx += offset
		start := strings.LastIndexByte(query[:idx], '$')
		if start < 0 {
			offset = idx + len(" IS NULL")
			continue
		}
		parameter := query[start:idx]
		if _, err := strconv.Atoi(parameter[1:]); err != nil {
			offset = idx + len(" IS NULL")
			continue
		}
		query = query[:idx] + "::timestamptz" + query[idx:]
		offset = idx + len("::timestamptz IS NULL")
	}
}

func postgresSessionQuery(query string) (string, bool) {
	switch {
	case strings.Contains(query, "FailPendingResumeTasksForMissingRunner"):
		return `
UPDATE chetter_tasks t
SET status = 'error',
    error = CONCAT('pinned runner ', t.required_runner_id, ' is not alive'),
    error_category = 'runner_unavailable',
    ended_at = ?,
    updated_at = ?,
    last_event_at = ?
WHERE t.status = 'pending'
  AND t.required_runner_id IS NOT NULL
  AND t.required_runner_id <> ''
  AND NOT EXISTS (
    SELECT 1 FROM chetter_runners r
    WHERE r.id = t.required_runner_id
      AND r.status = 'active'
      AND r.last_seen_at > DATE_SUB(NOW(), INTERVAL ? SECOND)
  )
  AND EXISTS (
    SELECT 1
    FROM chetter_session_runs sr
    JOIN chetter_agent_sessions s ON s.id = sr.agent_session_id
    WHERE sr.task_id = t.id
      AND sr.status = 'pending'
      AND s.status = 'resuming'
  )`, true
	case strings.Contains(query, "FailPendingSessionRunsForUnavailableRunner"):
		return `
UPDATE chetter_session_runs sr
SET status = 'failed',
    error = t.error,
    ended_at = COALESCE(sr.ended_at, ?),
    updated_at = ?
FROM chetter_tasks t
WHERE t.id = sr.task_id
  AND sr.status = 'pending'
  AND t.status = 'error'
  AND t.error_category = 'runner_unavailable'`, true
	case strings.Contains(query, "MarkResumingSessionsFailedForUnavailableRunner"):
		return `
UPDATE chetter_agent_sessions s
SET status = 'error',
    error = COALESCE(sr.error, t.error),
    updated_at = ?
FROM chetter_session_runs sr
JOIN chetter_tasks t ON t.id = sr.task_id
WHERE sr.agent_session_id = s.id
  AND s.status = 'resuming'
  AND sr.status = 'failed'
  AND t.status = 'error'
  AND t.error_category = 'runner_unavailable'`, true
	case strings.Contains(query, "ReapStaleSessionRuns"):
		return `
UPDATE chetter_session_runs sr
SET status = CASE
      WHEN t.status = 'done' THEN 'completed'
      WHEN t.status = 'cancelled' THEN 'cancelled'
      ELSE 'failed'
    END,
    error = COALESCE(NULLIF(sr.error, ''), t.error, sr.error),
    ended_at = COALESCE(sr.ended_at, t.ended_at, NOW()),
    updated_at = NOW()
FROM chetter_tasks t
WHERE t.id = sr.task_id
  AND sr.status = 'running'
  AND t.status IN ('done', 'error', 'cancelled')`, true
	case strings.Contains(query, "ReapStaleSessionsForTerminalRuns"):
		return `
UPDATE chetter_agent_sessions s
SET status = CASE
      WHEN t.status = 'done' THEN 'completed'
      WHEN t.status = 'cancelled' THEN 'error'
      ELSE 'error'
    END,
    error = COALESCE(NULLIF(s.error, ''), t.error, s.error),
    updated_at = NOW()
FROM chetter_session_runs sr
JOIN chetter_tasks t ON t.id = sr.task_id
WHERE sr.agent_session_id = s.id
  AND s.status = 'running'
  AND sr.status IN ('failed', 'completed', 'cancelled')
  AND t.status IN ('done', 'error', 'cancelled')`, true
	case strings.Contains(query, "RevertOrphanedRunningSessionRuns"):
		return `
UPDATE chetter_session_runs sr
SET status = 'pending',
    started_at = NULL,
    updated_at = NOW()
FROM chetter_tasks t
WHERE t.id = sr.task_id
  AND sr.status = 'running'
  AND t.status = 'pending'`, true
	default:
		return "", false
	}
}

func rebindPostgresPlaceholders(query string) string {
	var b strings.Builder
	b.Grow(len(query) + 16)
	arg := 0
	inQuote := false
	for _, r := range query {
		if r == '\'' {
			inQuote = !inQuote
		}
		if r == '?' && !inQuote {
			arg++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(arg))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func replaceIntervalSeconds(query string) string {
	for {
		start := strings.Index(query, " SECOND)")
		if start < 0 {
			return query
		}
		open := strings.LastIndex(query[:start], "NOW() - (")
		if open < 0 {
			return query
		}
		value := "$" + query[open+len("NOW() - ("):start]
		query = query[:open] + "NOW() - (" + value + " * INTERVAL '1 second')" + query[start+len(" SECOND)"):]
	}
}

func postgresUpsert(query string) string {
	marker := "ON DUPLICATE KEY UPDATE"
	idx := strings.Index(query, marker)
	if idx < 0 {
		return query
	}
	table, target, ok := postgresConflictTarget(query)
	if !ok {
		return query
	}
	updates := postgresExcludedValues(query[idx+len(marker):])
	switch table {
	case "chetter_runners":
		updates = strings.ReplaceAll(updates, "COALESCE(EXCLUDED.started_at, started_at)", "COALESCE(EXCLUDED.started_at, chetter_runners.started_at)")
	case "definition_sources":
		updates = strings.ReplaceAll(updates, "COALESCE(EXCLUDED.last_sync_at, last_sync_at)", "COALESCE(EXCLUDED.last_sync_at, definition_sources.last_sync_at)")
	}
	return query[:idx] + "ON CONFLICT " + target + " DO UPDATE SET" + updates
}

func postgresExcludedValues(query string) string {
	const marker = "VALUES("
	for {
		start := strings.Index(query, marker)
		if start < 0 {
			return query
		}
		end := strings.IndexByte(query[start+len(marker):], ')')
		if end < 0 {
			return query
		}
		end += start + len(marker)
		query = query[:start] + "EXCLUDED." + query[start+len(marker):end] + query[end+1:]
	}
}

func postgresConflictTarget(query string) (string, string, bool) {
	for table, target := range map[string]string{
		"chetter_runners":             "(id)",
		"chetter_triggers":            "(name)",
		"definition_sources":          "(name)",
		"definitions":                 "(source_id, definition_type, name, path)",
		"chetter_model_catalogs":      "(name)",
		"definition_change_proposals": "(repo, pr_number)",
	} {
		if strings.Contains(query, "INSERT INTO "+table) {
			return table, target, true
		}
	}
	return "", "", false
}

// PostgreSQLQuery exposes the dialect conversion for raw SQL callers that do
// not execute through Chetter's database pool.
func PostgreSQLQuery(query string) string { return postgresQuery(query) }
