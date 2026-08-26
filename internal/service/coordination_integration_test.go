package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/internal/store"
)

func TestTriggerLockDeduplicatesPersistedScheduleInterval(t *testing.T) {
	_, _, tdb, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	firstTick := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)

	finish, err := acquireTriggerLock(ctx, tdb.DB, tdb.Dialect(), "trigger_multi", "@hourly", firstTick)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if finish == nil {
		t.Fatal("first interval was not acquired")
	}
	if err := finish(true); err != nil {
		t.Fatalf("commit first interval: %v", err)
	}

	finish, err = acquireTriggerLock(ctx, tdb.DB, tdb.Dialect(), "trigger_multi", "@hourly", firstTick.Add(time.Second))
	if err != nil {
		t.Fatalf("duplicate acquire: %v", err)
	}
	if finish != nil {
		_ = finish(false)
		t.Fatal("same hourly interval was acquired twice")
	}

	finish, err = acquireTriggerLock(ctx, tdb.DB, tdb.Dialect(), "trigger_multi", "@hourly", firstTick.Add(time.Hour))
	if err != nil {
		t.Fatalf("next acquire: %v", err)
	}
	if finish == nil {
		t.Fatal("next hourly interval was not acquired")
	}
	if err := finish(false); err != nil {
		t.Fatalf("rollback next interval: %v", err)
	}
}

func TestTriggerLockSkipsConcurrentReplica(t *testing.T) {
	_, _, tdb, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	tick := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)

	finish, err := acquireTriggerLock(ctx, tdb.DB, tdb.Dialect(), "trigger_locked", "@hourly", tick)
	if err != nil || finish == nil {
		t.Fatalf("first acquire: finish=%v err=%v", finish != nil, err)
	}
	type result struct {
		finish func(bool) error
		err    error
	}
	resultCh := make(chan result, 1)
	go func() {
		other, err := acquireTriggerLock(ctx, tdb.DB, tdb.Dialect(), "trigger_locked", "@hourly", tick)
		resultCh <- result{finish: other, err: err}
	}()
	time.Sleep(100 * time.Millisecond)
	if err := finish(true); err != nil {
		t.Fatalf("commit first interval: %v", err)
	}

	select {
	case got := <-resultCh:
		if got.err != nil {
			t.Fatalf("concurrent acquire: %v", got.err)
		}
		if got.finish != nil {
			_ = got.finish(false)
			t.Fatal("concurrent replica acquired the same trigger interval")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent trigger lock did not resolve after first commit")
	}
}

func TestTriggerLockFinishIsIdempotent(t *testing.T) {
	_, _, tdb, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	tick := time.Date(2026, 8, 26, 11, 0, 0, 0, time.UTC)

	finish, err := acquireTriggerLock(ctx, tdb.DB, tdb.Dialect(), "trigger_idem", "@hourly", tick)
	if err != nil || finish == nil {
		t.Fatalf("acquire: finish=%v err=%v", finish != nil, err)
	}
	if err := finish(true); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// The deferred rollback-style call after an explicit commit must be a
	// no-op, not a double-commit or error.
	if err := finish(false); err != nil {
		t.Fatalf("second finish call must be a no-op, got: %v", err)
	}
	if err := finish(true); err != nil {
		t.Fatalf("third finish call must be a no-op, got: %v", err)
	}

	// Rollback variant: repeated finish(false) is also safe.
	finish, err = acquireTriggerLock(ctx, tdb.DB, tdb.Dialect(), "trigger_idem2", "@hourly", tick)
	if err != nil || finish == nil {
		t.Fatalf("second acquire: finish=%v err=%v", finish != nil, err)
	}
	if err := finish(false); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := finish(false); err != nil {
		t.Fatalf("repeated rollback must be a no-op, got: %v", err)
	}
}

func TestIsLockWaitTimeout(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("some other error"), false},
		{errors.New("Error 1205 (HY000): Lock wait timeout exceeded; try restarting transaction"), true},
		{errors.New("Error 1213 (40001): Deadlock found when trying to get lock; try restarting transaction"), true},
		{errors.New("pq: deadlock detected"), true},
		{errors.New("pq: canceling statement due to lock timeout"), true},
	}
	for _, tc := range cases {
		if got := isLockWaitTimeout(tc.err); got != tc.want {
			t.Errorf("isLockWaitTimeout(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// TestRunnerCommandsDegradesOnDrainConsumeError verifies the heartbeat drain
// path degrades gracefully when checking the durable drain request fails:
// the heartbeat must not error (it renews leases), and the drain command is
// simply retried on the next heartbeat since the request row is durable.
func TestRunnerCommandsDegradesOnDrainConsumeError(t *testing.T) {
	closed, err := sql.Open("mysql", "root@tcp(127.0.0.1:1)/?parseTime=true")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := closed.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	rpc := NewRunnerRPCService(nil, closed, store.DialectTiDB)
	commands, err := rpc.runnerCommands(context.Background(), &runnerv1.RunnerInfo{RunnerId: "runner_degraded"})
	if err != nil {
		t.Fatalf("runnerCommands must degrade on drain-consume errors, got: %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("expected no commands on degraded drain consume, got %d", len(commands))
	}
}
