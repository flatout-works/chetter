package execution

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestNoopBackendRecordsCalls(t *testing.T) {
	ctx := context.Background()
	b := &NoopBackend{}

	b.Run(ctx, "run", "-d", "--name", "test")
	b.Stop(ctx, "test")
	b.Remove(ctx, "test")
	b.Logs(ctx, "test")
	b.Inspect(ctx, "test", "{{.State.Status}}")
	b.Exec(ctx, "test", "echo", "hello")
	b.Checkpoint(ctx, "create", "test", "--checkpoint-dir", "/tmp")
	b.PS(ctx, "-a")
	b.Ping(ctx)

	expected := []string{"Run", "Stop", "Remove", "Logs", "Inspect", "Exec", "Checkpoint", "PS", "Ping"}
	if len(b.Calls) != len(expected) {
		t.Fatalf("expected %d calls, got %d: %v", len(expected), len(b.Calls), b.Calls)
	}
	for i, want := range expected {
		if b.Calls[i] != want {
			t.Fatalf("call[%d]: want %q, got %q", i, want, b.Calls[i])
		}
	}
}

func TestNoopBackendErrorPropagation(t *testing.T) {
	ctx := context.Background()
	testErr := errors.New("test error")

	b := &NoopBackend{
		RunError:        testErr,
		StopError:       testErr,
		RemoveError:     testErr,
		LogsError:       testErr,
		InspectError:    testErr,
		ExecError:       testErr,
		CheckpointError: testErr,
		PSError:         testErr,
		PingError:       testErr,
	}

	if _, err := b.Run(ctx); err != testErr {
		t.Fatalf("Run: want %v, got %v", testErr, err)
	}
	if err := b.Stop(ctx, "x"); err != testErr {
		t.Fatalf("Stop: want %v, got %v", testErr, err)
	}
	if err := b.Remove(ctx, "x"); err != testErr {
		t.Fatalf("Remove: want %v, got %v", testErr, err)
	}
	if _, err := b.Logs(ctx, "x"); err != testErr {
		t.Fatalf("Logs: want %v, got %v", testErr, err)
	}
	if _, err := b.Inspect(ctx, "x", ""); err != testErr {
		t.Fatalf("Inspect: want %v, got %v", testErr, err)
	}
	if _, err := b.Exec(ctx, "x"); err != testErr {
		t.Fatalf("Exec: want %v, got %v", testErr, err)
	}
	if _, err := b.Checkpoint(ctx, "ls", "x"); err != testErr {
		t.Fatalf("Checkpoint: want %v, got %v", testErr, err)
	}
	if _, err := b.PS(ctx); err != testErr {
		t.Fatalf("PS: want %v, got %v", testErr, err)
	}
	if err := b.Ping(ctx); err != testErr {
		t.Fatalf("Ping: want %v, got %v", testErr, err)
	}
}

func TestNoopBackendOutputPropagation(t *testing.T) {
	ctx := context.Background()
	expected := []byte("output")

	b := &NoopBackend{
		RunOutput:        expected,
		LogsOutput:       expected,
		InspectOutput:    expected,
		ExecOutput:       expected,
		CheckpointOutput: expected,
		PSOutput:         expected,
	}

	if out, _ := b.Run(ctx); string(out) != string(expected) {
		t.Fatalf("Run: want %q, got %q", expected, out)
	}
	if out, _ := b.Logs(ctx, "x"); string(out) != string(expected) {
		t.Fatalf("Logs: want %q, got %q", expected, out)
	}
	if out, _ := b.Inspect(ctx, "x", ""); string(out) != string(expected) {
		t.Fatalf("Inspect: want %q, got %q", expected, out)
	}
	if out, _ := b.Exec(ctx, "x"); string(out) != string(expected) {
		t.Fatalf("Exec: want %q, got %q", expected, out)
	}
	if out, _ := b.Checkpoint(ctx, "ls", "x"); string(out) != string(expected) {
		t.Fatalf("Checkpoint: want %q, got %q", expected, out)
	}
	if out, _ := b.PS(ctx); string(out) != string(expected) {
		t.Fatalf("PS: want %q, got %q", expected, out)
	}
}

func TestDockerBackendPingRequiresDocker(t *testing.T) {
	// Only run if docker is available.
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}

	b := NewDockerBackend()
	err := b.Ping(context.Background())
	if err != nil {
		// Docker might be installed but not running — skip gracefully.
		msg := err.Error()
		if strings.Contains(msg, "Cannot connect to the Docker daemon") ||
			strings.Contains(msg, "permission denied") ||
			strings.Contains(msg, "exit status 1") {
			t.Skip("docker daemon not accessible")
		}
		t.Fatalf("Ping failed: %v", err)
	}
}


