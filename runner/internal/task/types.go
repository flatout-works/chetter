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
	TaskID                 string            `json:"task_id"`
	AgentImage             string            `json:"agent_image"`
	Prompt                 string            `json:"prompt,omitempty"`
	Command                []string          `json:"command,omitempty"`
	GitURL                 string            `json:"git_url,omitempty"`
	GitRef                 string            `json:"git_ref,omitempty"`
	Agent                  string            `json:"agent,omitempty"`
	ProviderID             string            `json:"provider_id,omitempty"`
	ModelID                string            `json:"model_id,omitempty"`
	VariantID              string            `json:"variant_id,omitempty"`
	Skills                 []string          `json:"skills,omitempty"`
	Harness                string            `json:"harness,omitempty"`
	TimeoutSec             int               `json:"timeout_sec"`
	MaxMemoryMB            int               `json:"max_memory_mb"`
	MaxCPU                 int               `json:"max_cpu"`
	Env                    map[string]string `json:"env,omitempty"`
	CheckpointAfterSuccess bool              `json:"checkpoint_after_success,omitempty"`
	ResumeCheckpointPath   string            `json:"resume_checkpoint_path,omitempty"`
	ResumeWorkspacePath    string            `json:"resume_workspace_path,omitempty"`
}

// TaskResponse carries a task status event reported back to the control plane.
type TaskResponse struct {
	TaskID            string    `json:"task_id"`
	Status            string    `json:"status"`
	Summary           string    `json:"summary,omitempty"`
	Error             string    `json:"error,omitempty"`
	Artifacts         []string  `json:"artifacts,omitempty"`
	ProviderID        string    `json:"provider_id,omitempty"`
	ModelID           string    `json:"model_id,omitempty"`
	VariantID         string    `json:"variant_id,omitempty"`
	OpenCodeSessionID string    `json:"opencode_session_id,omitempty"`
	RunnerImageDigest string    `json:"runner_image_digest,omitempty"`
	SessionExport     string    `json:"session_export,omitempty"`
	StartedAt         time.Time `json:"started_at,omitempty"`
	EndedAt           time.Time `json:"ended_at,omitempty"`
	CheckpointPath    string    `json:"checkpoint_path,omitempty"`
	WorkspacePath     string    `json:"workspace_path,omitempty"`
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
