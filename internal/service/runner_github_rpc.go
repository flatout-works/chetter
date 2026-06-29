package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/internal/webhook"
)

// GitHubActionService is the subset of Service used by RunnerRPCService to
// perform GitHub operations and record the resulting artifacts.
type GitHubActionService interface {
	GitHubClient() *webhook.Client
	GitHubInstallationToken() (string, error)
	GitHubInstallationTokenForRepository(repo string) (string, error)
	GitHubReadInstallationTokenForRepository(repo string) (string, error)
	RecordArtifact(ctx context.Context, params RecordArtifactParams) error
	LogAuditEvent(ctx context.Context, params AuditEventParams) error
	GetTaskSignature(ctx context.Context, taskID string) (string, error)
}

// WithGitHubActions injects the GitHub action service into RunnerRPCService so
// that the runner-initiated GitHub RPCs can perform API calls and record artifacts.
func (s *RunnerRPCService) WithGitHubActions(gh GitHubActionService) *RunnerRPCService {
	s.ghActions = gh
	return s
}

func (s *RunnerRPCService) GitHubCreateIssue(ctx context.Context, req *connect.Request[runnerv1.GitHubCreateIssueRequest]) (*connect.Response[runnerv1.GitHubCreateIssueResponse], error) {
	gh, err := s.requireGitHub()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Msg.Title) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("title is required"))
	}
	if err := s.validateGitHubRPCRepoScope(ctx, req.Msg.TaskId, req.Msg.Repo); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	sig, err := s.ghActions.GetTaskSignature(ctx, req.Msg.TaskId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get task signature: %w", err))
	}
	body := appendChetterSignature(req.Msg.Body, sig)
	created, err := gh.CreateIssue(ctx, req.Msg.Repo, req.Msg.Title, body, req.Msg.Labels)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create GitHub issue: %w", err))
	}
	if err := s.recordGitHubRPCArtifact(ctx, req.Msg.TaskId, "issue", req.Msg.Repo, created.Number, created.URL, ""); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&runnerv1.GitHubCreateIssueResponse{
		Number: int32(created.Number),
		Url:    created.URL,
	}), nil
}

func (s *RunnerRPCService) GitHubIssueComment(ctx context.Context, req *connect.Request[runnerv1.GitHubIssueCommentRequest]) (*connect.Response[runnerv1.GitHubIssueCommentResponse], error) {
	gh, err := s.requireGitHub()
	if err != nil {
		return nil, err
	}
	if err := s.validateGitHubRPCArtifactScope(ctx, req.Msg.TaskId, req.Msg.Repo, int(req.Msg.IssueNumber), "issue_or_pr"); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	sig, err := s.ghActions.GetTaskSignature(ctx, req.Msg.TaskId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get task signature: %w", err))
	}
	body := appendChetterSignature(req.Msg.Body, sig)
	created, err := gh.CreateIssueCommentWithResponse(ctx, req.Msg.Repo, int(req.Msg.IssueNumber), body)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create GitHub issue comment: %w", err))
	}
	if err := s.recordGitHubRPCArtifact(ctx, req.Msg.TaskId, "issue_comment", req.Msg.Repo, int(req.Msg.IssueNumber), created.URL, ""); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&runnerv1.GitHubIssueCommentResponse{Url: created.URL}), nil
}

func (s *RunnerRPCService) GitHubCreatePR(ctx context.Context, req *connect.Request[runnerv1.GitHubCreatePRRequest]) (*connect.Response[runnerv1.GitHubCreatePRResponse], error) {
	gh, err := s.requireGitHub()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Msg.Title) == "" || strings.TrimSpace(req.Msg.Head) == "" || strings.TrimSpace(req.Msg.Base) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("title, head, and base are required"))
	}
	if err := s.validateGitHubRPCRepoScope(ctx, req.Msg.TaskId, req.Msg.Repo); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	sig, err := s.ghActions.GetTaskSignature(ctx, req.Msg.TaskId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get task signature: %w", err))
	}
	body := appendChetterSignature(req.Msg.Body, sig)
	created, err := gh.CreatePullRequest(ctx, req.Msg.Repo, req.Msg.Title, body, req.Msg.Head, req.Msg.Base, req.Msg.Draft)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create GitHub pull request: %w", err))
	}
	if err := s.recordGitHubRPCArtifact(ctx, req.Msg.TaskId, "pr", req.Msg.Repo, created.Number, created.URL, req.Msg.Head); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&runnerv1.GitHubCreatePRResponse{
		Number: int32(created.Number),
		Url:    created.URL,
	}), nil
}

func (s *RunnerRPCService) GitHubPRReview(ctx context.Context, req *connect.Request[runnerv1.GitHubPRReviewRequest]) (*connect.Response[runnerv1.GitHubPRReviewResponse], error) {
	gh, err := s.requireGitHub()
	if err != nil {
		return nil, err
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
	if err := s.validateGitHubRPCArtifactScope(ctx, req.Msg.TaskId, req.Msg.Repo, int(req.Msg.PrNumber), "pr"); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	sig, err := s.ghActions.GetTaskSignature(ctx, req.Msg.TaskId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get task signature: %w", err))
	}
	body := appendChetterSignature(req.Msg.Body, sig)
	created, err := gh.CreatePullRequestReview(ctx, req.Msg.Repo, int(req.Msg.PrNumber), event, body)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create GitHub PR review: %w", err))
	}
	if err := s.recordGitHubRPCArtifact(ctx, req.Msg.TaskId, "pr_review", req.Msg.Repo, int(req.Msg.PrNumber), created.URL, ""); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&runnerv1.GitHubPRReviewResponse{Url: created.URL}), nil
}

func (s *RunnerRPCService) requireGitHub() (*webhook.Client, error) {
	if s.ghActions == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("GitHub App is not configured on this server"))
	}
	gh := s.ghActions.GitHubClient()
	if gh == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("GitHub App is not configured on this server"))
	}
	return gh, nil
}

func (s *RunnerRPCService) validateGitHubRPCArtifactScope(ctx context.Context, taskID, repo string, number int, artifactKind string) error {
	task, err := s.db.GetTaskByID(ctx, taskID)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("task %q not found", taskID)
		}
		return fmt.Errorf("get task: %w", err)
	}
	return validateGitHubToolArtifactScope(task, repo, number, artifactKind)
}

func (s *RunnerRPCService) validateGitHubRPCRepoScope(ctx context.Context, taskID, repo string) error {
	task, err := s.db.GetTaskByID(ctx, taskID)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("task %q not found", taskID)
		}
		return fmt.Errorf("get task: %w", err)
	}
	return validateGitHubToolRepoScope(task, repo)
}

func (s *RunnerRPCService) recordGitHubRPCArtifact(ctx context.Context, taskID, artifactType, repo string, number int, url, ref string) error {
	var agentSessionID, sessionRunID string
	if run, err := s.db.GetSessionRunByTaskID(ctx, taskID); err == nil {
		agentSessionID = run.AgentSessionID
		sessionRunID = run.ID
	} else if err != sql.ErrNoRows {
		return fmt.Errorf("get session run: %w", err)
	}
	if err := s.ghActions.RecordArtifact(ctx, RecordArtifactParams{
		TaskID:          taskID,
		AgentSessionID:  agentSessionID,
		SessionRunID:    sessionRunID,
		ArtifactType:    artifactType,
		Repo:            repo,
		Number:          number,
		URL:             url,
		Ref:             ref,
		DiscoverySource: "rpc_tool",
	}); err != nil {
		return fmt.Errorf("record GitHub artifact: %w", err)
	}
	if err := s.ghActions.LogAuditEvent(ctx, AuditEventParams{
		EventType:  "github_artifact_created",
		SourceType: "task",
		SourceID:   taskID,
		TargetType: artifactType,
		TargetID:   fmt.Sprintf("%s#%d", repo, number),
		Repo:       repo,
		Detail:     fmt.Sprintf("created %s %s#%d via runner RPC", artifactType, repo, number),
	}); err != nil {
		return fmt.Errorf("log GitHub artifact audit event: %w", err)
	}
	return nil
}
