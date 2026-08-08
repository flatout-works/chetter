package controller

import (
	"errors"
	"testing"
	"time"
)

func TestSandboxMetricsCounters(t *testing.T) {
	m := newSandboxMetrics()

	m.recordStart(1500 * time.Millisecond)
	m.recordStart(500 * time.Millisecond)
	m.recordStartFailure()
	m.recordCrash(10 * time.Second)
	m.recordFinish(20 * time.Second)
	m.recordObserved(256, 42.5)
	m.recordObserved(128, 60.25)

	total, startFailures, crashes, startLatencyMS, lifetimeMS, maxRSSMB, maxCPUPercent := m.snapshot()
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if startFailures != 1 {
		t.Errorf("startFailures = %d, want 1", startFailures)
	}
	if crashes != 1 {
		t.Errorf("crashes = %d, want 1", crashes)
	}
	if startLatencyMS != 2000 {
		t.Errorf("startLatencyMS = %d, want 2000", startLatencyMS)
	}
	// 10s crash + 20s finish = 30s
	if lifetimeMS != 30000 {
		t.Errorf("lifetimeMS = %d, want 30000", lifetimeMS)
	}
	if maxRSSMB != 256 {
		t.Errorf("maxRSSMB = %d, want 256", maxRSSMB)
	}
	if maxCPUPercent != 60.25 {
		t.Errorf("maxCPUPercent = %v, want 60.25", maxCPUPercent)
	}

	// A fresh metrics set reports zero values.
	fresh := newSandboxMetrics()
	total, startFailures, crashes, startLatencyMS, lifetimeMS, maxRSSMB, maxCPUPercent = fresh.snapshot()
	if total != 0 || startFailures != 0 || crashes != 0 || startLatencyMS != 0 || lifetimeMS != 0 || maxRSSMB != 0 || maxCPUPercent != 0 {
		t.Errorf("fresh snapshot = (%d,%d,%d,%d,%d,%d,%v), want all zeros",
			total, startFailures, crashes, startLatencyMS, lifetimeMS, maxRSSMB, maxCPUPercent)
	}
}

func TestIsSandboxStartFailure(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		output string
		want   bool
	}{
		{"runsc create failure", errors.New("exit status 1"), "docker: Error response from daemon: runsc create failed: sandbox start error.", true},
		{"runsc runtime error", errors.New("exit status 1"), "Error response from daemon: OCI runtime create failed: runsc: failed to create sandbox: unknown", true},
		{"network sandbox join", errors.New("exit status 1"), "Error response from daemon: error creating network sandbox: operation not permitted", true},
		{"sandbox runtime message", errors.New("exit status 1"), "daemon: sandbox start failed: runtime error", true},
		{"image not found", errors.New("exit status 1"), "docker: Error response from daemon: pull access denied for nope/nope, repository does not exist", false},
		{"command not found in image", errors.New("exit status 1"), "docker: Error response from daemon: failed to create task for container: invalid mount config", false},
		{"plain daemon error", errors.New("exit status 1"), "Error response from daemon: network chetter-net not found", false},
		{"nil error", nil, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSandboxStartFailure(tc.err, tc.output); got != tc.want {
				t.Errorf("isSandboxStartFailure(%v, %q) = %v, want %v", tc.err, tc.output, got, tc.want)
			}
		})
	}
}

func TestParseDockerMemUsage(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"12.5MiB / 500MiB", 12},
		{"512MiB / 1GiB", 512},
		{"1.5GiB / 2GiB", 1536},
		{"1.024KiB / 10MiB", 0},
		{"2048KiB / 4MiB", 2},
		{"1048576B / 4MiB", 1},
		{"0B / 4MiB", 0},
		{"", 0},
		{"garbage", 0},
	}
	for _, tc := range tests {
		if got := parseDockerMemUsage(tc.input); got != tc.want {
			t.Errorf("parseDockerMemUsage(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestParseDockerCPUPercent(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"1.25%", 1.25},
		{"42%", 42},
		{"0.00%", 0},
		{"", 0},
		{"abc", 0},
		{"-1%", 0},
	}
	for _, tc := range tests {
		if got := parseDockerCPUPercent(tc.input); got != tc.want {
			t.Errorf("parseDockerCPUPercent(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestClassifySandboxCrash(t *testing.T) {
	tests := []struct {
		name    string
		state   *dockerContainerState
		want    bool
		wantErr bool // want the reason non-empty
	}{
		{"nil state", nil, false, false},
		{"running sandbox", &dockerContainerState{Status: "running", Running: true}, false, true},
		{"normal stop by us", &dockerContainerState{Status: "exited", Running: false, ExitCode: 0}, false, true},
		{"stop timeout kill", &dockerContainerState{Status: "exited", Running: false, ExitCode: 137}, false, true},
		{"oom killed", &dockerContainerState{Status: "exited", Running: false, ExitCode: 137, OOMKilled: true}, false, true},
		{"runsc runtime error", &dockerContainerState{Status: "exited", Running: false, ExitCode: 1, Error: "runsc: sandbox exited with error"}, true, true},
		{"sandbox error mid-run", &dockerContainerState{Status: "exited", Running: false, ExitCode: 1, Error: "sandbox teardown failed"}, true, true},
		{"unexpected exit with daemon error", &dockerContainerState{Status: "exited", Running: false, ExitCode: 1, Error: "container task failed to start"}, true, true},
		{"harness process exit no error", &dockerContainerState{Status: "exited", Running: false, ExitCode: 1}, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reason, crashed := classifySandboxCrash(tc.state)
			if crashed != tc.want {
				t.Errorf("classifySandboxCrash(%+v) crashed = %v, want %v", tc.state, crashed, tc.want)
			}
			if (reason != "") != tc.wantErr {
				t.Errorf("classifySandboxCrash(%+v) reason = %q, want non-empty=%v", tc.state, reason, tc.wantErr)
			}
		})
	}
}

func TestClassifySandboxRuntimeError(t *testing.T) {
	tests := []struct {
		name  string
		state *dockerContainerState
		want  bool
	}{
		{"nil state", nil, false},
		{"runsc error", &dockerContainerState{Error: "runsc: create sandbox: unknown"}, true},
		{"sandbox error", &dockerContainerState{Error: "sandbox runtime failure"}, true},
		{"runc error", &dockerContainerState{Error: "runc create failed"}, false},
		{"harness non-zero exit", &dockerContainerState{Status: "exited", Running: false, ExitCode: 3}, false},
		{"running", &dockerContainerState{Status: "running", Running: true}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sandboxRuntimeErrorText(tc.state); got != tc.want {
				t.Errorf("sandboxRuntimeErrorText(%+v) = %v, want %v", tc.state, got, tc.want)
			}
		})
	}
}

func TestSandboxRuntimeAvailable(t *testing.T) {
	resetRunscProbeCache()
	t.Cleanup(resetRunscProbeCache)

	origAvailable := runscAvailable
	origProbe := runscVersionProbe
	defer func() {
		runscAvailable = origAvailable
		runscVersionProbe = origProbe
	}()

	t.Run("runsc missing", func(t *testing.T) {
		runscAvailable = func() bool { return false }
		if sandboxRuntimeAvailable() {
			t.Fatal("sandboxRuntimeAvailable = true when runsc is missing")
		}
	})

	t.Run("runsc present and working", func(t *testing.T) {
		resetRunscProbeCache()
		runscAvailable = func() bool { return true }
		runscVersionProbe = func() (string, bool) { return "runsc version release-20240101", true }
		if !sandboxRuntimeAvailable() {
			t.Fatal("sandboxRuntimeAvailable = false for a working runsc")
		}
	})

	t.Run("runsc present but broken", func(t *testing.T) {
		resetRunscProbeCache()
		runscAvailable = func() bool { return true }
		runscVersionProbe = func() (string, bool) { return "", false }
		if sandboxRuntimeAvailable() {
			t.Fatal("sandboxRuntimeAvailable = true for a broken runsc")
		}
	})
}

func TestRunnerSandboxAvailabilityModes(t *testing.T) {
	resetRunscProbeCache()
	t.Cleanup(resetRunscProbeCache)

	origAvailable := runscAvailable
	origProbe := runscVersionProbe
	defer func() {
		runscAvailable = origAvailable
		runscVersionProbe = origProbe
	}()
	runscAvailable = func() bool { return true }
	runscVersionProbe = func() (string, bool) { return "runsc version release-20240101", true }

	r, _ := newDrainTestRunner(t)

	// docker mode probes the runsc runtime.
	r.cfg.Execution.Backend = "docker"
	if !r.sandboxAvailability() {
		t.Fatal("docker runner with working runsc should report sandbox available")
	}

	// kubernetes mode reports availability from the runtime class.
	r.cfg.Execution.Backend = "kubernetes"
	r.cfg.Kubernetes.RuntimeClass = "gvisor"
	if !r.sandboxAvailability() {
		t.Fatal("kubernetes runner with gvisor runtime class should report sandbox available")
	}
	r.cfg.Kubernetes.RuntimeClass = "kata"
	if r.sandboxAvailability() {
		t.Fatal("kubernetes runner with non-gvisor runtime class should report sandbox unavailable")
	}

	// local mode has no sandbox runtime.
	r.cfg.Execution.Backend = "local"
	if r.sandboxAvailability() {
		t.Fatal("local runner should report sandbox unavailable")
	}
}
