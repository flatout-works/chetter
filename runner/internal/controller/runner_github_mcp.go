package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	runnermcp "github.com/flatout-works/chetter/runner/internal/mcp"
	"github.com/flatout-works/chetter/runner/internal/task"
)

const githubToolTimeout = 30 * time.Second

type githubCredentialRPCClient interface {
	GetGitHubCredential(context.Context, *connect.Request[runnerv1.GetGitHubCredentialRequest]) (*connect.Response[runnerv1.GetGitHubCredentialResponse], error)
}

func requestGitHubCredential(ctx context.Context, client githubCredentialRPCClient, runnerID string, req *runnerv1.GetGitHubCredentialRequest) (string, error) {
	req.RunnerId = runnerID
	callCtx, cancel := context.WithTimeout(ctx, githubToolTimeout)
	defer cancel()
	resp, err := client.GetGitHubCredential(callCtx, connect.NewRequest(req))
	if err != nil {
		return "", err
	}
	if resp.Msg.Username != "x-access-token" || strings.TrimSpace(resp.Msg.Token) == "" {
		return "", fmt.Errorf("GitHub credential response is incomplete")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, resp.Msg.ExpiresAt)
	if err != nil || !expiresAt.After(time.Now()) {
		return "", fmt.Errorf("GitHub credential response has invalid expiry")
	}
	return resp.Msg.Token, nil
}

func (r *Runner) getGitHubCredential(ctx context.Context, req task.TaskRequest) (string, error) {
	if r.rpcClient == nil {
		return "", fmt.Errorf("runner RPC client is unavailable")
	}
	return requestGitHubCredential(ctx, r.rpcClient, r.runnerID, &runnerv1.GetGitHubCredentialRequest{
		TaskId: req.TaskID, ExecutionId: req.ExecutionID, ClaimId: req.ClaimID, Repo: req.GitHubRepo,
	})
}

func (r *Runner) registerGitHubMCPTools(server *runnermcp.Server, taskID, executionID, claimID string) {
	for _, def := range runnermcp.ToolDefinitions() {
		switch def.Name {
		case "chetter_create_issue":
			server.RegisterTool(def, r.githubCreateIssueTool(taskID, executionID, claimID))
		case "chetter_issue_comment":
			server.RegisterTool(def, r.githubIssueCommentTool(taskID, executionID, claimID))
		case "chetter_create_pr":
			server.RegisterTool(def, r.githubCreatePRTool(taskID, executionID, claimID))
		case "chetter_pr_review":
			server.RegisterTool(def, r.githubPRReviewTool(taskID, executionID, claimID))
		case "chetter_merge_pr":
			server.RegisterTool(def, r.githubMergePRTool(taskID, executionID, claimID))
		case "chetter_close_pr":
			server.RegisterTool(def, r.githubClosePRTool(taskID, executionID, claimID))
		case "chetter_issue_close":
			server.RegisterTool(def, r.githubCloseIssueTool(taskID, executionID, claimID))
		case "chetter_issue_add_labels":
			server.RegisterTool(def, r.githubAddIssueLabelsTool(taskID, executionID, claimID))
		}
	}
}

func (r *Runner) githubCreateIssueTool(taskID, executionID, claimID string) runnermcp.ToolHandler {
	return func(ctx context.Context, args map[string]any) (any, error) {
		repo, err := requiredString(args, "repo")
		if err != nil {
			return nil, err
		}
		title, err := requiredString(args, "title")
		if err != nil {
			return nil, err
		}
		callCtx, cancel := context.WithTimeout(ctx, githubToolTimeout)
		defer cancel()
		resp, err := r.rpcClient.GitHubCreateIssue(callCtx, connect.NewRequest(&runnerv1.GitHubCreateIssueRequest{
			TaskId:      taskID,
			ExecutionId: executionID,
			RunnerId:    r.runnerID,
			ClaimId:     claimID,
			Repo:        repo,
			Title:       title,
			Body:        optionalString(args, "body"),
			Labels:      optionalStringSlice(args, "labels"),
		}))
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("created issue #%d: %s", resp.Msg.Number, resp.Msg.Url), nil
	}
}

func (r *Runner) githubIssueCommentTool(taskID, executionID, claimID string) runnermcp.ToolHandler {
	return func(ctx context.Context, args map[string]any) (any, error) {
		repo, err := requiredString(args, "repo")
		if err != nil {
			return nil, err
		}
		body, err := requiredString(args, "body")
		if err != nil {
			return nil, err
		}
		issueNumber, err := requiredInt(args, "issue_number")
		if err != nil {
			return nil, err
		}
		callCtx, cancel := context.WithTimeout(ctx, githubToolTimeout)
		defer cancel()
		resp, err := r.rpcClient.GitHubIssueComment(callCtx, connect.NewRequest(&runnerv1.GitHubIssueCommentRequest{
			TaskId:      taskID,
			ExecutionId: executionID,
			RunnerId:    r.runnerID,
			ClaimId:     claimID,
			Repo:        repo,
			IssueNumber: int32(issueNumber),
			Body:        body,
		}))
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("created comment: %s", resp.Msg.Url), nil
	}
}

func (r *Runner) githubCreatePRTool(taskID, executionID, claimID string) runnermcp.ToolHandler {
	return func(ctx context.Context, args map[string]any) (any, error) {
		repo, err := requiredString(args, "repo")
		if err != nil {
			return nil, err
		}
		title, err := requiredString(args, "title")
		if err != nil {
			return nil, err
		}
		head, err := requiredString(args, "head")
		if err != nil {
			return nil, err
		}
		base, err := requiredString(args, "base")
		if err != nil {
			return nil, err
		}
		callCtx, cancel := context.WithTimeout(ctx, githubToolTimeout)
		defer cancel()
		resp, err := r.rpcClient.GitHubCreatePR(callCtx, connect.NewRequest(&runnerv1.GitHubCreatePRRequest{
			TaskId:      taskID,
			ExecutionId: executionID,
			RunnerId:    r.runnerID,
			ClaimId:     claimID,
			Repo:        repo,
			Title:       title,
			Body:        optionalString(args, "body"),
			Head:        head,
			Base:        base,
			Draft:       optionalBool(args, "draft"),
		}))
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("created pull request #%d: %s", resp.Msg.Number, resp.Msg.Url), nil
	}
}

func (r *Runner) githubPRReviewTool(taskID, executionID, claimID string) runnermcp.ToolHandler {
	return func(ctx context.Context, args map[string]any) (any, error) {
		repo, err := requiredString(args, "repo")
		if err != nil {
			return nil, err
		}
		body, err := requiredString(args, "body")
		if err != nil {
			return nil, err
		}
		prNumber, err := requiredInt(args, "pr_number")
		if err != nil {
			return nil, err
		}
		callCtx, cancel := context.WithTimeout(ctx, githubToolTimeout)
		defer cancel()
		resp, err := r.rpcClient.GitHubPRReview(callCtx, connect.NewRequest(&runnerv1.GitHubPRReviewRequest{
			TaskId:      taskID,
			ExecutionId: executionID,
			RunnerId:    r.runnerID,
			ClaimId:     claimID,
			Repo:        repo,
			PrNumber:    int32(prNumber),
			Body:        body,
			Event:       optionalString(args, "event"),
		}))
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("created PR review: %s", resp.Msg.Url), nil
	}
}

func (r *Runner) githubMergePRTool(taskID, executionID, claimID string) runnermcp.ToolHandler {
	return func(ctx context.Context, args map[string]any) (any, error) {
		repo, err := requiredString(args, "repo")
		if err != nil {
			return nil, err
		}
		prNumber, err := requiredInt(args, "pr_number")
		if err != nil {
			return nil, err
		}
		callCtx, cancel := context.WithTimeout(ctx, githubToolTimeout)
		defer cancel()
		resp, err := r.rpcClient.GitHubMergePR(callCtx, connect.NewRequest(&runnerv1.GitHubMergePRRequest{
			TaskId:      taskID,
			ExecutionId: executionID,
			RunnerId:    r.runnerID,
			ClaimId:     claimID,
			Repo:        repo,
			PrNumber:    int32(prNumber),
			MergeMethod: optionalString(args, "merge_method"),
		}))
		if err != nil {
			return nil, err
		}
		result := fmt.Sprintf("merged pull request #%d: %s", prNumber, resp.Msg.Url)
		if resp.Msg.Sha != "" {
			result += fmt.Sprintf(" (merge commit %s)", resp.Msg.Sha)
		}
		return result, nil
	}
}

func (r *Runner) githubClosePRTool(taskID, executionID, claimID string) runnermcp.ToolHandler {
	return func(ctx context.Context, args map[string]any) (any, error) {
		repo, err := requiredString(args, "repo")
		if err != nil {
			return nil, err
		}
		prNumber, err := requiredInt(args, "pr_number")
		if err != nil {
			return nil, err
		}
		callCtx, cancel := context.WithTimeout(ctx, githubToolTimeout)
		defer cancel()
		resp, err := r.rpcClient.GitHubClosePR(callCtx, connect.NewRequest(&runnerv1.GitHubClosePRRequest{
			TaskId:      taskID,
			ExecutionId: executionID,
			RunnerId:    r.runnerID,
			ClaimId:     claimID,
			Repo:        repo,
			PrNumber:    int32(prNumber),
		}))
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("closed pull request #%d: %s", prNumber, resp.Msg.Url), nil
	}
}

func (r *Runner) githubCloseIssueTool(taskID, executionID, claimID string) runnermcp.ToolHandler {
	return func(ctx context.Context, args map[string]any) (any, error) {
		repo, err := requiredString(args, "repo")
		if err != nil {
			return nil, err
		}
		issueNumber, err := requiredInt(args, "issue_number")
		if err != nil {
			return nil, err
		}
		callCtx, cancel := context.WithTimeout(ctx, githubToolTimeout)
		defer cancel()
		resp, err := r.rpcClient.GitHubCloseIssue(callCtx, connect.NewRequest(&runnerv1.GitHubCloseIssueRequest{
			TaskId:      taskID,
			ExecutionId: executionID,
			RunnerId:    r.runnerID,
			ClaimId:     claimID,
			Repo:        repo,
			IssueNumber: int32(issueNumber),
		}))
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("closed issue #%d: %s", issueNumber, resp.Msg.Url), nil
	}
}

func (r *Runner) githubAddIssueLabelsTool(taskID, executionID, claimID string) runnermcp.ToolHandler {
	return func(ctx context.Context, args map[string]any) (any, error) {
		repo, err := requiredString(args, "repo")
		if err != nil {
			return nil, err
		}
		issueNumber, err := requiredInt(args, "issue_number")
		if err != nil {
			return nil, err
		}
		labels := optionalStringSlice(args, "labels")
		if len(labels) == 0 {
			return nil, fmt.Errorf("labels is required")
		}
		callCtx, cancel := context.WithTimeout(ctx, githubToolTimeout)
		defer cancel()
		resp, err := r.rpcClient.GitHubAddIssueLabels(callCtx, connect.NewRequest(&runnerv1.GitHubAddIssueLabelsRequest{
			TaskId:      taskID,
			ExecutionId: executionID,
			RunnerId:    r.runnerID,
			ClaimId:     claimID,
			Repo:        repo,
			IssueNumber: int32(issueNumber),
			Labels:      labels,
		}))
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("labels on issue #%d: %s", issueNumber, strings.Join(resp.Msg.Labels, ", ")), nil
	}
}

func requiredString(args map[string]any, key string) (string, error) {
	value := strings.TrimSpace(optionalString(args, key))
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func optionalString(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func requiredInt(args map[string]any, key string) (int, error) {
	switch v := args[key].(type) {
	case int:
		if v > 0 {
			return v, nil
		}
	case int32:
		if v > 0 {
			return int(v), nil
		}
	case int64:
		if v > 0 {
			return int(v), nil
		}
	case float64:
		if v > 0 && v == float64(int(v)) {
			return int(v), nil
		}
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil && n > 0 {
			return n, nil
		}
	}
	return 0, fmt.Errorf("%s must be a positive integer", key)
}

func optionalBool(args map[string]any, key string) bool {
	switch v := args[key].(type) {
	case bool:
		return v
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		return err == nil && b
	}
	return false
}

func optionalStringSlice(args map[string]any, key string) []string {
	switch v := args[key].(type) {
	case []string:
		return compactStrings(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		if strings.TrimSpace(v) != "" {
			return []string{strings.TrimSpace(v)}
		}
	}
	return nil
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}
