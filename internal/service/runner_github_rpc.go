package service

import (
	"context"
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
	RecordArtifact(ctx context.Context, params RecordArtifactParams) error
	LogAuditEvent(ctx context.Context, params AuditEventParams) error
	GetTaskSignature(ctx context.Context, taskID, executionAttemptID string) (string, error)
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
	sig, err := s.ghActions.GetTaskSignature(ctx, req.Msg.TaskId, req.Msg.ExecutionId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get task signature: %w", err))
	}
	body := appendChetterSignature(req.Msg.Body, sig)
	created, err := gh.CreateIssue(ctx, req.Msg.Repo, req.Msg.Title, body, req.Msg.Labels)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create GitHub issue: %w", err))
	}
	if err := s.recordGitHubRPCArtifact(ctx, req.Msg.TaskId, req.Msg.ExecutionId, "issue", req.Msg.Repo, created.Number, created.URL, ""); err != nil {
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
	sig, err := s.ghActions.GetTaskSignature(ctx, req.Msg.TaskId, req.Msg.ExecutionId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get task signature: %w", err))
	}
	body := appendChetterSignature(req.Msg.Body, sig)
	created, err := gh.CreateIssueCommentWithResponse(ctx, req.Msg.Repo, int(req.Msg.IssueNumber), body)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create GitHub issue comment: %w", err))
	}
	if err := s.recordGitHubRPCArtifact(ctx, req.Msg.TaskId, req.Msg.ExecutionId, "issue_comment", req.Msg.Repo, int(req.Msg.IssueNumber), created.URL, ""); err != nil {
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
	sig, err := s.ghActions.GetTaskSignature(ctx, req.Msg.TaskId, req.Msg.ExecutionId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get task signature: %w", err))
	}
	body := appendChetterSignature(req.Msg.Body, sig)
	created, err := gh.CreatePullRequest(ctx, req.Msg.Repo, req.Msg.Title, body, req.Msg.Head, req.Msg.Base, req.Msg.Draft)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create GitHub pull request: %w", err))
	}
	if err := s.recordGitHubRPCArtifact(ctx, req.Msg.TaskId, req.Msg.ExecutionId, "pr", req.Msg.Repo, created.Number, created.URL, req.Msg.Head); err != nil {
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
	sig, err := s.ghActions.GetTaskSignature(ctx, req.Msg.TaskId, req.Msg.ExecutionId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get task signature: %w", err))
	}
	body := appendChetterSignature(req.Msg.Body, sig)
	created, err := gh.CreatePullRequestReview(ctx, req.Msg.Repo, int(req.Msg.PrNumber), event, body)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create GitHub PR review: %w", err))
	}
	if err := s.recordGitHubRPCArtifact(ctx, req.Msg.TaskId, req.Msg.ExecutionId, "pr_review", req.Msg.Repo, int(req.Msg.PrNumber), created.URL, ""); err != nil {
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

func (s *RunnerRPCService) recordGitHubRPCArtifact(ctx context.Context, taskID, executionAttemptID, artifactType, repo string, number int, url, ref string) error {
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
