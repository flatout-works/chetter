package controller

import (
	"context"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/runner/internal/config"
	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestCheckIsolationPolicy(t *testing.T) {
	oldRunsc := runscAvailable
	defer func() { runscAvailable = oldRunsc }()

	tests := []struct {
		name         string
		req          task.TaskRequest
		cfg          *config.Config
		runscPresent bool
		wantRefusal  bool
	}{
		{"task not isolation-required is accepted", task.TaskRequest{IsolationRequired: false},
			&config.Config{Execution: config.ExecutionConfig{Backend: "docker"}}, false, false},
		{"required + gVisor enforced is accepted", task.TaskRequest{IsolationRequired: true},
			&config.Config{Execution: config.ExecutionConfig{Backend: "docker", UseGVisor: true}}, true, false},
		{"required + kubernetes gvisor is accepted", task.TaskRequest{IsolationRequired: true},
			&config.Config{Execution: config.ExecutionConfig{Backend: "kubernetes"}, Kubernetes: config.KubernetesConfig{RuntimeClass: "gvisor"}}, false, false},
		{"required + no sandbox is refused", task.TaskRequest{IsolationRequired: true},
			&config.Config{Execution: config.ExecutionConfig{Backend: "docker"}}, false, true},
		{"required + gvisor configured but runsc missing is refused", task.TaskRequest{IsolationRequired: true},
			&config.Config{Execution: config.ExecutionConfig{Backend: "docker", UseGVisor: true}}, false, true},
		{"required + no sandbox + escape hatch accepted", task.TaskRequest{IsolationRequired: true},
			&config.Config{Execution: config.ExecutionConfig{Backend: "docker", AllowUnisolated: true}}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runscAvailable = func() bool { return tt.runscPresent }
			tt.req.TaskID = "task_1"
			tt.req.ExecutionID = "exec_1"
			r := &Runner{cfg: tt.cfg}
			message := r.checkIsolationPolicy(tt.req)
			if (message != "") != tt.wantRefusal {
				t.Fatalf("checkIsolationPolicy() = %q, wantRefusal=%v", message, tt.wantRefusal)
			}
		})
	}
}

func TestEnforcedIsolationCapability(t *testing.T) {
	oldRunsc := runscAvailable
	defer func() { runscAvailable = oldRunsc }()

	tests := []struct {
		name    string
		cfg     *config.Config
		runscOK bool
		want    bool
	}{
		{"docker gvisor + runsc installed", &config.Config{Execution: config.ExecutionConfig{Backend: "docker", UseGVisor: true}}, true, true},
		{"docker gvisor + runsc missing", &config.Config{Execution: config.ExecutionConfig{Backend: "docker", UseGVisor: true}}, false, false},
		{"docker without gvisor", &config.Config{Execution: config.ExecutionConfig{Backend: "docker"}}, true, false},
		{"kubernetes gvisor runtime class", &config.Config{Execution: config.ExecutionConfig{Backend: "kubernetes"}, Kubernetes: config.KubernetesConfig{RuntimeClass: "gvisor"}}, false, true},
		{"kubernetes other runtime class", &config.Config{Execution: config.ExecutionConfig{Backend: "kubernetes"}, Kubernetes: config.KubernetesConfig{RuntimeClass: "runc"}}, false, false},
		{"local mode never enforced", &config.Config{Execution: config.ExecutionConfig{Backend: "local", UseGVisor: true}}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runscAvailable = func() bool { return tt.runscOK }
			r := &Runner{cfg: tt.cfg}
			if got := r.enforcedIsolation(); got != tt.want {
				t.Fatalf("enforcedIsolation() = %v, want %v", got, tt.want)
			}
		})
	}
}

// isolationEventClient captures full task events so tests can assert the
// terminal error classification of refused tasks.
type isolationEventClient struct {
	runnerRPCClient
	mu     sync.Mutex
	events []*runnerv1.TaskEvent
}

func (m *isolationEventClient) ReportTaskEvents(_ context.Context, req *connect.Request[runnerv1.ReportTaskEventsRequest]) (*connect.Response[runnerv1.ReportTaskEventsResponse], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if req.Msg != nil {
		m.events = append(m.events, req.Msg.Events...)
	}
	return connect.NewResponse(&runnerv1.ReportTaskEventsResponse{}), nil
}

func (m *isolationEventClient) lastEvent() *runnerv1.TaskEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.events) == 0 {
		return nil
	}
	return m.events[len(m.events)-1]
}

// newIsolationRefusalRunner builds a Runner minimal enough for runTask's early
// isolation gate to execute and report the refusal.
func newIsolationRefusalRunner(t *testing.T, cfg *config.Config) (*Runner, *isolationEventClient) {
	t.Helper()
	client := &isolationEventClient{}
	return &Runner{
		cfg:            cfg,
		rpcClient:      client,
		runnerID:       "runner-test",
		tasks:          make(map[string]*task.TaskSession),
		tasksChanged:   make(chan struct{}),
		terminalTasks:  make(map[string]struct{}),
		cancelledTasks: make(map[string]struct{}),
		sem:            make(chan struct{}, 1),
	}, client
}

func TestRunTaskRefusesIsolationRequiredTaskWithoutSandbox(t *testing.T) {
	oldRunsc := runscAvailable
	runscAvailable = func() bool { return false }
	defer func() { runscAvailable = oldRunsc }()

	r, client := newIsolationRefusalRunner(t, &config.Config{Execution: config.ExecutionConfig{Backend: "docker"}})
	r.runCtx = context.Background()
	// runTask releases its concurrency slot on exit; pre-fill it since the test
	// drives runTask synchronously instead of through the claim loop.
	r.sem <- struct{}{}

	r.runTask(task.TaskRequest{
		TaskID:            "task_1",
		ExecutionID:       "exec_1",
		ClaimID:           "claim_1",
		AgentSessionID:    "sess_1",
		UserPromptID:      "prompt_1",
		IsolationRequired: true,
		TimeoutSec:        60,
	})

	event := client.lastEvent()
	if event == nil {
		t.Fatal("no task event reported")
	}
	if event.Status != "error" {
		t.Fatalf("status = %q, want error", event.Status)
	}
	if event.ErrorCategory != "isolation_unavailable" {
		t.Fatalf("error_category = %q, want isolation_unavailable", event.ErrorCategory)
	}
	if event.Error == "" {
		t.Fatal("refusal error message is empty")
	}
}

func TestRunTaskAcceptsNonIsolationTaskWithoutSandbox(t *testing.T) {
	oldRunsc := runscAvailable
	runscAvailable = func() bool { return false }
	defer func() { runscAvailable = oldRunsc }()

	r, client := newIsolationRefusalRunner(t, &config.Config{Execution: config.ExecutionConfig{Backend: "docker"}})
	r.runCtx = context.Background()
	// runTask releases its concurrency slot on exit; pre-fill it since the test
	// drives runTask synchronously instead of through the claim loop.
	r.sem <- struct{}{}

	// A task without the isolation requirement must NOT be refused; the runner
	// proceeds to workspace creation, which fails here because the workspace
	// manager is unset — but the failure must not be classified as
	// isolation_unavailable.
	r.runTask(task.TaskRequest{
		TaskID:            "task_2",
		ExecutionID:       "exec_2",
		ClaimID:           "claim_2",
		AgentSessionID:    "sess_2",
		UserPromptID:      "prompt_2",
		IsolationRequired: false,
		TimeoutSec:        60,
	})

	event := client.lastEvent()
	if event == nil {
		t.Fatal("no task event reported")
	}
	if event.ErrorCategory == "isolation_unavailable" {
		t.Fatalf("non-isolation task wrongly classified as isolation_unavailable: %q", event.Error)
	}
}

func TestRunTaskAcceptsIsolationRequiredWithEscapeHatch(t *testing.T) {
	oldRunsc := runscAvailable
	runscAvailable = func() bool { return false }
	defer func() { runscAvailable = oldRunsc }()

	r, client := newIsolationRefusalRunner(t, &config.Config{Execution: config.ExecutionConfig{Backend: "docker", AllowUnisolated: true}})
	r.runCtx = context.Background()
	// runTask releases its concurrency slot on exit; pre-fill it since the test
	// drives runTask synchronously instead of through the claim loop.
	r.sem <- struct{}{}

	r.runTask(task.TaskRequest{
		TaskID:            "task_3",
		ExecutionID:       "exec_3",
		ClaimID:           "claim_3",
		AgentSessionID:    "sess_3",
		UserPromptID:      "prompt_3",
		IsolationRequired: true,
		TimeoutSec:        60,
	})

	event := client.lastEvent()
	if event == nil {
		t.Fatal("no task event reported")
	}
	if event.ErrorCategory == "isolation_unavailable" {
		t.Fatalf("escape hatch should allow isolation-requiring task, got isolation_unavailable: %q", event.Error)
	}
}

func TestRunnerInfoProtoAdvertisesEnforcedIsolation(t *testing.T) {
	oldRunsc := runscAvailable
	defer func() { runscAvailable = oldRunsc }()

	// Docker runner with gVisor configured and runsc installed advertises
	// enforced isolation; without runsc it does not.
	runscAvailable = func() bool { return true }
	gvisor := &Runner{
		cfg:            &config.Config{Execution: config.ExecutionConfig{Backend: "docker", UseGVisor: true}},
		runnerID:       "runner-gvisor",
		startedAt:      time.Now().UTC(),
		tasks:          make(map[string]*task.TaskSession),
		terminalTasks:  make(map[string]struct{}),
		cancelledTasks: make(map[string]struct{}),
		sem:            make(chan struct{}, 1),
	}
	if !gvisor.runnerInfoProto("active").EnforcedIsolation {
		t.Fatal("gVisor runner should advertise enforced_isolation")
	}

	runscAvailable = func() bool { return false }
	if gvisor.runnerInfoProto("active").EnforcedIsolation {
		t.Fatal("runner without runsc binary must not advertise enforced_isolation")
	}

	// Kubernetes runner with a gVisor runtime class advertises enforcement.
	kube := &Runner{
		cfg:            &config.Config{Execution: config.ExecutionConfig{Backend: "kubernetes"}, Kubernetes: config.KubernetesConfig{RuntimeClass: "gvisor"}},
		runnerID:       "runner-kube-gvisor",
		startedAt:      time.Now().UTC(),
		tasks:          make(map[string]*task.TaskSession),
		terminalTasks:  make(map[string]struct{}),
		cancelledTasks: make(map[string]struct{}),
		sem:            make(chan struct{}, 1),
	}
	if !kube.runnerInfoProto("active").EnforcedIsolation {
		t.Fatal("kubernetes gVisor runner should advertise enforced_isolation")
	}

	// Plain docker runner without gVisor does not advertise it.
	plain := &Runner{
		cfg:            &config.Config{Execution: config.ExecutionConfig{Backend: "docker"}},
		runnerID:       "runner-plain",
		startedAt:      time.Now().UTC(),
		tasks:          make(map[string]*task.TaskSession),
		terminalTasks:  make(map[string]struct{}),
		cancelledTasks: make(map[string]struct{}),
		sem:            make(chan struct{}, 1),
	}
	if plain.runnerInfoProto("active").EnforcedIsolation {
		t.Fatal("plain docker runner must not advertise enforced_isolation")
	}
}
