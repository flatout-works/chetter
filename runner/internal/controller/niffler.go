package controller

import (
	"github.com/flatout-works/chetter/runner/harness"
	"github.com/flatout-works/chetter/runner/internal/network"
	"github.com/flatout-works/chetter/runner/internal/task"
)

// Niffler persists MCP configuration across stack restarts. Never hand its
// store a runner-wide control token, even for explicitly unisolated local use.
func (r *Runner) nifflerLocalRelay(req task.TaskRequest, h harness.Harness) (string, string, func(), error) {
	url, token := r.taskChetterMCPURL(), r.taskChetterMCPToken(req.RunnerMCPToken)
	noop := func() {}
	if h.Name() != "niffler" || r.executionMode() != "local" || url == "" {
		return url, token, noop, nil
	}
	relay, e := network.NewMCPRelay("127.0.0.1:0", url, token)
	if e != nil {
		return "", "", noop, e
	}
	if e = relay.Start(); e != nil {
		return "", "", noop, e
	}
	unregister, e := relay.RegisterClaim(req.RunnerMCPToken, req.TaskID, req.ExecutionID)
	if e != nil {
		_ = relay.Stop()
		return "", "", noop, e
	}
	return "http://" + relay.Addr() + "/mcp", req.RunnerMCPToken, func() { unregister(); _ = relay.Stop() }, nil
}
