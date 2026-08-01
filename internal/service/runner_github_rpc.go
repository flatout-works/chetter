package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/internal/githubrepo"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/webhook"
)

type githubActionAuthorization struct {
	client         *webhook.Client
	repo           string
	installationID int64
	signature      string
}

// GitHubActionService authorizes runner-owned execution attempts, resolves the
// task's immutable installation client, and records successful artifacts.
type GitHubActionService interface {
	authorizeGitHubAction(ctx context.Context, taskID, executionAttemptID, runnerID, repo string) (githubActionAuthorization, error)
	RecordArtifact(ctx context.Context, params RecordArtifactParams) error
	LogAuditEvent(ctx context.Context, params AuditEventParams) error
}

// WithGitHubActions injects the GitHub action service into RunnerRPCService so
// runner-initiated GitHub RPCs can be fenced before making external API calls.
func (s *RunnerRPCService) WithGitHubActions(gh GitHubActionService) *RunnerRPCService {
	s.ghActions = gh
	return s
}

func (s *RunnerRPCService) GitHubCreateIssue(ctx context.Context, req *connect.Request[runnerv1.GitHubCreateIssueRequest]) (*connect.Response[runnerv1.GitHubCreateIssueResponse], error) {
	if strings.TrimSpace(req.Msg.Title) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("title is required"))
	}
	authz, err := s.authorizeGitHubAction(ctx, req.Msg.TaskId, req.Msg.ExecutionId, req.Msg.RunnerId, req.Msg.Repo)
	if err != nil {
		return nil, err
	}
	body := appendChetterSignature(req.Msg.Body, authz.signature)
	created, err := authz.client.CreateIssue(ctx, authz.repo, req.Msg.Title, body, req.Msg.Labels)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("create GitHub issue: %w", err))
	}
	if err := s.recordGitHubRPCArtifact(ctx, req.Msg.TaskId, req.Msg.ExecutionId, "issue", authz.repo, authz.installationID, created.Number, created.URL, ""); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&runnerv1.GitHubCreateIssueResponse{Number: int32(created.Number), Url: created.URL}), nil
}

func (s *RunnerRPCService) GitHubIssueComment(ctx context.Context, req *connect.Request[runnerv1.GitHubIssueCommentRequest]) (*connect.Response[runnerv1.GitHubIssueCommentResponse], error) {
	if req.Msg.IssueNumber <= 0 || strings.TrimSpace(req.Msg.Body) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("issue_number and body are required"))
	}
	authz, err := s.authorizeGitHubAction(ctx, req.Msg.TaskId, req.Msg.ExecutionId, req.Msg.RunnerId, req.Msg.Repo)
	if err != nil {
		return nil, err
	}
	body := appendChetterSignature(req.Msg.Body, authz.signature)
	created, err := authz.client.CreateIssueCommentWithResponse(ctx, authz.repo, int(req.Msg.IssueNumber), body)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("create GitHub issue comment: %w", err))
	}
	if err := s.recordGitHubRPCArtifact(ctx, req.Msg.TaskId, req.Msg.ExecutionId, "issue_comment", authz.repo, authz.installationID, int(req.Msg.IssueNumber), created.URL, ""); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&runnerv1.GitHubIssueCommentResponse{Url: created.URL}), nil
}

func (s *RunnerRPCService) GitHubCreatePR(ctx context.Context, req *connect.Request[runnerv1.GitHubCreatePRRequest]) (*connect.Response[runnerv1.GitHubCreatePRResponse], error) {
	if strings.TrimSpace(req.Msg.Title) == "" || strings.TrimSpace(req.Msg.Head) == "" || strings.TrimSpace(req.Msg.Base) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("title, head, and base are required"))
	}
	authz, err := s.authorizeGitHubAction(ctx, req.Msg.TaskId, req.Msg.ExecutionId, req.Msg.RunnerId, req.Msg.Repo)
	if err != nil {
		return nil, err
	}
	body := appendChetterSignature(req.Msg.Body, authz.signature)
	created, err := authz.client.CreatePullRequest(ctx, authz.repo, req.Msg.Title, body, req.Msg.Head, req.Msg.Base, req.Msg.Draft)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("create GitHub pull request: %w", err))
	}
	if err := s.recordGitHubRPCArtifact(ctx, req.Msg.TaskId, req.Msg.ExecutionId, "pr", authz.repo, authz.installationID, created.Number, created.URL, req.Msg.Head); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&runnerv1.GitHubCreatePRResponse{Number: int32(created.Number), Url: created.URL}), nil
}

func (s *RunnerRPCService) GitHubPRReview(ctx context.Context, req *connect.Request[runnerv1.GitHubPRReviewRequest]) (*connect.Response[runnerv1.GitHubPRReviewResponse], error) {
	if req.Msg.PrNumber <= 0 || strings.TrimSpace(req.Msg.Body) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("pr_number and body are required"))
	}
	event := strings.ToUpper(strings.TrimSpace(req.Msg.Event))
	if event == "" {
		event = "COMMENT"
	}
	switch event {
	case "COMMENT", "APPROVE", "REQUEST_CHANGES":
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("event must be COMMENT, APPROVE, or REQUEST_CHANGES"))
	}
	authz, err := s.authorizeGitHubAction(ctx, req.Msg.TaskId, req.Msg.ExecutionId, req.Msg.RunnerId, req.Msg.Repo)
	if err != nil {
		return nil, err
	}
	body := appendChetterSignature(req.Msg.Body, authz.signature)
	created, err := authz.client.CreatePullRequestReview(ctx, authz.repo, int(req.Msg.PrNumber), event, body)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("create GitHub PR review: %w", err))
	}
	if err := s.recordGitHubRPCArtifact(ctx, req.Msg.TaskId, req.Msg.ExecutionId, "pr_review", authz.repo, authz.installationID, int(req.Msg.PrNumber), created.URL, ""); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&runnerv1.GitHubPRReviewResponse{Url: created.URL}), nil
}

func (s *RunnerRPCService) GetGitHubCredential(ctx context.Context, req *connect.Request[runnerv1.GetGitHubCredentialRequest]) (*connect.Response[runnerv1.GetGitHubCredentialResponse], error) {
	if req == nil || req.Msg == nil || strings.TrimSpace(req.Msg.RunnerId) == "" || strings.TrimSpace(req.Msg.TaskId) == "" || strings.TrimSpace(req.Msg.ExecutionId) == "" || strings.TrimSpace(req.Msg.Repo) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("runner_id, task_id, execution_id, and repo are required"))
	}
	authz, err := s.authorizeGitHubAction(ctx, req.Msg.TaskId, req.Msg.ExecutionId, req.Msg.RunnerId, req.Msg.Repo)
	if err != nil {
		return nil, err
	}
	credential, err := authz.client.CredentialForRepo(ctx, authz.repo, webhook.PermissionProfileTaskGit)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("issue repository-restricted GitHub credential: %w", err))
	}
	// The token exchange can outlive the lease that authorized it. Re-run the
	// full task/execution/runner/repository fence immediately before disclosure.
	if _, err := s.authorizeGitHubAction(ctx, req.Msg.TaskId, req.Msg.ExecutionId, req.Msg.RunnerId, req.Msg.Repo); err != nil {
		return nil, err
	}
	return connect.NewResponse(&runnerv1.GetGitHubCredentialResponse{
		Username:  "x-access-token",
		Token:     credential.Token,
		ExpiresAt: credential.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}), nil
}

func (s *RunnerRPCService) authorizeGitHubAction(ctx context.Context, taskID, executionAttemptID, runnerID, repo string) (githubActionAuthorization, error) {
	if s.ghActions == nil {
		return githubActionAuthorization{}, connect.NewError(connect.CodeUnavailable, fmt.Errorf("GitHub App is not configured on this server"))
	}
	return s.ghActions.authorizeGitHubAction(ctx, taskID, executionAttemptID, runnerID, repo)
}

func (s *Service) authorizeGitHubAction(ctx context.Context, taskID, executionAttemptID, runnerID, requestedRepo string) (githubActionAuthorization, error) {
	if strings.TrimSpace(taskID) == "" || strings.TrimSpace(executionAttemptID) == "" || strings.TrimSpace(runnerID) == "" || strings.TrimSpace(requestedRepo) == "" {
		return githubActionAuthorization{}, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("task_id, execution_id, runner_id, and repo are required"))
	}
	requested, err := githubrepo.Parse(requestedRepo)
	if err != nil {
		return githubActionAuthorization{}, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid repo: %w", err))
	}
	if s.githubManager() == nil {
		return githubActionAuthorization{}, connect.NewError(connect.CodeUnavailable, fmt.Errorf("GitHub App is not configured on this server"))
	}

	execution, err := s.repo.GetGitHubExecutionContext(ctx, executionAttemptID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return githubActionAuthorization{}, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("execution attempt is not authorized for task"))
		}
		return githubActionAuthorization{}, connect.NewError(connect.CodeInternal, fmt.Errorf("get GitHub execution context: %w", err))
	}
	if execution.TaskID != taskID {
		return githubActionAuthorization{}, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("execution attempt %q does not belong to task %q", executionAttemptID, taskID))
	}
	if execution.ExecutionStatus != "running" {
		return githubActionAuthorization{}, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("execution attempt %q is %s, not running", executionAttemptID, execution.ExecutionStatus))
	}
	if execution.TaskStatus != "running" {
		return githubActionAuthorization{}, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %q is %s, not running", taskID, execution.TaskStatus))
	}
	if !execution.LeaseExpiresAt.Valid {
		return githubActionAuthorization{}, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("execution attempt %q has no active lease", executionAttemptID))
	}
	if !execution.LeaseExpiresAt.Time.After(time.Now()) {
		return githubActionAuthorization{}, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("execution attempt %q lease has expired", executionAttemptID))
	}
	if !execution.RunnerID.Valid || execution.RunnerID.String != runnerID {
		return githubActionAuthorization{}, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("runner %q does not own execution attempt %q", runnerID, executionAttemptID))
	}
	if !execution.GithubRepo.Valid || strings.TrimSpace(execution.GithubRepo.String) == "" {
		return githubActionAuthorization{}, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %q has no GitHub repository identity", taskID))
	}
	taskRepo, err := githubrepo.Parse(execution.GithubRepo.String)
	if err != nil {
		return githubActionAuthorization{}, connect.NewError(connect.CodeInternal, fmt.Errorf("task %q has invalid GitHub repository identity: %w", taskID, err))
	}
	if requested.Normalized() != taskRepo.Normalized() {
		return githubActionAuthorization{}, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("requested repository %q does not match task repository %q", requested.FullName(), taskRepo.FullName()))
	}

	client, installationID, err := s.githubClientForExecution(ctx, execution, taskRepo.FullName())
	if err != nil {
		return githubActionAuthorization{}, err
	}
	return githubActionAuthorization{
		client:         client,
		repo:           taskRepo.FullName(),
		installationID: installationID,
		signature:      s.githubToolSignatureForContext(ctx, taskID, execution.AgentSessionID, execution.UserPromptID, executionAttemptID),
	}, nil
}

func (s *Service) githubClientForExecution(ctx context.Context, execution repository.GetGitHubExecutionContextRow, repo string) (*webhook.Client, int64, error) {
	if execution.GithubInstallationID.Valid {
		if execution.GithubInstallationID.Int64 <= 0 {
			return nil, 0, connect.NewError(connect.CodeInternal, fmt.Errorf("task %q has invalid GitHub installation ID", execution.TaskID))
		}
		client, err := s.githubManager().ClientForInstallation(ctx, execution.GithubInstallationID.Int64)
		if err != nil {
			return nil, 0, connect.NewError(connect.CodeUnavailable, fmt.Errorf("resolve pinned GitHub installation: %w", err))
		}
		return client, execution.GithubInstallationID.Int64, nil
	}

	client, err := s.githubManager().ClientForRepo(ctx, repo)
	if err != nil {
		return nil, 0, connect.NewError(connect.CodeUnavailable, fmt.Errorf("resolve GitHub repository installation: %w", err))
	}
	rows, err := s.repo.PinTaskGitHubInstallation(ctx, repository.PinTaskGitHubInstallationParams{
		GithubInstallationID: sql.NullInt64{Int64: client.InstallationID, Valid: true},
		UpdatedAt:            time.Now().UTC(),
		ID:                   execution.TaskID,
	})
	if err != nil {
		return nil, 0, connect.NewError(connect.CodeInternal, fmt.Errorf("pin task GitHub installation: %w", err))
	}
	if rows == 0 {
		reloaded, err := s.repo.GetGitHubExecutionContext(ctx, execution.ExecutionAttemptID)
		if err != nil {
			return nil, 0, connect.NewError(connect.CodeInternal, fmt.Errorf("reload task GitHub installation: %w", err))
		}
		if !reloaded.GithubInstallationID.Valid || reloaded.GithubInstallationID.Int64 != client.InstallationID {
			return nil, 0, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %q GitHub installation was pinned concurrently to a different installation", execution.TaskID))
		}
	}
	return client, client.InstallationID, nil
}

func (s *RunnerRPCService) recordGitHubRPCArtifact(ctx context.Context, taskID, executionAttemptID, artifactType, repo string, installationID int64, number int, url, ref string) error {
	if err := s.ghActions.RecordArtifact(ctx, RecordArtifactParams{
		TaskID:             taskID,
		ExecutionAttemptID: executionAttemptID,
		ArtifactType:       artifactType,
		Repo:               repo,
		Number:             number,
		URL:                url,
		Ref:                ref,
		DiscoverySource:    "rpc_tool",
	}); err != nil {
		return fmt.Errorf("record GitHub artifact: %w", err)
	}
	payload, err := json.Marshal(map[string]any{"github_installation_id": installationID})
	if err != nil {
		return fmt.Errorf("marshal GitHub artifact audit payload: %w", err)
	}
	if err := s.ghActions.LogAuditEvent(ctx, AuditEventParams{
		EventType:  "github_artifact_created",
		SourceType: "task",
		SourceID:   taskID,
		TargetType: artifactType,
		TargetID:   fmt.Sprintf("%s#%d", repo, number),
		Repo:       repo,
		Detail:     fmt.Sprintf("created %s %s#%d via runner RPC using GitHub installation %d", artifactType, repo, number, installationID),
		Payload:    payload,
	}); err != nil {
		return fmt.Errorf("log GitHub artifact audit event: %w", err)
	}
	return nil
}
