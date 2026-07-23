package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/config"
)

func TestRunReaperCycleRecoversFromPanicAndContinues(t *testing.T) {
	var calls atomic.Int32
	s := &Service{}
	s.reaperSteps = []func(){
		func() {
			if calls.Add(1) == 1 {
				panic("simulated reaper panic")
			}
		},
	}

	if !s.LastReapAt().IsZero() {
		t.Fatalf("lastReapAt should be zero before any cycle")
	}

	s.runReaperCycle()
	if calls.Load() != 1 {
		t.Fatalf("expected first cycle to run the step once, got %d", calls.Load())
	}
	if !s.LastReapAt().IsZero() {
		t.Fatalf("lastReapAt must not advance after a panicking cycle")
	}

	s.runReaperCycle()
	if calls.Load() != 2 {
		t.Fatalf("expected second cycle to run the step again, got %d", calls.Load())
	}
	if s.LastReapAt().IsZero() {
		t.Fatalf("lastReapAt must advance after a successful cycle")
	}
}

func TestTaskReaperSurvivesInitialPanic(t *testing.T) {
	var calls atomic.Int32
	ran := make(chan struct{})
	s := &Service{reaperStop: make(chan struct{})}
	s.reaperSteps = []func(){
		func() {
			if calls.Add(1) == 1 {
				close(ran)
				panic("simulated reaper panic")
			}
		},
	}

	done := make(chan struct{})
	go func() {
		s.taskReaper()
		close(done)
	}()

	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatalf("reaper initial cycle did not run")
	}

	close(s.reaperStop)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("taskReaper did not exit after reaperStop")
	}

	if calls.Load() != 1 {
		t.Fatalf("expected exactly one cycle before stop, got %d", calls.Load())
	}
	if !s.LastReapAt().IsZero() {
		t.Fatalf("lastReapAt should remain zero because the only cycle panicked")
	}
}

// TestRunnerIsDeliberatelyDrained verifies the drain-state check used by the
// reaper to skip auto-recovery for deliberately drained runners. Without a
// database, the query fails and returns false (safe default — requeue). See
// issue #96 criterion 5.
func TestRunnerIsDeliberatelyDrained(t *testing.T) {
	s := &Service{}
	// No DB connected — query returns error, so the helper returns false
	// (safe default: treat as not deliberately drained, so the task is
	// auto-recovered rather than silently left failed).
	if s.runnerIsDeliberatelyDrained(context.Background(), "runner-1") {
		t.Fatal("runnerIsDeliberatelyDrained should return false when DB is unavailable")
	}
	if s.runnerIsDeliberatelyDrained(context.Background(), "") {
		t.Fatal("runnerIsDeliberatelyDrained should return false for empty runner ID")
	}
}

// TestAutoRecoveryConfig verifies the opt-out mechanism is wired correctly.
// When DEFAULT_AUTO_RECOVERY=false, the reaper should skip the requeue loop
// so all expired leases are marked failed. See issue #96 criterion 4.
func TestAutoRecoveryConfig(t *testing.T) {
	t.Setenv("DEFAULT_AUTO_RECOVERY", "false")
	cfg := config.Load()
	if cfg.AutoRecovery {
		t.Fatal("AutoRecovery should be false when DEFAULT_AUTO_RECOVERY=false")
	}

	// Default should be true (auto-recovery enabled).
	t.Setenv("DEFAULT_AUTO_RECOVERY", "")
	cfg = config.Load()
	if !cfg.AutoRecovery {
		t.Fatal("AutoRecovery should default to true")
	}
}
