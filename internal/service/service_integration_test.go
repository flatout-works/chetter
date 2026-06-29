package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/config"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/store"
	"github.com/flatout-works/chetter/internal/testdb"
	"github.com/flatout-works/chetter/pkg/definitions"
	"github.com/flatout-works/chetter/pkg/modelcatalog"
)

var svcTestDB *testdb.PackageDB

func TestMain(m *testing.M) {
	svcTestDB = testdb.StartPackageDB(m)
	if svcTestDB == nil {
		os.Exit(0)
	}
	code := m.Run()
	svcTestDB.Close()
	os.Exit(code)
}

func newServiceForTest(t *testing.T) (*Service, *testdb.TestDB, func()) {
	t.Helper()
	tdb, cleanup := svcTestDB.NewTestDB(t)
	tdb.Truncate(t)
	cfg := config.Config{
		DefaultAgentImage:     "runner:latest",
		DefaultTaskTimeoutSec: 600,
	}
	st, err := store.Open(tdb.DSN)
	if err != nil {
		cleanup()
		t.Fatalf("store.Open: %v", err)
	}
	svc := New(cfg, st)
	return svc, tdb, func() {
		_ = st.Close()
		cleanup()
	}
}

func TestSubmitTaskQueuesPendingRow(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:     "fix bug",
		AgentImage: "runner:latest",
		Env:        map[string]string{"FOO": "bar", "SECRET": "shh"},
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	if rec.Status != "pending" {
		t.Errorf("expected status=pending, got %s", rec.Status)
	}
	if rec.Prompt != "fix bug" {
		t.Errorf("prompt mismatch: %s", rec.Prompt)
	}
	if rec.Env["SECRET"] != "[redacted]" {
		t.Errorf("expected SECRET redacted, got %q", rec.Env["SECRET"])
	}
	if rec.Env["FOO"] != "bar" {
		t.Errorf("expected FOO=bar, got %q", rec.Env["FOO"])
	}
	if rec.AgentImage != "runner:latest" {
		t.Errorf("agent_image mismatch: %s", rec.AgentImage)
	}

	// Verify via direct repo query
	q := repository.New(tdb.DB)
	row, err := q.GetTaskByID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if row.Status != "pending" {
		t.Errorf("db status: %s", row.Status)
	}
	if row.TimeoutSec != 600 {
		t.Errorf("timeout_sec: %d", row.TimeoutSec)
	}
	run, err := q.GetSessionRunByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get session run: %v", err)
	}
	if run.Status != "pending" {
		t.Errorf("session run status: %s", run.Status)
	}
	if run.TaskID != rec.ID {
		t.Errorf("session run task_id: %s", run.TaskID)
	}
	session, err := q.GetAgentSessionByID(ctx, run.AgentSessionID)
	if err != nil {
		t.Fatalf("get agent session: %v", err)
	}
	if session.Status != "running" {
		t.Errorf("agent session status: %s", session.Status)
	}
	if session.ResumeMode != "none" {
		t.Errorf("agent session resume_mode: %s", session.ResumeMode)
	}
}

func TestSubmitTaskInjectsTaskExportFilesWithoutExposingEnvPayload(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	source, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "source", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit source task: %v", err)
	}
	if _, err := tdb.DB.ExecContext(ctx, "UPDATE chetter_tasks SET session_export = ? WHERE id = ?", "source export", source.ID); err != nil {
		t.Fatalf("set session export: %v", err)
	}
	target, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:     "synthesize",
		AgentImage: "runner:latest",
		ExtraFiles: map[string]string{
			"reviews/status.json": `{"standard":"done"}`,
		},
		TaskExportFiles: []TaskExportFileRequest{{
			TaskID: source.ID,
			Path:   "reviews/standard.md",
		}},
	})
	if err != nil {
		t.Fatalf("submit target task: %v", err)
	}
	if _, ok := target.Env[extraFilesEnv]; ok {
		t.Fatalf("internal extra files payload leaked from SubmitTask: %#v", target.Env)
	}

	row, err := repository.New(tdb.DB).GetTaskByID(ctx, target.ID)
	if err != nil {
		t.Fatalf("get target task: %v", err)
	}
	proto := taskToProto(row, "", "")
	if _, ok := proto.Env[extraFilesEnv]; ok {
		t.Fatalf("internal extra files payload leaked into runner env: %#v", proto.Env)
	}
	if string(proto.ExtraFiles["reviews/standard.md"]) != "source export" {
		t.Fatalf("task export file = %q", string(proto.ExtraFiles["reviews/standard.md"]))
	}
	if string(proto.ExtraFiles["reviews/status.json"]) != `{"standard":"done"}` {
		t.Fatalf("inline extra file = %q", string(proto.ExtraFiles["reviews/status.json"]))
	}
	rec, err := svc.GetTask(ctx, target.ID)
	if err != nil {
		t.Fatalf("get task record: %v", err)
	}
	if _, ok := rec.Env[extraFilesEnv]; ok {
		t.Fatalf("internal extra files payload leaked into task record: %#v", rec.Env)
	}
}

func TestSubmitTaskRejectsMissingMCPProfile(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	_, err := svc.SubmitTask(context.Background(), SubmitTaskRequest{
		Prompt:      "x",
		AgentImage:  "runner:latest",
		MCPProfiles: []string{"missing-profile"},
	})
	if err == nil {
		t.Fatal("expected missing mcp profile to be rejected")
	}
	if !strings.Contains(err.Error(), `"missing-profile" is not an active mcp profile`) {
		t.Fatalf("SubmitTask error = %q, want missing profile", err)
	}
}

func TestSubmitTaskStripsPrivateGitHubTokenMarkers(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:     "x",
		AgentImage: "runner:latest",
		Env: map[string]string{
			"GITHUB_TOKEN":            "caller-token",
			"GITHUB_REPO":             "flatout-works/chetter",
			"PR_NUMBER":               "123",
			gitHubTokenAllowedEnv:     "true",
			gitHubReadTokenAllowedEnv: "true",
			injectedGitHubTokenEnv:    "ghs_fake",
			definitionRepoEnv:         "flatout-works/private",
		},
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	row, err := repository.New(tdb.DB).GetTaskByID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	var env map[string]string
	if err := json.Unmarshal(row.Env, &env); err != nil {
		t.Fatalf("unmarshal env: %v", err)
	}
	if env["GITHUB_TOKEN"] != "[redacted]" {
		t.Fatalf("GITHUB_TOKEN = %q, want redacted", env["GITHUB_TOKEN"])
	}
	if _, ok := env[gitHubTokenAllowedEnv]; ok {
		t.Fatalf("public task env kept private marker: %#v", env)
	}
	if _, ok := env[gitHubReadTokenAllowedEnv]; ok {
		t.Fatalf("public task env kept private read marker: %#v", env)
	}
	if _, ok := env[injectedGitHubTokenEnv]; ok {
		t.Fatalf("public task env kept injected token marker: %#v", env)
	}
	if _, ok := env[definitionRepoEnv]; ok {
		t.Fatalf("public task env kept definition repo marker: %#v", env)
	}
}

func TestSubmitTaskAllowsGitHubTokenForInternalRequest(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:           "x",
		AgentImage:       "runner:latest",
		Env:              map[string]string{"GITHUB_TOKEN": "caller-token", "GITHUB_REPO": "flatout-works/chetter", "PR_NUMBER": "123"},
		AllowGitHubToken: true,
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	row, err := repository.New(tdb.DB).GetTaskByID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	var env map[string]string
	if err := json.Unmarshal(row.Env, &env); err != nil {
		t.Fatalf("unmarshal env: %v", err)
	}
	if env[gitHubTokenAllowedEnv] != "true" {
		t.Fatalf("github token marker = %q, want true; env=%#v", env[gitHubTokenAllowedEnv], env)
	}
	if env["GITHUB_TOKEN"] != "[redacted]" {
		t.Fatalf("GITHUB_TOKEN = %q, want redacted", env["GITHUB_TOKEN"])
	}
	if env[definitionRepoEnv] != "flatout-works/chetter" {
		t.Fatalf("definition repo marker = %q, want flatout-works/chetter", env[definitionRepoEnv])
	}
}

func TestSubmitTaskDoesNotUsePublicGitHubRepoForDefinitionScope(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	insertDefinition(t, repository.New(tdb.DB), "repo-profile", definitions.DefinitionTypeMCPProfile, "repo-tools", "repo", "", "github.com/acme/service", "mcp-profiles/repo-tools.yaml", "name: repo-tools\nurl: http://repo-tools:8080/mcp\n", now)

	_, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:      "x",
		GitURL:      "https://github.com/contributor/service.git",
		AgentImage:  "runner:latest",
		MCPProfiles: []string{"repo-tools"},
		Env:         map[string]string{"GITHUB_REPO": "acme/service"},
	})
	if err == nil {
		t.Fatal("expected public GITHUB_REPO scope spoofing to be rejected")
	}
	if !strings.Contains(err.Error(), `"repo-tools" is not an active mcp profile`) {
		t.Fatalf("SubmitTask error = %q, want missing profile", err)
	}
}

func TestSubmitTaskUsesTrustedDefinitionRepoForDefinitionScope(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	insertDefinition(t, repository.New(tdb.DB), "repo-profile", definitions.DefinitionTypeMCPProfile, "repo-tools", "repo", "", "github.com/acme/service", "mcp-profiles/repo-tools.yaml", "name: repo-tools\nurl: http://repo-tools:8080/mcp\n", now)

	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:         "x",
		GitURL:         "https://github.com/contributor/service.git",
		DefinitionRepo: "acme/service",
		AgentImage:     "runner:latest",
		MCPProfiles:    []string{"repo-tools"},
		Env:            map[string]string{"GITHUB_REPO": "contributor/service"},
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	row, err := repository.New(tdb.DB).GetTaskByID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	var env map[string]string
	if err := json.Unmarshal(row.Env, &env); err != nil {
		t.Fatalf("unmarshal env: %v", err)
	}
	if env[definitionRepoEnv] != "acme/service" {
		t.Fatalf("definition repo marker = %q, want acme/service", env[definitionRepoEnv])
	}
}

func TestSubmitTaskToolUsesDefinitionRepoWithoutGitHubTokenInheritance(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := ctxWithAdmin(context.Background())
	now := time.Now().UTC()
	q := repository.New(tdb.DB)
	insertDefinition(t, q, "base-agent", definitions.DefinitionTypeAgent, "review-synthesizer", "repo", "", "github.com/flatout-works/chetter", "agents/review-synthesizer.md", "base-synthesizer", now)
	insertDefinition(t, q, "fork-agent", definitions.DefinitionTypeAgent, "review-synthesizer", "repo", "", "github.com/contributor/chetter", "agents/review-synthesizer.md", "fork-synthesizer", now.Add(time.Second))
	insertDefinition(t, q, "base-skill", definitions.DefinitionTypeSkill, "pr-review-workflow", "repo", "", "github.com/flatout-works/chetter", "skills/pr-review-workflow/SKILL.md", "base-skill", now)

	_, out, err := svc.submitTaskTool(ctx, nil, SubmitTaskInput{
		Prompt:         "synthesize",
		GitURL:         "https://github.com/contributor/chetter.git",
		GitRef:         "fork-branch",
		Agent:          "review-synthesizer",
		Skills:         []string{"pr-review-workflow"},
		DefinitionRepo: "flatout-works/chetter",
		Env: map[string]string{
			"GITHUB_REPO": "flatout-works/chetter",
			"PR_NUMBER":   "123",
		},
	})
	if err != nil {
		t.Fatalf("submitTaskTool: %v", err)
	}
	row, err := q.GetTaskByID(ctx, out.Task.ID)
	if err != nil {
		t.Fatalf("get submitted task: %v", err)
	}
	env := parseJSON[map[string]string](row.Env, "task:"+row.ID+" env")
	if env[definitionRepoEnv] != "flatout-works/chetter" {
		t.Fatalf("definition repo marker = %q, want flatout-works/chetter", env[definitionRepoEnv])
	}
	if _, ok := env[gitHubTokenAllowedEnv]; ok {
		t.Fatalf("definition-only task should not request GitHub write token: %#v", env)
	}
	if _, ok := env[gitHubReadTokenAllowedEnv]; ok {
		t.Fatalf("definition-only task should not request GitHub read token: %#v", env)
	}

	rpc := NewRunnerRPCService(q, tdb.DB)
	claim, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{RunnerId: "runner_1", WaitSeconds: 0}))
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if claim.Msg.Task == nil {
		t.Fatal("expected claimed task")
	}
	if claim.Msg.Task.AgentDefinition != "base-synthesizer" {
		t.Fatalf("agent definition = %q, want base-synthesizer", claim.Msg.Task.AgentDefinition)
	}
	if string(claim.Msg.Task.SkillDefinitions["pr-review-workflow"]) != "base-skill" {
		t.Fatalf("skill definition = %q, want base-skill", string(claim.Msg.Task.SkillDefinitions["pr-review-workflow"]))
	}
	if _, ok := claim.Msg.Task.Env[definitionRepoEnv]; ok {
		t.Fatalf("definition repo marker leaked to runner env: %#v", claim.Msg.Task.Env)
	}
	if _, ok := claim.Msg.Task.Env[injectedGitHubTokenEnv]; ok {
		t.Fatalf("definition-only task received GitHub token: %#v", claim.Msg.Task.Env)
	}
}

func TestSubmitTaskInheritsGitHubTokenFromAuthorizedParentTask(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := ctxWithAdmin(context.Background())
	parent, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:           "parent",
		AgentImage:       "runner:latest",
		Env:              map[string]string{"GITHUB_TOKEN": "caller-token", "GITHUB_REPO": "flatout-works/chetter", "PR_NUMBER": "123"},
		AllowGitHubToken: true,
	})
	if err != nil {
		t.Fatalf("SubmitTask parent: %v", err)
	}

	child, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:     "child",
		AgentImage: "runner:latest",
		Env: map[string]string{
			"GITHUB_REPO":            "https://github.com/flatout-works/chetter.git",
			"PR_NUMBER":              "123",
			gitHubTokenParentTaskEnv: parent.ID,
		},
	})
	if err != nil {
		t.Fatalf("SubmitTask child: %v", err)
	}
	row, err := repository.New(tdb.DB).GetTaskByID(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	var env map[string]string
	if err := json.Unmarshal(row.Env, &env); err != nil {
		t.Fatalf("unmarshal env: %v", err)
	}
	if env[gitHubTokenAllowedEnv] != "true" {
		t.Fatalf("github token marker = %q, want true; env=%#v", env[gitHubTokenAllowedEnv], env)
	}
	if env["GITHUB_TOKEN"] != "[redacted]" {
		t.Fatalf("GITHUB_TOKEN = %q, want redacted placeholder", env["GITHUB_TOKEN"])
	}
	if env["GITHUB_REPO"] != "flatout-works/chetter" {
		t.Fatalf("GITHUB_REPO = %q, want flatout-works/chetter", env["GITHUB_REPO"])
	}
	if env[definitionRepoEnv] != "flatout-works/chetter" {
		t.Fatalf("definition repo marker = %q, want child GITHUB_REPO", env[definitionRepoEnv])
	}
}

func TestSubmitTaskInheritsReadOnlyGitHubTokenFromAuthorizedParentTask(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := ctxWithAdmin(context.Background())
	parent, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:           "parent",
		AgentImage:       "runner:latest",
		Env:              map[string]string{"GITHUB_TOKEN": "caller-token", "GITHUB_REPO": "flatout-works/chetter", "PR_NUMBER": "123"},
		AllowGitHubToken: true,
	})
	if err != nil {
		t.Fatalf("SubmitTask parent: %v", err)
	}

	child, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:     "child",
		AgentImage: "runner:latest",
		Env: map[string]string{
			"GITHUB_REPO":            "https://github.com/flatout-works/chetter.git",
			"PR_NUMBER":              "123",
			gitHubTokenParentTaskEnv: parent.ID,
			gitHubAuthModeEnv:        "read",
		},
	})
	if err != nil {
		t.Fatalf("SubmitTask child: %v", err)
	}
	row, err := repository.New(tdb.DB).GetTaskByID(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	var env map[string]string
	if err := json.Unmarshal(row.Env, &env); err != nil {
		t.Fatalf("unmarshal env: %v", err)
	}
	if _, ok := env[gitHubTokenAllowedEnv]; ok {
		t.Fatalf("read-only child received write marker: %#v", env)
	}
	if env[gitHubReadTokenAllowedEnv] != "true" {
		t.Fatalf("github read token marker = %q, want true; env=%#v", env[gitHubReadTokenAllowedEnv], env)
	}
	if env["GITHUB_TOKEN"] != "[redacted]" {
		t.Fatalf("GITHUB_TOKEN = %q, want redacted placeholder", env["GITHUB_TOKEN"])
	}
	if env["GITHUB_REPO"] != "flatout-works/chetter" {
		t.Fatalf("GITHUB_REPO = %q, want flatout-works/chetter", env["GITHUB_REPO"])
	}
	if _, ok := env[gitHubAuthModeEnv]; ok {
		t.Fatalf("public auth mode should not be persisted: %#v", env)
	}
	if env[definitionRepoEnv] != "flatout-works/chetter" {
		t.Fatalf("definition repo marker = %q, want child GITHUB_REPO", env[definitionRepoEnv])
	}
	if err := validateGitHubToolRepoScope(row, "flatout-works/chetter"); err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("read-only child write scope error = %v, want not authorized", err)
	}
}

func TestSubmitTaskRejectsUnknownGitHubAuthMode(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := ctxWithAdmin(context.Background())
	parent, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:           "parent",
		AgentImage:       "runner:latest",
		Env:              map[string]string{"GITHUB_TOKEN": "caller-token", "GITHUB_REPO": "flatout-works/chetter", "PR_NUMBER": "123"},
		AllowGitHubToken: true,
	})
	if err != nil {
		t.Fatalf("SubmitTask parent: %v", err)
	}

	_, err = svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:     "child",
		AgentImage: "runner:latest",
		Env: map[string]string{
			"GITHUB_REPO":            "flatout-works/chetter",
			"PR_NUMBER":              "123",
			gitHubTokenParentTaskEnv: parent.ID,
			gitHubAuthModeEnv:        "admin",
		},
	})
	if err == nil {
		t.Fatal("expected invalid GitHub auth mode to fail")
	}
	if !strings.Contains(err.Error(), gitHubAuthModeEnv+" must be read or write") {
		t.Fatalf("SubmitTask error = %q, want auth mode validation", err)
	}
}

func TestSubmitTaskRejectsGitHubTokenInheritanceWithoutAdmin(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	parent, err := svc.SubmitTask(ctxWithAdmin(ctx), SubmitTaskRequest{
		Prompt:           "parent",
		AgentImage:       "runner:latest",
		Env:              map[string]string{"GITHUB_TOKEN": "caller-token", "GITHUB_REPO": "flatout-works/chetter", "PR_NUMBER": "123"},
		AllowGitHubToken: true,
	})
	if err != nil {
		t.Fatalf("SubmitTask parent: %v", err)
	}

	_, err = svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:     "child",
		AgentImage: "runner:latest",
		Env: map[string]string{
			"GITHUB_REPO":            "flatout-works/chetter",
			"PR_NUMBER":              "123",
			gitHubTokenParentTaskEnv: parent.ID,
		},
	})
	if err == nil {
		t.Fatal("expected non-admin inheritance to fail")
	}
	if !strings.Contains(err.Error(), "admin access") {
		t.Fatalf("SubmitTask error = %q, want admin access", err)
	}
}

func TestSubmitTaskRejectsGitHubTokenInheritanceForDifferentRepo(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := ctxWithAdmin(context.Background())
	parent, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:           "parent",
		AgentImage:       "runner:latest",
		Env:              map[string]string{"GITHUB_TOKEN": "caller-token", "GITHUB_REPO": "flatout-works/chetter", "PR_NUMBER": "123"},
		AllowGitHubToken: true,
	})
	if err != nil {
		t.Fatalf("SubmitTask parent: %v", err)
	}

	_, err = svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:     "child",
		AgentImage: "runner:latest",
		Env: map[string]string{
			"GITHUB_REPO":            "other/repo",
			"PR_NUMBER":              "123",
			gitHubTokenParentTaskEnv: parent.ID,
		},
	})
	if err == nil {
		t.Fatal("expected mismatched repo inheritance to fail")
	}
	if !strings.Contains(err.Error(), "matching GITHUB_REPO") {
		t.Fatalf("SubmitTask error = %q, want matching GITHUB_REPO", err)
	}
}

func TestRecoverTaskPreservesGitHubTokenAuthorization(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := ctxWithAdmin(context.Background())
	orig, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:           "review",
		AgentImage:       "runner:latest",
		Env:              map[string]string{"GITHUB_TOKEN": "caller-token", "GITHUB_REPO": "flatout-works/chetter", "PR_NUMBER": "123"},
		AllowGitHubToken: true,
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	if _, err := tdb.DB.ExecContext(ctx, "UPDATE chetter_tasks SET status = 'error', session_export = ? WHERE id = ?", "previous transcript", orig.ID); err != nil {
		t.Fatalf("mark task recoverable: %v", err)
	}

	recovered, err := svc.RecoverTask(ctx, orig.ID)
	if err != nil {
		t.Fatalf("RecoverTask: %v", err)
	}
	row, err := repository.New(tdb.DB).GetTaskByID(ctx, recovered.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	var env map[string]string
	if err := json.Unmarshal(row.Env, &env); err != nil {
		t.Fatalf("unmarshal env: %v", err)
	}
	if env[gitHubTokenAllowedEnv] != "true" {
		t.Fatalf("github token marker = %q, want true; env=%#v", env[gitHubTokenAllowedEnv], env)
	}
	if env["GITHUB_TOKEN"] != "[redacted]" {
		t.Fatalf("GITHUB_TOKEN = %q, want redacted placeholder", env["GITHUB_TOKEN"])
	}
}

func TestTeamRecoverTaskStripsGitHubTokenAuthorization(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	teamID, _ := seedTeam(t, tdb.DB, "engineering", "alice")
	ctx := ctxWithTeam(context.Background(), teamID)
	orig, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:           "review",
		AgentImage:       "runner:latest",
		Env:              map[string]string{"GITHUB_TOKEN": "caller-token", "GITHUB_REPO": "flatout-works/chetter", "PR_NUMBER": "123"},
		AllowGitHubToken: true,
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	if _, err := tdb.DB.ExecContext(ctx, "UPDATE chetter_tasks SET status = 'error', session_export = ? WHERE id = ?", "previous transcript", orig.ID); err != nil {
		t.Fatalf("mark task recoverable: %v", err)
	}

	recovered, err := svc.RecoverTask(ctx, orig.ID)
	if err != nil {
		t.Fatalf("RecoverTask: %v", err)
	}
	row, err := repository.New(tdb.DB).GetTaskByID(ctx, recovered.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	var env map[string]string
	if err := json.Unmarshal(row.Env, &env); err != nil {
		t.Fatalf("unmarshal env: %v", err)
	}
	if _, ok := env[gitHubTokenAllowedEnv]; ok {
		t.Fatalf("team recovery preserved github token marker: %#v", env)
	}
	if env["GITHUB_TOKEN"] != "[redacted]" {
		t.Fatalf("GITHUB_TOKEN = %q, want redacted placeholder", env["GITHUB_TOKEN"])
	}
}

func TestTeamScopedSubmitTaskRejectsPrivilegedMCPProfile(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	teamID, _ := seedTeam(t, tdb.DB, "engineering", "alice")
	seedMCPProfile(t, tdb.DB, "chetter-orchestration", "name: chetter-orchestration\nurl: http://chetter-mcp:8080/mcp\nauth:\n  type: bearer\n  token: ${env:CHETTER_MCP_AUTH_TOKEN}\n")

	_, err := svc.SubmitTask(ctxWithTeam(context.Background(), teamID), SubmitTaskRequest{
		Prompt:      "x",
		AgentImage:  "runner:latest",
		MCPProfiles: []string{"chetter-orchestration"},
	})
	if err == nil {
		t.Fatal("expected team-scoped privileged profile use to be rejected")
	}
	if !strings.Contains(err.Error(), `"chetter-orchestration" requires admin access`) {
		t.Fatalf("SubmitTask error = %q, want admin access error", err)
	}
}

func TestSubmitTaskRejectsPrivilegedMCPProfileWithoutAdminScope(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	seedMCPProfile(t, tdb.DB, "chetter-orchestration", "name: chetter-orchestration\nurl: http://chetter-mcp:8080/mcp\nauth:\n  type: bearer\n  token: ${env:CHETTER_MCP_AUTH_TOKEN}\n")

	_, err := svc.SubmitTask(context.Background(), SubmitTaskRequest{
		Prompt:      "x",
		AgentImage:  "runner:latest",
		MCPProfiles: []string{"chetter-orchestration"},
	})
	if err == nil {
		t.Fatal("expected unscoped privileged profile use to be rejected")
	}
	if !strings.Contains(err.Error(), `"chetter-orchestration" requires admin access`) {
		t.Fatalf("SubmitTask error = %q, want admin access error", err)
	}
}

func TestSubmitTaskRejectsCredentialURLMCPProfileWithoutAdminScope(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	seedMCPProfile(t, tdb.DB, "userinfo-profile", "name: userinfo-profile\nurl: https://user:pass@mcp.example.test/mcp\n")
	seedMCPProfile(t, tdb.DB, "query-token-profile", "name: query-token-profile\nurl: https://mcp.example.test/mcp?api_key=secret\n")
	seedMCPProfile(t, tdb.DB, "query-jwt-profile", "name: query-jwt-profile\nurl: https://mcp.example.test/mcp?jwt=secret\n")
	seedMCPProfile(t, tdb.DB, "query-sig-profile", "name: query-sig-profile\nurl: https://mcp.example.test/mcp?sig=secret\n")
	seedMCPProfile(t, tdb.DB, "query-signature-profile", "name: query-signature-profile\nurl: https://mcp.example.test/mcp?signature=secret\n")
	seedMCPProfile(t, tdb.DB, "fragment-token-profile", "name: fragment-token-profile\nurl: https://mcp.example.test/mcp#access_token=secret\n")
	seedMCPProfile(t, tdb.DB, "fragment-route-token-profile", "name: fragment-route-token-profile\nurl: https://mcp.example.test/mcp#/callback?signature=secret\n")
	seedMCPProfile(t, tdb.DB, "path-token-profile", "name: path-token-profile\nurl: https://mcp.example.test/mcp/tok_live_abc123secret\n")
	seedMCPProfile(t, tdb.DB, "path-key-profile", "name: path-key-profile\nurl: https://mcp.example.test/mcp/access_token/abc123secret\n")

	for _, profileName := range []string{
		"userinfo-profile",
		"query-token-profile",
		"query-jwt-profile",
		"query-sig-profile",
		"query-signature-profile",
		"fragment-token-profile",
		"fragment-route-token-profile",
		"path-token-profile",
		"path-key-profile",
	} {
		t.Run(profileName, func(t *testing.T) {
			_, err := svc.SubmitTask(context.Background(), SubmitTaskRequest{
				Prompt:      "x",
				AgentImage:  "runner:latest",
				MCPProfiles: []string{profileName},
			})
			if err == nil {
				t.Fatal("expected credential-bearing profile use to be rejected")
			}
			if !strings.Contains(err.Error(), `"`+profileName+`" requires admin access`) {
				t.Fatalf("SubmitTask error = %q, want admin access error", err)
			}
		})
	}
}

func TestAdminSubmitTaskAllowsPrivilegedMCPProfile(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	seedMCPProfile(t, tdb.DB, "chetter-orchestration", "name: chetter-orchestration\nurl: http://chetter-mcp:8080/mcp\nauth:\n  type: bearer\n  token: ${env:CHETTER_MCP_AUTH_TOKEN}\n")

	rec, err := svc.SubmitTask(ctxWithAdmin(context.Background()), SubmitTaskRequest{
		Prompt:      "x",
		AgentImage:  "runner:latest",
		MCPProfiles: []string{"chetter-orchestration"},
	})
	if err != nil {
		t.Fatalf("admin SubmitTask with privileged profile: %v", err)
	}
	row, err := repository.New(tdb.DB).GetTaskByID(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	var env map[string]string
	if err := json.Unmarshal(row.Env, &env); err != nil {
		t.Fatalf("unmarshal env: %v", err)
	}
	if env[mcpProfilePrivilegedEnv] != "true" {
		t.Fatalf("privileged mcp marker = %q, want true; env=%#v", env[mcpProfilePrivilegedEnv], env)
	}
}

func TestSubmitTaskRejectsCredentialedAllowlistedMCPProfile(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	seedMCPProfile(t, tdb.DB, "chetter-orchestration", "name: chetter-orchestration\nurl: http://chetter-mcp:8080/mcp\nauth:\n  type: bearer\n  token: ${env:CHETTER_MCP_AUTH_TOKEN}\ntool_allowlist:\n  - chetter_submit_task\n")

	_, err := svc.SubmitTask(ctxWithAdmin(context.Background()), SubmitTaskRequest{
		Prompt:      "x",
		AgentImage:  "runner:latest",
		MCPProfiles: []string{"chetter-orchestration"},
	})
	if err == nil {
		t.Fatal("expected credentialed allowlisted profile to be rejected")
	}
	if !strings.Contains(err.Error(), `"chetter-orchestration" combines tool_allowlist with credentials`) {
		t.Fatalf("SubmitTask error = %q, want credentialed allowlist error", err)
	}
}

func TestAdminSubmitTaskAllowsCredentialURLMCPProfile(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	seedMCPProfile(t, tdb.DB, "query-token-profile", "name: query-token-profile\nurl: https://mcp.example.test/mcp?token=secret\n")

	rec, err := svc.SubmitTask(ctxWithAdmin(context.Background()), SubmitTaskRequest{
		Prompt:      "x",
		AgentImage:  "runner:latest",
		MCPProfiles: []string{"query-token-profile"},
	})
	if err != nil {
		t.Fatalf("admin SubmitTask with credential URL profile: %v", err)
	}
	row, err := repository.New(tdb.DB).GetTaskByID(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	var env map[string]string
	if err := json.Unmarshal(row.Env, &env); err != nil {
		t.Fatalf("unmarshal env: %v", err)
	}
	if env[mcpProfilePrivilegedEnv] != "true" {
		t.Fatalf("privileged mcp marker = %q, want true; env=%#v", env[mcpProfilePrivilegedEnv], env)
	}
}

func TestMCPProfileDefinitionsRequireAdminReadAccess(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	seedMCPProfile(t, tdb.DB, "secret-profile", "name: secret-profile\nurl: http://example.test/mcp\nauth:\n  type: bearer\n  token: literal-secret\n")

	if _, _, err := svc.getDefinitionTool(ctx, nil, GetDefinitionInput{DefinitionType: definitions.DefinitionTypeMCPProfile, Name: "secret-profile", SourceID: "test"}); err == nil {
		t.Fatal("expected non-admin get mcp profile definition to fail")
	}
	if _, _, err := svc.listDefinitionsTool(ctx, nil, ListDefinitionsInput{DefinitionType: definitions.DefinitionTypeMCPProfile}); err == nil {
		t.Fatal("expected non-admin list mcp profile definitions to fail")
	}
	_, out, err := svc.listDefinitionsTool(ctx, nil, ListDefinitionsInput{})
	if err != nil {
		t.Fatalf("list definitions: %v", err)
	}
	for _, def := range out.Definitions {
		if def.DefinitionType == definitions.DefinitionTypeMCPProfile {
			t.Fatalf("non-admin unfiltered list exposed mcp profile: %#v", def)
		}
	}
	if _, _, err := svc.getDefinitionTool(ctxWithAdmin(ctx), nil, GetDefinitionInput{DefinitionType: definitions.DefinitionTypeMCPProfile, Name: "secret-profile", SourceID: "test"}); err != nil {
		t.Fatalf("admin get mcp profile definition: %v", err)
	}
}

func TestSubmitTaskRejectsMissingPrompt(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	_, err := svc.SubmitTask(context.Background(), SubmitTaskRequest{
		AgentImage: "runner:latest",
	})
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}
}

func TestSubmitTaskAppliesDefaultAgentImage(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()

	rec, err := svc.SubmitTask(context.Background(), SubmitTaskRequest{Prompt: "x"})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	if rec.AgentImage != "runner:latest" {
		t.Errorf("default agent_image not applied: %s", rec.AgentImage)
	}
}

func TestRunnerTerminalEventCompletesSessionRun(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "x", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	rpc := NewRunnerRPCService(repository.New(tdb.DB), tdb.DB)
	if _, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{RunnerId: "runner_1", WaitSeconds: 1})); err != nil {
		t.Fatalf("claim: %v", err)
	}
	endedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := rpc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_1",
		Events: []*runnerv1.TaskEvent{{
			TaskId:            rec.ID,
			Status:            "done",
			Summary:           "finished",
			OpencodeSessionId: "opencode-session-1",
			SessionExport:     "export",
			EndedAt:           endedAt,
		}},
	})); err != nil {
		t.Fatalf("report terminal event: %v", err)
	}

	q := repository.New(tdb.DB)
	run, err := q.GetSessionRunByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get session run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("session run status = %s, want completed", run.Status)
	}
	if run.Summary.String != "finished" {
		t.Fatalf("session run summary = %q", run.Summary.String)
	}
	session, err := q.GetAgentSessionByID(ctx, run.AgentSessionID)
	if err != nil {
		t.Fatalf("get agent session: %v", err)
	}
	if session.Status != "completed" {
		t.Fatalf("agent session status = %s, want completed", session.Status)
	}
	if session.HarnessSessionID.String != "opencode-session-1" {
		t.Fatalf("harness session id = %q", session.HarnessSessionID.String)
	}
}

func TestRunnerTerminalEventPausesResumableSession(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:      "write code",
		AgentImage:  "runner:latest",
		SessionMode: "resumable",
		PauseReason: "waiting_for_pr_feedback",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	q := repository.New(tdb.DB)
	task, err := q.GetTaskByID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if !task.CheckpointAfterSuccess {
		t.Fatal("expected checkpoint_after_success=true for resumable session")
	}

	run, err := q.GetSessionRunByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get session run: %v", err)
	}
	if run.Status != "pending" {
		t.Fatalf("run status = %s, want pending", run.Status)
	}
	session, err := q.GetAgentSessionByID(ctx, run.AgentSessionID)
	if err != nil {
		t.Fatalf("get agent session: %v", err)
	}
	if session.ResumeMode != "harness_session" {
		t.Fatalf("resume_mode = %s, want harness_session", session.ResumeMode)
	}
	if session.PauseReason.String != "waiting_for_pr_feedback" {
		t.Fatalf("pause_reason = %s, want waiting_for_pr_feedback", session.PauseReason.String)
	}

	rpc := NewRunnerRPCService(repository.New(tdb.DB), tdb.DB)
	if _, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{RunnerId: "runner_1", WaitSeconds: 1})); err != nil {
		t.Fatalf("claim: %v", err)
	}
	endedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := rpc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_1",
		Events: []*runnerv1.TaskEvent{{
			TaskId:            rec.ID,
			Status:            "done",
			Summary:           "created PR",
			EndedAt:           endedAt,
			OpencodeSessionId: "oc_session_123",
			WorkspacePath:     "/var/lib/runner/" + rec.ID + "/workspace",
		}},
	})); err != nil {
		t.Fatalf("report terminal event: %v", err)
	}

	run, err = q.GetSessionRunByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get session run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("session run status = %s, want completed", run.Status)
	}

	session, err = q.GetAgentSessionByID(ctx, run.AgentSessionID)
	if err != nil {
		t.Fatalf("get agent session: %v", err)
	}
	if session.Status != "paused" {
		t.Fatalf("agent session status = %s, want paused", session.Status)
	}
	if session.PinnedRunnerID.String != "runner_1" {
		t.Fatalf("pinned_runner_id = %s, want runner_1", session.PinnedRunnerID.String)
	}
	if session.WorkspacePath.String != "/var/lib/runner/"+rec.ID+"/workspace" {
		t.Fatalf("workspace_path = %s", session.WorkspacePath.String)
	}
	if session.HarnessSessionID.String != "oc_session_123" {
		t.Fatalf("harness_session_id = %s, want oc_session_123", session.HarnessSessionID.String)
	}
}

func TestResumeAgentSessionFullFlow(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	q := repository.New(tdb.DB)
	rpc := NewRunnerRPCService(repository.New(tdb.DB), tdb.DB)

	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:      "create a PR",
		AgentImage:  "runner:latest",
		SessionMode: "resumable",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	run, err := q.GetSessionRunByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get session run: %v", err)
	}
	session, err := q.GetAgentSessionByID(ctx, run.AgentSessionID)
	if err != nil {
		t.Fatalf("get agent session: %v", err)
	}
	if session.ResumeMode != "harness_session" {
		t.Fatalf("resume_mode = %s, want harness_session", session.ResumeMode)
	}

	claimResp, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
		RunnerId: "runner_1", WaitSeconds: 1,
	}))
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimResp.Msg.Task == nil || claimResp.Msg.Task.TaskId != rec.ID {
		t.Fatalf("claim returned wrong task: %+v", claimResp.Msg.Task)
	}
	if claimResp.Msg.Task.ResumeWorkspacePath != "" {
		t.Fatalf("first run should have no resume workspace, got %q", claimResp.Msg.Task.ResumeWorkspacePath)
	}
	if claimResp.Msg.Task.ResumeHarnessSessionId != "" {
		t.Fatalf("first run should have no resume session ID, got %q", claimResp.Msg.Task.ResumeHarnessSessionId)
	}

	now := time.Now().UTC()
	if err := q.UpsertRunnerHeartbeat(ctx, repository.UpsertRunnerHeartbeatParams{
		ID:            "runner_1",
		Status:        "active",
		MaxConcurrent: 1,
		FirstSeenAt:   now,
		LastSeenAt:    now,
		UpdatedAt:     now,
		Metadata:      json.RawMessage("{}"),
	}); err != nil {
		t.Fatalf("upsert runner: %v", err)
	}

	endedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := rpc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_1",
		Events: []*runnerv1.TaskEvent{{
			TaskId:            rec.ID,
			Status:            "done",
			Summary:           "created PR #1",
			EndedAt:           endedAt,
			OpencodeSessionId: "oc_sid_abc",
			WorkspacePath:     "/var/lib/runner/" + rec.ID + "/workspace",
		}},
	})); err != nil {
		t.Fatalf("report terminal event: %v", err)
	}

	session, err = q.GetAgentSessionByID(ctx, run.AgentSessionID)
	if err != nil {
		t.Fatalf("get agent session after pause: %v", err)
	}
	if session.Status != "paused" {
		t.Fatalf("session status = %s, want paused", session.Status)
	}
	if session.PinnedRunnerID.String != "runner_1" {
		t.Fatalf("pinned_runner_id = %s, want runner_1", session.PinnedRunnerID.String)
	}
	if session.WorkspacePath.String != "/var/lib/runner/"+rec.ID+"/workspace" {
		t.Fatalf("workspace_path = %s", session.WorkspacePath.String)
	}
	if session.HarnessSessionID.String != "oc_sid_abc" {
		t.Fatalf("harness_session_id = %s, want oc_sid_abc", session.HarnessSessionID.String)
	}

	resumeOut, err := svc.ResumeAgentSession(ctx, session.ID, "address feedback", 600)
	if err != nil {
		t.Fatalf("resume agent session: %v", err)
	}
	if resumeOut.Task.ID == "" {
		t.Fatal("resume task ID is empty")
	}
	resumeTask, err := q.GetTaskByID(ctx, resumeOut.Task.ID)
	if err != nil {
		t.Fatalf("get resume task: %v", err)
	}
	if !resumeTask.RequiredRunnerID.Valid || resumeTask.RequiredRunnerID.String != "runner_1" {
		t.Fatalf("resume task required_runner_id = %s, want runner_1", resumeTask.RequiredRunnerID.String)
	}
	if !resumeTask.CheckpointAfterSuccess {
		t.Fatal("resume task should have checkpoint_after_success=true")
	}

	session, err = q.GetAgentSessionByID(ctx, run.AgentSessionID)
	if err != nil {
		t.Fatalf("get agent session after resume: %v", err)
	}
	if session.Status != "resuming" {
		t.Fatalf("session status = %s, want resuming", session.Status)
	}

	resumeClaim, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
		RunnerId: "runner_1", WaitSeconds: 0,
	}))
	if err != nil {
		t.Fatalf("claim resume task: %v", err)
	}
	if resumeClaim.Msg.Task == nil || resumeClaim.Msg.Task.TaskId != resumeOut.Task.ID {
		t.Fatalf("wrong resume task claimed: %+v", resumeClaim.Msg.Task)
	}
	if resumeClaim.Msg.Task.ResumeWorkspacePath != "/var/lib/runner/"+rec.ID+"/workspace" {
		t.Fatalf("resume workspace_path = %q, want /var/lib/runner/%s/workspace",
			resumeClaim.Msg.Task.ResumeWorkspacePath, rec.ID)
	}
	if resumeClaim.Msg.Task.ResumeHarnessSessionId != "oc_sid_abc" {
		t.Fatalf("resume harness_session_id = %q, want oc_sid_abc",
			resumeClaim.Msg.Task.ResumeHarnessSessionId)
	}
	if !resumeClaim.Msg.Task.CheckpointAfterSuccess {
		t.Fatal("resume claim should have checkpoint_after_success=true")
	}

	endedAt2 := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := rpc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_1",
		Events: []*runnerv1.TaskEvent{{
			TaskId:            resumeOut.Task.ID,
			Status:            "done",
			Summary:           "addressed feedback",
			EndedAt:           endedAt2,
			OpencodeSessionId: "oc_sid_abc",
			WorkspacePath:     "/var/lib/runner/" + rec.ID + "/workspace",
		}},
	})); err != nil {
		t.Fatalf("report resume terminal event: %v", err)
	}

	session, err = q.GetAgentSessionByID(ctx, run.AgentSessionID)
	if err != nil {
		t.Fatalf("get agent session after resume complete: %v", err)
	}
	if session.Status != "paused" {
		t.Fatalf("session status after resume complete = %s, want paused", session.Status)
	}

	t.Run("resumable timeout becomes recoverable", func(t *testing.T) {
		rec3, err := svc.SubmitTask(ctx, SubmitTaskRequest{
			Prompt: "continue work", AgentImage: "runner:latest", SessionMode: "resumable",
		})
		if err != nil {
			t.Fatalf("submit recoverable task: %v", err)
		}
		if _, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
			RunnerId: "runner_1", WaitSeconds: 1,
		})); err != nil {
			t.Fatalf("claim recoverable task: %v", err)
		}
		if _, err := rpc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
			RunnerId: "runner_1",
			Events: []*runnerv1.TaskEvent{{
				TaskId:            rec3.ID,
				Status:            "error",
				Error:             "prompt failed: context deadline exceeded",
				ErrorCategory:     "timeout",
				EndedAt:           time.Now().UTC().Format(time.RFC3339Nano),
				OpencodeSessionId: "oc_sid_timeout",
				WorkspacePath:     "/var/lib/runner/" + rec3.ID + "/workspace",
			}},
		})); err != nil {
			t.Fatalf("report timeout terminal event: %v", err)
		}

		run3, err := q.GetSessionRunByTaskID(ctx, rec3.ID)
		if err != nil {
			t.Fatalf("get timeout run: %v", err)
		}
		if run3.Status != "failed" {
			t.Fatalf("timeout run status = %s, want failed", run3.Status)
		}
		sess3, err := q.GetAgentSessionByID(ctx, run3.AgentSessionID)
		if err != nil {
			t.Fatalf("get recoverable session: %v", err)
		}
		if sess3.Status != "recoverable" {
			t.Fatalf("session status = %s, want recoverable", sess3.Status)
		}
		if sess3.WorkspacePath.String != "/var/lib/runner/"+rec3.ID+"/workspace" {
			t.Fatalf("recoverable workspace_path = %s", sess3.WorkspacePath.String)
		}
		if sess3.HarnessSessionID.String != "oc_sid_timeout" {
			t.Fatalf("recoverable harness_session_id = %s", sess3.HarnessSessionID.String)
		}

		resume3, err := svc.ResumeAgentSession(ctx, sess3.ID, "continue after timeout", 600)
		if err != nil {
			t.Fatalf("resume recoverable session: %v", err)
		}
		claim3, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
			RunnerId: "runner_1", WaitSeconds: 0,
		}))
		if err != nil {
			t.Fatalf("claim recoverable resume task: %v", err)
		}
		if claim3.Msg.Task == nil || claim3.Msg.Task.TaskId != resume3.Task.ID {
			t.Fatalf("wrong recoverable resume task claimed: %+v", claim3.Msg.Task)
		}
		if claim3.Msg.Task.ResumeWorkspacePath != "/var/lib/runner/"+rec3.ID+"/workspace" {
			t.Fatalf("resume workspace_path = %q", claim3.Msg.Task.ResumeWorkspacePath)
		}
		if claim3.Msg.Task.ResumeHarnessSessionId != "oc_sid_timeout" {
			t.Fatalf("resume harness_session_id = %q", claim3.Msg.Task.ResumeHarnessSessionId)
		}
		if _, err := rpc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
			RunnerId: "runner_1",
			Events: []*runnerv1.TaskEvent{{
				TaskId:            resume3.Task.ID,
				Status:            "done",
				EndedAt:           time.Now().UTC().Format(time.RFC3339Nano),
				OpencodeSessionId: "oc_sid_timeout",
				WorkspacePath:     "/var/lib/runner/" + rec3.ID + "/workspace",
			}},
		})); err != nil {
			t.Fatalf("finish recoverable resume task: %v", err)
		}
	})

	t.Run("other runner cannot claim pinned resume task", func(t *testing.T) {
		rec2, err := svc.SubmitTask(ctx, SubmitTaskRequest{
			Prompt: "further feedback", AgentImage: "runner:latest", SessionMode: "resumable",
		})
		if err != nil {
			t.Fatalf("submit second task: %v", err)
		}
		if _, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
			RunnerId: "runner_1", WaitSeconds: 1,
		})); err != nil {
			t.Fatalf("claim second task: %v", err)
		}
		if _, err := rpc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
			RunnerId: "runner_1",
			Events: []*runnerv1.TaskEvent{{
				TaskId:            rec2.ID,
				Status:            "done",
				EndedAt:           time.Now().UTC().Format(time.RFC3339Nano),
				OpencodeSessionId: "oc_sid_xyz",
				WorkspacePath:     "/var/lib/runner/" + rec2.ID + "/workspace",
			}},
		})); err != nil {
			t.Fatalf("report second terminal event: %v", err)
		}

		run2, _ := q.GetSessionRunByTaskID(ctx, rec2.ID)
		sess2, _ := q.GetAgentSessionByID(ctx, run2.AgentSessionID)
		resume2, err := svc.ResumeAgentSession(ctx, sess2.ID, "even more feedback", 600)
		if err != nil {
			t.Fatalf("resume second session: %v", err)
		}

		claim, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
			RunnerId: "runner_other", WaitSeconds: 0,
		}))
		if err != nil {
			t.Fatalf("claim other runner: %v", err)
		}
		if claim.Msg.Task != nil && claim.Msg.Task.TaskId == resume2.Task.ID {
			t.Fatal("other runner should NOT be able to claim pinned resume task")
		}
	})
}

func TestResumeAgentSessionPreservesGitHubContextAndMCPProfiles(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := ctxWithAdmin(context.Background())
	q := repository.New(tdb.DB)
	seedMCPProfile(t, tdb.DB, "review-tools", "name: review-tools\nurl: http://review-tools:8080/mcp\n")

	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:           "create a PR",
		AgentImage:       "runner:latest",
		Skills:           []string{"pr-review-workflow"},
		MCPProfiles:      []string{"review-tools"},
		Env:              map[string]string{"GITHUB_TOKEN": "caller-token", "GITHUB_REPO": "flatout-works/chetter", "PR_NUMBER": "123"},
		SessionMode:      "resumable",
		AllowGitHubToken: true,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	run, err := q.GetSessionRunByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get session run: %v", err)
	}
	now := time.Now().UTC()
	if err := q.UpsertRunnerHeartbeat(ctx, repository.UpsertRunnerHeartbeatParams{
		ID:            "runner_1",
		Status:        "active",
		MaxConcurrent: 1,
		FirstSeenAt:   now,
		LastSeenAt:    now,
		UpdatedAt:     now,
		Metadata:      json.RawMessage("{}"),
	}); err != nil {
		t.Fatalf("upsert runner: %v", err)
	}
	if _, err := tdb.DB.ExecContext(ctx, `
		UPDATE chetter_agent_sessions
		SET status='paused', pinned_runner_id=?, workspace_path=?, harness_session_id=?, paused_at=?, updated_at=?
		WHERE id=?`,
		"runner_1", "/var/lib/runner/"+rec.ID+"/workspace", "oc_sid_abc", now, now, run.AgentSessionID,
	); err != nil {
		t.Fatalf("pause session: %v", err)
	}

	resumeOut, err := svc.ResumeAgentSession(ctx, run.AgentSessionID, "address feedback", 600)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	resumeTask, err := q.GetTaskByID(ctx, resumeOut.Task.ID)
	if err != nil {
		t.Fatalf("get resume task: %v", err)
	}
	var env map[string]string
	if err := json.Unmarshal(resumeTask.Env, &env); err != nil {
		t.Fatalf("unmarshal env: %v", err)
	}
	if env[gitHubTokenAllowedEnv] != "true" {
		t.Fatalf("github token marker = %q, want true; env=%#v", env[gitHubTokenAllowedEnv], env)
	}
	if env["GITHUB_TOKEN"] != "[redacted]" {
		t.Fatalf("GITHUB_TOKEN = %q, want redacted placeholder", env["GITHUB_TOKEN"])
	}
	var skills []string
	if err := json.Unmarshal(resumeTask.Skills, &skills); err != nil {
		t.Fatalf("unmarshal skills: %v", err)
	}
	if len(skills) != 1 || skills[0] != "pr-review-workflow" {
		t.Fatalf("skills = %#v, want pr-review-workflow", skills)
	}
	var profiles []string
	if err := json.Unmarshal(resumeTask.McpProfiles, &profiles); err != nil {
		t.Fatalf("unmarshal mcp_profiles: %v", err)
	}
	if len(profiles) != 1 || profiles[0] != "review-tools" {
		t.Fatalf("mcp_profiles = %#v, want review-tools", profiles)
	}
}

func TestTeamResumeAgentSessionStripsGitHubTokenAuthorization(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	teamID, _ := seedTeam(t, tdb.DB, "engineering", "alice")
	ctx := ctxWithTeam(context.Background(), teamID)
	q := repository.New(tdb.DB)

	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:           "create a PR",
		AgentImage:       "runner:latest",
		Env:              map[string]string{"GITHUB_TOKEN": "caller-token", "GITHUB_REPO": "flatout-works/chetter", "PR_NUMBER": "123"},
		SessionMode:      "resumable",
		AllowGitHubToken: true,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	run, err := q.GetSessionRunByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get session run: %v", err)
	}
	now := time.Now().UTC()
	if err := q.UpsertRunnerHeartbeat(ctx, repository.UpsertRunnerHeartbeatParams{
		ID:            "runner_1",
		Status:        "active",
		MaxConcurrent: 1,
		FirstSeenAt:   now,
		LastSeenAt:    now,
		UpdatedAt:     now,
		Metadata:      json.RawMessage("{}"),
	}); err != nil {
		t.Fatalf("upsert runner: %v", err)
	}
	if _, err := tdb.DB.ExecContext(ctx, `
		UPDATE chetter_agent_sessions
		SET status='paused', pinned_runner_id=?, workspace_path=?, harness_session_id=?, paused_at=?, updated_at=?
		WHERE id=?`,
		"runner_1", "/var/lib/runner/"+rec.ID+"/workspace", "oc_sid_abc", now, now, run.AgentSessionID,
	); err != nil {
		t.Fatalf("pause session: %v", err)
	}

	resumeOut, err := svc.ResumeAgentSession(ctx, run.AgentSessionID, "address feedback", 600)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	resumeTask, err := q.GetTaskByID(ctx, resumeOut.Task.ID)
	if err != nil {
		t.Fatalf("get resume task: %v", err)
	}
	var env map[string]string
	if err := json.Unmarshal(resumeTask.Env, &env); err != nil {
		t.Fatalf("unmarshal env: %v", err)
	}
	if _, ok := env[gitHubTokenAllowedEnv]; ok {
		t.Fatalf("team resume preserved github token marker: %#v", env)
	}
	if env["GITHUB_TOKEN"] != "[redacted]" {
		t.Fatalf("GITHUB_TOKEN = %q, want redacted placeholder", env["GITHUB_TOKEN"])
	}
}

func TestResumeSessionForPRFindsPRReviewArtifact(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	q := repository.New(tdb.DB)
	now := time.Now().UTC()
	insertDefinition(t, q, "repo-profile", definitions.DefinitionTypeMCPProfile, "chetter-orchestration", "repo", "", "github.com/flatout-works/chetter", "mcp-profiles/chetter-orchestration.yaml", "name: chetter-orchestration\nurl: http://chetter-mcp:8080/mcp\nauth:\n  type: bearer\n  token: ${env:CHETTER_MCP_AUTH_TOKEN}\n", now)
	insertDefinition(t, q, "repo-profile", definitions.DefinitionTypeMCPProfile, "public-tools", "repo", "", "github.com/flatout-works/chetter", "mcp-profiles/public-tools.yaml", "name: public-tools\nurl: http://public-tools:8080/mcp\n", now)

	rec, err := svc.SubmitTask(ctxWithAdmin(ctx), SubmitTaskRequest{
		Prompt:         "create a PR",
		GitURL:         "https://github.com/fork/chetter.git",
		AgentImage:     "runner:latest",
		SessionMode:    "resumable",
		MCPProfiles:    []string{"chetter-orchestration", "public-tools"},
		DefinitionRepo: "flatout-works/chetter",
		Env: map[string]string{
			"GITHUB_TOKEN": "caller-token",
			"GITHUB_REPO":  "flatout-works/chetter",
			"PR_NUMBER":    "123",
		},
		AllowGitHubToken: true,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	run, err := q.GetSessionRunByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get session run: %v", err)
	}
	if err := q.UpsertRunnerHeartbeat(ctx, repository.UpsertRunnerHeartbeatParams{
		ID:            "runner_1",
		Status:        "active",
		MaxConcurrent: 1,
		FirstSeenAt:   now,
		LastSeenAt:    now,
		UpdatedAt:     now,
		Metadata:      json.RawMessage("{}"),
	}); err != nil {
		t.Fatalf("upsert runner: %v", err)
	}
	if _, err := tdb.DB.ExecContext(ctx, `
		UPDATE chetter_agent_sessions
		SET status='paused', pinned_runner_id=?, workspace_path=?, harness_session_id=?, paused_at=?, updated_at=?
		WHERE id=?`,
		"runner_1", "/var/lib/runner/"+rec.ID+"/workspace", "oc_sid_abc", now, now, run.AgentSessionID,
	); err != nil {
		t.Fatalf("pause session: %v", err)
	}
	if err := q.InsertTaskArtifact(ctx, repository.InsertTaskArtifactParams{
		ID:              "art_pr_review_resume",
		TaskID:          rec.ID,
		AgentSessionID:  sql.NullString{String: run.AgentSessionID, Valid: true},
		SessionRunID:    sql.NullString{String: run.ID, Valid: true},
		ArtifactType:    "pr_review",
		Repo:            "flatout-works/chetter",
		Number:          sql.NullInt32{Int32: 123, Valid: true},
		Url:             sql.NullString{String: "https://github.com/flatout-works/chetter/pull/123#pullrequestreview-1", Valid: true},
		CreatedAt:       now,
		DiscoveredAt:    now,
		DiscoverySource: "test",
	}); err != nil {
		t.Fatalf("insert artifact: %v", err)
	}

	if err := svc.ResumeSessionForPR(ctx, "flatout-works/chetter", 123); err != nil {
		t.Fatalf("ResumeSessionForPR: %v", err)
	}
	session, err := q.GetAgentSessionByID(ctx, run.AgentSessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Status != "resuming" {
		t.Fatalf("session status = %s, want resuming", session.Status)
	}
	runs, err := q.ListSessionRunsBySession(ctx, run.AgentSessionID)
	if err != nil {
		t.Fatalf("list session runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("session runs = %d, want original and resume: %#v", len(runs), runs)
	}
	resumeTask, err := q.GetTaskByID(ctx, runs[1].TaskID)
	if err != nil {
		t.Fatalf("get resume task: %v", err)
	}
	var env map[string]string
	if err := json.Unmarshal(resumeTask.Env, &env); err != nil {
		t.Fatalf("unmarshal resume env: %v", err)
	}
	if env[gitHubTokenAllowedEnv] != "true" {
		t.Fatalf("resume should preserve github token authorization: %#v", env)
	}
	if env["GITHUB_TOKEN"] != "[redacted]" {
		t.Fatalf("resume GITHUB_TOKEN = %q, want redacted placeholder", env["GITHUB_TOKEN"])
	}
	if _, ok := env[mcpProfilePrivilegedEnv]; ok {
		t.Fatalf("PR feedback resume preserved privileged mcp marker: %#v", env)
	}
	var profiles []string
	if err := json.Unmarshal(resumeTask.McpProfiles, &profiles); err != nil {
		t.Fatalf("unmarshal resume mcp_profiles: %v", err)
	}
	if len(profiles) != 1 || profiles[0] != "public-tools" {
		t.Fatalf("resume mcp_profiles = %#v, want only public-tools", profiles)
	}
}

func TestReaperFailsResumeWhenPinnedRunnerDisappears(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	q := repository.New(tdb.DB)
	rpc := NewRunnerRPCService(repository.New(tdb.DB), tdb.DB)

	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:      "create a PR",
		AgentImage:  "runner:latest",
		SessionMode: "resumable",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
		RunnerId: "runner_gone", WaitSeconds: 0,
	})); err != nil {
		t.Fatalf("claim: %v", err)
	}
	now := time.Now().UTC()
	if err := q.UpsertRunnerHeartbeat(ctx, repository.UpsertRunnerHeartbeatParams{
		ID:            "runner_gone",
		Status:        "active",
		MaxConcurrent: 1,
		FirstSeenAt:   now,
		LastSeenAt:    now,
		UpdatedAt:     now,
		Metadata:      json.RawMessage("{}"),
	}); err != nil {
		t.Fatalf("upsert runner: %v", err)
	}
	if _, err := rpc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_gone",
		Events: []*runnerv1.TaskEvent{{
			TaskId:            rec.ID,
			Status:            "done",
			EndedAt:           now.Format(time.RFC3339Nano),
			OpencodeSessionId: "oc_sid_gone",
			WorkspacePath:     "/var/lib/runner/" + rec.ID + "/workspace",
		}},
	})); err != nil {
		t.Fatalf("report terminal event: %v", err)
	}

	run, err := q.GetSessionRunByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get session run: %v", err)
	}
	session, err := q.GetAgentSessionByID(ctx, run.AgentSessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	resumeOut, err := svc.ResumeAgentSession(ctx, session.ID, "address feedback", 600)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}

	stale := now.Add(-5 * time.Minute)
	if err := q.UpsertRunnerHeartbeat(ctx, repository.UpsertRunnerHeartbeatParams{
		ID:            "runner_gone",
		Status:        "active",
		MaxConcurrent: 1,
		FirstSeenAt:   stale,
		LastSeenAt:    stale,
		UpdatedAt:     stale,
		Metadata:      json.RawMessage("{}"),
	}); err != nil {
		t.Fatalf("mark runner stale: %v", err)
	}

	svc.reapUnavailablePinnedResumeTasks()

	resumeTask, err := q.GetTaskByID(ctx, resumeOut.Task.ID)
	if err != nil {
		t.Fatalf("get resume task: %v", err)
	}
	if resumeTask.Status != "error" || resumeTask.ErrorCategory.String != "runner_unavailable" {
		t.Fatalf("resume task status/category = %s/%s, want error/runner_unavailable", resumeTask.Status, resumeTask.ErrorCategory.String)
	}
	resumeRun, err := q.GetSessionRunByTaskID(ctx, resumeOut.Task.ID)
	if err != nil {
		t.Fatalf("get resume run: %v", err)
	}
	if resumeRun.Status != "failed" {
		t.Fatalf("resume run status = %s, want failed", resumeRun.Status)
	}
	session, err = q.GetAgentSessionByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("get session after reaper: %v", err)
	}
	if session.Status != "error" || !strings.Contains(session.Error.String, "runner_gone") {
		t.Fatalf("session status/error = %s/%q, want error mentioning runner_gone", session.Status, session.Error.String)
	}
}

func TestServiceCancelTaskMarksRunningAsCancelled(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "x", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	// Claim the task
	now := time.Now().UTC()
	q := repository.New(tdb.DB)
	rows, err := q.MarkTaskClaimed(ctx, repository.MarkTaskClaimedParams{
		RunnerID:       sql.NullString{String: "runner_1", Valid: true},
		ClaimedAt:      sql.NullTime{Time: now, Valid: true},
		LeaseExpiresAt: sql.NullTime{Time: now.Add(time.Hour), Valid: true},
		StartedAt:      sql.NullTime{Time: now, Valid: true},
		UpdatedAt:      now,
		LastEventAt:    sql.NullTime{Time: now, Valid: true},
		ID:             rec.ID,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if rows != 1 {
		t.Fatalf("claim rows: %d", rows)
	}

	rows, err = svc.repo.CancelTask(ctx, repository.CancelTaskParams{
		Error:     sql.NullString{String: "by operator", Valid: true},
		EndedAt:   sql.NullTime{Time: now, Valid: true},
		UpdatedAt: now,
		ID:        rec.ID,
	})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if rows != 1 {
		t.Fatalf("cancel rows: %d", rows)
	}

	row, err := q.GetTaskByID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.Status != "cancelled" {
		t.Errorf("expected status=cancelled, got %s", row.Status)
	}
	if row.Error.String != "by operator" {
		t.Errorf("error not stored: %q", row.Error.String)
	}
}

func TestServiceClearPendingTasksCancelsQueued(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	rec1, _ := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "a", AgentImage: "runner:latest"})
	rec2, _ := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "b", AgentImage: "runner:latest"})

	cancelled, err := svc.repo.ClearPendingTasks(ctx, repository.ClearPendingTasksParams{
		Error:     sql.NullString{String: "queue cleared", Valid: true},
		EndedAt:   sql.NullTime{Time: time.Now().UTC(), Valid: true},
		UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if cancelled != 2 {
		t.Errorf("expected 2 cancelled, got %d", cancelled)
	}

	q := repository.New(tdb.DB)
	for _, id := range []string{rec1.ID, rec2.ID} {
		row, err := q.GetTaskByID(ctx, id)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if row.Status != "cancelled" {
			t.Errorf("expected cancelled, got %s", row.Status)
		}
	}
}

func TestServiceCreateTriggerPersistsAndActivates(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()

	rec, err := svc.CreateTrigger(ctx, store.TriggerInput{
		Name:        "hourly-check",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "@hourly",
		Prompt:      "check the logs",
		AgentImage:  "runner:latest",
		TimeoutSec:  300,
	})
	if err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}
	if rec.Name != "hourly-check" {
		t.Errorf("name: %s", rec.Name)
	}
	if !rec.Enabled {
		t.Error("new trigger should be enabled")
	}
	if rec.NextRunAt == nil {
		t.Error("next_run_at should be set after activation")
	}

	q := repository.New(tdb.DB)
	row, err := q.GetTriggerByName(ctx, "hourly-check")
	if err != nil {
		t.Fatalf("get trigger: %v", err)
	}
	if row.Prompt != "check the logs" {
		t.Errorf("prompt: %s", row.Prompt)
	}
}

func TestRunTriggerNowStampsTaskAttribution(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	teamID, _ := seedTeam(t, tdb.DB, "automation", "alice")

	if _, err := svc.CreateTrigger(ctxWithTeam(ctx, teamID), store.TriggerInput{
		Name:        "attributed-check",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "@hourly",
		Prompt:      "check attribution",
		AgentImage:  "runner:latest",
		TimeoutSec:  300,
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	task, err := svc.RunTriggerNow(ctx, "attributed-check")
	if err != nil {
		t.Fatalf("RunTriggerNow: %v", err)
	}
	if task.TeamID != teamID || task.TriggerName != "attributed-check" || task.TriggerType != store.TriggerTypeCron {
		t.Fatalf("returned task missing trigger attribution: %+v", task)
	}

	row, err := repository.New(tdb.DB).GetTaskByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if row.TeamID.String != teamID || row.TriggerName.String != "attributed-check" || row.TriggerType.String != store.TriggerTypeCron {
		t.Fatalf("persisted task missing trigger attribution: %+v", row)
	}
}

func TestServiceCreateTriggerRejectsInvalidCron(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	_, err := svc.CreateTrigger(context.Background(), store.TriggerInput{
		Name:        "bad",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "not a cron",
		Prompt:      "x",
		AgentImage:  "runner:latest",
		TimeoutSec:  60,
	})
	if err == nil {
		t.Fatal("expected error for invalid cron")
	}
}

func TestUpdateTriggerRejectsUnknownTypeWithoutRemovingCronEntry(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	rec, err := svc.CreateTrigger(ctx, store.TriggerInput{
		Name:        "unknown-type-update",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "@hourly",
		Prompt:      "original",
		AgentImage:  "runner:latest",
		TimeoutSec:  60,
	})
	if err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}
	svc.cronMu.Lock()
	_, hadEntry := svc.cronEntries[rec.ID]
	svc.cronMu.Unlock()
	if !hadEntry {
		t.Fatal("expected cron entry before update")
	}

	_, err = svc.UpdateTrigger(ctx, "unknown-type-update", store.TriggerInput{
		Name:        "unknown-type-update",
		TriggerType: "webhook",
		CronExpr:    "@daily",
		Prompt:      "updated",
		AgentImage:  "runner:latest",
		TimeoutSec:  60,
	}, true)
	if err == nil {
		t.Fatal("expected unknown trigger_type to be rejected")
	}
	if !strings.Contains(err.Error(), `unknown trigger_type "webhook"`) {
		t.Fatalf("UpdateTrigger error = %q, want unknown trigger_type", err)
	}

	row, getErr := repository.New(tdb.DB).GetTriggerByName(ctx, "unknown-type-update")
	if getErr != nil {
		t.Fatalf("GetTriggerByName: %v", getErr)
	}
	if row.TriggerType != store.TriggerTypeCron || row.CronExpr != "@hourly" || row.Prompt != "original" {
		t.Fatalf("trigger was modified despite validation error: %#v", row)
	}
	svc.cronMu.Lock()
	_, stillHasEntry := svc.cronEntries[rec.ID]
	svc.cronMu.Unlock()
	if !stillHasEntry {
		t.Fatal("cron entry should remain after rejected update")
	}
}

func TestCreateTriggerRejectsMissingMCPProfile(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	_, err := svc.CreateTrigger(context.Background(), store.TriggerInput{
		Name:        "missing-profile-trigger",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "@hourly",
		Prompt:      "check",
		AgentImage:  "runner:latest",
		MCPProfiles: []string{"missing-profile"},
	})
	if err == nil {
		t.Fatal("expected missing mcp profile to be rejected")
	}
	if !strings.Contains(err.Error(), `"missing-profile" is not an active mcp profile`) {
		t.Fatalf("CreateTrigger error = %q, want missing profile", err)
	}
}

func TestCreateTriggerRejectsCredentialURLMCPProfileWithoutAdminScope(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	seedMCPProfile(t, tdb.DB, "query-token-profile", "name: query-token-profile\nurl: https://mcp.example.test/mcp?access_token=secret\n")
	_, err := svc.CreateTrigger(context.Background(), store.TriggerInput{
		Name:        "credential-profile-trigger",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "@hourly",
		Prompt:      "check",
		AgentImage:  "runner:latest",
		MCPProfiles: []string{"query-token-profile"},
	})
	if err == nil {
		t.Fatal("expected credential-bearing mcp profile to be rejected")
	}
	if !strings.Contains(err.Error(), `"query-token-profile" requires admin access`) {
		t.Fatalf("CreateTrigger error = %q, want admin access", err)
	}
}

func TestCreateWebhookTriggerValidatesMCPProfilesAgainstWatchedRepo(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	insertDefinition(t, repository.New(tdb.DB), "repo-profile", definitions.DefinitionTypeMCPProfile, "repo-tools", "repo", "", "github.com/acme/service", "mcp-profiles/repo-tools.yaml", "name: repo-tools\nurl: http://repo-tools:8080/mcp\n", now)
	triggerConfig := mustMarshalJSON(store.PRReviewTriggerConfig{Repo: "acme/service"})

	for _, triggerType := range []string{store.TriggerTypePRReview, store.TriggerTypeIssue} {
		t.Run(triggerType, func(t *testing.T) {
			if _, err := svc.CreateTrigger(ctx, store.TriggerInput{
				Name:          "repo-profile-" + triggerType,
				TriggerType:   triggerType,
				TriggerConfig: string(triggerConfig),
				AgentImage:    "runner:latest",
				MCPProfiles:   []string{"repo-tools"},
			}); err != nil {
				t.Fatalf("CreateTrigger with repo-scoped profile: %v", err)
			}
		})
	}
}

func TestUpdateTriggerRejectsMissingMCPProfile(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := svc.CreateTrigger(ctx, store.TriggerInput{
		Name:        "profile-update",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "@hourly",
		Prompt:      "original",
		AgentImage:  "runner:latest",
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}
	_, err := svc.UpdateTrigger(ctx, "profile-update", store.TriggerInput{
		Name:        "profile-update",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "@daily",
		Prompt:      "updated",
		AgentImage:  "runner:latest",
		MCPProfiles: []string{"missing-profile"},
	}, true)
	if err == nil {
		t.Fatal("expected missing mcp profile to be rejected")
	}
	if !strings.Contains(err.Error(), `"missing-profile" is not an active mcp profile`) {
		t.Fatalf("UpdateTrigger error = %q, want missing profile", err)
	}
	row, getErr := repository.New(tdb.DB).GetTriggerByName(ctx, "profile-update")
	if getErr != nil {
		t.Fatalf("GetTriggerByName: %v", getErr)
	}
	if row.Prompt != "original" {
		t.Fatalf("trigger was modified despite validation error: %#v", row)
	}
}

func TestUpdateWebhookTriggerValidatesMCPProfilesAgainstWatchedRepo(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	insertDefinition(t, repository.New(tdb.DB), "repo-profile", definitions.DefinitionTypeMCPProfile, "repo-tools", "repo", "", "github.com/acme/service", "mcp-profiles/repo-tools.yaml", "name: repo-tools\nurl: http://repo-tools:8080/mcp\n", now)
	originalConfig := mustMarshalJSON(store.PRReviewTriggerConfig{Repo: "acme/service"})
	if _, err := svc.CreateTrigger(ctx, store.TriggerInput{
		Name:          "repo-profile-update",
		TriggerType:   store.TriggerTypePRReview,
		TriggerConfig: string(originalConfig),
		AgentImage:    "runner:latest",
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	if _, err := svc.UpdateTrigger(ctx, "repo-profile-update", store.TriggerInput{
		Name:          "repo-profile-update",
		TriggerType:   store.TriggerTypePRReview,
		TriggerConfig: string(originalConfig),
		AgentImage:    "runner:latest",
		MCPProfiles:   []string{"repo-tools"},
	}, true); err != nil {
		t.Fatalf("UpdateTrigger with repo-scoped profile: %v", err)
	}
}

func TestUpdateIssueTriggerAllowsEmptyPrompt(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	triggerConfig := string(mustMarshalJSON(map[string]any{"repo": "flatout-works/chetter", "event": "opened"}))
	if _, err := svc.CreateTrigger(ctx, store.TriggerInput{
		Name:          "issue-no-prompt-update",
		TriggerType:   store.TriggerTypeIssue,
		TriggerConfig: triggerConfig,
		AgentImage:    "runner:latest",
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	rec, err := svc.UpdateTrigger(ctx, "issue-no-prompt-update", store.TriggerInput{
		Name:          "issue-no-prompt-update",
		TriggerType:   store.TriggerTypeIssue,
		TriggerConfig: string(mustMarshalJSON(map[string]any{"repo": "flatout-works/chetter", "event": "comment"})),
		AgentImage:    "runner:latest",
	}, true)
	if err != nil {
		t.Fatalf("UpdateTrigger issue with empty prompt: %v", err)
	}
	if rec.Prompt != "" {
		t.Fatalf("prompt = %q, want empty", rec.Prompt)
	}
	if got := triggerConfigRepo(rec.TriggerConfig); got != "flatout-works/chetter" {
		t.Fatalf("repo = %q, want flatout-works/chetter", got)
	}
}

func TestUpdateIssueTriggerRejectsMissingRepo(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := svc.CreateTrigger(ctx, store.TriggerInput{
		Name:          "issue-missing-repo-update",
		TriggerType:   store.TriggerTypeIssue,
		TriggerConfig: string(mustMarshalJSON(map[string]any{"repo": "flatout-works/chetter"})),
		AgentImage:    "runner:latest",
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	_, err := svc.UpdateTrigger(ctx, "issue-missing-repo-update", store.TriggerInput{
		Name:          "issue-missing-repo-update",
		TriggerType:   store.TriggerTypeIssue,
		TriggerConfig: "{}",
		AgentImage:    "runner:latest",
	}, true)
	if err == nil {
		t.Fatal("expected issue update without repo to be rejected")
	}
	if !strings.Contains(err.Error(), "repo is required in trigger_config for issue triggers") {
		t.Fatalf("UpdateTrigger error = %q, want missing issue repo", err)
	}
}

func TestServiceCreateTriggerAppliesDefaultAgentImage(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()

	rec, err := svc.CreateTrigger(context.Background(), store.TriggerInput{
		Name:        "default-image",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "@hourly",
		Prompt:      "x",
		TimeoutSec:  60,
	})
	if err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}
	if rec.AgentImage != "runner:latest" {
		t.Fatalf("agent image = %q, want runner:latest", rec.AgentImage)
	}
}

func TestServiceCreateTriggerRequiresPrompt(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	_, err := svc.CreateTrigger(context.Background(), store.TriggerInput{
		Name:        "no-prompt",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "@hourly",
		AgentImage:  "runner:latest",
		TimeoutSec:  60,
	})
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}
}

func TestServiceListTriggersReturnsEnabled(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := svc.CreateTrigger(ctx, store.TriggerInput{
		Name: "enabled", TriggerType: store.TriggerTypeCron, CronExpr: "@hourly", Prompt: "x",
		AgentImage: "runner:latest", TimeoutSec: 60,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.CreateTrigger(ctx, store.TriggerInput{
		Name: "disabled", TriggerType: store.TriggerTypeCron, CronExpr: "@daily", Prompt: "y",
		AgentImage: "runner:latest", TimeoutSec: 60,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.UpdateTrigger(ctx, "disabled", store.TriggerInput{
		Name: "disabled", TriggerType: store.TriggerTypeCron, CronExpr: "@daily", Prompt: "y",
		AgentImage: "runner:latest", TimeoutSec: 60,
	}, false); err != nil {
		t.Fatalf("update: %v", err)
	}

	q := repository.New(svc.repo.DB())
	enabled, err := q.ListEnabledTriggers(ctx)
	if err != nil {
		t.Fatalf("list enabled: %v", err)
	}
	if len(enabled) != 1 || enabled[0].Name != "enabled" {
		t.Errorf("expected only 'enabled' in list, got %+v", enabled)
	}
}

// TestListEnabledPRReviewTriggersByRepoMatchesRepo verifies the webhook
// trigger lookup returns the right triggers for a given repo. This guards
// against the bug where the repo string was wrapped in JSON quotes and
// the query's `->>` operator compared against an unquoted string.
func TestListEnabledPRReviewTriggersByRepoMatchesRepo(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	cfg := store.PRReviewTriggerConfig{Repo: "flatout-works/chetter"}
	triggerConfig, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal trigger config: %v", err)
	}

	// Create one pr_review trigger for our repo.
	if _, err := svc.CreateTrigger(ctx, store.TriggerInput{
		Name:          "deep-review",
		TriggerType:   store.TriggerTypePRReview,
		TriggerConfig: string(triggerConfig),
		Prompt:        "review please",
		AgentImage:    "runner:latest",
		Agent:         "pr-reviewer",
		ProviderID:    "opencode",
		ModelID:       "minimax-m3",
		Harness:       "pi",
		TimeoutSec:    3600,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Create a pr_review trigger for a different repo to confirm filtering.
	cfg2 := store.PRReviewTriggerConfig{Repo: "flatout-works/other"}
	triggerConfig2, _ := json.Marshal(cfg2)
	if _, err := svc.CreateTrigger(ctx, store.TriggerInput{
		Name:          "other-review",
		TriggerType:   store.TriggerTypePRReview,
		TriggerConfig: string(triggerConfig2),
		Prompt:        "review please",
		AgentImage:    "runner:latest",
		Agent:         "pr-reviewer",
		ProviderID:    "opencode",
		ModelID:       "minimax-m3",
		TimeoutSec:    3600,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	matches, err := svc.ListEnabledPRReviewTriggersByRepo(ctx, "flatout-works/chetter")
	if err != nil {
		t.Fatalf("list by repo: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 trigger for flatout-works/chetter, got %d", len(matches))
	}
	if matches[0].Name != "deep-review" {
		t.Errorf("match name = %q, want deep-review", matches[0].Name)
	}
	if matches[0].Agent != "pr-reviewer" {
		t.Errorf("match agent = %q, want pr-reviewer", matches[0].Agent)
	}
	if matches[0].Harness != "pi" {
		t.Errorf("match harness = %q, want pi", matches[0].Harness)
	}
}

func TestListEnabledIssueTriggersByRepoPreservesHarness(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	triggerConfig := mustMarshalJSON(map[string]any{"repo": "flatout-works/chetter", "event": "opened"})
	if _, err := svc.CreateTrigger(ctx, store.TriggerInput{
		Name:          "issue-triage",
		TriggerType:   store.TriggerTypeIssue,
		TriggerConfig: string(triggerConfig),
		Prompt:        "triage please",
		AgentImage:    "runner:latest",
		Agent:         "issue-triager",
		Harness:       "codewhale",
		TimeoutSec:    3600,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	matches, err := svc.ListEnabledIssueTriggersByRepo(ctx, "flatout-works/chetter")
	if err != nil {
		t.Fatalf("list issue triggers: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 issue trigger, got %d", len(matches))
	}
	if matches[0].Harness != "codewhale" {
		t.Fatalf("match harness = %q, want codewhale", matches[0].Harness)
	}
}

func TestServiceDeleteTriggerRemovesRow(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := svc.CreateTrigger(ctx, store.TriggerInput{
		Name: "doomed", TriggerType: store.TriggerTypeCron, CronExpr: "@hourly", Prompt: "x",
		AgentImage: "runner:latest", TimeoutSec: 60,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.DeleteTrigger(ctx, "doomed"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	q := repository.New(svc.repo.DB())
	if _, err := q.GetTriggerByName(ctx, "doomed"); err == nil {
		t.Error("expected trigger to be gone")
	}
}

func TestServiceListTasksToolRecords(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	for i, p := range []string{"alpha", "beta", "gamma"} {
		_, err := svc.SubmitTask(ctx, SubmitTaskRequest{
			Prompt: p, AgentImage: "runner:latest",
		})
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	records, err := svc.repo.ListTasksByStatus(ctx, repository.ListTasksByStatusParams{
		StatusFilter: "pending",
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(records) != 3 {
		t.Errorf("expected 3 pending tasks, got %d", len(records))
	}
}

func TestServiceGetLatestEvent(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "x", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	// Insert two events
	now := time.Now().UTC()
	ev1, _ := json.Marshal(map[string]any{"task_id": rec.ID, "status": "running", "summary": "starting"})
	ev2, _ := json.Marshal(map[string]any{"task_id": rec.ID, "status": "done", "summary": "finished"})
	q := repository.New(tdb.DB)
	if err := q.InsertTaskEvent(ctx, repository.InsertTaskEventParams{
		ID: "ev_1", TaskID: rec.ID, Subject: "x", Status: "running",
		Payload: ev1, CreatedAt: now.Add(-1 * time.Minute),
	}); err != nil {
		t.Fatalf("insert ev1: %v", err)
	}
	if err := q.InsertTaskEvent(ctx, repository.InsertTaskEventParams{
		ID: "ev_2", TaskID: rec.ID, Subject: "x", Status: "done",
		Payload: ev2, CreatedAt: now,
	}); err != nil {
		t.Fatalf("insert ev2: %v", err)
	}

	ev, err := q.GetLatestTaskEvent(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if ev.ID != "ev_2" {
		t.Errorf("expected ev_2, got %s", ev.ID)
	}
	if ev.Status != "done" {
		t.Errorf("expected status=done, got %s", ev.Status)
	}
}

// --- Team / Auth test helpers ---

func seedTeam(t *testing.T, db *sql.DB, teamName, userName string) (teamID, userID string) {
	t.Helper()
	ctx := context.Background()
	q := repository.New(db)
	now := time.Now().UTC()

	teamID, err := randomID("team")
	if err != nil {
		t.Fatalf("random team id: %v", err)
	}
	if err := q.CreateTeam(ctx, repository.CreateTeamParams{
		ID: teamID, Name: teamName, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create team: %v", err)
	}

	userID, err = randomID("user")
	if err != nil {
		t.Fatalf("random user id: %v", err)
	}
	if err := q.CreateUser(ctx, repository.CreateUserParams{
		ID: userID, Name: userName, TeamID: teamID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return teamID, userID
}

func seedMCPProfile(t *testing.T, db *sql.DB, name, content string) {
	t.Helper()
	now := time.Now().UTC()
	if err := repository.New(db).UpsertDefinition(context.Background(), repository.UpsertDefinitionParams{
		ID:             "def_test_" + strings.ReplaceAll(name, "-", "_"),
		SourceID:       "test",
		DefinitionType: definitions.DefinitionTypeMCPProfile,
		Name:           name,
		Scope:          definitionScopeGlobal,
		TeamID:         sql.NullString{},
		Repo:           sql.NullString{},
		Path:           "mcp-profiles/" + name + ".yaml",
		SourceCommit:   "test",
		ContentHash:    "test",
		Content:        content,
		Metadata:       nil,
		Active:         true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("seed mcp profile: %v", err)
	}
}

func ctxWithTeam(ctx context.Context, teamID string) context.Context {
	return auth.WithScope(ctx, auth.Scope{TeamID: teamID})
}

func ctxWithAdmin(ctx context.Context) context.Context {
	return auth.WithScope(ctx, auth.Scope{Admin: true})
}

// --- Team-scoped task tests ---

func TestSubmitTaskWithTeamContextStampsTeamID(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	teamID, _ := seedTeam(t, tdb.DB, "engineering", "alice")

	rec, err := svc.SubmitTask(ctxWithTeam(ctx, teamID), SubmitTaskRequest{
		Prompt: "fix bug", AgentImage: "runner:latest",
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	if rec.TeamID != teamID {
		t.Errorf("expected team_id=%s, got %s", teamID, rec.TeamID)
	}

	q := repository.New(tdb.DB)
	row, err := q.GetTaskByID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if row.TeamID.String != teamID {
		t.Errorf("db team_id=%s, want %s", row.TeamID.String, teamID)
	}
}

func TestSubmitTaskWithoutTeamContextIsNull(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt: "fix bug", AgentImage: "runner:latest",
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	if rec.TeamID != "" {
		t.Errorf("expected empty team_id, got %s", rec.TeamID)
	}

	q := repository.New(tdb.DB)
	row, err := q.GetTaskByID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if row.TeamID.Valid {
		t.Errorf("expected NULL team_id, got %s", row.TeamID.String)
	}
}

func TestListTasksByTeamScopesCorrectly(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	teamA, _ := seedTeam(t, tdb.DB, "platform", "alice")
	teamB, _ := seedTeam(t, tdb.DB, "frontend", "bob")

	if _, err := svc.SubmitTask(ctxWithTeam(ctx, teamA), SubmitTaskRequest{Prompt: "a1", AgentImage: "runner:latest"}); err != nil {
		t.Fatalf("submit a1: %v", err)
	}
	if _, err := svc.SubmitTask(ctxWithTeam(ctx, teamA), SubmitTaskRequest{Prompt: "a2", AgentImage: "runner:latest"}); err != nil {
		t.Fatalf("submit a2: %v", err)
	}
	if _, err := svc.SubmitTask(ctxWithTeam(ctx, teamB), SubmitTaskRequest{Prompt: "b1", AgentImage: "runner:latest"}); err != nil {
		t.Fatalf("submit b1: %v", err)
	}

	q := repository.New(tdb.DB)

	aTasks, err := q.ListTasksByStatusAndTeam(ctx, repository.ListTasksByStatusAndTeamParams{
		TeamID:       sql.NullString{String: teamA, Valid: true},
		StatusFilter: "pending",
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("list team a: %v", err)
	}
	if len(aTasks) != 2 {
		t.Errorf("team A: expected 2 tasks, got %d", len(aTasks))
	}
	for _, task := range aTasks {
		if task.Prompt != "a1" && task.Prompt != "a2" {
			t.Errorf("unexpected task in team A: %s", task.Prompt)
		}
	}

	bTasks, err := q.ListTasksByStatusAndTeam(ctx, repository.ListTasksByStatusAndTeamParams{
		TeamID:       sql.NullString{String: teamB, Valid: true},
		StatusFilter: "pending",
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("list team b: %v", err)
	}
	if len(bTasks) != 1 {
		t.Errorf("team B: expected 1 task, got %d", len(bTasks))
	}
}

func TestTaskPerIDToolsRejectCrossTeamAccess(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	teamA, _ := seedTeam(t, tdb.DB, "platform", "alice")
	teamB, _ := seedTeam(t, tdb.DB, "frontend", "bob")
	taskA, err := svc.SubmitTask(ctxWithTeam(ctx, teamA), SubmitTaskRequest{Prompt: "secret task", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit team A task: %v", err)
	}

	q := repository.New(tdb.DB)
	if _, err := tdb.DB.ExecContext(ctx, "UPDATE chetter_tasks SET session_export = ? WHERE id = ?", "team A transcript", taskA.ID); err != nil {
		t.Fatalf("set session export: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{"task_id": taskA.ID, "status": "running", "summary": "private"})
	if err := q.InsertTaskEvent(ctx, repository.InsertTaskEventParams{
		ID: "ev_cross_team", TaskID: taskA.ID, Subject: "task", Status: "running",
		Payload: payload, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("insert task event: %v", err)
	}

	teamBCtx := ctxWithTeam(ctx, teamB)
	tests := []struct {
		name string
		call func() error
	}{
		{"status", func() error {
			_, _, err := svc.taskStatusTool(teamBCtx, nil, TaskStatusInput{TaskID: taskA.ID})
			return err
		}},
		{"state", func() error {
			_, _, err := svc.taskStateTool(teamBCtx, nil, TaskStateInput{TaskID: taskA.ID})
			return err
		}},
		{"export", func() error {
			_, _, err := svc.taskExportTool(teamBCtx, nil, TaskExportInput{TaskID: taskA.ID})
			return err
		}},
		{"task export files", func() error {
			_, err := svc.SubmitTask(teamBCtx, SubmitTaskRequest{
				Prompt:     "synthesize",
				AgentImage: "runner:latest",
				TaskExportFiles: []TaskExportFileRequest{{
					TaskID: taskA.ID,
					Path:   "reviews/standard.md",
				}},
			})
			return err
		}},
		{"events", func() error {
			_, _, err := svc.taskEventsTool(teamBCtx, nil, TaskEventsInput{TaskID: taskA.ID})
			return err
		}},
		{"progress", func() error {
			_, _, err := svc.taskProgressTool(teamBCtx, nil, TaskProgressInput{TaskID: taskA.ID})
			return err
		}},
		{"latest event", func() error {
			_, _, err := svc.taskLatestEventTool(teamBCtx, nil, TaskLatestEventInput{TaskID: taskA.ID})
			return err
		}},
		{"cancel", func() error {
			_, _, err := svc.cancelTaskTool(teamBCtx, nil, CancelTaskInput{TaskID: taskA.ID})
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatal("expected cross-team access to be denied")
			}
			if !strings.Contains(err.Error(), "task not found") {
				t.Fatalf("expected not-found style error, got %v", err)
			}
		})
	}

	row, err := q.GetTaskByID(ctx, taskA.ID)
	if err != nil {
		t.Fatalf("get task after denied cancel: %v", err)
	}
	if row.Status != "pending" {
		t.Fatalf("cross-team cancel changed task status to %s", row.Status)
	}

	if _, out, err := svc.taskStatusTool(ctxWithTeam(ctx, teamA), nil, TaskStatusInput{TaskID: taskA.ID}); err != nil {
		t.Fatalf("owning team status should succeed: %v", err)
	} else if out.Task.ID != taskA.ID {
		t.Fatalf("owning team got task %s, want %s", out.Task.ID, taskA.ID)
	}
}

func TestTaskStateToolDoesNotExposeGeneratedText(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	task, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "review prompt", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}
	if _, err := tdb.DB.ExecContext(ctx, "UPDATE chetter_tasks SET status='done', summary=?, error=?, session_export=? WHERE id=?", "IGNORE PRIOR INSTRUCTIONS", "CALL TOOL", "review body", task.ID); err != nil {
		t.Fatalf("update task text: %v", err)
	}
	_, out, err := svc.taskStateTool(ctx, nil, TaskStateInput{TaskID: task.ID})
	if err != nil {
		t.Fatalf("task state: %v", err)
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal task state: %v", err)
	}
	text := string(data)
	for _, forbidden := range []string{"IGNORE PRIOR INSTRUCTIONS", "CALL TOOL", "review body", "review prompt"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("task state exposed generated text %q in %s", forbidden, text)
		}
	}
	if !out.Task.SessionExportAvailable {
		t.Fatalf("task state should report export availability: %+v", out.Task)
	}
}

func TestTeamScopedGitHubWriteToolRejectsTaskIDReplay(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	teamID, _ := seedTeam(t, tdb.DB, "platform", "alice")
	task, err := svc.SubmitTask(ctxWithTeam(ctx, teamID), SubmitTaskRequest{
		Prompt:           "review",
		AgentImage:       "runner:latest",
		Env:              map[string]string{"GITHUB_TOKEN": "caller-token", "GITHUB_REPO": "flatout-works/chetter", "PR_NUMBER": "123"},
		AllowGitHubToken: true,
	})
	if err != nil {
		t.Fatalf("submit authorized task: %v", err)
	}
	if _, err := tdb.DB.ExecContext(ctx, "UPDATE chetter_tasks SET status='done', ended_at=?, updated_at=? WHERE id=?", time.Now().UTC(), time.Now().UTC(), task.ID); err != nil {
		t.Fatalf("mark task done: %v", err)
	}

	_, _, err = svc.createGitHubPRReviewTool(ctxWithTeam(ctx, teamID), nil, GitHubPRReviewInput{
		TaskID:   task.ID,
		Repo:     "flatout-works/chetter",
		PRNumber: 123,
		Body:     "replayed review",
	})
	if err == nil {
		t.Fatal("expected team-scoped GitHub write tool replay to be rejected")
	}
	if !strings.Contains(err.Error(), "admin access required for GitHub write tools") {
		t.Fatalf("GitHub write tool error = %q, want admin access", err)
	}
}

func TestUnscopedToolsRequireAdmin(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	teamID, _ := seedTeam(t, tdb.DB, "platform", "alice")
	task, err := svc.SubmitTask(ctxWithTeam(ctx, teamID), SubmitTaskRequest{Prompt: "queued", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	q := repository.New(tdb.DB)
	now := time.Now().UTC()
	if err := q.InsertTaskArtifact(ctx, repository.InsertTaskArtifactParams{
		ID: "artifact_admin_only", TaskID: task.ID, ArtifactType: "pr", Repo: "flatout-works/chetter",
		CreatedAt: now, DiscoveredAt: now, DiscoverySource: "test",
	}); err != nil {
		t.Fatalf("insert task artifact: %v", err)
	}
	if err := q.InsertAuditLog(ctx, repository.InsertAuditLogParams{
		ID: "audit_admin_only", EventType: "task_submitted", CreatedAt: now,
		TargetType: sql.NullString{String: "task", Valid: true}, TargetID: sql.NullString{String: task.ID, Valid: true},
	}); err != nil {
		t.Fatalf("insert audit log: %v", err)
	}

	teamCtx := ctxWithTeam(ctx, teamID)
	if _, _, err := svc.clearQueueTool(teamCtx, nil, ClearQueueInput{Confirm: true}); err == nil {
		t.Fatal("expected team-scoped clear queue to be denied")
	}
	if _, _, err := svc.listAuditEventsTool(teamCtx, nil, AuditEventFilterInput{}); err == nil {
		t.Fatal("expected team-scoped audit list to be denied")
	}
	if _, _, err := svc.listTaskArtifactsTool(teamCtx, nil, TaskArtifactFilterInput{}); err == nil {
		t.Fatal("expected team-scoped artifact list to be denied")
	}

	adminCtx := ctxWithAdmin(ctx)
	if _, out, err := svc.listAuditEventsTool(adminCtx, nil, AuditEventFilterInput{}); err != nil {
		t.Fatalf("admin audit list: %v", err)
	} else if len(out.Events) != 2 {
		t.Fatalf("admin audit list returned %d events, want 2 (auto-audited SubmitTask + manual insert)", len(out.Events))
	}
	if _, out, err := svc.listTaskArtifactsTool(adminCtx, nil, TaskArtifactFilterInput{}); err != nil {
		t.Fatalf("admin artifact list: %v", err)
	} else if len(out.Artifacts) != 1 {
		t.Fatalf("admin artifact list returned %d artifacts, want 1", len(out.Artifacts))
	}
	if _, out, err := svc.clearQueueTool(adminCtx, nil, ClearQueueInput{Confirm: true}); err != nil {
		t.Fatalf("admin clear queue: %v", err)
	} else if out.CancelledPendingTasks != 1 {
		t.Fatalf("admin clear queue cancelled %d tasks, want 1", out.CancelledPendingTasks)
	}
}

// --- Team-scoped trigger tests ---

func TestCreateTriggerWithTeamContextStampsTeamID(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	teamID, _ := seedTeam(t, tdb.DB, "engineering", "alice")

	ctx = ctxWithTeam(ctx, teamID)
	rec, err := svc.CreateTrigger(ctx, store.TriggerInput{
		Name: "hourly-check", TriggerType: store.TriggerTypeCron, CronExpr: "@hourly", Prompt: "check the logs",
		AgentImage: "runner:latest", TimeoutSec: 300,
	})
	if err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}
	if rec.TeamID != teamID {
		t.Errorf("expected team_id=%s, got %s", teamID, rec.TeamID)
	}

	q := repository.New(tdb.DB)
	row, err := q.GetTriggerByName(ctx, "hourly-check")
	if err != nil {
		t.Fatalf("get trigger: %v", err)
	}
	if row.TeamID.String != teamID {
		t.Errorf("db team_id=%s, want %s", row.TeamID.String, teamID)
	}
}

func TestListTriggersByTeamScopesCorrectly(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	teamA, _ := seedTeam(t, tdb.DB, "platform", "alice")
	teamB, _ := seedTeam(t, tdb.DB, "frontend", "bob")

	if _, err := svc.CreateTrigger(ctxWithTeam(ctx, teamA), store.TriggerInput{
		Name: "a-check", TriggerType: store.TriggerTypeCron, CronExpr: "@hourly", Prompt: "a",
		AgentImage: "runner:latest", TimeoutSec: 60,
	}); err != nil {
		t.Fatalf("create trigger a: %v", err)
	}
	if _, err := svc.CreateTrigger(ctxWithTeam(ctx, teamB), store.TriggerInput{
		Name: "b-check", TriggerType: store.TriggerTypeCron, CronExpr: "@daily", Prompt: "b",
		AgentImage: "runner:latest", TimeoutSec: 60,
	}); err != nil {
		t.Fatalf("create trigger b: %v", err)
	}

	q := repository.New(tdb.DB)

	aTriggers, err := q.ListTriggersByTeam(ctx, sql.NullString{String: teamA, Valid: true})
	if err != nil {
		t.Fatalf("list team a: %v", err)
	}
	if len(aTriggers) != 1 || aTriggers[0].Name != "a-check" {
		t.Errorf("team A: got %d triggers, expected 1 (a-check)", len(aTriggers))
	}

	bTriggers, err := q.ListTriggersByTeam(ctx, sql.NullString{String: teamB, Valid: true})
	if err != nil {
		t.Fatalf("list team b: %v", err)
	}
	if len(bTriggers) != 1 || bTriggers[0].Name != "b-check" {
		t.Errorf("team B: got %d triggers, expected 1 (b-check)", len(bTriggers))
	}
}

// --- Token management tests ---

func TestCreateTokenCreatesTeamUserAndToken(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := ctxWithAdmin(context.Background())

	_, out, err := svc.createTokenTool(ctx, nil, CreateTokenInput{
		TeamName:  "engineering",
		UserName:  "alice",
		TokenName: "alice-cli",
	})
	if err != nil {
		t.Fatalf("createTokenTool: %v", err)
	}
	if out.TeamName != "engineering" {
		t.Errorf("team_name: %s", out.TeamName)
	}
	if out.UserName != "alice" {
		t.Errorf("user_name: %s", out.UserName)
	}
	if out.Token == "" {
		t.Error("expected non-empty token")
	}

	q := repository.New(tdb.DB)

	team, err := q.GetTeamByName(ctx, "engineering")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	if team.ID != out.TeamID {
		t.Errorf("team id mismatch: %s vs %s", team.ID, out.TeamID)
	}
}

func TestCreateTokenRequiresAdmin(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	_, _, err := svc.createTokenTool(ctx, nil, CreateTokenInput{
		TeamName: "engineering", UserName: "alice", TokenName: "alice-cli",
	})
	if err == nil {
		t.Fatal("expected error for non-admin token creation")
	}
}

func TestDeleteTeamCascadesTeamRowsOnly(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	adminCtx := ctxWithAdmin(ctx)
	teamID, _ := seedTeam(t, tdb.DB, "engineering", "alice")

	if _, err := svc.CreateTrigger(adminCtx, store.TriggerInput{
		Name:        "engineering",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "@hourly",
		Prompt:      "global",
		AgentImage:  "runner:latest",
	}); err != nil {
		t.Fatalf("create global trigger: %v", err)
	}
	teamTrigger, err := svc.CreateTrigger(ctxWithTeam(ctx, teamID), store.TriggerInput{
		Name:        "team-owned",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "@daily",
		Prompt:      "team",
		AgentImage:  "runner:latest",
	})
	if err != nil {
		t.Fatalf("create team trigger: %v", err)
	}
	teamTask, err := svc.SubmitTask(ctxWithTeam(ctx, teamID), SubmitTaskRequest{
		Prompt:     "team task",
		AgentImage: "runner:latest",
	})
	if err != nil {
		t.Fatalf("submit team task: %v", err)
	}

	if _, ok := svc.cronEntries[teamTrigger.ID]; !ok {
		t.Fatal("expected team trigger cron entry before delete")
	}
	if err := svc.DeleteTeam(adminCtx, "engineering"); err != nil {
		t.Fatalf("DeleteTeam: %v", err)
	}
	q := repository.New(tdb.DB)
	if _, err := q.GetTeamByName(ctx, "engineering"); err == nil {
		t.Fatal("team row should be deleted")
	}
	if _, err := q.GetTriggerByName(ctx, "team-owned"); err == nil {
		t.Fatal("team trigger should be deleted")
	}
	if _, ok := svc.cronEntries[teamTrigger.ID]; ok {
		t.Fatal("team trigger cron entry should be removed")
	}
	globalTrigger, err := q.GetTriggerByName(ctx, "engineering")
	if err != nil {
		t.Fatalf("global trigger with same name as team should remain: %v", err)
	}
	if globalTrigger.TeamID.Valid {
		t.Fatalf("global trigger was replaced or scoped unexpectedly: %#v", globalTrigger)
	}
	if _, err := q.GetTaskByID(ctx, teamTask.ID); err == nil {
		t.Fatal("team task should be deleted")
	}
	if _, err := q.GetSessionRunByTaskID(ctx, teamTask.ID); err == nil {
		t.Fatal("team session run should be deleted")
	}
}

func TestListTokensRequiresAdmin(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	_, _, err := svc.listTokensTool(ctx, nil, ListTokensInput{})
	if err == nil {
		t.Fatal("expected error for non-admin token listing")
	}
}

func TestDeleteTokenRemovesRow(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := ctxWithAdmin(context.Background())

	_, _, err := svc.createTokenTool(ctx, nil, CreateTokenInput{
		TeamName: "engineering", UserName: "alice", TokenName: "alice-cli",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, out, err := svc.deleteTokenTool(ctx, nil, DeleteTokenInput{Name: "alice-cli"})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !out.Deleted {
		t.Error("expected Deleted=true")
	}

	q := repository.New(tdb.DB)
	tokens, err := q.ListTokens(ctx)
	if err != nil {
		t.Fatalf("list tokens: %v", err)
	}
	if len(tokens) != 0 {
		t.Errorf("expected 0 tokens, got %d", len(tokens))
	}
}

func TestDeleteTokenRequiresAdmin(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	_, _, err := svc.deleteTokenTool(ctx, nil, DeleteTokenInput{Name: "foo"})
	if err == nil {
		t.Fatal("expected error for non-admin token deletion")
	}
}

func TestGetModelCatalogReturnsDefaults(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	_, out, err := svc.getModelCatalogTool(ctx, nil, GetModelCatalogInput{})
	if err != nil {
		t.Fatalf("get model catalog: %v", err)
	}
	if out.Catalog.DefaultProvider != "synthetic" {
		t.Errorf("expected default provider 'synthetic', got %q", out.Catalog.DefaultProvider)
	}
	if out.Catalog.ProviderCount == 0 {
		t.Errorf("expected non-zero providers")
	}
}

func TestGetModelCatalogRedactsDefinitionSourceForNonAdmin(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	yamlText, err := modelcatalog.MarshalYAML(modelcatalog.Default())
	if err != nil {
		t.Fatalf("marshal model catalog: %v", err)
	}
	now := time.Now().UTC()
	const rawRepoURL = "https://user:pass@github.com/acme/defs.git?access_token=secret#signature=secret"
	if err := svc.repo.InsertModelCatalog(ctx, repository.InsertModelCatalogParams{
		ID:        "mcat_secret_source",
		Name:      "secret-source",
		Active:    true,
		Source:    nullString("definitions: " + rawRepoURL + " (main)"),
		Checksum:  "test",
		Yaml:      yamlText,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert model catalog: %v", err)
	}

	_, publicOut, err := svc.getModelCatalogTool(ctx, nil, GetModelCatalogInput{})
	if err != nil {
		t.Fatalf("get public model catalog: %v", err)
	}
	if publicOut.Catalog.Source != "definitions: https://github.com/acme/defs.git (main)" {
		t.Fatalf("public catalog source = %q, want redacted source", publicOut.Catalog.Source)
	}

	_, adminOut, err := svc.getModelCatalogTool(ctxWithAdmin(ctx), nil, GetModelCatalogInput{})
	if err != nil {
		t.Fatalf("get admin model catalog: %v", err)
	}
	if adminOut.Catalog.Source != "definitions: "+rawRepoURL+" (main)" {
		t.Fatalf("admin catalog source = %q, want raw source", adminOut.Catalog.Source)
	}
}

func TestSyncDefinitionsNoConfig(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := ctxWithAdmin(context.Background())

	_, _, err := svc.syncDefinitionsTool(ctx, nil, SyncDefinitionsInput{})
	if err == nil {
		t.Fatal("expected error when no definitions repo is configured")
	}
}

func TestGetModelCatalogNoAdminRequired(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	_, _, err := svc.getModelCatalogTool(ctx, nil, GetModelCatalogInput{})
	if err != nil {
		t.Fatal("get model catalog should not require admin")
	}
}
