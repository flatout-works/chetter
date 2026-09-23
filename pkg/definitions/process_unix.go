//go:build !windows

package definitions

import (
	"os/exec"
	"syscall"
)

// configureProcess makes cancellation apply to the whole git process tree
// rather than only the process started directly by the manager.
//
// `git pull` and `git clone` spawn child git processes (fetch, merge). The
// default exec.CommandContext cancellation kills only the direct child, which
// orphans those grandchildren. The MCP container runs /chetter as PID 1 with
// no init, so orphaned git processes are never reaped and accumulate as
// zombies (issue #427). Starting git in its own process group and killing the
// whole group on cancellation prevents the leak.
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
