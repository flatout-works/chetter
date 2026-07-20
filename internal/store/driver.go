package store

import _ "github.com/jackc/pgx/v5/stdlib"

// DriverName returns the database/sql driver name for a dialect.
func DriverName(dialect Dialect) string {
	if dialect == DialectPostgres {
		return "pgx"
	}
	return "mysql"
}
