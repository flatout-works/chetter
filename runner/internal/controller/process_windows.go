//go:build windows

package controller

import "os/exec"

func configureProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return terminateProcess(cmd) }
}

func terminateProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
