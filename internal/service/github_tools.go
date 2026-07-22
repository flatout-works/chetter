package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/webhook"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GitHubCreateIssueInput struct {
	TaskID             string   `json:"task_id" jsonschema:"Chetter task ID from CHETTER_TASK_ID"`
	ExecutionAttemptID string   `json:"execution_attempt_id" jsonschema:"Execution attempt ID from CHETTER_EXECUTION_ID"`
	Repo               string   `json:"repo" jsonschema:"Repository, e.g. flatout-works/chetter"`
	Title              string   `json:"title" jsonschema:"Issue title"`
	Body               string   `json:"body,omitempty" jsonschema:"Issue body without the Chetter footer"`
	Labels             []string `json:"labels,omitempty" jsonschema:"Labels to apply to the issue"`
}

type GitHubIssueCommentInput struct {
	TaskID             string `json:"task_id" jsonschema:"Chetter task ID from CHETTER_TASK_ID"`
	ExecutionAttemptID string `json:"execution_attempt_id" jsonschema:"Execution attempt ID from CHETTER_EXECUTION_ID"`
	Repo               string `json:"repo" jsonschema:"Repository, e.g. flatout-works/chetter"`
	IssueNumber        int    `json:"issue_number" jsonschema:"Issue or PR number to comment on"`
	Body               string `json:"body" jsonschema:"Comment body without the Chetter footer"`
}

type GitHubCreatePRInput struct {
	TaskID             string `json:"task_id" jsonschema:"Chetter task ID from CHETTER_TASK_ID"`
	ExecutionAttemptID string `json:"execution_attempt_id" jsonschema:"Execution attempt ID from CHETTER_EXECUTION_ID"`
	Repo               string `json:"repo" jsonschema:"Repository, e.g. flatout-works/chetter"`
	Title              string `json:"title" jsonschema:"Pull request title"`
	Body               string `json:"body,omitempty" jsonschema:"Pull request body without the Chetter footer"`
	Head               string `json:"head" jsonschema:"Head branch or owner:branch"`
	Base               string `json:"base" jsonschema:"Base branch"`
	Draft              bool   `json:"draft,omitempty" jsonschema:"Create a draft pull request"`
}

type GitHubPRReviewInput struct {
	TaskID             string `json:"task_id" jsonschema:"Chetter task ID from CHETTER_TASK_ID"`
	ExecutionAttemptID string `json:"execution_attempt_id" jsonschema:"Execution attempt ID from CHETTER_EXECUTION_ID"`
	Repo               string `json:"repo" jsonschema:"Repository, e.g. flatout-works/chetter"`
	PRNumber           int    `json:"pr_number" jsonschema:"Pull request number to review"`
	Event              string `json:"event,omitempty" jsonschema:"Review event: COMMENT, APPROVE, or REQUEST_CHANGES (default COMMENT)"`
	Body               string `json:"body" jsonschema:"Review body without the Chetter footer"`
}

type GitHubArtifactOutput struct {
	TaskID             string `json:"task_id"`
	ExecutionAttemptID string `json:"execution_attempt_id"`
	Repo               string `json:"repo"`
	ArtifactType       string `json:"artifact_type"`
	Number             int    `json:"number,omitempty"`
	URL                string `json:"url,omitempty"`
	Body               string `json:"body,omitempty"`
}

func (s *Service) createGitHubIssueTool(ctx context.Context, _ *mcp.CallToolRequest, in GitHubCreateIssueInput) (*mcp.CallToolResult, GitHubArtifactOutput, error) {
	if err := requireGitHubToolFields(in.TaskID, in.ExecutionAttemptID, in.Repo); err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	if strings.TrimSpace(in.Title) == "" {
		return nil, GitHubArtifactOutput{}, fmt.Errorf("title is required")
	}
	task, userPrompt, err := s.githubToolTaskContext(ctx, in.TaskID, in.ExecutionAttemptID)
	if err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	body := appendChetterSignature(in.Body, s.githubToolSignature(ctx, task, userPrompt, in.ExecutionAttemptID))
	created, err := s.githubClient().CreateIssue(ctx, in.Repo, in.Title, body, in.Labels)
	if err != nil {
		return nil, GitHubArtifactOutput{}, fmt.Errorf("create GitHub issue: %w", err)
	}
	return s.recordGitHubToolArtifact(ctx, task, userPrompt, in.ExecutionAttemptID, "issue", in.Repo, created.Number, created.URL, "", body, map[string]any{
		"title":  in.Title,
		"labels": in.Labels,
	})
}

func (s *Service) createGitHubIssueCommentTool(ctx context.Context, _ *mcp.CallToolRequest, in GitHubIssueCommentInput) (*mcp.CallToolResult, GitHubArtifactOutput, error) {
	if err := requireGitHubToolFields(in.TaskID, in.ExecutionAttemptID, in.Repo); err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	if in.IssueNumber <= 0 {
		return nil, GitHubArtifactOutput{}, fmt.Errorf("issue_number is required")
	}
	task, userPrompt, err := s.githubToolTaskContext(ctx, in.TaskID, in.ExecutionAttemptID)
	if err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	body := appendChetterSignature(in.Body, s.githubToolSignature(ctx, task, userPrompt, in.ExecutionAttemptID))
	created, err := s.githubClient().CreateIssueCommentWithResponse(ctx, in.Repo, in.IssueNumber, body)
	if err != nil {
		return nil, GitHubArtifactOutput{}, fmt.Errorf("create GitHub issue comment: %w", err)
	}
	return s.recordGitHubToolArtifact(ctx, task, userPrompt, in.ExecutionAttemptID, "issue_comment", in.Repo, in.IssueNumber, created.URL, "", body, map[string]any{
		"issue_number": in.IssueNumber,
	})
}

func (s *Service) createGitHubPRTool(ctx context.Context, _ *mcp.CallToolRequest, in GitHubCreatePRInput) (*mcp.CallToolResult, GitHubArtifactOutput, error) {
	if err := requireGitHubToolFields(in.TaskID, in.ExecutionAttemptID, in.Repo); err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	if strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Head) == "" || strings.TrimSpace(in.Base) == "" {
		return nil, GitHubArtifactOutput{}, fmt.Errorf("title, head, and base are required")
	}
	task, userPrompt, err := s.githubToolTaskContext(ctx, in.TaskID, in.ExecutionAttemptID)
	if err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	body := appendChetterSignature(in.Body, s.githubToolSignature(ctx, task, userPrompt, in.ExecutionAttemptID))
	created, err := s.githubClient().CreatePullRequest(ctx, in.Repo, in.Title, body, in.Head, in.Base, in.Draft)
	if err != nil {
		return nil, GitHubArtifactOutput{}, fmt.Errorf("create GitHub pull request: %w", err)
	}
	return s.recordGitHubToolArtifact(ctx, task, userPrompt, in.ExecutionAttemptID, "pr", in.Repo, created.Number, created.URL, in.Head, body, map[string]any{
		"title": in.Title,
		"head":  in.Head,
		"base":  in.Base,
		"draft": in.Draft,
	})
}

func (s *Service) createGitHubPRReviewTool(ctx context.Context, _ *mcp.CallToolRequest, in GitHubPRReviewInput) (*mcp.CallToolResult, GitHubArtifactOutput, error) {
	if err := requireGitHubToolFields(in.TaskID, in.ExecutionAttemptID, in.Repo); err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	if in.PRNumber <= 0 {
		return nil, GitHubArtifactOutput{}, fmt.Errorf("pr_number is required")
	}
	event := strings.ToUpper(strings.TrimSpace(in.Event))
	if event == "" {
		event = "COMMENT"
	}
	switch event {
	case "COMMENT", "APPROVE", "REQUEST_CHANGES":
	default:
		return nil, GitHubArtifactOutput{}, fmt.Errorf("event must be COMMENT, APPROVE, or REQUEST_CHANGES")
	}
	task, userPrompt, err := s.githubToolTaskContext(ctx, in.TaskID, in.ExecutionAttemptID)
	if err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	body := appendChetterSignature(in.Body, s.githubToolSignature(ctx, task, userPrompt, in.ExecutionAttemptID))
	created, err := s.githubClient().CreatePullRequestReview(ctx, in.Repo, in.PRNumber, event, body)
	if err != nil {
		return nil, GitHubArtifactOutput{}, fmt.Errorf("create GitHub pull request review: %w", err)
	}
	return s.recordGitHubToolArtifact(ctx, task, userPrompt, in.ExecutionAttemptID, "pr_review", in.Repo, in.PRNumber, created.URL, "", body, map[string]any{
		"pr_number": in.PRNumber,
		"event":     event,
	})
}

func (s *Service) githubClient() *webhook.Client {
	return s.github
}

func (s *Service) GitHubClient() *webhook.Client {
	return s.githubClient()
}

func (s *Service) GetTaskSignature(ctx context.Context, taskID, executionAttemptID string) (string, error) {
	task, userPrompt, err := s.githubToolTaskContext(ctx, taskID, executionAttemptID)
	if err != nil {
		return "", err
	}
	return s.githubToolSignature(ctx, task, userPrompt, executionAttemptID), nil
}

func requireGitHubToolFields(taskID, executionAttemptID, repo string) error {
	if strings.TrimSpace(taskID) == "" {
		return fmt.Errorf("task_id is required")
	}
	if strings.TrimSpace(executionAttemptID) == "" {
		return fmt.Errorf("execution_attempt_id is required")
	}
	if strings.TrimSpace(repo) == "" {
		return fmt.Errorf("repo is required")
	}
	return nil
}

func (s *Service) githubToolTaskContext(ctx context.Context, taskID, executionAttemptID string) (repository.ChetterTask, repository.ChetterUserPrompt, error) {
	if s.githubClient() == nil {
		return repository.ChetterTask{}, repository.ChetterUserPrompt{}, fmt.Errorf("GitHub App client is not configured")
	}
	ownership, err := s.repo.GetExecutionAttemptContext(ctx, executionAttemptID)
	if err != nil {
		return repository.ChetterTask{}, repository.ChetterUserPrompt{}, fmt.Errorf("get execution attempt context: %w", err)
	}
	if ownership.TaskID != taskID {
		return repository.ChetterTask{}, repository.ChetterUserPrompt{}, fmt.Errorf("execution attempt %q does not belong to task %q", executionAttemptID, taskID)
	}
	task, err := s.repo.GetTaskByID(ctx, taskID)
	if err != nil {
		if err == sql.ErrNoRows {
			return repository.ChetterTask{}, repository.ChetterUserPrompt{}, fmt.Errorf("task %q not found", taskID)
		}
		return repository.ChetterTask{}, repository.ChetterUserPrompt{}, fmt.Errorf("get task: %w", err)
	}
	if scope, ok := auth.GetScope(ctx); ok && !scope.Admin && (!task.TeamID.Valid || !scope.HasTeam(task.TeamID.String)) {
		return repository.ChetterTask{}, repository.ChetterUserPrompt{}, fmt.Errorf("task %q not found", taskID)
	}
	userPrompt, err := s.repo.GetUserPromptByID(ctx, ownership.UserPromptID)
	if err != nil {
		return repository.ChetterTask{}, repository.ChetterUserPrompt{}, fmt.Errorf("get user prompt: %w", err)
	}
	return task, userPrompt, nil
}

func (s *Service) githubToolSignature(ctx context.Context, task repository.ChetterTask, userPrompt repository.ChetterUserPrompt, executionAttemptID string) string {
	taskLink := task.ID
	if s.cfg.WebURL != "" {
		taskLink = fmt.Sprintf("[%s](%s/tasks/%s)", task.ID, strings.TrimRight(s.cfg.WebURL, "/"), task.ID)
	}
	parts := []string{fmt.Sprintf("Task: %s", taskLink)}
	if userPrompt.AgentSessionID != "" {
		parts = append(parts, "Session: "+userPrompt.AgentSessionID)
	}
	if userPrompt.ID != "" {
		parts = append(parts, "Prompt: "+userPrompt.ID)
	}
	parts = append(parts, "Attempt: "+executionAttemptID)
	if session, err := s.repo.GetAgentSessionByID(ctx, userPrompt.AgentSessionID); err == nil {
		if agent := strings.TrimSpace(session.Agent.String); agent != "" {
			parts = append(parts, "Agent: "+agent)
		}
		if model := strings.TrimSpace(session.ModelID.String); model != "" {
			parts = append(parts, "Model: "+model)
		}
	}
	return "---\n" + strings.Join(parts, " | ")
}

func appendChetterSignature(body, signature string) string {
	body = strings.TrimSpace(stripExistingChetterSignature(body))
	if body == "" {
		return signature
	}
	return body + "\n\n" + signature
}

func stripExistingChetterSignature(body string) string {
	idx := strings.LastIndex(body, "---\nTask:")
	if idx >= 0 {
		return strings.TrimSpace(body[:idx])
	}
	idx = strings.Index(body, "---\nGenerated by [Chetter]")
	if idx >= 0 {
		return strings.TrimSpace(body[:idx])
	}
	return body
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (s *Service) recordGitHubToolArtifact(ctx context.Context, task repository.ChetterTask, userPrompt repository.ChetterUserPrompt, executionAttemptID, artifactType, repo string, number int, url, ref, body string, detail map[string]any) (*mcp.CallToolResult, GitHubArtifactOutput, error) {
	if err := s.RecordArtifact(ctx, RecordArtifactParams{
		TaskID:             task.ID,
		AgentSessionID:     userPrompt.AgentSessionID,
		UserPromptID:       userPrompt.ID,
		ExecutionAttemptID: executionAttemptID,
		ArtifactType:       artifactType,
		Repo:               repo,
		Number:             number,
		URL:                url,
		Ref:                ref,
		DiscoverySource:    "mcp_tool",
	}); err != nil {
		return nil, GitHubArtifactOutput{}, fmt.Errorf("record GitHub artifact: %w", err)
	}
	payload, err := json.Marshal(detail)
	if err != nil {
		return nil, GitHubArtifactOutput{}, fmt.Errorf("marshal payload: %w", err)
	}
	if err := s.LogAuditEvent(ctx, AuditEventParams{
		EventType:  "github_artifact_created",
		SourceType: "task",
		SourceID:   task.ID,
		TargetType: artifactType,
		TargetID:   fmt.Sprintf("%s#%d", repo, number),
		Repo:       repo,
		Detail:     fmt.Sprintf("created %s %s#%d via Chetter MCP tool", artifactType, repo, number),
		Payload:    payload,
	}); err != nil {
		return nil, GitHubArtifactOutput{}, fmt.Errorf("log GitHub artifact audit event: %w", err)
	}
	return nil, GitHubArtifactOutput{TaskID: task.ID, ExecutionAttemptID: executionAttemptID, Repo: repo, ArtifactType: artifactType, Number: number, URL: url, Body: body}, nil
}
