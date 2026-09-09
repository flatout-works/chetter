package controller

import (
	"log/slog"
	"time"
)

// Host-pressure admission states surfaced in the runner's heartbeat status
// (issue #397). Operators use them to distinguish a runner that is
// deliberately shedding load ("admission paused: ...") from a wedged runner
// that silently stopped claiming. The values must fit the server's
// runners.status column (VARCHAR(32)).
const (
	admissionPausedMemory = "admission_paused:memory_pressure"
	admissionPausedLoad   = "admission_paused:host_load"
)

// claimAdmissionBackoff is how long the claim loop sleeps before re-evaluating
// the host-pressure gate after a pause. A package variable so tests can shrink
// it; production uses the 5s default.
var claimAdmissionBackoff = 5 * time.Second

// admissionPauseReason evaluates the host-pressure gate: it returns the
// admission-paused status to report when the runner must stop claiming new
// tasks, or "" when claiming is allowed. The gate is a dynamic ceiling on top
// of RUNNER_MAX_CONCURRENT — it never replaces it: a healthy host keeps its
// full configured concurrency, and only a host that is actually low on free
// memory or (opt-in) overloaded backs off. Free memory is checked first and
// wins when both thresholds trip, because memory exhaustion is the failure
// mode that deepens into host-wide thrashing. Thresholds of 0 disable their
// respective check.
//
// In-flight tasks are never touched: the gate only prevents new claims, so a
// runner already executing tasks under sustained host pressure finishes them
// before it can claim again (no premature lease drops or cancellations).
func admissionPauseReason(snap resourceSnapshot, minFreeMemoryMB int, maxHostLoad float64) string {
	if minFreeMemoryMB > 0 && snap.MemoryAvailableBytes != nil {
		freeMB := *snap.MemoryAvailableBytes / (1 << 20)
		if freeMB < int64(minFreeMemoryMB) {
			return admissionPausedMemory
		}
	}
	if maxHostLoad > 0 && snap.Load1 != nil && *snap.Load1 > maxHostLoad {
		return admissionPausedLoad
	}
	return ""
}

// sampleHost returns the latest host resource snapshot. Tests inject a fake
// sampler via Runner.hostSampler to simulate a low-memory or overloaded host;
// nil (the production default) samples /proc directly.
func (r *Runner) sampleHost() resourceSnapshot {
	if r.hostSampler != nil {
		return r.hostSampler()
	}
	return collectResourceSnapshot()
}

// heartbeatStatus derives the status published on each heartbeat tick:
// "draining" while draining, the admission-paused reason while the
// host-pressure gate is tripped, and "active" otherwise. Surfacing the pause
// in the heartbeat is what lets operators tell deliberate load shedding apart
// from a wedged runner that is silently not claiming.
func (r *Runner) heartbeatStatus() string {
	if r.draining.Load() {
		return "draining"
	}
	if reason := admissionPauseReason(r.sampleHost(), r.cfg.Runner.MinFreeHostMemoryMB, r.cfg.Runner.MaxHostLoad); reason != "" {
		return reason
	}
	return "active"
}

// noteAdmissionTransition logs claim-loop pause/resume transitions exactly
// once per state change so a host under sustained pressure does not spam the
// logs every backoff interval.
func (r *Runner) noteAdmissionTransition(reason string) {
	prev, _ := r.lastAdmissionPause.Load().(string)
	if prev == reason {
		return
	}
	r.lastAdmissionPause.Store(reason)
	if reason == "" {
		slog.Info("host pressure cleared; resuming task claims", "runner_id", r.runnerID)
		return
	}
	slog.Warn("host pressure detected; pausing task claims", "runner_id", r.runnerID, "reason", reason)
}
