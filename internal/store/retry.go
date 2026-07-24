package store

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

// transientBackoff defines the exponential backoff steps for retryable
// connection-level transient errors: 100ms → 500ms → 1s → 2s → 5s.
var transientBackoff = []time.Duration{
	100 * time.Millisecond,
	500 * time.Millisecond,
	1 * time.Second,
	2 * time.Second,
	5 * time.Second,
}

const transientMaxRetries = 3

// IsTransientError reports whether err indicates a temporary database
// unavailability that is likely to self-heal — for example a connection
// refused during leader re-election, a broken-pipe after keepalive loss,
// or a TiDB "information schema is changed" error. Non-transient errors
// (syntax errors, constraint violations, auth failures) return false and
// must propagate immediately.
func IsTransientError(err error) bool {
	if err == nil {
		return false
	}

	// Connection-level transport errors that happen before the query
	// reaches the database.
	if isNetworkError(err) {
		return true
	}

	// MySQL / TiDB error codes.
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		switch mysqlErr.Number {
		case 1205: // Lock wait timeout exceeded — transient contention
			return true
		case 1213: // Deadlock found when trying to get lock
			return true
		case 2002: // Can't connect to local MySQL server through socket
			return true
		case 2003: // Can't connect to MySQL server
			return true
		case 2006: // MySQL server has gone away
			return true
		case 2013: // Lost connection to MySQL server during query
			return true
		case 8028: // TiDB: information schema is changed, retry
			return true
		case 9007: // TiDB: write conflict
			return true
		}
	}

	// PostgreSQL error codes.
	var postgresErr *pgconn.PgError
	if errors.As(err, &postgresErr) {
		switch postgresErr.Code {
		case "08000": // connection exception
			return true
		case "08001": // sqlclient unable to establish sqlconnection
			return true
		case "08003": // connection does not exist
			return true
		case "08004": // sqlserver rejected establishment of sqlconnection
			return true
		case "08006": // connection failure
			return true
		case "08007": // transaction resolution unknown
			return true
		case "40001": // serialization failure
			return true
		case "40P01": // deadlock detected
			return true
		case "53P00": // insufficient resources
			return true
		case "55P03": // lock not available
			return true
		case "57P01": // admin shutdown
			return true
		case "57P02": // crash shutdown
			return true
		case "57P03": // cannot connect now
			return true
		}
	}

	// String-based fallback for errors that aren't typed.
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "try again later") ||
		strings.Contains(msg, "Deadlock") ||
		strings.Contains(msg, "Lock wait timeout") ||
		strings.Contains(msg, "Write conflict")
}

// isNetworkError returns true when err is a low-level network/transport
// error that occurs before the SQL protocol layer.
func isNetworkError(err error) bool {
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return true
	}
	// syscall errors like ECONNREFUSED, EPIPE, ECONNRESET.
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "connect:") && (strings.Contains(msg, "connection refused") || strings.Contains(msg, "no route to host"))
}

// RetryTransient calls fn up to 1 + transientMaxRetries times with
// exponential backoff when fn returns a transient error. Non-transient
// errors are returned immediately. Context cancellation is checked before
// each attempt. Returns the last transient error if all retries are
// exhausted.
func RetryTransient(ctx context.Context, fn func() error) error {
	return retryTransient(ctx, fn, transientMaxRetries)
}

// RetryTransientWithMax is like RetryTransient but allows the caller to
// override the maximum number of retries. Used for startup retry with
// unlimited attempts within a deadline.
func RetryTransientWithMax(ctx context.Context, fn func() error, maxAttempts int) error {
	return retryTransient(ctx, fn, maxAttempts)
}

func retryTransient(ctx context.Context, fn func() error, maxRetries int) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		err := fn()
		if err == nil {
			if attempt > 0 {
				slog.Info("database operation recovered after transient error",
					"attempt", attempt, "totalAttempts", attempt+1)
			}
			return nil
		}

		if !IsTransientError(err) {
			return err
		}

		lastErr = err
		if attempt >= maxRetries {
			break
		}

		delay := transientBackoff[min(attempt, len(transientBackoff)-1)]
		slog.Warn("database transient error, retrying",
			"attempt", attempt+1,
			"maxRetries", maxRetries,
			"delay", delay,
			"error", err,
		)

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return lastErr
}
