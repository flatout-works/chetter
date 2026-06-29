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
	TaskID string   `json:"task_id" jsonschema:"Chetter task ID from CHETTER_TASK_ID"`
	Repo   string   `json:"repo" jsonschema:"Repository, e.g. flatout-works/chetter"`
	Title  string   `json:"title" jsonschema:"Issue title"`
	Body   string   `json:"body,omitempty" jsonschema:"Issue body without the Chetter footer"`
	Labels []string `json:"labels,omitempty" jsonschema:"Labels to apply to the issue"`
}

type GitHubIssueCommentInput struct {
	TaskID      string `json:"task_id" jsonschema:"Chetter task ID from CHETTER_TASK_ID"`
	Repo        string `json:"repo" jsonschema:"Repository, e.g. flatout-works/chetter"`
	IssueNumber int    `json:"issue_number" jsonschema:"Issue or PR number to comment on"`
	Body        string `json:"body" jsonschema:"Comment body without the Chetter footer"`
}

type GitHubCreatePRInput struct {
	TaskID string `json:"task_id" jsonschema:"Chetter task ID from CHETTER_TASK_ID"`
	Repo   string `json:"repo" jsonschema:"Repository, e.g. flatout-works/chetter"`
	Title  string `json:"title" jsonschema:"Pull request title"`
	Body   string `json:"body,omitempty" jsonschema:"Pull request body without the Chetter footer"`
	Head   string `json:"head" jsonschema:"Head branch or owner:branch"`
	Base   string `json:"base" jsonschema:"Base branch"`
	Draft  bool   `json:"draft,omitempty" jsonschema:"Create a draft pull request"`
}

type GitHubPRReviewInput struct {
	TaskID           string `json:"task_id" jsonschema:"Chetter task ID from CHETTER_TASK_ID"`
	Repo             string `json:"repo" jsonschema:"Repository, e.g. flatout-works/chetter"`
	PRNumber         int    `json:"pr_number" jsonschema:"Pull request number to review"`
	Event            string `json:"event,omitempty" jsonschema:"Review event: COMMENT, APPROVE, or REQUEST_CHANGES (default COMMENT)"`
	Body             string `json:"body,omitempty" jsonschema:"Review body without the Chetter footer"`
	BodyTaskExportID string `json:"body_task_export_id,omitempty" jsonschema:"Use this task's session export as the review body without returning the export text to the caller"`
}

type GitHubArtifactOutput struct {
	TaskID       string `json:"task_id"`
	Repo         string `json:"repo"`
	ArtifactType string `json:"artifact_type"`
	Number       int    `json:"number,omitempty"`
	URL          string `json:"url,omitempty"`
	Body         string `json:"body,omitempty"`
}

const (
	reviewBodyStartMarker = "<!-- CHETTER_REVIEW_BODY_START -->"
	reviewBodyEndMarker   = "<!-- CHETTER_REVIEW_BODY_END -->"
)

func (s *Service) createGitHubIssueTool(ctx context.Context, _ *mcp.CallToolRequest, in GitHubCreateIssueInput) (*mcp.CallToolResult, GitHubArtifactOutput, error) {
	if err := requireAdminGitHubWriteTool(ctx); err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	if err := requireGitHubToolFields(in.TaskID, in.Repo); err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	if strings.TrimSpace(in.Title) == "" {
		return nil, GitHubArtifactOutput{}, fmt.Errorf("title is required")
	}
	task, sessionRun, err := s.githubToolTaskContext(ctx, in.TaskID)
	if err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	if err := validateGitHubToolCreationScope(task, in.Repo); err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	body := appendChetterSignature(in.Body, githubToolSignature(task, sessionRun, s.cfg.WebURL))
	created, err := s.githubClient().CreateIssue(ctx, in.Repo, in.Title, body, in.Labels)
	if err != nil {
		return nil, GitHubArtifactOutput{}, fmt.Errorf("create GitHub issue: %w", err)
	}
	return s.recordGitHubToolArtifact(ctx, task, sessionRun, "issue", in.Repo, created.Number, created.URL, "", body, map[string]any{
		"title":  in.Title,
		"labels": in.Labels,
	})
}

func (s *Service) createGitHubIssueCommentTool(ctx context.Context, _ *mcp.CallToolRequest, in GitHubIssueCommentInput) (*mcp.CallToolResult, GitHubArtifactOutput, error) {
	if err := requireAdminGitHubWriteTool(ctx); err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	if err := requireGitHubToolFields(in.TaskID, in.Repo); err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	if in.IssueNumber <= 0 {
		return nil, GitHubArtifactOutput{}, fmt.Errorf("issue_number is required")
	}
	task, sessionRun, err := s.githubToolTaskContext(ctx, in.TaskID)
	if err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	if err := validateGitHubToolArtifactScope(task, in.Repo, in.IssueNumber, "issue_or_pr"); err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	body := appendChetterSignature(in.Body, githubToolSignature(task, sessionRun, s.cfg.WebURL))
	created, err := s.githubClient().CreateIssueCommentWithResponse(ctx, in.Repo, in.IssueNumber, body)
	if err != nil {
		return nil, GitHubArtifactOutput{}, fmt.Errorf("create GitHub issue comment: %w", err)
	}
	return s.recordGitHubToolArtifact(ctx, task, sessionRun, "issue_comment", in.Repo, in.IssueNumber, created.URL, "", body, map[string]any{
		"issue_number": in.IssueNumber,
	})
}

func (s *Service) createGitHubPRTool(ctx context.Context, _ *mcp.CallToolRequest, in GitHubCreatePRInput) (*mcp.CallToolResult, GitHubArtifactOutput, error) {
	if err := requireAdminGitHubWriteTool(ctx); err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	if err := requireGitHubToolFields(in.TaskID, in.Repo); err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	if strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Head) == "" || strings.TrimSpace(in.Base) == "" {
		return nil, GitHubArtifactOutput{}, fmt.Errorf("title, head, and base are required")
	}
	task, sessionRun, err := s.githubToolTaskContext(ctx, in.TaskID)
	if err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	if err := validateGitHubToolCreationScope(task, in.Repo); err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	body := appendChetterSignature(in.Body, githubToolSignature(task, sessionRun, s.cfg.WebURL))
	created, err := s.githubClient().CreatePullRequest(ctx, in.Repo, in.Title, body, in.Head, in.Base, in.Draft)
	if err != nil {
		return nil, GitHubArtifactOutput{}, fmt.Errorf("create GitHub pull request: %w", err)
	}
	return s.recordGitHubToolArtifact(ctx, task, sessionRun, "pr", in.Repo, created.Number, created.URL, in.Head, body, map[string]any{
		"title": in.Title,
		"head":  in.Head,
		"base":  in.Base,
		"draft": in.Draft,
	})
}

func (s *Service) createGitHubPRReviewTool(ctx context.Context, _ *mcp.CallToolRequest, in GitHubPRReviewInput) (*mcp.CallToolResult, GitHubArtifactOutput, error) {
	if err := requireAdminGitHubWriteTool(ctx); err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	if err := requireGitHubToolFields(in.TaskID, in.Repo); err != nil {
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
	task, sessionRun, err := s.githubToolTaskContext(ctx, in.TaskID)
	if err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	if err := validateGitHubToolArtifactScope(task, in.Repo, in.PRNumber, "pr"); err != nil {
		return nil, GitHubArtifactOutput{}, err
	}
	bodySourceIsExport := strings.TrimSpace(in.BodyTaskExportID) != ""
	bodyText := in.Body
	if bodySourceIsExport {
		if strings.TrimSpace(in.Body) != "" {
			return nil, GitHubArtifactOutput{}, fmt.Errorf("body and body_task_export_id are mutually exclusive")
		}
		exportTask, err := s.taskForToolAccess(ctx, in.BodyTaskExportID)
		if err != nil {
			return nil, GitHubArtifactOutput{}, err
		}
		if !exportTask.SessionExport.Valid {
			return nil, GitHubArtifactOutput{}, fmt.Errorf("no session export available for task %s", in.BodyTaskExportID)
		}
		bodyText, err = reviewBodyFromSessionExport(strings.ReplaceAll(exportTask.SessionExport.String, "\\n", "\n"))
		if err != nil {
			return nil, GitHubArtifactOutput{}, err
		}
	}
	body := appendChetterSignature(bodyText, githubToolSignature(task, sessionRun, s.cfg.WebURL))
	created, err := s.githubClient().CreatePullRequestReview(ctx, in.Repo, in.PRNumber, event, body)
	if err != nil {
		return nil, GitHubArtifactOutput{}, fmt.Errorf("create GitHub pull request review: %w", err)
	}
	result, out, err := s.recordGitHubToolArtifact(ctx, task, sessionRun, "pr_review", in.Repo, in.PRNumber, created.URL, "", body, map[string]any{
		"pr_number": in.PRNumber,
		"event":     event,
	})
	if bodySourceIsExport {
		out.Body = ""
	}
	return result, out, err
}

func reviewBodyFromSessionExport(export string) (string, error) {
	section := export
	if idx := strings.LastIndex(export, "\n## Assistant\n"); idx >= 0 {
		section = export[idx+1:]
	} else if strings.HasPrefix(export, "## Assistant\n") {
		section = export
	}
	if count := strings.Count(section, reviewBodyStartMarker); count != 1 {
		return "", fmt.Errorf("session export must contain exactly one %s in the final assistant message, got %d", reviewBodyStartMarker, count)
	}
	if count := strings.Count(section, reviewBodyEndMarker); count != 1 {
		return "", fmt.Errorf("session export must contain exactly one %s in the final assistant message, got %d", reviewBodyEndMarker, count)
	}
	start := strings.Index(section, reviewBodyStartMarker)
	if start < 0 {
		return "", fmt.Errorf("session export does not contain %s", reviewBodyStartMarker)
	}
	afterStart := section[start+len(reviewBodyStartMarker):]
	end := strings.Index(afterStart, reviewBodyEndMarker)
	if end < 0 {
		return "", fmt.Errorf("session export does not contain %s after %s", reviewBodyEndMarker, reviewBodyStartMarker)
	}
	body := strings.TrimSpace(afterStart[:end])
	if body == "" {
		return "", fmt.Errorf("marked review body is empty")
	}
	return body, nil
}

func (s *Service) githubClient() *webhook.Client {
	return s.github
}

func (s *Service) GitHubClient() *webhook.Client {
	return s.githubClient()
}

func (s *Service) GitHubInstallationToken() (string, error) {
	if s.githubClient() == nil {
		return "", fmt.Errorf("GitHub App client is not configured")
	}
	return s.githubClient().InstallationToken()
}

func (s *Service) GitHubInstallationTokenForRepository(repo string) (string, error) {
	if s.githubClient() == nil {
		return "", fmt.Errorf("GitHub App client is not configured")
	}
	return s.githubClient().InstallationTokenForRepository(repo)
}

func (s *Service) GitHubReadInstallationTokenForRepository(repo string) (string, error) {
	if s.githubClient() == nil {
		return "", fmt.Errorf("GitHub App client is not configured")
	}
	return s.githubClient().InstallationReadTokenForRepository(repo)
}

func (s *Service) GetTaskSignature(ctx context.Context, taskID string) (string, error) {
	task, sessionRun, err := s.githubToolTaskContext(ctx, taskID)
	if err != nil {
		return "", err
	}
	return githubToolSignature(task, sessionRun, s.cfg.WebURL), nil
}

func requireGitHubToolFields(taskID, repo string) error {
	if strings.TrimSpace(taskID) == "" {
		return fmt.Errorf("task_id is required")
	}
	if strings.TrimSpace(repo) == "" {
		return fmt.Errorf("repo is required")
	}
	return nil
}

func requireAdminGitHubWriteTool(ctx context.Context) error {
	if !isAdmin(ctx) {
		return fmt.Errorf("admin access required for GitHub write tools")
	}
	return nil
}

func validateGitHubToolArtifactScope(task repository.ChetterTask, repo string, number int, artifactKind string) error {
	env, err := validateGitHubToolRepoScopeEnv(task, repo)
	if err != nil {
		return err
	}
	taskNumber := ""
	switch artifactKind {
	case "pr":
		taskNumber = strings.TrimSpace(env["PR_NUMBER"])
		if taskNumber == "" {
			return fmt.Errorf("task %s has no pull request scope", task.ID)
		}
	case "issue_or_pr":
		taskNumber = strings.TrimSpace(env["PR_NUMBER"])
		if taskNumber == "" {
			taskNumber = strings.TrimSpace(env["ISSUE_NUMBER"])
		}
	default:
		return fmt.Errorf("unknown GitHub artifact scope kind %q", artifactKind)
	}
	if taskNumber == "" {
		return fmt.Errorf("task %s has no GitHub artifact number scope", task.ID)
	}
	if taskNumber != fmt.Sprintf("%d", number) {
		return fmt.Errorf("GitHub tool target number %d does not match task number %s", number, taskNumber)
	}
	return nil
}

func validateGitHubToolCreationScope(task repository.ChetterTask, repo string) error {
	env, err := validateGitHubToolRepoScopeEnv(task, repo)
	if err != nil {
		return err
	}
	if strings.TrimSpace(env["PR_NUMBER"]) != "" || strings.TrimSpace(env["ISSUE_NUMBER"]) != "" {
		return fmt.Errorf("task %s is scoped to an existing GitHub artifact and cannot create new issues or pull requests", task.ID)
	}
	return nil
}

func validateGitHubToolRepoScope(task repository.ChetterTask, repo string) error {
	_, err := validateGitHubToolRepoScopeEnv(task, repo)
	return err
}

func validateGitHubToolRepoScopeEnv(task repository.ChetterTask, repo string) (map[string]string, error) {
	env := parseJSON[map[string]string](task.Env, "task:"+task.ID+" env")
	if env == nil {
		return nil, fmt.Errorf("task %s has no GitHub artifact scope", task.ID)
	}
	if env[gitHubTokenAllowedEnv] != "true" {
		return nil, fmt.Errorf("task %s is not authorized for GitHub write tools", task.ID)
	}
	taskRepo, ok := canonicalRepoName(env["GITHUB_REPO"])
	if !ok {
		return nil, fmt.Errorf("task %s has no GitHub repo scope", task.ID)
	}
	targetRepo, ok := canonicalRepoName(repo)
	if !ok {
		return nil, fmt.Errorf("repo must resolve to owner/repo")
	}
	if taskRepo != targetRepo {
		return nil, fmt.Errorf("GitHub tool target repo %q does not match task repo %q", targetRepo, taskRepo)
	}
	return env, nil
}

func (s *Service) githubToolTaskContext(ctx context.Context, taskID string) (repository.ChetterTask, repository.ChetterSessionRun, error) {
	if s.githubClient() == nil {
		return repository.ChetterTask{}, repository.ChetterSessionRun{}, fmt.Errorf("GitHub App client is not configured")
	}
	task, err := s.repo.GetTaskByID(ctx, taskID)
	if err != nil {
		if err == sql.ErrNoRows {
			return repository.ChetterTask{}, repository.ChetterSessionRun{}, fmt.Errorf("task %q not found", taskID)
		}
		return repository.ChetterTask{}, repository.ChetterSessionRun{}, fmt.Errorf("get task: %w", err)
	}
	if scope, ok := auth.GetScope(ctx); ok && !scope.Admin && scope.TeamID != "" && task.TeamID.String != scope.TeamID {
		return repository.ChetterTask{}, repository.ChetterSessionRun{}, fmt.Errorf("task %q not found", taskID)
	}
	sessionRun, err := s.repo.GetSessionRunByTaskID(ctx, taskID)
	if err != nil && err != sql.ErrNoRows {
		return repository.ChetterTask{}, repository.ChetterSessionRun{}, fmt.Errorf("get session run: %w", err)
	}
	return task, sessionRun, nil
}

func githubToolSignature(task repository.ChetterTask, sessionRun repository.ChetterSessionRun, webURL string) string {
	taskLink := task.ID
	if webURL != "" {
		taskLink = fmt.Sprintf("[%s](%s/tasks/%s)", task.ID, strings.TrimRight(webURL, "/"), task.ID)
	}
	parts := []string{fmt.Sprintf("Task: %s", taskLink)}
	if agent := strings.TrimSpace(task.Agent.String); agent != "" {
		parts = append(parts, "Agent: "+agent)
	}
	if model := strings.TrimSpace(task.ModelID.String); model != "" {
		parts = append(parts, "Model: "+model)
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

func (s *Service) recordGitHubToolArtifact(ctx context.Context, task repository.ChetterTask, sessionRun repository.ChetterSessionRun, artifactType, repo string, number int, url, ref, body string, detail map[string]any) (*mcp.CallToolResult, GitHubArtifactOutput, error) {
	if err := s.RecordArtifact(ctx, RecordArtifactParams{
		TaskID:          task.ID,
		AgentSessionID:  sessionRun.AgentSessionID,
		SessionRunID:    sessionRun.ID,
		ArtifactType:    artifactType,
		Repo:            repo,
		Number:          number,
		URL:             url,
		Ref:             ref,
		DiscoverySource: "mcp_tool",
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
	return nil, GitHubArtifactOutput{TaskID: task.ID, Repo: repo, ArtifactType: artifactType, Number: number, URL: url, Body: body}, nil
}
