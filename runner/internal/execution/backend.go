// Package execution defines the container execution abstraction and its
// implementations (Docker, noop test double).
package execution

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Backend abstracts container lifecycle, introspection, and health operations
// behind a narrow interface. Callers build Docker CLI arguments from their own
// domain types (task request, harness config, etc.) and pass them through.
type Backend interface {
	// Run runs a container (docker run). Returns combined stdout+stderr on error.
	Run(ctx context.Context, args ...string) ([]byte, error)

	// Stop stops a running container by name or ID.
	Stop(ctx context.Context, containerID string) error

	// Remove forcefully removes a container by name or ID.
	Remove(ctx context.Context, containerID string) error

	// Logs returns container logs.
	Logs(ctx context.Context, containerID string, extraArgs ...string) ([]byte, error)

	// Inspect returns container inspect output for the given Go-template format.
	Inspect(ctx context.Context, containerID, format string) ([]byte, error)

	// Exec runs a command in a running container and returns combined output.
	Exec(ctx context.Context, containerID string, cmd ...string) ([]byte, error)

	// Checkpoint runs a gVisor checkpoint command (create, ls, rm) against a container.
	Checkpoint(ctx context.Context, cmd string, args ...string) ([]byte, error)

	// PS lists containers with the given filter/format arguments.
	PS(ctx context.Context, args ...string) ([]byte, error)

	// Ping returns nil if the backend is reachable.
	Ping(ctx context.Context) error
}

// DockerBackend executes container operations via the Docker CLI.
type DockerBackend struct{}

// NewDockerBackend returns a Backend that shells out to "docker".
func NewDockerBackend() Backend {
	return &DockerBackend{}
}

func (b *DockerBackend) Run(ctx context.Context, args ...string) ([]byte, error) {
	all := append([]string{"run"}, args...)
	return exec.CommandContext(ctx, "docker", all...).CombinedOutput()
}

func (b *DockerBackend) Stop(ctx context.Context, containerID string) error {
	out, err := exec.CommandContext(ctx, "docker", "stop", containerID).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker stop %s: %w\n%s", containerID, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (b *DockerBackend) Remove(ctx context.Context, containerID string) error {
	out, err := exec.CommandContext(ctx, "docker", "rm", "-f", containerID).CombinedOutput()
	if err != nil && ctx.Err() != nil {
		return fmt.Errorf("docker rm %s: %w\n%s", containerID, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (b *DockerBackend) Logs(ctx context.Context, containerID string, extraArgs ...string) ([]byte, error) {
	args := append([]string{"logs", containerID}, extraArgs...)
	return exec.CommandContext(ctx, "docker", args...).CombinedOutput()
}

func (b *DockerBackend) Inspect(ctx context.Context, containerID, format string) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", "inspect", "-f", format, containerID).CombinedOutput()
}

func (b *DockerBackend) Exec(ctx context.Context, containerID string, cmd ...string) ([]byte, error) {
	args := append([]string{"exec", containerID}, cmd...)
	return exec.CommandContext(ctx, "docker", args...).CombinedOutput()
}

func (b *DockerBackend) Checkpoint(ctx context.Context, cmd string, args ...string) ([]byte, error) {
	all := append([]string{"checkpoint", cmd}, args...)
	return exec.CommandContext(ctx, "docker", all...).CombinedOutput()
}

func (b *DockerBackend) PS(ctx context.Context, args ...string) ([]byte, error) {
	all := append([]string{"ps"}, args...)
	return exec.CommandContext(ctx, "docker", all...).CombinedOutput()
}

func (b *DockerBackend) Ping(ctx context.Context) error {
	return exec.CommandContext(ctx, "docker", "info").Run()
}

// NoopBackend is a test double that records operations and returns
// configurable responses.
type NoopBackend struct {
	// RunOutput is returned from Run. If RunError is set it is returned as the error.
	RunOutput []byte
	RunError  error

	StopError   error
	RemoveError error

	LogsOutput []byte
	LogsError  error

	InspectOutput []byte
	InspectError  error

	ExecOutput []byte
	ExecError  error

	CheckpointOutput []byte
	CheckpointError  error

	PSOutput []byte
	PSError  error

	PingError error

	// Calls records the method calls for assertion.
	Calls []string
}

func (b *NoopBackend) record(call string) {
	if b.Calls == nil {
		b.Calls = make([]string, 0)
	}
	b.Calls = append(b.Calls, call)
}

func (b *NoopBackend) Run(ctx context.Context, args ...string) ([]byte, error) {
	b.record("Run")
	return b.RunOutput, b.RunError
}

func (b *NoopBackend) Stop(ctx context.Context, containerID string) error {
	b.record("Stop")
	return b.StopError
}

func (b *NoopBackend) Remove(ctx context.Context, containerID string) error {
	b.record("Remove")
	return b.RemoveError
}

func (b *NoopBackend) Logs(ctx context.Context, containerID string, extraArgs ...string) ([]byte, error) {
	b.record("Logs")
	return b.LogsOutput, b.LogsError
}

func (b *NoopBackend) Inspect(ctx context.Context, containerID, format string) ([]byte, error) {
	b.record("Inspect")
	return b.InspectOutput, b.InspectError
}

func (b *NoopBackend) Exec(ctx context.Context, containerID string, cmd ...string) ([]byte, error) {
	b.record("Exec")
	return b.ExecOutput, b.ExecError
}

func (b *NoopBackend) Checkpoint(ctx context.Context, cmd string, args ...string) ([]byte, error) {
	b.record("Checkpoint")
	return b.CheckpointOutput, b.CheckpointError
}

func (b *NoopBackend) PS(ctx context.Context, args ...string) ([]byte, error) {
	b.record("PS")
	return b.PSOutput, b.PSError
}

func (b *NoopBackend) Ping(ctx context.Context) error {
	b.record("Ping")
	return b.PingError
}
