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
)

const githubToolTimeout = 30 * time.Second

func (r *Runner) registerGitHubMCPTools(server *runnermcp.Server, taskID string) {
	server.RegisterTool("chetter_create_issue", r.githubCreateIssueTool(taskID))
	server.RegisterTool("chetter_issue_comment", r.githubIssueCommentTool(taskID))
	server.RegisterTool("chetter_create_pr", r.githubCreatePRTool(taskID))
	server.RegisterTool("chetter_pr_review", r.githubPRReviewTool(taskID))
}

func (r *Runner) githubCreateIssueTool(taskID string) runnermcp.ToolHandler {
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
			TaskId: taskID,
			Repo:   repo,
			Title:  title,
			Body:   optionalString(args, "body"),
			Labels: optionalStringSlice(args, "labels"),
		}))
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("created issue #%d: %s", resp.Msg.Number, resp.Msg.Url), nil
	}
}

func (r *Runner) githubIssueCommentTool(taskID string) runnermcp.ToolHandler {
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

func (r *Runner) githubCreatePRTool(taskID string) runnermcp.ToolHandler {
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
			TaskId: taskID,
			Repo:   repo,
			Title:  title,
			Body:   optionalString(args, "body"),
			Head:   head,
			Base:   base,
			Draft:  optionalBool(args, "draft"),
		}))
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("created pull request #%d: %s", resp.Msg.Number, resp.Msg.Url), nil
	}
}

func (r *Runner) githubPRReviewTool(taskID string) runnermcp.ToolHandler {
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
			TaskId:   taskID,
			Repo:     repo,
			PrNumber: int32(prNumber),
			Body:     body,
			Event:    optionalString(args, "event"),
		}))
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("created PR review: %s", resp.Msg.Url), nil
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
