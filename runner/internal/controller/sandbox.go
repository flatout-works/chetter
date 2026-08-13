package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// This file implements runtime sandbox monitoring for isolated (gVisor/runsc)
// executions. Once a task is admitted to a sandboxed runner the control plane
// previously had no visibility into the sandbox runtime itself: runsc
// start/teardown failures and in-sandbox resource pressure were
// indistinguishable from ordinary agent failures. The helpers here collect
// per-sandbox metrics (start latency, lifetime, peak RSS/CPU, teardown
// reason), classify sandbox lifecycle failures as distinct error categories
// (sandbox_start_failed, sandbox_crashed), and probe runsc availability so
// fleet health surfaces sandbox drift rather than only task staleness. See
// issue #302.

// sandboxMetrics holds cumulative per-sandbox runtime metrics for isolated
// executions, surfaced in runner heartbeats and exposed via the server's
// Prometheus endpoint. All counters are atomics so they can be updated from
// task goroutines and read from the heartbeat loop without locking.
type sandboxMetrics struct {
	total             atomic.Int64 // sandboxes started successfully
	startFailures     atomic.Int64 // sandbox start failures (docker run)
	crashes           atomic.Int64 // sandbox crashes detected at teardown
	startLatencyMS    atomic.Int64 // cumulative sandbox start latency
	lifetimeMS        atomic.Int64 // cumulative sandbox lifetime (start -> teardown)
	maxRSSMB          atomic.Int64 // peak observed sandbox RSS in MiB
	maxCPUThousandths atomic.Int64 // peak observed sandbox CPU percent * 1000
}

func newSandboxMetrics() *sandboxMetrics {
	return &sandboxMetrics{}
}

// recordStart accounts a successfully started sandbox and accumulates its
// start latency (docker run wall time).
func (m *sandboxMetrics) recordStart(latency time.Duration) {
	m.total.Add(1)
	m.startLatencyMS.Add(latency.Milliseconds())
}

// recordStartFailure accounts a sandbox that failed to start (docker run
// failure classified as a sandbox runtime error).
func (m *sandboxMetrics) recordStartFailure() {
	m.startFailures.Add(1)
}

// recordCrash accounts a sandbox that died from an infrastructure failure.
// Lifetime accounting is owned by the teardown path (recordFinish), which
// runs exactly once per started sandbox, so a crash increments only the
// crash counter here and never double-counts the sandbox's lifetime.
func (m *sandboxMetrics) recordCrash() {
	m.crashes.Add(1)
}

// recordFinish accumulates the lifetime of a sandbox that ended normally.
func (m *sandboxMetrics) recordFinish(lifetime time.Duration) {
	m.lifetimeMS.Add(lifetime.Milliseconds())
}

// recordObserved tracks the peak sandbox resource usage observed at teardown.
func (m *sandboxMetrics) recordObserved(rssMB int64, cpuPercent float64) {
	if rssMB > 0 {
		for {
			cur := m.maxRSSMB.Load()
			if rssMB <= cur || m.maxRSSMB.CompareAndSwap(cur, rssMB) {
				break
			}
		}
	}
	if cpuPercent > 0 {
		thousandths := int64(cpuPercent * 1000)
		for {
			cur := m.maxCPUThousandths.Load()
			if thousandths <= cur || m.maxCPUThousandths.CompareAndSwap(cur, thousandths) {
				break
			}
		}
	}
}

// snapshot returns a consistent-ish read of all counters for heartbeat
// reporting. Individual counters are atomic; the tuple may mix values from
// different instants, which is fine for cumulative telemetry.
func (m *sandboxMetrics) snapshot() (total, startFailures, crashes, startLatencyMS, lifetimeMS, maxRSSMB int64, maxCPUPercent float64) {
	return m.total.Load(), m.startFailures.Load(), m.crashes.Load(),
		m.startLatencyMS.Load(), m.lifetimeMS.Load(), m.maxRSSMB.Load(),
		float64(m.maxCPUThousandths.Load()) / 1000
}

// runscProbeInterval bounds how often sandboxRuntimeAvailable re-probes the
// runsc runtime. Heartbeats run every 5s; shelling out to runsc on each tick
// would be wasteful, so the probe result is cached for this interval.
const runscProbeInterval = 60 * time.Second

var (
	runscProbeMu     sync.Mutex
	runscProbeResult bool
	runscProbeAt     time.Time
)

// runscVersionProbe executes `runsc --version` with a short timeout and
// reports whether the runtime answered. It is a var so tests can stub it.
var runscVersionProbe = func() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "runsc", "--version").CombinedOutput()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// resetRunscProbeCache clears the cached probe result so tests can force a
// fresh probe.
func resetRunscProbeCache() {
	runscProbeMu.Lock()
	defer runscProbeMu.Unlock()
	runscProbeAt = time.Time{}
	runscProbeResult = false
}

// sandboxRuntimeAvailable reports whether the runsc sandbox runtime is present
// and working on this runner (binary on PATH and `runsc --version` succeeds).
// The probe result is cached for runscProbeInterval. See issue #302 AC4.
func sandboxRuntimeAvailable() bool {
	if !runscAvailable() {
		return false
	}
	runscProbeMu.Lock()
	defer runscProbeMu.Unlock()
	if time.Since(runscProbeAt) < runscProbeInterval {
		return runscProbeResult
	}
	_, ok := runscVersionProbe()
	runscProbeAt = time.Now()
	runscProbeResult = ok
	return runscProbeResult
}

// isSandboxStartFailure reports whether a failed docker run for an isolated
// task looks like a sandbox runtime (runsc) infrastructure failure rather than
// an image or command problem. Docker surfaces runsc start failures as OCI
// runtime errors mentioning "runsc"; network sandbox join failures mention
// "sandbox" alongside a create/start/runtime error. See issue #302 AC2.
func isSandboxStartFailure(err error, output string) bool {
	text := strings.ToLower(output)
	if err != nil {
		text = strings.ToLower(err.Error() + "\n" + output)
	}
	if strings.Contains(text, "runsc") {
		return true
	}
	if !strings.Contains(text, "sandbox") {
		return false
	}
	return strings.Contains(text, "runtime") ||
		strings.Contains(text, "create") ||
		strings.Contains(text, "start") ||
		strings.Contains(text, "failed") ||
		strings.Contains(text, "error")
}

// dockerContainerState mirrors the subset of `docker inspect .State` used for
// sandbox teardown classification.
type dockerContainerState struct {
	Status     string `json:"Status"`
	Running    bool   `json:"Running"`
	ExitCode   int    `json:"ExitCode"`
	OOMKilled  bool   `json:"OOMKilled"`
	Error      string `json:"Error"`
	FinishedAt string `json:"FinishedAt"`
}

// inspectContainerState returns the current docker container state, or nil
// when the container is missing or uninspectable.
func inspectContainerState(containerName string) *dockerContainerState {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{json .State}}", containerName).CombinedOutput()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return nil
	}
	var st dockerContainerState
	if json.Unmarshal(out, &st) != nil {
		return nil
	}
	return &st
}

// sandboxTeardownReason renders a short operator-facing summary of the
// container state used in task events and logs.
func sandboxTeardownReason(st *dockerContainerState) string {
	if st == nil {
		return ""
	}
	return fmt.Sprintf("status=%s exit=%d oom=%v error=%q", st.Status, st.ExitCode, st.OOMKilled, st.Error)
}

// sandboxRuntimeErrorText reports whether the container's recorded runtime
// error is a sandbox (runsc) infrastructure failure.
func sandboxRuntimeErrorText(st *dockerContainerState) bool {
	if st == nil {
		return false
	}
	lower := strings.ToLower(st.Error)
	return strings.Contains(lower, "runsc") || strings.Contains(lower, "sandbox")
}

// classifySandboxCrash classifies a serve-mode gVisor container state: the
// sandbox crashed when the daemon recorded a runsc/sandbox runtime error, or
// when the container exited unexpectedly (non-zero code, no OOM, with a
// daemon-recorded error) while the harness was still supposed to be running.
func classifySandboxCrash(st *dockerContainerState) (reason string, crashed bool) {
	reason = sandboxTeardownReason(st)
	if st == nil {
		return "", false
	}
	// An OOM-killed container is a resource_limit outcome, not a sandbox
	// crash, even if its daemon-recorded error string mentions runsc/sandbox.
	// Gate the runtime-error branch on !st.OOMKilled so the OOM classification
	// set by the caller is never overwritten with sandbox_crashed.
	if !st.OOMKilled && sandboxRuntimeErrorText(st) {
		return reason, true
	}
	// Serve-mode harness containers are expected to stay alive until we stop
	// them (docker stop exits 0, or 137 after the stop timeout). An exited
	// container with a non-zero code, no OOM, and a daemon-recorded error is a
	// runtime-level death — in a gVisor fleet that is a sandbox failure.
	if !st.Running && st.ExitCode != 0 && st.ExitCode != 137 && !st.OOMKilled && st.Error != "" {
		return reason, true
	}
	return reason, false
}

// dockerContainerSandboxCrashed inspects a serve-mode gVisor task container
// before removal and reports whether the sandbox died from an infrastructure
// failure. A missing or uninspectable container reports not crashed. See
// issue #302 AC2.
func dockerContainerSandboxCrashed(containerName string) (reason string, crashed bool) {
	return classifySandboxCrash(inspectContainerState(containerName))
}

// dockerContainerSandboxRuntimeError inspects an RPC-mode gVisor task
// container (whose exit code is the harness's own, so non-zero exits are the
// normal agent outcome) and reports only daemon-recorded runsc/sandbox
// runtime errors as crashes.
func dockerContainerSandboxRuntimeError(containerName string) (reason string, crashed bool) {
	st := inspectContainerState(containerName)
	reason = sandboxTeardownReason(st)
	if st == nil {
		return "", false
	}
	return reason, sandboxRuntimeErrorText(st)
}

// dockerSandboxStats samples the sandboxed task container's live resource
// usage (RSS MiB and CPU percent) via `docker stats --no-stream` at teardown.
// Failures report 0s.
func dockerSandboxStats(containerName string) (rssMB int64, cpuPercent float64) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{.MemUsage}}|{{.CPUPerc}}", containerName).CombinedOutput()
	if err != nil {
		return 0, 0
	}
	fields := strings.SplitN(strings.TrimSpace(string(out)), "|", 2)
	if len(fields) != 2 {
		return 0, 0
	}
	return parseDockerMemUsage(fields[0]), parseDockerCPUPercent(fields[1])
}

// parseDockerMemUsage converts a docker stats memory string such as
// "12.5MiB / 500MiB" into whole MiB of usage. Unparsable input reports 0.
func parseDockerMemUsage(s string) int64 {
	usage := strings.TrimSpace(strings.SplitN(s, "/", 2)[0])
	if usage == "" {
		return 0
	}
	i := 0
	for i < len(usage) {
		c := usage[i]
		if (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '+' {
			i++
			continue
		}
		break
	}
	value, err := strconv.ParseFloat(usage[:i], 64)
	if err != nil {
		return 0
	}
	switch strings.ToLower(strings.TrimSpace(usage[i:])) {
	case "b":
		return int64(value / (1 << 20))
	case "kib", "kb", "k":
		return int64(value / (1 << 10))
	case "mib", "mb", "m":
		return int64(value)
	case "gib", "gb", "g":
		return int64(value * (1 << 10))
	default:
		return 0
	}
}

// parseDockerCPUPercent converts a docker stats CPU string such as "1.25%"
// into a percentage. Unparsable or negative input reports 0.
func parseDockerCPUPercent(s string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "%"), 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

// recordSandboxTeardown captures teardown metrics for a gVisor task container
// before removal: the sandbox lifetime, a runtime-state summary (teardown
// reason) for the log, and peak observed RSS/CPU. This helper is the single
// owner of lifetime accounting (recordFinish) — crash detection call sites
// only increment the crash counter via recordCrash, so a sandbox's lifetime
// is recorded exactly once no matter how it ended.
func (r *Runner) recordSandboxTeardown(containerName string, sandboxStart time.Time) {
	st := inspectContainerState(containerName)
	reason := sandboxTeardownReason(st)
	r.sandbox.recordFinish(time.Since(sandboxStart))
	rssMB, cpuPercent := dockerSandboxStats(containerName)
	r.sandbox.recordObserved(rssMB, cpuPercent)
	if reason != "" {
		slog.Debug("sandbox teardown", "container", containerName, "reason", reason)
	}
}

// recordSandboxObserved samples the sandboxed task container's live resource
// usage while it is still running (before docker stop on the success path).
// The teardown helper samples again for the error paths where the container is
// still up at publish time.
func (r *Runner) recordSandboxObserved(containerName string) {
	rssMB, cpuPercent := dockerSandboxStats(containerName)
	r.sandbox.recordObserved(rssMB, cpuPercent)
}

// rpcSandboxCrashMessage inspects an RPC-mode task container for a
// daemon-recorded runsc/sandbox runtime error. When the sandbox crashed it
// records the crash metric and returns a terminal error message; otherwise it
// returns "". RPC-mode containers exit with the harness's own code, so only
// daemon-recorded sandbox runtime errors are classified as crashes there.
// Lifetime accounting happens once in the teardown path, not here. See issue
// #302 AC2.
func (r *Runner) rpcSandboxCrashMessage(containerName string, fallback string) string {
	if !r.cfg.Execution.UseGVisor || containerName == "" {
		return ""
	}
	reason, crashed := dockerContainerSandboxRuntimeError(containerName)
	if !crashed {
		return ""
	}
	r.sandbox.recordCrash()
	return fmt.Sprintf("sandbox crashed: %s: %s", reason, fallback)
}
