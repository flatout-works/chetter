package controller

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/runner/internal/config"
)

// helper constructors for simulated snapshots.
func snapshotWithFreeMemory(mb int64) resourceSnapshot {
	avail := mb << 20
	return resourceSnapshot{MemoryAvailableBytes: &avail}
}

func snapshotWithLoad(load float64) resourceSnapshot {
	return resourceSnapshot{Load1: &load}
}

// TestAdmissionStatusFitsRunnersStatusColumn guards the admission-paused
// status values against the server-side runners.status column (VARCHAR(32)):
// the runner heartbeat status is persisted verbatim, so an overlong value
// would be truncated on the server.
func TestAdmissionStatusFitsRunnersStatusColumn(t *testing.T) {
	for _, status := range []string{admissionPausedMemory, admissionPausedLoad} {
		if len(status) > 32 {
			t.Errorf("admission status %q is %d bytes; runners.status is VARCHAR(32)", status, len(status))
		}
	}
}

func TestAdmissionPauseReason(t *testing.T) {
	const minFreeMB = 1024
	tests := []struct {
		name      string
		snap      resourceSnapshot
		minFreeMB int
		maxLoad   float64
		want      string
	}{
		{name: "disabled memory gate claims normally", snap: snapshotWithFreeMemory(64), minFreeMB: 0, maxLoad: 0, want: ""},
		{name: "disabled load gate claims normally", snap: snapshotWithLoad(999), minFreeMB: minFreeMB, maxLoad: 0, want: ""},
		{name: "nil metrics never pause", snap: resourceSnapshot{}, minFreeMB: minFreeMB, maxLoad: 4, want: ""},
		{name: "plenty of free memory claims normally", snap: snapshotWithFreeMemory(4096), minFreeMB: minFreeMB, maxLoad: 0, want: ""},
		{name: "at threshold claims normally", snap: snapshotWithFreeMemory(minFreeMB), minFreeMB: minFreeMB, maxLoad: 0, want: ""},
		{name: "below threshold pauses for memory", snap: snapshotWithFreeMemory(minFreeMB - 1), minFreeMB: minFreeMB, maxLoad: 0, want: admissionPausedMemory},
		{name: "load at threshold claims normally", snap: snapshotWithLoad(4), minFreeMB: 0, maxLoad: 4, want: ""},
		{name: "load above threshold pauses for load", snap: snapshotWithLoad(4.5), minFreeMB: 0, maxLoad: 4, want: admissionPausedLoad},
		{name: "memory pressure wins over load", snap: func() resourceSnapshot {
			s := snapshotWithFreeMemory(64)
			load := 99.0
			s.Load1 = &load
			return s
		}(), minFreeMB: minFreeMB, maxLoad: 4, want: admissionPausedMemory},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := admissionPauseReason(tc.snap, tc.minFreeMB, tc.maxLoad); got != tc.want {
				t.Errorf("admissionPauseReason() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHeartbeatStatusReflectsHostPressure(t *testing.T) {
	lowMem := snapshotWithFreeMemory(256)
	healthy := snapshotWithFreeMemory(8192)
	heavyLoad := snapshotWithLoad(64)

	tests := []struct {
		name      string
		sampler   func() resourceSnapshot
		minFreeMB int
		maxLoad   float64
		draining  bool
		want      string
	}{
		{name: "healthy host reports active", sampler: func() resourceSnapshot { return healthy }, minFreeMB: 1024, want: "active"},
		{name: "low memory reports memory pressure pause", sampler: func() resourceSnapshot { return lowMem }, minFreeMB: 1024, want: admissionPausedMemory},
		{name: "high load reports load pause", sampler: func() resourceSnapshot { return heavyLoad }, minFreeMB: 1024, maxLoad: 8, want: admissionPausedLoad},
		{name: "draining overrides pause", sampler: func() resourceSnapshot { return lowMem }, minFreeMB: 1024, draining: true, want: "draining"},
		{name: "disabled gate reports active under pressure", sampler: func() resourceSnapshot { return lowMem }, minFreeMB: 0, want: "active"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &Runner{
				cfg: &config.Config{Runner: config.RunnerConfig{
					MinFreeHostMemoryMB: tc.minFreeMB,
					MaxHostLoad:         tc.maxLoad,
				}},
				hostSampler: tc.sampler,
			}
			if tc.draining {
				r.draining.Store(true)
			}
			if got := r.heartbeatStatus(); got != tc.want {
				t.Errorf("heartbeatStatus() = %q, want %q", got, tc.want)
			}
		})
	}
}

// mockClaimClient records ClaimTask invocations and always returns an empty
// poll result immediately, so the claim loop spins without ever claiming a
// real task.
type mockClaimClient struct {
	runnerRPCClient
	claims atomic.Int64
}

func (m *mockClaimClient) ClaimTask(_ context.Context, _ *connect.Request[runnerv1.ClaimTaskRequest]) (*connect.Response[runnerv1.ClaimTaskResponse], error) {
	m.claims.Add(1)
	return connect.NewResponse(&runnerv1.ClaimTaskResponse{}), nil
}

func TestClaimLoopBacksOffUnderHostPressureAndRecovers(t *testing.T) {
	oldBackoff := claimAdmissionBackoff
	claimAdmissionBackoff = 10 * time.Millisecond
	defer func() { claimAdmissionBackoff = oldBackoff }()

	state := &atomic.Value{}
	state.Store(snapshotWithFreeMemory(256)) // start under memory pressure

	r := &Runner{
		cfg: &config.Config{Runner: config.RunnerConfig{
			MaxConcurrent:       2,
			MinFreeHostMemoryMB: 1024,
		}},
		runnerID:    "runner-admission-test",
		claimClient: &mockClaimClient{},
		sem:         make(chan struct{}, 2+1),
		hostSampler: func() resourceSnapshot { return state.Load().(resourceSnapshot) },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.claimLoop(ctx)

	// Under memory pressure the loop must not call ClaimTask.
	time.Sleep(80 * time.Millisecond)
	client := r.claimClient.(*mockClaimClient)
	if got := client.claims.Load(); got != 0 {
		t.Fatalf("ClaimTask called %d times while host under memory pressure; want 0", got)
	}
	if reason, _ := r.lastAdmissionPause.Load().(string); reason != admissionPausedMemory {
		t.Fatalf("lastAdmissionPause = %q, want %q", reason, admissionPausedMemory)
	}

	// Clear the pressure: the loop resumes claiming.
	state.Store(snapshotWithFreeMemory(8192))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if client.claims.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := client.claims.Load(); got == 0 {
		t.Fatal("ClaimTask was not called after host pressure cleared")
	}
	if reason, _ := r.lastAdmissionPause.Load().(string); reason != "" {
		t.Fatalf("lastAdmissionPause = %q after recovery, want \"\"", reason)
	}
}

func TestClaimLoopDisabledGateRetainsTodayBehavior(t *testing.T) {
	oldBackoff := claimAdmissionBackoff
	claimAdmissionBackoff = 10 * time.Millisecond
	defer func() { claimAdmissionBackoff = oldBackoff }()

	// Gate disabled (0): even a simulated low-memory host must not stop
	// claiming — this preserves the pre-#397 behavior for operators who opt
	// out.
	r := &Runner{
		cfg: &config.Config{Runner: config.RunnerConfig{
			MaxConcurrent:       2,
			MinFreeHostMemoryMB: 0, // disabled
		}},
		runnerID:    "runner-admission-disabled",
		claimClient: &mockClaimClient{},
		sem:         make(chan struct{}, 2+1),
		hostSampler: func() resourceSnapshot { return snapshotWithFreeMemory(64) },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.claimLoop(ctx)

	client := r.claimClient.(*mockClaimClient)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if client.claims.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := client.claims.Load(); got == 0 {
		t.Fatal("ClaimTask was not called with the gate disabled")
	}
}
