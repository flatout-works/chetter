package controller

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/runner/internal/task"
)

type selfTestRPCClient struct {
	runnerRPCClient
	events      []*runnerv1.TaskEvent
	err         error
	githubErr   error
	githubCalls int
}

func (c *selfTestRPCClient) GetGitHubCredential(_ context.Context, _ *connect.Request[runnerv1.GetGitHubCredentialRequest]) (*connect.Response[runnerv1.GetGitHubCredentialResponse], error) {
	c.githubCalls++
	if c.githubErr != nil {
		return nil, c.githubErr
	}
	return connect.NewResponse(&runnerv1.GetGitHubCredentialResponse{
		Username: "x-access-token", Token: "installation-token", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano),
	}), nil
}

func (c *selfTestRPCClient) ReportTaskEvents(_ context.Context, req *connect.Request[runnerv1.ReportTaskEventsRequest]) (*connect.Response[runnerv1.ReportTaskEventsResponse], error) {
	if c.err != nil {
		return nil, c.err
	}
	c.events = append(c.events, req.Msg.Events...)
	return connect.NewResponse(&runnerv1.ReportTaskEventsResponse{}), nil
}

func TestSelfTestEchoToolPersistsFencedEvidence(t *testing.T) {
	client := &selfTestRPCClient{}
	runner := &Runner{rpcClient: client, runnerID: "runner_1"}
	req := task.TaskRequest{
		TaskID: "task_1", ExecutionID: "exec_1", ClaimID: "claim_1",
		AgentSessionID: "sess_1", UserPromptID: "prompt_1", SelfTestNonce: "nonce_1",
	}
	result, err := runner.selfTestEchoTool(req)(context.Background(), map[string]any{"nonce": "nonce_1"})
	if err != nil {
		t.Fatalf("selfTestEchoTool: %v", err)
	}
	if result.(map[string]any)["observed"] != true || len(client.events) != 1 {
		t.Fatalf("result = %#v, events = %#v", result, client.events)
	}
	event := client.events[0]
	if event.TaskId != req.TaskID || event.ExecutionId != req.ExecutionID || event.ClaimId != req.ClaimID || event.AgentSessionId != req.AgentSessionID || event.UserPromptId != req.UserPromptID {
		t.Fatalf("event ownership = %+v", event)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(event.PayloadJson), &payload); err != nil {
		t.Fatalf("parse evidence: %v", err)
	}
	if payload["kind"] != runnerSelfTestKind || payload["tool"] != runnerSelfTestToolName || payload["nonce"] != req.SelfTestNonce || payload["observed"] != true {
		t.Fatalf("evidence payload = %#v", payload)
	}
}

func TestSelfTestEchoToolRejectsUnobservedSuccess(t *testing.T) {
	tests := []struct {
		name  string
		nonce string
		err   error
	}{
		{name: "wrong nonce", nonce: "wrong"},
		{name: "report failure", nonce: "nonce_1", err: errors.New("control plane unavailable")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &selfTestRPCClient{err: tt.err}
			runner := &Runner{rpcClient: client, runnerID: "runner_1"}
			req := task.TaskRequest{TaskID: "task_1", ExecutionID: "exec_1", ClaimID: "claim_1", AgentSessionID: "sess_1", UserPromptID: "prompt_1", SelfTestNonce: "nonce_1"}
			if _, err := runner.selfTestEchoTool(req)(context.Background(), map[string]any{"nonce": tt.nonce}); err == nil {
				t.Fatal("expected self-test tool failure")
			}
			if len(client.events) != 0 {
				t.Fatalf("failed call persisted %d events", len(client.events))
			}
		})
	}
}

func TestSelfTestGitHubCheckRequiresInstallationCredential(t *testing.T) {
	for _, tt := range []struct {
		name      string
		githubErr error
		wantErr   bool
	}{
		{name: "credential available"},
		{name: "credential unavailable", githubErr: errors.New("GitHub App is not configured"), wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &selfTestRPCClient{githubErr: tt.githubErr}
			runner := &Runner{rpcClient: client, runnerID: "runner_1"}
			req := task.TaskRequest{
				TaskID: "task_1", ExecutionID: "exec_1", ClaimID: "claim_1", AgentSessionID: "sess_1", UserPromptID: "prompt_1",
				SelfTestNonce: "nonce_1", SelfTestCheck: "github:credentials", GitHubRepo: "flatout-works/diagnostics",
			}
			_, err := runner.selfTestEchoTool(req)(context.Background(), map[string]any{"nonce": "nonce_1"})
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if client.githubCalls != 1 {
				t.Fatalf("GitHub credential calls = %d, want 1", client.githubCalls)
			}
			if got := len(client.events); got != 1 && !tt.wantErr || got != 0 && tt.wantErr {
				t.Fatalf("evidence events = %d, wantErr %v", got, tt.wantErr)
			}
		})
	}
}

func TestProtoTaskToRequestPreservesTrustedSelfTestNonce(t *testing.T) {
	req := protoTaskToRequest(&runnerv1.Task{TaskId: "task_1", ExecutionId: "exec_1", AgentSessionId: "sess_1", UserPromptId: "prompt_1", SelfTestNonce: "nonce_1", SelfTestCheck: "quick"})
	if req.SelfTestNonce != "nonce_1" || req.SelfTestCheck != "quick" {
		t.Fatalf("self-test metadata = nonce %q check %q", req.SelfTestNonce, req.SelfTestCheck)
	}
}
