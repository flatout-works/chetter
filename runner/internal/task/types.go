// Package task defines the core data types for task requests, responses,
// sessions, and MCP reports exchanged between the runner and its agents.
package task

import (
	"context"
	"fmt"
	"time"
)

// TaskRequest is a task claimed from the control plane to spawn a new agent session.
type TaskRequest struct {
	TaskID      string            `json:"task_id"`
	AgentImage  string            `json:"agent_image"`       // e.g. "ghcr.io/opencode-ai/opencode:latest"
	Prompt      string            `json:"prompt,omitempty"`  // the actual task instruction for the harness
	Command     []string          `json:"command,omitempty"` // optional command override for the agent image
	GitURL      string            `json:"git_url,omitempty"` // optional: clone before starting
	GitRef      string            `json:"git_ref,omitempty"` // branch/tag/commit
	Agent       string            `json:"agent,omitempty"`   // optional OpenCode agent name
	ProviderID  string            `json:"provider_id,omitempty"`
	ModelID     string            `json:"model_id,omitempty"`
	VariantID   string            `json:"variant_id,omitempty"`
	Skills      []string          `json:"skills,omitempty"` // hints for the harness
	TimeoutSec  int               `json:"timeout_sec"`      // default 3600
	MaxMemoryMB int               `json:"max_memory_mb"`    // default 4096
	MaxCPU      int               `json:"max_cpu"`          // default 2
	Env         map[string]string `json:"env,omitempty"`    // extra env vars for harness
}

// TaskResponse carries a task status event reported back to the control plane.
type TaskResponse struct {
	TaskID            string    `json:"task_id"`
	Status            string    `json:"status"` // pending, running, done, error, cancelled
	Summary           string    `json:"summary,omitempty"`
	Error             string    `json:"error,omitempty"`
	Artifacts         []string  `json:"artifacts,omitempty"`
	ProviderID        string    `json:"provider_id,omitempty"`
	ModelID           string    `json:"model_id,omitempty"`
	VariantID         string    `json:"variant_id,omitempty"`
	OpenCodeSessionID string    `json:"opencode_session_id,omitempty"`
	RunnerImageDigest string    `json:"runner_image_digest,omitempty"`
	StartedAt         time.Time `json:"started_at,omitempty"`
	EndedAt           time.Time `json:"ended_at,omitempty"`
}

// TaskSession represents one running task inside the runner.
type TaskSession struct {
	TaskID       string
	Request      TaskRequest
	WorkspaceDir string
	SocketPath   string
	StartedAt    time.Time
	Ctx          context.Context
	Cancel       context.CancelFunc
	ResultChan   chan TaskResponse
}

// SocketPath returns the path to the MCP Unix socket for a given task ID.
// Uses /tmp with a short prefix to stay under the 108-char Unix socket path limit.
// This is shared between the runner and builder so both can compute the same path.
func SocketPath(taskID string) string {
	shortID := taskID
	if len(shortID) > 12 {
		shortID = shortID[len(shortID)-12:]
	}
	return fmt.Sprintf("/tmp/chetter-%s.sock", shortID)
}

// Report is sent by MCP-aware agents via the report_result tool.
type Report struct {
	Status    string   `json:"status"` // success, error, cancelled
	Summary   string   `json:"summary,omitempty"`
	Artifacts []string `json:"artifacts,omitempty"`
}
