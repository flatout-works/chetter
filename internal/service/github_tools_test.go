package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/flatout-works/chetter/internal/repository"
)

func TestValidateGitHubToolArtifactScope(t *testing.T) {
	task := repository.ChetterTask{
		ID: "task_123",
		Env: mustMarshalJSON(map[string]string{
			"GITHUB_REPO":         "flatout-works/chetter",
			"PR_NUMBER":           "123",
			gitHubTokenAllowedEnv: "true",
		}),
	}
	if err := validateGitHubToolArtifactScope(task, "https://github.com/flatout-works/chetter.git", 123, "pr"); err != nil {
		t.Fatalf("validateGitHubToolArtifactScope matching target: %v", err)
	}
	if err := validateGitHubToolArtifactScope(task, "other/repo", 123, "pr"); err == nil || !strings.Contains(err.Error(), "does not match task repo") {
		t.Fatalf("repo mismatch error = %v, want task repo mismatch", err)
	}
	if err := validateGitHubToolArtifactScope(task, "flatout-works/chetter", 124, "pr"); err == nil || !strings.Contains(err.Error(), "does not match task number") {
		t.Fatalf("number mismatch error = %v, want task number mismatch", err)
	}
}

func TestValidateGitHubToolArtifactScopeIssueNumber(t *testing.T) {
	env, err := json.Marshal(map[string]string{
		"GITHUB_REPO":         "flatout-works/chetter",
		"ISSUE_NUMBER":        "77",
		gitHubTokenAllowedEnv: "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	task := repository.ChetterTask{ID: "task_123", Env: env}
	if err := validateGitHubToolArtifactScope(task, "flatout-works/chetter", 77, "issue_or_pr"); err != nil {
		t.Fatalf("validate issue scope: %v", err)
	}
	if err := validateGitHubToolArtifactScope(task, "flatout-works/chetter", 77, "pr"); err == nil || !strings.Contains(err.Error(), "no pull request scope") {
		t.Fatalf("issue-scoped PR review error = %v, want no pull request scope", err)
	}
}

func TestValidateGitHubToolArtifactScopeRequiresAuthorizationMarker(t *testing.T) {
	task := repository.ChetterTask{
		ID: "task_123",
		Env: mustMarshalJSON(map[string]string{
			"GITHUB_REPO": "flatout-works/chetter",
			"PR_NUMBER":   "123",
		}),
	}
	if err := validateGitHubToolArtifactScope(task, "flatout-works/chetter", 123, "pr"); err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("unauthorized scope error = %v, want not authorized", err)
	}
}

func TestValidateGitHubToolRepoScope(t *testing.T) {
	task := repository.ChetterTask{
		ID: "task_123",
		Env: mustMarshalJSON(map[string]string{
			"GITHUB_REPO":         "flatout-works/chetter",
			gitHubTokenAllowedEnv: "true",
		}),
	}
	if err := validateGitHubToolRepoScope(task, "https://github.com/flatout-works/chetter.git"); err != nil {
		t.Fatalf("validateGitHubToolRepoScope matching target: %v", err)
	}
	if err := validateGitHubToolRepoScope(task, "flatout-works/other"); err == nil || !strings.Contains(err.Error(), "does not match task repo") {
		t.Fatalf("repo mismatch error = %v, want task repo mismatch", err)
	}

	unauthorizedTask := repository.ChetterTask{
		ID: "task_456",
		Env: mustMarshalJSON(map[string]string{
			"GITHUB_REPO": "flatout-works/chetter",
		}),
	}
	if err := validateGitHubToolRepoScope(unauthorizedTask, "flatout-works/chetter"); err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("unauthorized scope error = %v, want not authorized", err)
	}
}
