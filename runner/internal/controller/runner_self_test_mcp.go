package controller

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	runnermcp "github.com/flatout-works/chetter/runner/internal/mcp"
	"github.com/flatout-works/chetter/runner/internal/task"
)

const (
	runnerSelfTestToolName = "chetter_runner_self_test_echo"
	runnerSelfTestKind     = "runner_mcp_self_test"
	runnerSelfTestTimeout  = 10 * time.Second
)

func (r *Runner) registerSelfTestMCPTool(server *runnermcp.Server, req task.TaskRequest) error {
	if strings.TrimSpace(req.SelfTestNonce) == "" {
		return nil
	}
	if r.rpcClient == nil {
		return fmt.Errorf("runner RPC client is unavailable")
	}
	server.RegisterTool(runnermcp.ToolDef{
		Name:        runnerSelfTestToolName,
		Description: "Acknowledge a Chetter deployment self-test nonce through the authenticated runner bridge.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"nonce": map[string]string{"type": "string", "description": "Exact nonce supplied in the self-test prompt"},
			},
			"required": []string{"nonce"},
		},
	}, r.selfTestEchoTool(req))
	return nil
}

func (r *Runner) selfTestEchoTool(req task.TaskRequest) runnermcp.ToolHandler {
	return func(ctx context.Context, args map[string]any) (any, error) {
		nonce, err := requiredString(args, "nonce")
		if err != nil {
			return nil, err
		}
		if subtle.ConstantTimeCompare([]byte(nonce), []byte(req.SelfTestNonce)) != 1 {
			return nil, fmt.Errorf("self-test nonce does not match this execution")
		}
		githubCredentials := false
		if req.SelfTestCheck == "github:credentials" {
			if _, err := r.getGitHubCredential(ctx, req); err != nil {
				return nil, fmt.Errorf("verify GitHub App credential: %w", err)
			}
			githubCredentials = true
		}
		payload, err := json.Marshal(map[string]any{
			"kind": runnerSelfTestKind, "tool": runnerSelfTestToolName,
			"nonce": nonce, "observed": true, "check": req.SelfTestCheck,
			"github_credentials": githubCredentials,
		})
		if err != nil {
			return nil, fmt.Errorf("marshal self-test evidence: %w", err)
		}
		callCtx, cancel := context.WithTimeout(ctx, runnerSelfTestTimeout)
		defer cancel()
		_, err = r.rpcClient.ReportTaskEvents(callCtx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
			RunnerId: r.runnerID,
			Events: []*runnerv1.TaskEvent{{
				TaskId: req.TaskID, ExecutionId: req.ExecutionID, ClaimId: req.ClaimID,
				AgentSessionId: req.AgentSessionID, UserPromptId: req.UserPromptID,
				Status: "running", Summary: "runner self-test MCP echo observed", PayloadJson: string(payload),
			}},
		}))
		if err != nil {
			return nil, fmt.Errorf("persist self-test evidence: %w", err)
		}
		return map[string]any{"nonce": nonce, "observed": true}, nil
	}
}
