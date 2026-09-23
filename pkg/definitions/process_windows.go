//go:build windows

package definitions

import "os/exec"

// configureProcess mirrors the unix process-group cancellation on platforms
// that do not support process groups. Windows has no direct equivalent, so we
// fall back to killing the direct child only.
func configureProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return terminateProcess(cmd) }
}

func terminateProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
