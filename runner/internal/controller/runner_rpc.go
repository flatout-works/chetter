package controller

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/gen/proto/runner/v1/runnerv1connect"
	"github.com/flatout-works/chetter/runner/internal/task"
)

const (
	rpcTimeout                = 10 * time.Second
	claimTimeout              = 45 * time.Second
	terminalReportRetryWindow = time.Minute
)

type runnerRPCClient interface {
	RegisterRunner(context.Context, *connect.Request[runnerv1.RegisterRunnerRequest]) (*connect.Response[runnerv1.RegisterRunnerResponse], error)
	Heartbeat(context.Context, *connect.Request[runnerv1.HeartbeatRequest]) (*connect.Response[runnerv1.HeartbeatResponse], error)
	ClaimTask(context.Context, *connect.Request[runnerv1.ClaimTaskRequest]) (*connect.Response[runnerv1.ClaimTaskResponse], error)
	ReportTaskEvents(context.Context, *connect.Request[runnerv1.ReportTaskEventsRequest]) (*connect.Response[runnerv1.ReportTaskEventsResponse], error)
	PruneWorkspaces(context.Context, *connect.Request[runnerv1.PruneWorkspacesRequest]) (*connect.Response[runnerv1.PruneWorkspacesResponse], error)
	GitHubCreateIssue(context.Context, *connect.Request[runnerv1.GitHubCreateIssueRequest]) (*connect.Response[runnerv1.GitHubCreateIssueResponse], error)
	GitHubIssueComment(context.Context, *connect.Request[runnerv1.GitHubIssueCommentRequest]) (*connect.Response[runnerv1.GitHubIssueCommentResponse], error)
	GitHubCreatePR(context.Context, *connect.Request[runnerv1.GitHubCreatePRRequest]) (*connect.Response[runnerv1.GitHubCreatePRResponse], error)
	GitHubPRReview(context.Context, *connect.Request[runnerv1.GitHubPRReviewRequest]) (*connect.Response[runnerv1.GitHubPRReviewResponse], error)
}

func (r *Runner) startConnectRPC(ctx context.Context) error {
	client := &http.Client{Timeout: rpcTimeout}
	claimHTTP := &http.Client{Timeout: claimTimeout}
	if r.cfg.Server.AuthToken != "" {
		client.Transport = bearerRoundTripper{token: r.cfg.Server.AuthToken, next: http.DefaultTransport}
		claimHTTP.Transport = bearerRoundTripper{token: r.cfg.Server.AuthToken, next: http.DefaultTransport}
	}
	r.rpcClient = runnerv1connect.NewRunnerServiceClient(client, strings.TrimRight(r.cfg.Server.URL, "/"))
	r.claimClient = runnerv1connect.NewRunnerServiceClient(claimHTTP, strings.TrimRight(r.cfg.Server.URL, "/"))
	r.runCtx = ctx
	if _, err := r.rpcClient.RegisterRunner(ctx, connect.NewRequest(&runnerv1.RegisterRunnerRequest{Runner: r.runnerInfoProto("active")})); err != nil {
		return fmt.Errorf("register runner: %w", err)
	}
	go r.heartbeatLoop(ctx)

	pruneCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	if err := r.pruneOrphanedWorkspaces(pruneCtx); err != nil {
		slog.Warn("prune orphaned workspaces on startup", "err", err)
	}
	cancel()
	go r.pruneWorkspacesPeriodically(ctx)

	slog.Info("claiming tasks via ConnectRPC", "url", r.cfg.Server.URL)
	for i := 0; i < r.cfg.Runner.MaxConcurrent; i++ {
		go r.claimLoop(ctx)
	}

	<-ctx.Done()
	if r.draining.Load() {
		r.waitDrain(10 * time.Minute)
	}
	r.publishRunnerHeartbeat("stopping")
	r.stopNetwork()
	return nil
}

func (r *Runner) claimLoop(ctx context.Context) {
	for {
		if r.draining.Load() {
			return
		}
		resp, err := r.claimClient.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
			RunnerId:     r.runnerID,
			WaitSeconds:  30,
			LeaseSeconds: 120,
		}))
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("claim task failed", "err", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if resp.Msg.Task == nil || resp.Msg.Task.TaskId == "" {
			if ctx.Err() != nil {
				return
			}
			continue
		}

		if r.draining.Load() {
			return
		}
		select {
		case r.sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		r.runTask(protoTaskToRequest(resp.Msg.Task))
	}
}

func protoTaskToRequest(t *runnerv1.Task) task.TaskRequest {
	timeoutSec := int(t.TimeoutSeconds)
	if timeoutSec == 0 {
		timeoutSec = defaultTaskTimeoutSec
	}
	return task.TaskRequest{
		TaskID:                 t.TaskId,
		AgentImage:             t.AgentImage,
		Prompt:                 t.Prompt,
		GitURL:                 t.GitUrl,
		GitRef:                 t.GitRef,
		Agent:                  t.Agent,
		ProviderID:             t.ProviderId,
		ModelID:                t.ModelId,
		ProviderName:           t.ProviderName,
		ProviderBaseURL:        t.ProviderBaseUrl,
		ProviderAPIKeyEnv:      t.ProviderApiKeyEnv,
		ProviderAPI:            t.ProviderApi,
		ProviderAuthHeader:     t.ProviderAuthHeader,
		VariantID:              t.VariantId,
		Skills:                 t.Skills,
		TimeoutSec:             timeoutSec,
		MaxMemoryMB:            int(t.MaxMemoryMb),
		MaxCPU:                 int(t.MaxCpu),
		Env:                    t.Env,
		CheckpointAfterSuccess: t.CheckpointAfterSuccess,
		ResumeCheckpointPath:   t.ResumeCheckpointPath,
		ResumeWorkspacePath:    t.ResumeWorkspacePath,
		ResumeHarnessSessionID: t.ResumeHarnessSessionId,
		Harness:                t.Harness,
		AgentDefinition:        t.AgentDefinition,
		SkillDefinitions:       t.SkillDefinitions,
		ExtraFiles:             t.ExtraFiles,
	}
}

func (r *Runner) reportTaskResponse(resp task.TaskResponse) {
	terminal := isTerminalStatus(resp.Status)
	if terminal {
		r.recordTerminalStatus(resp.TaskID, resp.Status)
	}
	r.dispatchReport(resp, terminal)
}

func (r *Runner) dispatchReport(resp task.TaskResponse, terminal bool) {
	event := &runnerv1.TaskEvent{
		TaskId:            resp.TaskID,
		Status:            resp.Status,
		Summary:           resp.Summary,
		Error:             resp.Error,
		Artifacts:         resp.Artifacts,
		ProviderId:        resp.ProviderID,
		ModelId:           resp.ModelID,
		VariantId:         resp.VariantID,
		OpencodeSessionId: resp.OpenCodeSessionID,
		RunnerImageDigest: resp.RunnerImageDigest,
		SessionExport:     resp.SessionExport,
		StartedAt:         formatProtoTime(resp.StartedAt),
		EndedAt:           formatProtoTime(resp.EndedAt),
		CheckpointPath:    resp.CheckpointPath,
		WorkspacePath:     resp.WorkspacePath,
		ErrorCategory:     resp.ErrorCategory,
		TokenUsage: &runnerv1.TokenUsage{
			InputTokens:     resp.TokenUsage.InputTokens,
			OutputTokens:    resp.TokenUsage.OutputTokens,
			CacheReadTokens: resp.TokenUsage.CacheReadTokens,
			CacheWriteTokens: resp.TokenUsage.CacheWriteTokens,
			ReasoningTokens: resp.TokenUsage.ReasoningTokens,
			CostCents:       resp.TokenUsage.CostCents,
		},
	}
	report := func(ctx context.Context) error {
		_, err := r.rpcClient.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
			RunnerId: r.runnerID,
			Events:   []*runnerv1.TaskEvent{event},
		}))
		return err
	}
	if !terminal {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := report(ctx); err != nil {
			slog.Error("failed to report task event", "taskID", resp.TaskID, "status", resp.Status, "err", err)
		}
		return
	}
	go func() {
		deadline := time.Now().Add(terminalReportRetryWindow)
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := report(ctx)
			cancel()
			if err == nil {
				return
			}
			if time.Now().After(deadline) {
				slog.Error("failed to report terminal task event", "taskID", resp.TaskID, "status", resp.Status, "err", err)
				return
			}
			slog.Warn("retrying terminal task event report", "taskID", resp.TaskID, "status", resp.Status, "err", err)
			time.Sleep(2 * time.Second)
		}
	}()
}

func isTerminalStatus(status string) bool {
	return status == "done" || status == "error" || status == "cancelled"
}

func formatProtoTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

type bearerRoundTripper struct {
	token string
	next  http.RoundTripper
}

func (b bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(clone)
}
