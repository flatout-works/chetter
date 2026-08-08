package controller

import (
	"log/slog"
	"os/exec"
	"strings"

	"github.com/flatout-works/chetter/runner/internal/task"
)

// enforcedIsolation reports whether this runner can actually enforce a sandbox
// for task containers (gVisor/`--runtime runsc` in effect). The runner
// advertises this in claim/heartbeat metadata so the control plane only admits
// isolation-requiring tasks to capable runners, and uses it at claim time to
// refuse tasks it cannot isolate. See issue #291.
//
// Docker mode requires the use_gvisor config AND the runsc binary to be
// available on PATH — a configured runtime that is not installed cannot be
// enforced. Kubernetes mode treats a gVisor runtime class as enforced.
func (r *Runner) enforcedIsolation() bool {
	switch r.executionMode() {
	case "kubernetes":
		return strings.EqualFold(strings.TrimSpace(r.cfg.Kubernetes.RuntimeClass), "gvisor")
	case "docker":
		return r.cfg.Execution.UseGVisor && runscAvailable()
	default:
		// local mode has no container runtime and therefore never enforces a
		// sandbox.
		return false
	}
}

// runscAvailable checks for the runsc binary. It is a var so tests can stub
// the environment; the production check is exec.LookPath.
var runscAvailable = func() bool {
	if _, err := exec.LookPath("runsc"); err != nil {
		return false
	}
	return true
}

// checkIsolationPolicy evaluates the claim-time isolation gate. It returns a
// terminal error message when the task requires enforced isolation but this
// runner cannot enforce it and the deployment has not opted out. The task is
// reported as a terminal error with error_category isolation_unavailable — a
// clear classification, not a retryable failure — so it never runs unsandboxed
// on this runner. See issue #291.
func (r *Runner) checkIsolationPolicy(req task.TaskRequest) string {
	if !req.IsolationRequired {
		return ""
	}
	if r.enforcedIsolation() {
		return ""
	}
	if r.cfg.Execution.AllowUnisolated {
		slog.Warn("running isolation-requiring task without enforced sandbox — deployment opted out via CHETTER_ALLOW_UNISOLATED",
			"task_id", req.TaskID, "execution_id", req.ExecutionID)
		return ""
	}
	return "task requires enforced isolation (gVisor) but this runner cannot enforce it"
}

// sandboxAvailability reports sandbox runtime availability (runsc present and
// working) for fleet health. In docker mode it probes the runsc binary; in
// kubernetes mode the runner cannot probe the node runtime, so it reports
// availability from the configured runtime class. Local mode has no container
// runtime and reports false. See issue #302 AC4.
func (r *Runner) sandboxAvailability() bool {
	switch r.executionMode() {
	case "kubernetes":
		return strings.EqualFold(strings.TrimSpace(r.cfg.Kubernetes.RuntimeClass), "gvisor")
	case "docker":
		return sandboxRuntimeAvailable()
	default:
		return false
	}
}
