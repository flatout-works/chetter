// Package containerd wraps ctr CLI invocations to pull images, run Kata
// containers, and manage containerd tasks.
package containerd

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Client wraps `ctr` command invocations for pulling images, running Kata
// containers, listing tasks, and cleaning up.
type Client struct {
	Namespace string
}

// NewClient creates a containerd client wrapper.
func NewClient(namespace string) *Client {
	if namespace == "" {
		namespace = "devfleet-runner"
	}
	return &Client{Namespace: namespace}
}

// Pull pulls an image using ctr.
func (c *Client) Pull(ctx context.Context, image string) error {
	cmd := exec.CommandContext(ctx, "ctr", "-n", c.Namespace, "image", "pull", image)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ctr pull %s: %w\n%s", image, err, string(out))
	}
	return nil
}

// RunKata starts a Kata container with the given config.
func (c *Client) RunKata(ctx context.Context, taskID, image string, mounts []Mount, env map[string]string, netNSPath string, command []string) (string, error) {
	args := c.runKataArgs(taskID, image, mounts, env, netNSPath, command)
	cmd := exec.CommandContext(ctx, "ctr", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("ctr stdout pipe %s: %w", taskID, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("ctr stderr pipe %s: %w", taskID, err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("ctr run %s: %w", taskID, err)
	}

	var buf bytes.Buffer
	var mu sync.Mutex
	var wg sync.WaitGroup
	stream := func(name string, r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
		slog.Debug("kata output", "taskID", taskID, "stream", name, "line", line)
			mu.Lock()
			buf.WriteString(line)
			buf.WriteByte('\n')
			mu.Unlock()
		}
		if err := scanner.Err(); err != nil {
			slog.Warn("kata read error", "taskID", taskID, "stream", name, "err", err)
		}
	}
	wg.Add(2)
	go stream("stdout", stdout)
	go stream("stderr", stderr)

	err = cmd.Wait()
	wg.Wait()
	out := buf.String()
	if err != nil {
		return out, fmt.Errorf("ctr run %s: %w\n%s", taskID, err, out)
	}
	return out, nil
}

func (c *Client) runKataArgs(taskID, image string, mounts []Mount, env map[string]string, netNSPath string, command []string) []string {
	var mountArgs []string
	for _, m := range mounts {
		mountArgs = append(mountArgs, "--mount", fmt.Sprintf("type=%s,src=%s,dst=%s,options=%s", m.Type, m.Source, m.Destination, strings.Join(m.Options, ":")))
	}

	var envArgs []string
	for k, v := range env {
		envArgs = append(envArgs, "--env", fmt.Sprintf("%s=%s", k, v))
	}

	args := []string{
		"-n", c.Namespace,
		"run", "--rm",
		"--runtime", "io.containerd.kata.v2",
	}
	if netNSPath != "" {
		args = append(args, "--with-ns", "network:"+netNSPath)
	}
	args = append(args, mountArgs...)
	args = append(args, envArgs...)
	args = append(args, image, taskID)
	args = append(args, command...)
	return args
}

// Kill sends a kill signal to a task.
func (c *Client) Kill(ctx context.Context, taskID string) error {
	cmd := exec.CommandContext(ctx, "ctr", "-n", c.Namespace, "task", "kill", "--signal", "SIGKILL", taskID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Task might already be gone
		if strings.Contains(string(out), "not found") {
			return nil
		}
		return fmt.Errorf("ctr task kill %s: %w\n%s", taskID, err, string(out))
	}
	return nil
}

// Delete removes a container.
func (c *Client) Delete(ctx context.Context, taskID string) error {
	cmd := exec.CommandContext(ctx, "ctr", "-n", c.Namespace, "container", "delete", taskID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "not found") {
			return nil
		}
		return fmt.Errorf("ctr container delete %s: %w\n%s", taskID, err, string(out))
	}
	return nil
}

// ListTasks shows running tasks.
func (c *Client) ListTasks(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "ctr", "-n", c.Namespace, "task", "ls")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ctr task ls: %w", err)
	}
	// Parse output (skip header)
	lines := strings.Split(string(out), "\n")
	var tasks []string
	for i, line := range lines {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			tasks = append(tasks, fields[0])
		}
	}
	return tasks, nil
}

// WaitForExit polls until a task exits or context times out.
func (c *Client) WaitForExit(ctx context.Context, taskID string, pollInterval time.Duration) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			tasks, err := c.ListTasks(ctx)
			if err != nil {
				continue
			}
			found := false
			for _, t := range tasks {
				if t == taskID {
					found = true
					break
				}
			}
			if !found {
				return nil // Task exited
			}
		}
	}
}

// CheckInstall verifies ctr and containerd socket access.
// The Kata shim is resolved by the host containerd service, so it may not be
// present inside a privileged runner container that only mounts the host socket.
func CheckInstall() error {
	if _, err := exec.LookPath("ctr"); err != nil {
		return fmt.Errorf("ctr not found in PATH — install containerd")
	}
	if runningInDocker() {
		for _, path := range []string{"/run/containerd", "/run/netns", "/tmp", "/var/lib/containerd"} {
			mounted, err := isMountPoint(path)
			if err != nil {
				return fmt.Errorf("check container mount %s: %w", path, err)
			}
			if !mounted {
				return fmt.Errorf("%s must be bind-mounted into the runner container when using host containerd", path)
			}
		}
	}
	if err := os.MkdirAll(os.TempDir(), 0755); err != nil {
		return fmt.Errorf("create ctr temp dir %s: %w", os.TempDir(), err)
	}
	// Quick check that we can talk to host containerd (tests socket permissions).
	cmd := exec.Command("ctr", "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cannot connect to containerd socket (permission denied?). Run as root, or add user to containerd group. Output: %s", string(out))
	}
	return nil
}

func runningInDocker() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

func isMountPoint(path string) (bool, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 5 && fields[4] == path {
			return true, nil
		}
	}
	return false, nil
}

// Mount describes a container bind mount with type, source, destination,
// and mount options.
type Mount struct {
	Type        string
	Source      string
	Destination string
	Options     []string
}
