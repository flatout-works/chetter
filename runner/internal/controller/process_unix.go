//go:build !windows

package controller

import (
	"os/exec"
	"syscall"
)

// configureProcess makes cancellation apply to the harness process tree rather
// than only the process started directly by the runner.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return terminateProcess(cmd) }
}

func terminateProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
