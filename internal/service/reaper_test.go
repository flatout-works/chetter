package service

import (
	"sync/atomic"
	"testing"
	"time"
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
