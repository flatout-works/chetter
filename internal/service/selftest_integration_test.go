package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/repository"
)

func TestRunSelfTestQuickPersistsTrustedTaskMetadata(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := ctxWithAdmin(context.Background())

	run, err := svc.RunSelfTest(ctx, "quick")
	if err != nil {
		t.Fatalf("RunSelfTest: %v", err)
	}
	if run.Profile != "quick" || run.Status != "pending" || len(run.Checks) != 1 {
		t.Fatalf("unexpected run: %+v", run)
	}
	tasks, err := svc.repo.ListTasksBySelfTestRun(ctx, nullString(run.ID))
	if err != nil {
		t.Fatalf("ListTasksBySelfTestRun: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}
	taskRow := tasks[0]
	if taskRow.SubmissionSource != selfTestSubmissionSource || taskRow.SelfTestProfile.String != "quick" || taskRow.SelfTestCheck.String != "quick" || taskRow.SelfTestNonce.String == "" {
		t.Fatalf("unexpected task metadata: %+v", taskRow)
	}
	if !strings.Contains(taskRow.Prompt, taskRow.SelfTestNonce.String) || !strings.Contains(taskRow.Prompt, selfTestToolName) {
		t.Fatalf("self-test prompt does not identify tool and nonce: %q", taskRow.Prompt)
	}

	status, err := svc.GetSelfTestStatus(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetSelfTestStatus: %v", err)
	}
	if status.Status != "pending" || status.Checks[0].Evidence {
		t.Fatalf("unexpected initial status: %+v", status)
	}
}

func TestGetSelfTestStatusRequiresRunnerEvidence(t *testing.T) {
	tests := []struct {
		name     string
		evidence bool
		want     string
	}{
		{name: "matching evidence passes", evidence: true, want: "passed"},
		{name: "missing evidence fails", evidence: false, want: "failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, tdb, cleanup := newServiceForTest(t)
			defer cleanup()
			ctx := ctxWithAdmin(context.Background())
			run, err := svc.RunSelfTest(ctx, "quick")
			if err != nil {
				t.Fatalf("RunSelfTest: %v", err)
			}
			tasks, err := svc.repo.ListTasksBySelfTestRun(ctx, nullString(run.ID))
			if err != nil || len(tasks) != 1 {
				t.Fatalf("self-test tasks = %v, %v", tasks, err)
			}
			taskRow := tasks[0]
			if tt.evidence {
				payload, _ := json.Marshal(map[string]any{"kind": selfTestEvidenceKind, "tool": selfTestToolName, "nonce": taskRow.SelfTestNonce.String, "check": taskRow.SelfTestCheck.String, "observed": true})
				if err := svc.repo.InsertTaskEvent(ctx, repository.InsertTaskEventParams{
					ID: "evt_selftest", TaskID: taskRow.ID, AgentSessionID: sql.NullString{}, UserPromptID: sql.NullString{}, ExecutionAttemptID: sql.NullString{},
					Subject: "runner.test", Status: "running", EventType: "task.progress", Payload: payload, CreatedAt: time.Now().UTC(),
				}); err != nil {
					t.Fatalf("InsertTaskEvent: %v", err)
				}
			}
			if _, err := tdb.DB.Exec(testQuery(tdb.Dialect(), "UPDATE chetter_tasks SET status='done' WHERE id=?", "UPDATE chetter_tasks SET status='done' WHERE id=$1"), taskRow.ID); err != nil {
				t.Fatalf("complete task: %v", err)
			}
			status, err := svc.GetSelfTestStatus(ctx, run.ID)
			if err != nil {
				t.Fatalf("GetSelfTestStatus: %v", err)
			}
			if status.Status != tt.want || status.Checks[0].Status != tt.want || status.Checks[0].Evidence != tt.evidence {
				t.Fatalf("status = %+v, want %s evidence=%v", status, tt.want, tt.evidence)
			}
		})
	}
}

func TestRunSelfTestProfilesAndAuthorization(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	if _, err := svc.RunSelfTest(context.Background(), "quick"); err == nil || err.Error() != "admin access required" {
		t.Fatalf("non-admin error = %v", err)
	}
	if _, err := svc.RunSelfTest(ctxWithAdmin(context.Background()), "unknown"); err == nil || !strings.Contains(err.Error(), "invalid self-test profile") {
		t.Fatalf("invalid profile error = %v", err)
	}

	harnesses, err := svc.selfTestSpecs(context.Background(), "harnesses")
	if err != nil || len(harnesses) != 5 {
		t.Fatalf("harness profile = %v, %v", harnesses, err)
	}
	for _, spec := range harnesses {
		if spec.providerID == "" || spec.modelID == "" {
			t.Fatalf("harness check must pin a provider/model: %+v", spec)
		}
	}
	providers, err := svc.selfTestSpecs(context.Background(), "providers")
	if err != nil || len(providers) == 0 {
		t.Fatalf("provider profile = %v, %v", providers, err)
	}
	full, err := svc.selfTestSpecs(context.Background(), "full")
	if err != nil || len(full) != len(harnesses)+len(providers) {
		t.Fatalf("full profile checks = %d, want %d: %v", len(full), len(harnesses)+len(providers), err)
	}
	svc.cfg.SelfTestGitHubRepo = "flatout-works/chetter-diagnostics"
	full, err = svc.selfTestSpecs(context.Background(), "full")
	if err != nil || len(full) != len(harnesses)+len(providers)+1 {
		t.Fatalf("full profile with GitHub check = %d: %v", len(full), err)
	}
	github := full[len(full)-1]
	if github.name != "github:credentials" || github.githubRepo != svc.cfg.SelfTestGitHubRepo || github.gitURL != "https://github.com/flatout-works/chetter-diagnostics.git" {
		t.Fatalf("GitHub self-test spec = %+v", github)
	}
}

func TestTaskToProtoCarriesTrustedSelfTestMetadata(t *testing.T) {
	proto := taskToProto(
		repository.ChetterTask{ID: "task_1", SelfTestNonce: nullString("nonce_1"), SelfTestCheck: nullString("harness:codex")},
		repository.ChetterAgentSession{ID: "sess_1", ResumeMode: "none", Skills: json.RawMessage("[]"), Env: json.RawMessage("{}")},
		repository.ChetterExecutionAttempt{ID: "exec_1", ClaimID: "claim_1"},
		1, "", "",
	)
	if proto.SelfTestNonce != "nonce_1" || proto.SelfTestCheck != "harness:codex" {
		t.Fatalf("self-test proto metadata = nonce %q check %q", proto.SelfTestNonce, proto.SelfTestCheck)
	}
}

func TestSelfTestAggregateWaitsForAllChecksToFinish(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := ctxWithAdmin(context.Background())
	run, err := svc.RunSelfTest(ctx, "harnesses")
	if err != nil {
		t.Fatalf("RunSelfTest: %v", err)
	}
	tasks, err := svc.repo.ListTasksBySelfTestRun(ctx, nullString(run.ID))
	if err != nil || len(tasks) < 2 {
		t.Fatalf("self-test tasks = %d, %v", len(tasks), err)
	}
	update := testQuery(tdb.Dialect(), "UPDATE chetter_tasks SET status=? WHERE id=?", "UPDATE chetter_tasks SET status=$1 WHERE id=$2")
	if _, err := tdb.DB.Exec(update, "error", tasks[0].ID); err != nil {
		t.Fatalf("fail first check: %v", err)
	}
	status, err := svc.GetSelfTestStatus(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetSelfTestStatus: %v", err)
	}
	if status.Status != "running" {
		t.Fatalf("mixed terminal/pending aggregate = %q, want running", status.Status)
	}
	for _, taskRow := range tasks[1:] {
		if _, err := tdb.DB.Exec(update, "cancelled", taskRow.ID); err != nil {
			t.Fatalf("cancel check %s: %v", taskRow.ID, err)
		}
	}
	status, err = svc.GetSelfTestStatus(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetSelfTestStatus terminal: %v", err)
	}
	if status.Status != "failed" {
		t.Fatalf("terminal aggregate = %q, want failed", status.Status)
	}
}
