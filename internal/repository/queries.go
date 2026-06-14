package repository

import "database/sql"

// DB returns the underlying database handle when the Queries was constructed
// with a *sql.DB. It returns nil for transactions or any other DBTX
// implementation. This file is hand-written so the sqlc generator can
// overwrite db.go without losing the helper.
func (q *Queries) DB() *sql.DB {
	if db, ok := q.db.(*sql.DB); ok {
		return db
	}
	return nil
}
