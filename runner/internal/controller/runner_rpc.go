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
	GetGitHubCredential(context.Context, *connect.Request[runnerv1.GetGitHubCredentialRequest]) (*connect.Response[runnerv1.GetGitHubCredentialResponse], error)
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
	// A single claim loop polls for tasks; concurrency is bounded by the
	// semaphore in runTask (one extra slot is reserved for this poller).
	// Previously one goroutine per concurrent slot long-polled the server,
	// each issuing a DB transaction every second while idle — that polling
	// dominated the fleet's query rate. See internal/service/runner_rpc.go
	// claimPollInterval.
	go r.claimLoop(ctx)

	<-ctx.Done()
	if r.draining.Load() {
		// On a graceful drain (operator-initiated or SIGTERM/SIGINT via
		// BeginGracefulShutdown) wait for in-flight tasks to finish within the
		// timeout-aware drain deadline (derived from the tasks' own remaining
		// timeouts, clamped by CHETTER_DRAIN_TIMEOUT_SEC), force-cancelling
		// any that overrun. Record whether we had to force-cancel so main.go
		// can set the exit code. See issues #97 and #160.
		r.forcedExit.Store(r.waitDrain(r.computeDrainDeadline()))
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
		// Reserve a concurrency slot before claiming so we never hold a
		// claimed task while waiting for a free slot. The semaphore carries
		// one extra slot (MaxConcurrent+1) reserved for this poller, so the
		// runner still executes up to MaxConcurrent tasks concurrently. The
		// slot is transferred to runTask on a successful claim (its defer
		// releases it) and released here on every other path.
		select {
		case r.sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		if r.draining.Load() {
			<-r.sem
			return
		}
		resp, err := r.claimClient.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
			RunnerId:     r.runnerID,
			WaitSeconds:  30,
			LeaseSeconds: 120,
		}))
		if err != nil {
			<-r.sem
			if ctx.Err() != nil {
				return
			}
			slog.Warn("claim task failed", "err", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if resp.Msg.Task == nil || resp.Msg.Task.TaskId == "" {
			<-r.sem
			if ctx.Err() != nil {
				return
			}
			continue
		}

		if r.draining.Load() {
			<-r.sem
			return
		}
		go r.runTask(protoTaskToRequest(resp.Msg.Task))
	}
}

func protoTaskToRequest(t *runnerv1.Task) task.TaskRequest {
	timeoutSec := int(t.TimeoutSeconds)
	if timeoutSec == 0 {
		timeoutSec = defaultTaskTimeoutSec
	}
	req := task.TaskRequest{
		TaskID:                 t.TaskId,
		ExecutionID:            t.ExecutionId,
		ClaimID:                t.ClaimId,
		AgentSessionID:         t.AgentSessionId,
		UserPromptID:           t.UserPromptId,
		AgentImage:             t.AgentImage,
		Prompt:                 t.Prompt,
		GitURL:                 t.GitUrl,
		GitRef:                 t.GitRef,
		GitHubRepo:             t.GithubRepo,
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
		IsolationRequired:      t.IsolationRequired,
		Harness:                t.Harness,
		AgentDefinition:        t.AgentDefinition,
		SkillDefinitions:       t.SkillDefinitions,
		ExtraFiles:             t.ExtraFiles,
		SelfTestNonce:          t.SelfTestNonce,
		SelfTestCheck:          t.SelfTestCheck,
		GitIdentityID:          t.GitIdentityId,
		GitAuthorName:          t.GitAuthorName,
		GitAuthorEmail:         t.GitAuthorEmail,
	}
	for _, endpoint := range t.McpEndpoints {
		if endpoint == nil {
			continue
		}
		req.McpEndpoints = append(req.McpEndpoints, task.MCPEndpoint{
			Name:           endpoint.Name,
			Transport:      endpoint.Transport,
			URL:            endpoint.Url,
			Headers:        endpoint.Headers,
			BearerTokenEnv: endpoint.BearerTokenEnv,
		})
	}
	return req
}

func (r *Runner) reportTaskResponse(resp task.TaskResponse) {
	terminal := isTerminalStatus(resp.Status)
	if terminal {
		r.recordTerminalStatus(resp.ExecutionID, resp.Status)
		// Track the in-flight terminal report on the report barrier so the
		// drain cleanup phase can join on it before exit (issue #313).
		r.reportWG.Add(1)
		defer r.reportWG.Done()
	}
	r.dispatchReport(resp, terminal)
}

func (r *Runner) dispatchReport(resp task.TaskResponse, terminal bool) {
	event := &runnerv1.TaskEvent{
		TaskId:            resp.TaskID,
		ExecutionId:       resp.ExecutionID,
		ClaimId:           resp.ClaimID,
		AgentSessionId:    resp.AgentSessionID,
		UserPromptId:      resp.UserPromptID,
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
			InputTokens:      resp.TokenUsage.InputTokens,
			OutputTokens:     resp.TokenUsage.OutputTokens,
			CacheReadTokens:  resp.TokenUsage.CacheReadTokens,
			CacheWriteTokens: resp.TokenUsage.CacheWriteTokens,
			ReasoningTokens:  resp.TokenUsage.ReasoningTokens,
			CostCents:        resp.TokenUsage.CostCents,
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
	// The retry window is bounded by terminalReportRetryWindow (fixed at loop
	// entry, preserving the pre-existing 1-minute bound), but once a forced
	// drain has started (drainHardKillDeadline set by waitForTaskCleanup) the
	// window is clamped to the remaining hard-kill budget each iteration so
	// total reporting time is provably <= CHETTER_DRAIN_HARD_KILL_TIMEOUT_SEC
	// and a blocked report can never outlive the runner. See issue #313.
	deadline := time.Now().Add(terminalReportRetryWindow)
	for {
		if hard := r.drainHardKillDeadlineValue(); !hard.IsZero() && hard.Before(deadline) {
			deadline = hard
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			slog.Error("terminal task event report budget exhausted", "taskID", resp.TaskID, "status", resp.Status)
			return
		}
		attempt := 10 * time.Second
		if remaining < attempt {
			attempt = remaining
		}
		ctx, cancel := context.WithTimeout(context.Background(), attempt)
		err := report(ctx)
		cancel()
		if err == nil {
			r.markTerminalReportDelivered(resp.ExecutionID)
			return
		}
		if time.Now().After(deadline) {
			slog.Error("failed to report terminal task event", "taskID", resp.TaskID, "status", resp.Status, "err", err)
			return
		}
		slog.Warn("retrying terminal task event report", "taskID", resp.TaskID, "status", resp.Status, "err", err)
		backoff := 2 * time.Second
		if backoff > remaining {
			backoff = remaining
		}
		time.Sleep(backoff)
	}
}

func isTerminalStatus(status string) bool {
	return status == "done" || status == "error" || status == "cancelled"
}

// drainHardKillDeadlineValue returns the forced-cleanup deadline, or the zero
// time when no forced cleanup is in progress. Guarded by r.mu.
func (r *Runner) drainHardKillDeadlineValue() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.drainHardKillDeadline
}

// markTerminalReportDelivered records that the server accepted the terminal
// report for an execution. The hard-kill audit log uses this to report which
// in-flight executions lost their terminal result. See issue #313.
func (r *Runner) markTerminalReportDelivered(executionID string) {
	if executionID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reportDelivered == nil {
		r.reportDelivered = make(map[string]bool)
	}
	r.reportDelivered[executionID] = true
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
