package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/flatout-works/chetter/internal/repository"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

const txMaxAttempts = 3

func withTxRetry(ctx context.Context, db *sql.DB, fn func(*repository.Queries) error) error {
	var lastErr error
	for attempt := 0; attempt < txMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}

		err = fn(repository.New(tx))
		if err != nil {
			_ = tx.Rollback()
			if isRetryableTxError(err) && attempt < txMaxAttempts-1 {
				lastErr = err
				time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
				continue
			}
			return err
		}

		if err := tx.Commit(); err != nil {
			// A commit error can be ambiguous: the transaction may have committed
			// even though the client did not receive the result. Retrying a
			// non-idempotent transaction such as task claiming can double-claim work.
			return err
		}
		return nil
	}
	return lastErr
}

func isRetryableTxError(err error) bool {
	if err == nil {
		return false
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		switch mysqlErr.Number {
		case 1205, 1213, 8028, 9007:
			return true
		}
	}
	var postgresErr *pgconn.PgError
	if errors.As(err, &postgresErr) {
		switch postgresErr.Code {
		case "40001", "40P01", "55P03":
			return true
		}
	}
	msg := err.Error()
	return strings.Contains(msg, "Deadlock") ||
		strings.Contains(msg, "Lock wait timeout") ||
		strings.Contains(msg, "Write conflict") ||
		strings.Contains(msg, "try again later")
}
