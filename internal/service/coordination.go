package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/flatout-works/chetter/internal/store"
)

// ---------------------------------------------------------------------------
// Tier 1: Claim notification counter (cross-replica claim wake-up)
// ---------------------------------------------------------------------------

// bumpClaimNotifyCounter atomically increments the claim_notify_counter row.
// Called after any task becomes claimable so idle ClaimTask long-polls on
// other replicas re-check the queue without waiting for the safety-net poll.
func bumpClaimNotifyCounter(ctx context.Context, db *sql.DB, dialect store.Dialect) {
	if db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	query := sqlQuery(dialect, `UPDATE claim_notify_counter SET counter = counter + 1 WHERE id = 1`)
	if _, err := db.ExecContext(ctx, query); err != nil && ctx.Err() == nil {
		slog.Warn("bump claim notify counter", "err", err)
	}
}

// getClaimNotifyCounter reads the current claim notification counter value.
// Returns 0 if the table or row does not exist yet (pre-migration deployments).
func getClaimNotifyCounter(ctx context.Context, db *sql.DB, dialect store.Dialect) (int64, error) {
	query := sqlQuery(dialect, `SELECT counter FROM claim_notify_counter WHERE id = 1`)
	var counter int64
	err := db.QueryRowContext(ctx, query).Scan(&counter)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get claim notify counter: %w", err)
	}
	return counter, nil
}

// ---------------------------------------------------------------------------
// Tier 2: Trigger lock (prevents duplicate cron execution across replicas)
// ---------------------------------------------------------------------------

// acquireTriggerLock claims one scheduled interval for a trigger using
// SELECT FOR UPDATE. The persisted last_triggered_at value prevents a
// slightly later replica from running the same interval after the first
// replica has already released its row lock.
//
// Delivery is at-least-once: if the winning replica crashes between
// committing its trigger task and committing the interval marker, the marker
// rollback leaves the interval unclaimed and a later tick fires it again.
// Tasks themselves are idempotent-safe to resubmit at this frequency, and
// at-least-once is the safe default for a scheduler.
//
// The returned finish function commits the interval marker when successful
// and rolls it back when unsuccessful; it is idempotent, so the deferred
// release and an explicit commit can both call it safely. A nil finish means
// another replica is running this trigger or already ran the current
// interval.
func acquireTriggerLock(ctx context.Context, db *sql.DB, dialect store.Dialect, triggerID, cronExpr string, triggeredAt time.Time) (finish func(bool) error, err error) {
	// Avoid an upsert against an existing row: on MySQL/TiDB an upsert would
	// unnecessarily wait behind a concurrently held trigger row lock.
	existsSQL := sqlQuery(dialect, `SELECT 1 FROM trigger_locks WHERE trigger_id = ?`)
	var exists int
	err = db.QueryRowContext(ctx, existsSQL, triggerID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		ensureSQL := sqlQuery(dialect, `INSERT IGNORE INTO trigger_locks (trigger_id, created_at) VALUES (?, ?)`)
		if dialect == store.DialectPostgres {
			ensureSQL = `INSERT INTO trigger_locks (trigger_id, created_at) VALUES ($1, $2) ON CONFLICT (trigger_id) DO NOTHING`
		}
		if _, err = db.ExecContext(ctx, ensureSQL, triggerID, time.Now().UTC()); err != nil {
			return nil, fmt.Errorf("ensure trigger lock row: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("check trigger lock row: %w", err)
	}

	// Begin a transaction and try to lock the row. The transaction stays
	// open until the returned release function is called.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin trigger lock tx: %w", err)
	}

	lockSQL := sqlQuery(dialect, `SELECT last_triggered_at FROM trigger_locks WHERE trigger_id = ? FOR UPDATE`)
	var lastTriggeredAt sql.NullTime
	err = tx.QueryRowContext(ctx, lockSQL, triggerID).Scan(&lastTriggeredAt)
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		return nil, nil
	}
	if isLockWaitTimeout(err) {
		// Another replica holds the lock longer than the DB lock-wait
		// timeout (e.g. its submission transaction is slow). The interval is
		// being handled, so skip rather than surface a trigger error.
		_ = tx.Rollback()
		slog.Debug("trigger lock wait timeout; interval handled elsewhere", "trigger_id", triggerID)
		return nil, nil
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("acquire trigger lock: %w", err)
	}

	schedule, err := defaultCronParser.Parse(cronExpr)
	if err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("parse trigger schedule: %w", err)
	}
	if lastTriggeredAt.Valid && triggeredAt.Before(schedule.Next(lastTriggeredAt.Time)) {
		_ = tx.Rollback()
		return nil, nil
	}
	updateSQL := sqlQuery(dialect, `UPDATE trigger_locks SET last_triggered_at = ? WHERE trigger_id = ?`)
	if _, err := tx.ExecContext(ctx, updateSQL, triggeredAt, triggerID); err != nil {
		if isLockWaitTimeout(err) {
			_ = tx.Rollback()
			slog.Debug("trigger lock wait timeout on interval mark; interval handled elsewhere", "trigger_id", triggerID)
			return nil, nil
		}
		_ = tx.Rollback()
		return nil, fmt.Errorf("mark trigger interval: %w", err)
	}

	// finish is idempotent: whichever call happens first (an explicit commit
	// or the deferred rollback) ends the transaction; later calls are no-ops.
	var finished bool
	return func(success bool) error {
		if finished {
			return nil
		}
		finished = true
		if !success {
			return tx.Rollback()
		}
		return tx.Commit()
	}, nil
}

// isLockWaitTimeout reports whether err is a row-lock wait timeout or
// deadlock error from any supported dialect: MySQL/TiDB surfaces lock-wait
// timeouts as error 1205 ("Lock wait timeout exceeded") and deadlocks as
// 1213 ("Deadlock found when trying to get lock"); PostgreSQL reports
// "deadlock detected" (40P01) or a lock-timeout cancellation. Matching on
// the message keeps this driver- and dialect-agnostic.
func isLockWaitTimeout(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Lock wait timeout exceeded") ||
		strings.Contains(msg, "Error 1205") ||
		strings.Contains(msg, "Deadlock found") ||
		strings.Contains(msg, "deadlock detected") ||
		strings.Contains(msg, "lock timeout")
}

// ---------------------------------------------------------------------------
// Tier 3: Runner drain requests (cross-replica drain queue)
// ---------------------------------------------------------------------------

// drainRequestTTL bounds how long an unacknowledged drain request is
// re-delivered. Beyond it the request is dropped: a runner that never
// acknowledged is likely gone, and an operator should re-request rather
// than have a stale drain resurrect later. It comfortably exceeds the
// runner's drain deadline plus hard-kill timeout.
const drainRequestTTL = 30 * time.Minute

// requestRunnerDrainDB inserts a drain request for the given runner. Any
// replica's heartbeat handler will deliver it.
func requestRunnerDrainDB(ctx context.Context, db *sql.DB, dialect store.Dialect, runnerID string) error {
	query := sqlQuery(dialect, `INSERT IGNORE INTO runner_drain_requests (runner_id, created_at) VALUES (?, ?)`)
	if dialect == store.DialectPostgres {
		query = `INSERT INTO runner_drain_requests (runner_id, created_at) VALUES ($1, $2) ON CONFLICT (runner_id) DO NOTHING`
	}
	_, err := db.ExecContext(ctx, query, runnerID, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("request runner drain: %w", err)
	}
	return nil
}

// peekRunnerDrainDB reports whether a pending drain request exists for the
// given runner. The row is intentionally left in place so the drain command
// is delivered at-least-once: if a heartbeat response carrying the command is
// lost, the next heartbeat re-delivers it. The runner acknowledges by
// reporting a draining status, at which point ackRunnerDrainDB removes the
// row. Requests older than drainRequestTTL are dropped.
func peekRunnerDrainDB(ctx context.Context, db *sql.DB, dialect store.Dialect, runnerID string) (bool, error) {
	query := sqlQuery(dialect, `SELECT created_at FROM runner_drain_requests WHERE runner_id = ?`)
	var createdAt time.Time
	err := db.QueryRowContext(ctx, query, runnerID).Scan(&createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("peek runner drain request: %w", err)
	}
	if time.Since(createdAt) > drainRequestTTL {
		if _, err := db.ExecContext(ctx, sqlQuery(dialect, `DELETE FROM runner_drain_requests WHERE runner_id = ?`), runnerID); err != nil {
			return false, fmt.Errorf("drop stale runner drain request: %w", err)
		}
		return false, nil
	}
	return true, nil
}

// ackRunnerDrainDB removes the drain request after the runner acknowledged it
// by reporting a draining status.
func ackRunnerDrainDB(ctx context.Context, db *sql.DB, dialect store.Dialect, runnerID string) error {
	query := sqlQuery(dialect, `DELETE FROM runner_drain_requests WHERE runner_id = ?`)
	if _, err := db.ExecContext(ctx, query, runnerID); err != nil {
		return fmt.Errorf("ack runner drain request: %w", err)
	}
	return nil
}
