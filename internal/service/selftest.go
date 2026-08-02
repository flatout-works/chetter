package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/flatout-works/chetter/internal/githubrepo"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/pkg/modelcatalog"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	selfTestSubmissionSource = "self_test"
	selfTestEvidenceKind     = "runner_mcp_self_test"
	selfTestToolName         = "chetter_runner_self_test_echo"
	selfTestTimeoutSec       = 300
)

// RunSelfTestInput starts one of Chetter's built-in deployment self-test profiles.
type RunSelfTestInput struct {
	Profile string `json:"profile" jsonschema:"Self-test profile: quick, harnesses, providers, or full"`
}

// SelfTestStatusInput identifies a deployment self-test run.
type SelfTestStatusInput struct {
	RunID string `json:"run_id" jsonschema:"Self-test run identifier returned by chetter_run_self_test"`
}

// SelfTestCheckRecord reports one task-backed deployment check.
type SelfTestCheckRecord struct {
	Name       string `json:"name"`
	TaskID     string `json:"task_id"`
	Harness    string `json:"harness,omitempty"`
	ProviderID string `json:"provider_id,omitempty"`
	ModelID    string `json:"model_id,omitempty"`
	Status     string `json:"status"`
	Evidence   bool   `json:"evidence"`
	Summary    string `json:"summary,omitempty"`
	Error      string `json:"error,omitempty"`
}

// SelfTestRunRecord is the aggregate view of a deployment self-test run.
type SelfTestRunRecord struct {
	ID        string                `json:"id"`
	Profile   string                `json:"profile"`
	Status    string                `json:"status"`
	Checks    []SelfTestCheckRecord `json:"checks"`
	CreatedAt time.Time             `json:"created_at"`
}

// RunSelfTestOutput is returned by chetter_run_self_test.
type RunSelfTestOutput struct {
	Run SelfTestRunRecord `json:"run"`
}

// SelfTestStatusOutput is returned by chetter_self_test_status.
type SelfTestStatusOutput struct {
	Run SelfTestRunRecord `json:"run"`
}

type selfTestSpec struct {
	name       string
	harness    string
	providerID string
	modelID    string
	gitURL     string
	githubRepo string
}

// RunSelfTest submits the checks in a built-in deployment profile through the
// normal task path. It is intentionally admin-only because profiles consume
// external provider quota and may fan out across the runner fleet.
func (s *Service) RunSelfTest(ctx context.Context, profile string) (SelfTestRunRecord, error) {
	if !isAdmin(ctx) {
		return SelfTestRunRecord{}, fmt.Errorf("admin access required")
	}
	profile = strings.ToLower(strings.TrimSpace(profile))
	specs, err := s.selfTestSpecs(ctx, profile)
	if err != nil {
		return SelfTestRunRecord{}, err
	}
	runID, err := randomID("selftest")
	if err != nil {
		return SelfTestRunRecord{}, fmt.Errorf("generate self-test run id: %w", err)
	}

	run := SelfTestRunRecord{ID: runID, Profile: profile, Status: "pending", Checks: make([]SelfTestCheckRecord, 0, len(specs)), CreatedAt: time.Now().UTC()}
	for _, spec := range specs {
		nonce, err := randomID("nonce")
		if err != nil {
			return run, fmt.Errorf("generate nonce for %s: %w", spec.name, err)
		}
		prompt := fmt.Sprintf("Chetter deployment self-test. Call the runner-bridge MCP tool %s exactly once with nonce %q. Do not merely repeat the nonce or claim success without calling the tool. After the tool returns, reply with a short confirmation.", selfTestToolName, nonce)
		taskRecord, err := s.SubmitTask(ctx, SubmitTaskRequest{
			Prompt:           prompt,
			Harness:          spec.harness,
			ProviderID:       spec.providerID,
			ModelID:          spec.modelID,
			GitURL:           spec.gitURL,
			GitHubRepo:       spec.githubRepo,
			TimeoutSec:       selfTestTimeoutSec,
			SubmissionSource: selfTestSubmissionSource,
			SelfTestRunID:    runID,
			SelfTestProfile:  profile,
			SelfTestCheck:    spec.name,
			SelfTestNonce:    nonce,
		})
		if err != nil {
			for _, submitted := range run.Checks {
				_, _ = s.CancelTask(ctx, submitted.TaskID, "self-test profile submission failed")
			}
			return run, fmt.Errorf("submit self-test check %s: %w", spec.name, err)
		}
		run.Checks = append(run.Checks, SelfTestCheckRecord{
			Name: spec.name, TaskID: taskRecord.ID, Harness: spec.harness,
			ProviderID: spec.providerID, ModelID: spec.modelID, Status: "pending",
		})
	}

	payload, _ := json.Marshal(map[string]any{"profile": profile, "checks": len(run.Checks)})
	s.auditAsync(ctx, AuditEventParams{EventType: "self_test.started", SourceType: "api", SourceID: runID, TargetType: "self_test", TargetID: runID, Payload: payload})
	return run, nil
}

// GetSelfTestStatus derives aggregate status from the task rows and requires
// runner-observed MCP evidence before a completed check is considered passed.
func (s *Service) GetSelfTestStatus(ctx context.Context, runID string) (SelfTestRunRecord, error) {
	if !isAdmin(ctx) {
		return SelfTestRunRecord{}, fmt.Errorf("admin access required")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return SelfTestRunRecord{}, fmt.Errorf("run_id is required")
	}
	tasks, err := s.repo.ListTasksBySelfTestRun(ctx, nullString(runID))
	if err != nil {
		return SelfTestRunRecord{}, fmt.Errorf("list self-test tasks: %w", err)
	}
	if len(tasks) == 0 {
		return SelfTestRunRecord{}, fmt.Errorf("self-test run %q not found", runID)
	}

	run := SelfTestRunRecord{ID: runID, Profile: tasks[0].SelfTestProfile.String, Status: "pending", Checks: make([]SelfTestCheckRecord, 0, len(tasks)), CreatedAt: tasks[0].CreatedAt}
	allPassed := true
	anyRunning := false
	anyUnfinished := false
	anyFailed := false
	for _, taskRow := range tasks {
		session, sessionErr := s.repo.GetAgentSessionByTaskID(ctx, taskRow.ID)
		if sessionErr != nil && sessionErr != sql.ErrNoRows {
			return SelfTestRunRecord{}, fmt.Errorf("get self-test session for %s: %w", taskRow.ID, sessionErr)
		}
		evidence, err := s.hasSelfTestEvidence(ctx, taskRow.ID, taskRow.SelfTestNonce.String, taskRow.SelfTestCheck.String)
		if err != nil {
			return SelfTestRunRecord{}, err
		}
		check := SelfTestCheckRecord{
			Name: taskRow.SelfTestCheck.String, TaskID: taskRow.ID,
			Harness: session.Harness.String, ProviderID: session.ProviderID.String, ModelID: session.ModelID.String,
			Status: taskRow.Status, Evidence: evidence, Summary: taskRow.Summary.String, Error: taskRow.Error.String,
		}
		switch taskRow.Status {
		case "done":
			if evidence {
				check.Status = "passed"
			} else {
				check.Status = "failed"
				check.Error = "task completed without runner-observed MCP self-test evidence"
				anyFailed = true
				allPassed = false
			}
		case "error", "cancelled":
			check.Status = "failed"
			anyFailed = true
			allPassed = false
		case "running":
			anyRunning = true
			anyUnfinished = true
			allPassed = false
		default:
			anyUnfinished = true
			allPassed = false
		}
		run.Checks = append(run.Checks, check)
	}
	switch {
	case allPassed:
		run.Status = "passed"
	case anyUnfinished && (anyRunning || anyFailed):
		run.Status = "running"
	case anyUnfinished:
		run.Status = "pending"
	case anyFailed:
		run.Status = "failed"
	default:
		run.Status = "pending"
	}
	return run, nil
}

func (s *Service) selfTestSpecs(ctx context.Context, profile string) ([]selfTestSpec, error) {
	switch profile {
	case "quick":
		return []selfTestSpec{{name: "quick", harness: "opencode", providerID: "deepseek", modelID: "deepseek-v4-flash"}}, nil
	case "harnesses":
		return selfTestHarnessSpecs(), nil
	case "providers":
		return s.selfTestProviderSpecs(ctx)
	case "full":
		providers, err := s.selfTestProviderSpecs(ctx)
		if err != nil {
			return nil, err
		}
		specs := append(selfTestHarnessSpecs(), providers...)
		if repo := strings.TrimSpace(s.cfg.SelfTestGitHubRepo); repo != "" {
			parsed, err := githubrepo.Parse(repo)
			if err != nil {
				return nil, fmt.Errorf("invalid CHETTER_SELF_TEST_GITHUB_REPO: %w", err)
			}
			repo = parsed.FullName()
			specs = append(specs, selfTestSpec{
				name: "github:credentials", harness: "opencode", githubRepo: repo,
				gitURL: "https://github.com/" + repo + ".git",
			})
		}
		return specs, nil
	default:
		return nil, fmt.Errorf("invalid self-test profile %q (want quick, harnesses, providers, or full)", profile)
	}
}

// selfTestHarnessSpecs returns one check per installed harness, pinned to
// cheap known-good provider/model combinations. Pinning keeps deployment
// checks on low-cost models and avoids depending on the active model
// catalog's per-harness defaults, which may be missing or point at provider
// names the harness CLI rejects (pi and codewhale, for example, only accept
// their native provider names and refuse "synthetic").
func selfTestHarnessSpecs() []selfTestSpec {
	return []selfTestSpec{
		{name: "harness:opencode", harness: "opencode", providerID: "deepseek", modelID: "deepseek-v4-flash"},
		{name: "harness:claude-code", harness: "claude-code", providerID: "synthetic", modelID: "hf:zai-org/GLM-5.2"},
		{name: "harness:pi", harness: "pi", providerID: "deepseek", modelID: "deepseek-v4-flash"},
		{name: "harness:codewhale", harness: "codewhale", providerID: "deepseek", modelID: "deepseek-v4-flash"},
		{name: "harness:codex", harness: "codex", providerID: "synthetic", modelID: "hf:zai-org/GLM-5.2"},
	}
}

func (s *Service) selfTestProviderSpecs(ctx context.Context) ([]selfTestSpec, error) {
	catalog, err := s.GetModelCatalog(ctx)
	if err != nil {
		return nil, fmt.Errorf("load model catalog: %w", err)
	}
	providerIDs := make([]string, 0, len(catalog.Providers))
	for providerID := range catalog.Providers {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)

	specs := make([]selfTestSpec, 0, len(providerIDs))
	for _, providerID := range providerIDs {
		provider := catalog.Providers[providerID]
		if mapping, ok := provider.Harnesses["opencode"]; ok && mapping.Disabled {
			continue
		}
		modelID := firstEnabledModel(provider, "opencode")
		if modelID == "" {
			continue
		}
		specs = append(specs, selfTestSpec{name: "provider:" + providerID, harness: "opencode", providerID: providerID, modelID: modelID})
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("model catalog has no providers enabled for opencode")
	}
	return specs, nil
}

func firstEnabledModel(provider modelcatalog.Provider, harness string) string {
	for _, model := range provider.Models {
		if mapping, ok := model.Harnesses[harness]; ok && mapping.Disabled {
			continue
		}
		return model.ID
	}
	return ""
}

func (s *Service) hasSelfTestEvidence(ctx context.Context, taskID, nonce, check string) (bool, error) {
	events, err := s.repo.ListTaskEvents(ctx, repository.ListTaskEventsParams{TaskID: taskID, Limit: 500, Offset: 0})
	if err != nil {
		return false, fmt.Errorf("list self-test events for %s: %w", taskID, err)
	}
	for _, event := range events {
		var payload struct {
			Kind              string `json:"kind"`
			Tool              string `json:"tool"`
			Nonce             string `json:"nonce"`
			Observed          bool   `json:"observed"`
			Check             string `json:"check"`
			GitHubCredentials bool   `json:"github_credentials"`
		}
		if json.Unmarshal(event.Payload, &payload) == nil && payload.Kind == selfTestEvidenceKind && payload.Tool == selfTestToolName && payload.Nonce == nonce && payload.Check == check && payload.Observed && (check != "github:credentials" || payload.GitHubCredentials) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) runSelfTestTool(ctx context.Context, _ *mcp.CallToolRequest, in RunSelfTestInput) (*mcp.CallToolResult, RunSelfTestOutput, error) {
	run, err := s.RunSelfTest(ctx, in.Profile)
	if err != nil {
		return nil, RunSelfTestOutput{}, err
	}
	return nil, RunSelfTestOutput{Run: run}, nil
}

func (s *Service) selfTestStatusTool(ctx context.Context, _ *mcp.CallToolRequest, in SelfTestStatusInput) (*mcp.CallToolResult, SelfTestStatusOutput, error) {
	run, err := s.GetSelfTestStatus(ctx, in.RunID)
	if err != nil {
		return nil, SelfTestStatusOutput{}, err
	}
	return nil, SelfTestStatusOutput{Run: run}, nil
}
