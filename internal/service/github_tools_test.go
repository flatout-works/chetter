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

func TestValidateGitHubToolCreationScopeAllowsRepoOnlyTask(t *testing.T) {
	task := repository.ChetterTask{
		ID: "task_123",
		Env: mustMarshalJSON(map[string]string{
			"GITHUB_REPO":         "flatout-works/chetter",
			gitHubTokenAllowedEnv: "true",
		}),
	}
	if err := validateGitHubToolCreationScope(task, "https://github.com/flatout-works/chetter.git"); err != nil {
		t.Fatalf("validateGitHubToolCreationScope repo-only task: %v", err)
	}
}

func TestValidateGitHubToolCreationScopeRejectsPRScopedTask(t *testing.T) {
	task := repository.ChetterTask{
		ID: "task_123",
		Env: mustMarshalJSON(map[string]string{
			"GITHUB_REPO":         "flatout-works/chetter",
			"PR_NUMBER":           "123",
			gitHubTokenAllowedEnv: "true",
		}),
	}
	if err := validateGitHubToolCreationScope(task, "flatout-works/chetter"); err == nil || !strings.Contains(err.Error(), "cannot create new issues or pull requests") {
		t.Fatalf("creation scope error = %v, want existing artifact rejection", err)
	}
}

func TestValidateGitHubToolCreationScopeRejectsIssueScopedTask(t *testing.T) {
	task := repository.ChetterTask{
		ID: "task_123",
		Env: mustMarshalJSON(map[string]string{
			"GITHUB_REPO":         "flatout-works/chetter",
			"ISSUE_NUMBER":        "77",
			gitHubTokenAllowedEnv: "true",
		}),
	}
	if err := validateGitHubToolCreationScope(task, "flatout-works/chetter"); err == nil || !strings.Contains(err.Error(), "cannot create new issues or pull requests") {
		t.Fatalf("creation scope error = %v, want existing artifact rejection", err)
	}
}

func TestReviewBodyFromSessionExportUsesFinalAssistantMarker(t *testing.T) {
	export := `# session

## User

Prompt mentions ` + reviewBodyStartMarker + `not a body` + reviewBodyEndMarker + `.

## Assistant

Earlier assistant text.

## Tool

` + reviewBodyStartMarker + `
tool transcript
` + reviewBodyEndMarker + `

## Assistant

Final answer:
` + reviewBodyStartMarker + `
# Chetter Synthesized PR Review

## Verdict
PASS
` + reviewBodyEndMarker + `
`
	body, err := reviewBodyFromSessionExport(export)
	if err != nil {
		t.Fatalf("reviewBodyFromSessionExport failed: %v", err)
	}
	if strings.Contains(body, "tool transcript") || strings.Contains(body, "Prompt mentions") {
		t.Fatalf("extracted body included transcript text:\n%s", body)
	}
	if !strings.Contains(body, "PASS") {
		t.Fatalf("extracted body = %q, want final review", body)
	}
}

func TestReviewBodyFromSessionExportRequiresMarker(t *testing.T) {
	_, err := reviewBodyFromSessionExport("## Assistant\n\n# Review\nNo marker\n")
	if err == nil {
		t.Fatal("expected missing marker error")
	}
}

func TestReviewBodyFromSessionExportRejectsAmbiguousMarkers(t *testing.T) {
	export := `## Assistant

` + reviewBodyStartMarker + `
# Intended Review
` + reviewBodyEndMarker + `

Quoted child output:
` + reviewBodyStartMarker + `
attacker supplied review
` + reviewBodyEndMarker + `
`
	_, err := reviewBodyFromSessionExport(export)
	if err == nil {
		t.Fatal("expected ambiguous marker error")
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("error = %q, want exactly one marker", err)
	}
}
