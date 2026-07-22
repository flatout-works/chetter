package pi

import (
	"io"

	"github.com/flatout-works/chetter/runner/harness"
	"github.com/flatout-works/chetter/runner/internal/task"
)

type Pi struct{}

var _ harness.RPCHarness = (*Pi)(nil)

func New() *Pi {
	return &Pi{}
}

func (p *Pi) Name() string { return "pi" }

func (p *Pi) GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error {
	return GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken, req, isLocal)
}

func (p *Pi) Env(wsDir string, secret string, _ task.TaskRequest) map[string]string {
	return map[string]string{
		"PI_CODING_AGENT_DIR":         wsDir + "/.pi/agent",
		"PI_CODING_AGENT_SESSION_DIR": wsDir + "/.pi/sessions",
		"PI_OFFLINE":                  "1",
		"PI_SKIP_VERSION_CHECK":       "1",
		"PI_TELEMETRY":                "0",
	}
}

func (p *Pi) ReadSessionExport(wsDir, sessionID string) (string, error) {
	return readSessionExport(wsDir)
}

func (p *Pi) PipeOutput(taskID, stream string, reader io.Reader) {
	pipeOutput(taskID, stream, reader)
}

func (p *Pi) RpcCommand(req task.TaskRequest) []string { return buildRPCCommand(req) }

func (p *Pi) ResolvedModelID(req task.TaskRequest) string { return resolvedModelID(req) }
