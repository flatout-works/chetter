package store

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsTransientError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		transient bool
	}{
		// Transient network errors.
		{name: "connection refused syscall", err: syscall.ECONNREFUSED, transient: true},
		{name: "broken pipe syscall", err: syscall.EPIPE, transient: true},
		{name: "connection reset syscall", err: syscall.ECONNRESET, transient: true},
		{
			name:      "net.OpError",
			err:       &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
			transient: true,
		},
		{
			name:      "wrapped network error",
			err:       fmt.Errorf("ping: %w", &net.OpError{Op: "read", Net: "tcp", Err: errors.New("i/o timeout")}),
			transient: true,
		},

		// MySQL transient error codes.
		{name: "mysql lock wait timeout", err: &mysql.MySQLError{Number: 1205, Message: "Lock wait timeout"}, transient: true},
		{name: "mysql deadlock", err: &mysql.MySQLError{Number: 1213, Message: "Deadlock"}, transient: true},
		{name: "mysql can't connect socket", err: &mysql.MySQLError{Number: 2002, Message: "Can't connect"}, transient: true},
		{name: "mysql can't connect tcp", err: &mysql.MySQLError{Number: 2003, Message: "Can't connect"}, transient: true},
		{name: "mysql server gone away", err: &mysql.MySQLError{Number: 2006, Message: "Gone away"}, transient: true},
		{name: "mysql lost connection", err: &mysql.MySQLError{Number: 2013, Message: "Lost connection"}, transient: true},
		{name: "tidb info schema changed", err: &mysql.MySQLError{Number: 8028, Message: "Info schema changed"}, transient: true},
		{name: "tidb write conflict", err: &mysql.MySQLError{Number: 9007, Message: "Write conflict"}, transient: true},

		// PostgreSQL transient error codes.
		{name: "pg connection exception", err: &pgconn.PgError{Code: "08000", Message: "connection exception"}, transient: true},
		{name: "pg connection failure", err: &pgconn.PgError{Code: "08006", Message: "connection failure"}, transient: true},
		{name: "pg serialization failure", err: &pgconn.PgError{Code: "40001", Message: "serialization failure"}, transient: true},
		{name: "pg deadlock detected", err: &pgconn.PgError{Code: "40P01", Message: "deadlock detected"}, transient: true},
		{name: "pg lock not available", err: &pgconn.PgError{Code: "55P03", Message: "lock not available"}, transient: true},
		{name: "pg admin shutdown", err: &pgconn.PgError{Code: "57P01", Message: "admin shutdown"}, transient: true},
		{name: "pg crash shutdown", err: &pgconn.PgError{Code: "57P02", Message: "crash shutdown"}, transient: true},
		{name: "pg cannot connect now", err: &pgconn.PgError{Code: "57P03", Message: "cannot connect now"}, transient: true},

		// String-based transient fallbacks.
		{name: "connection refused string", err: errors.New("dial tcp: connection refused"), transient: true},
		{name: "broken pipe string", err: errors.New("write pipe: broken pipe"), transient: true},
		{name: "i/o timeout", err: errors.New("read tcp: i/o timeout"), transient: true},

		// Non-transient errors — must not be retried.
		{name: "nil", err: nil, transient: false},
		{name: "mysql syntax error", err: &mysql.MySQLError{Number: 1064, Message: "Syntax error"}, transient: false},
		{name: "mysql auth failed", err: &mysql.MySQLError{Number: 1045, Message: "Access denied"}, transient: false},
		{name: "mysql constraint violation", err: &mysql.MySQLError{Number: 1062, Message: "Duplicate entry"}, transient: false},
		{name: "pg syntax error", err: &pgconn.PgError{Code: "42601", Message: "syntax error"}, transient: false},
		{name: "pg auth failed", err: &pgconn.PgError{Code: "28P01", Message: "invalid password"}, transient: false},
		{name: "pg unique violation", err: &pgconn.PgError{Code: "23505", Message: "duplicate key"}, transient: false},
		{name: "generic error", err: errors.New("something went wrong"), transient: false},
		{name: "wrapped non-transient", err: fmt.Errorf("query: %w", errors.New("something")), transient: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := IsTransientError(tt.err)
			if got != tt.transient {
				t.Errorf("IsTransientError(%v) = %v, want %v", tt.err, got, tt.transient)
			}
		})
	}
}

func TestRetryTransient_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var calls int
	err := RetryTransient(ctx, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestRetryTransient_NonTransient(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	testErr := errors.New("syntax error")
	var calls int
	err := RetryTransient(ctx, func() error {
		calls++
		return testErr
	})
	if err != testErr {
		t.Fatalf("expected %v, got %v", testErr, err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call (no retry for non-transient), got %d", calls)
	}
}

func TestRetryTransient_TransientSucceedsAfterRetries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	transientErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	var calls int
	err := RetryTransient(ctx, func() error {
		calls++
		if calls <= 2 {
			return transientErr
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls (2 transient + 1 success), got %d", calls)
	}
}

func TestRetryTransient_ExhaustedRetries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	transientErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}

	// Use a shorter max retries for testing to avoid the full backoff.
	done := make(chan error, 1)
	go func() {
		done <- RetryTransientWithMax(ctx, func() error {
			return transientErr
		}, 1) // 1 initial attempt + 1 retry
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !IsTransientError(err) {
			t.Fatalf("expected transient error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for retries to exhaust")
	}
}

func TestRetryTransient_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	transientErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}

	// Cancel after the first call so it aborts during the backoff sleep.
	var calls int
	done := make(chan error, 1)
	go func() {
		done <- RetryTransientWithMax(ctx, func() error {
			calls++
			if calls == 1 {
				cancel()
			}
			return transientErr
		}, 3)
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for context cancellation")
	}
}

func TestRetryTransientWithMax_ZeroRetries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	transientErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	err := RetryTransientWithMax(ctx, func() error {
		return transientErr
	}, 0)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !IsTransientError(err) {
		t.Fatalf("expected transient error, got %v", err)
	}
}
