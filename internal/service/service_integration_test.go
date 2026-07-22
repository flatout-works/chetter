package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/config"
	"github.com/flatout-works/chetter/internal/data"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/store"
	"github.com/flatout-works/chetter/internal/testdb"
)

var svcTestDB *testdb.PackageDB

func TestMain(m *testing.M) {
	svcTestDB = testdb.StartPackageDB(m)
	code := m.Run()
	if svcTestDB != nil {
		svcTestDB.Close()
	}
	os.Exit(code)
}

func newServiceForTest(t *testing.T) (*Service, *testdb.TestDB, func()) {
	t.Helper()
	if svcTestDB == nil {
		t.Skip("database unavailable; skipping integration test")
	}
	tdb, cleanup := svcTestDB.NewTestDB(t)
	tdb.Truncate(t)
	cfg := config.Config{
		DefaultAgentImage:     "runner:latest",
		DefaultTaskTimeoutSec: 600,
	}
	st, err := store.Open(tdb.DSN, tdb.Dialect())
	if err != nil {
		cleanup()
		t.Fatalf("store.Open: %v", err)
	}
	now := time.Now().UTC()
	if _, err := tdb.DB.Exec(testQuery(tdb.Dialect(),
		`INSERT INTO git_identities (id, team_id, name, git_author_name, git_author_email, credential_type, is_default, created_at, updated_at) VALUES (?, '', 'primary-bot', 'Primary Bot', 'primary-bot@example.com', 'github_app', true, ?, ?)`,
		`INSERT INTO git_identities (id, team_id, name, git_author_name, git_author_email, credential_type, is_default, created_at, updated_at) VALUES ($1, '', 'primary-bot', 'Primary Bot', 'primary-bot@example.com', 'github_app', true, $2, $3)`), "gid_primary", now, now); err != nil {
		cleanup()
		t.Fatalf("seed default Git identity: %v", err)
	}
	svc := New(cfg, st)
	return svc, tdb, func() {
		_ = st.Close()
		cleanup()
	}
}

func TestPostgresRepositoryUsesNativeQueries(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	if tdb.Dialect() != store.DialectPostgres {
		t.Skip("PostgreSQL-specific repository selection")
	}
	if _, ok := svc.repo.(*data.Queries); !ok {
		t.Fatalf("PostgreSQL repository = %T, want *data.Queries", svc.repo)
	}

	ctx := context.Background()
	_, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "native PostgreSQL search", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	// SearchTasks uses PostgreSQL ILIKE; this succeeds only through repositorypostgres.
	rows, err := svc.repo.SearchTasks(ctx, repository.SearchTasksParams{
		TeamFilter:        nullString(""),
		StatusFilter:      "",
		TriggerNameFilter: nullString(""),
		Search:            "POSTGRESQL",
		Limit:             10,
	})
	if err != nil {
		t.Fatalf("SearchTasks: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("SearchTasks returned %d rows, want 1", len(rows))
	}
}

func TestGetTaskProgressPaginatesRawEvents(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	task, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "paginate progress", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	base := time.Now().UTC().Add(-time.Minute)
	for i := range 5 {
		payload, err := json.Marshal(store.TaskResponse{Summary: fmt.Sprintf("progress %d", i)})
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		if err := svc.repo.InsertTaskEvent(ctx, repository.InsertTaskEventParams{
			ID:        fmt.Sprintf("evt_page_%d", i),
			TaskID:    task.ID,
			Subject:   "test.progress",
			Status:    "running",
			EventType: "task.progress",
			Payload:   payload,
			CreatedAt: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("InsertTaskEvent %d: %v", i, err)
		}
	}

	first, err := svc.GetTaskProgress(ctx, task.ID, 2, 0)
	if err != nil {
		t.Fatalf("GetTaskProgress first page: %v", err)
	}
	if !first.HasMore || first.NextOffset != 2 {
		t.Fatalf("first page metadata = has_more %v next_offset %d", first.HasMore, first.NextOffset)
	}
	if len(first.Entries) != 2 || first.Entries[0].Summary != "progress 3" || first.Entries[1].Summary != "progress 4" {
		t.Fatalf("first page entries = %+v", first.Entries)
	}

	second, err := svc.GetTaskProgress(ctx, task.ID, 2, first.NextOffset)
	if err != nil {
		t.Fatalf("GetTaskProgress second page: %v", err)
	}
	if !second.HasMore || second.NextOffset != 4 {
		t.Fatalf("second page metadata = has_more %v next_offset %d", second.HasMore, second.NextOffset)
	}
	if len(second.Entries) != 2 || second.Entries[0].Summary != "progress 1" || second.Entries[1].Summary != "progress 2" {
		t.Fatalf("second page entries = %+v", second.Entries)
	}

	last, err := svc.GetTaskProgress(ctx, task.ID, 2, second.NextOffset)
	if err != nil {
		t.Fatalf("GetTaskProgress last page: %v", err)
	}
	if last.HasMore || last.NextOffset != 5 {
		t.Fatalf("last page metadata = has_more %v next_offset %d", last.HasMore, last.NextOffset)
	}
	if len(last.Entries) != 1 || last.Entries[0].Summary != "progress 0" {
		t.Fatalf("last page entries = %+v", last.Entries)
	}
}

func TestReapExpiredLeasesRecordsReclaimEvent(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	task, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "reclaim me", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	now := time.Now().UTC()
	executionID := "exec_reclaim"
	if rows, err := svc.repo.MarkTaskClaimed(ctx, repository.MarkTaskClaimedParams{
		RunnerID:       sql.NullString{String: "runner_reclaim", Valid: true},
		ExecutionID:    executionID,
		ClaimedAt:      sql.NullTime{Time: now.Add(-time.Minute), Valid: true},
		LeaseExpiresAt: sql.NullTime{Time: now.Add(-time.Second), Valid: true},
		StartedAt:      sql.NullTime{Time: now.Add(-time.Minute), Valid: true},
		UpdatedAt:      now,
		LastEventAt:    sql.NullTime{Time: now, Valid: true},
		ID:             task.ID,
	}); err != nil {
		t.Fatalf("MarkTaskClaimed: %v", err)
	} else if rows != 1 {
		t.Fatalf("MarkTaskClaimed rows = %d", rows)
	}
	prompt, err := svc.repo.GetUserPromptByTaskID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetUserPromptByTaskID: %v", err)
	}
	if err := svc.repo.InsertExecutionAttempt(ctx, repository.InsertExecutionAttemptParams{
		ID:             executionID,
		UserPromptID:   prompt.ID,
		Sequence:       1,
		RunnerID:       nullString("runner_reclaim"),
		ClaimedAt:      sql.NullTime{Time: now.Add(-time.Minute), Valid: true},
		LeaseExpiresAt: sql.NullTime{Time: now.Add(-time.Second), Valid: true},
		StartedAt:      sql.NullTime{Time: now.Add(-time.Minute), Valid: true},
		CreatedAt:      now.Add(-time.Minute),
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("InsertExecutionAttempt: %v", err)
	}

	svc.reapExpiredLeases()

	reclaimed, err := svc.repo.GetTaskByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if reclaimed.Status != "pending" {
		t.Fatalf("task status = %q, want pending", reclaimed.Status)
	}
	attempt, err := svc.repo.GetExecutionAttemptByID(ctx, executionID)
	if err != nil {
		t.Fatalf("GetExecutionAttemptByID: %v", err)
	}
	if attempt.Status != "lost" || !attempt.EndedAt.Valid {
		t.Fatalf("attempt status/ended_at = %s/%v, want lost/set", attempt.Status, attempt.EndedAt.Valid)
	}
	events, err := svc.repo.ListTaskEvents(ctx, repository.ListTaskEventsParams{TaskID: task.ID, Limit: 10})
	if err != nil {
		t.Fatalf("ListTaskEvents: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "task.reclaimed" {
		t.Fatalf("reclaim events = %+v", events)
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal reclaim event: %v", err)
	}
	if payload["attempt"] != float64(1) || payload["runner_id"] != "runner_reclaim" {
		t.Fatalf("reclaim payload = %+v", payload)
	}
}

func TestSubmitTaskUsesDefaultGitIdentity(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	record, err := svc.SubmitTask(context.Background(), SubmitTaskRequest{Prompt: "agent-less task", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}
	if record.CommitAuthorName != "Primary Bot" || record.CommitAuthorEmail != "primary-bot@example.com" || record.GitIdentityID == "" {
		t.Fatalf("default task identity = %+v", record)
	}
}

func TestSetDefaultGitIdentityOverridesGlobalDefault(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	identity, err := svc.CreateGitIdentity(ctx, GitIdentityInput{Name: "release-bot", GitAuthorName: "Release Bot", GitAuthorEmail: "release-bot@example.com"})
	if err != nil {
		t.Fatalf("create Git identity: %v", err)
	}
	if _, err := svc.SetDefaultGitIdentity(ctx, "", "", identity.Name); err != nil {
		t.Fatalf("set default Git identity: %v", err)
	}
	record, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "agent-less task", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}
	if record.GitIdentityID != identity.ID || record.CommitAuthorName != "Release Bot" {
		t.Fatalf("default task identity = %+v", record)
	}
}

func TestSubmitTaskResolvesAgentGitIdentity(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	identity, err := svc.CreateGitIdentity(ctx, GitIdentityInput{
		Name:           "release-bot",
		GitAuthorName:  "Release Bot",
		GitAuthorEmail: "release-bot@example.com",
	})
	if err != nil {
		t.Fatalf("create Git identity: %v", err)
	}
	now := time.Now().UTC()
	if _, err := tdb.DB.Exec(testQuery(tdb.Dialect(),
		`INSERT INTO definitions (id, source_id, definition_type, name, scope, path, source_commit, content_hash, content, active, created_at, updated_at) VALUES (?, ?, 'agent', 'release', 'global', ?, ?, ?, ?, true, ?, ?)`,
		`INSERT INTO definitions (id, source_id, definition_type, name, scope, path, source_commit, content_hash, content, active, created_at, updated_at) VALUES ($1, $2, 'agent', 'release', 'global', $3, $4, $5, $6, true, $7, $8)`,
	), "def_release", "src_test", "agents/release.md", "test", strings.Repeat("1", 64), "---\nidentity: release-bot\n---\n# Release agent\n", now, now); err != nil {
		t.Fatalf("seed agent definition: %v", err)
	}
	record, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "prepare release", AgentImage: "runner:latest", Agent: "release"})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}
	if record.GitIdentityID != identity.ID || record.CommitAuthorName != "Release Bot" || record.CommitAuthorEmail != "release-bot@example.com" {
		t.Fatalf("task identity = %+v, want %q / Release Bot", record, identity.ID)
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
	q := data.New(tdb.DB, tdb.Dialect())
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
	run, err := q.GetUserPromptByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get user prompt: %v", err)
	}
	if run.Status != "pending" {
		t.Errorf("user prompt status: %s", run.Status)
	}
	if run.TaskID != rec.ID {
		t.Errorf("user prompt task_id: %s", run.TaskID)
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

func TestRunnerTerminalEventCompletesUserPrompt(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "x", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	rpc := NewRunnerRPCService(data.New(tdb.DB, tdb.Dialect()), tdb.DB, tdb.Dialect())
	claim, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{RunnerId: "runner_1", WaitSeconds: 1}))
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	endedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := rpc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_1",
		Events: []*runnerv1.TaskEvent{{
			TaskId:            rec.ID,
			ExecutionId:       claim.Msg.Task.ExecutionId,
			Status:            "done",
			Summary:           "finished",
			OpencodeSessionId: "opencode-session-1",
			SessionExport:     "export",
			EndedAt:           endedAt,
		}},
	})); err != nil {
		t.Fatalf("report terminal event: %v", err)
	}

	q := data.New(tdb.DB, tdb.Dialect())
	run, err := q.GetUserPromptByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get user prompt: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("user prompt status = %s, want completed", run.Status)
	}
	if run.Summary.String != "finished" {
		t.Fatalf("user prompt summary = %q", run.Summary.String)
	}
	attempt, err := q.GetExecutionAttemptByID(ctx, claim.Msg.Task.ExecutionId)
	if err != nil {
		t.Fatalf("get execution attempt: %v", err)
	}
	if attempt.UserPromptID != run.ID || attempt.Sequence != 1 || attempt.Status != "succeeded" {
		t.Fatalf("execution attempt = %+v, want prompt %s sequence 1 succeeded", attempt, run.ID)
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

	q := data.New(tdb.DB, tdb.Dialect())
	task, err := q.GetTaskByID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if !task.CheckpointAfterSuccess {
		t.Fatal("expected checkpoint_after_success=true for resumable session")
	}

	run, err := q.GetUserPromptByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get user prompt: %v", err)
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
	if session.TaskID != rec.ID || session.Sequence != 1 {
		t.Fatalf("session ownership = %s/%d, want %s/1", session.TaskID, session.Sequence, rec.ID)
	}
	if run.Sequence != 1 {
		t.Fatalf("initial prompt sequence = %d, want 1", run.Sequence)
	}
	if session.PauseReason.String != "waiting_for_pr_feedback" {
		t.Fatalf("pause_reason = %s, want waiting_for_pr_feedback", session.PauseReason.String)
	}

	rpc := NewRunnerRPCService(data.New(tdb.DB, tdb.Dialect()), tdb.DB, tdb.Dialect())
	claim, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{RunnerId: "runner_1", WaitSeconds: 1}))
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	endedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := rpc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_1",
		Events: []*runnerv1.TaskEvent{{
			TaskId:            rec.ID,
			ExecutionId:       claim.Msg.Task.ExecutionId,
			Status:            "done",
			Summary:           "created PR",
			EndedAt:           endedAt,
			OpencodeSessionId: "oc_session_123",
			WorkspacePath:     "/var/lib/runner/" + rec.ID + "/workspace",
		}},
	})); err != nil {
		t.Fatalf("report terminal event: %v", err)
	}

	run, err = q.GetUserPromptByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get user prompt: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("user prompt status = %s, want completed", run.Status)
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

	q := data.New(tdb.DB, tdb.Dialect())
	rpc := NewRunnerRPCService(data.New(tdb.DB, tdb.Dialect()), tdb.DB, tdb.Dialect())

	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:      "create a PR",
		AgentImage:  "runner:latest",
		SessionMode: "resumable",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	run, err := q.GetUserPromptByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get user prompt: %v", err)
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
			ExecutionId:       claimResp.Msg.Task.ExecutionId,
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
	if resumeOut.Task.ID != rec.ID {
		t.Fatalf("resume task ID = %s, want stable task ID %s", resumeOut.Task.ID, rec.ID)
	}
	if resumeOut.Prompt.Sequence != 2 {
		t.Fatalf("resume prompt sequence = %d, want 2", resumeOut.Prompt.Sequence)
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
	if resumeClaim.Msg.Task.Prompt != "address feedback" {
		t.Fatalf("resume prompt = %q, want address feedback", resumeClaim.Msg.Task.Prompt)
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
			ExecutionId:       resumeClaim.Msg.Task.ExecutionId,
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
		rec3Claim, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
			RunnerId: "runner_1", WaitSeconds: 1,
		}))
		if err != nil {
			t.Fatalf("claim recoverable task: %v", err)
		}
		if _, err := rpc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
			RunnerId: "runner_1",
			Events: []*runnerv1.TaskEvent{{
				TaskId:            rec3.ID,
				ExecutionId:       rec3Claim.Msg.Task.ExecutionId,
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

		run3, err := q.GetUserPromptByTaskID(ctx, rec3.ID)
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
				ExecutionId:       claim3.Msg.Task.ExecutionId,
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
		rec2Claim, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
			RunnerId: "runner_1", WaitSeconds: 1,
		}))
		if err != nil {
			t.Fatalf("claim second task: %v", err)
		}
		if _, err := rpc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
			RunnerId: "runner_1",
			Events: []*runnerv1.TaskEvent{{
				TaskId:            rec2.ID,
				ExecutionId:       rec2Claim.Msg.Task.ExecutionId,
				Status:            "done",
				EndedAt:           time.Now().UTC().Format(time.RFC3339Nano),
				OpencodeSessionId: "oc_sid_xyz",
				WorkspacePath:     "/var/lib/runner/" + rec2.ID + "/workspace",
			}},
		})); err != nil {
			t.Fatalf("report second terminal event: %v", err)
		}

		run2, _ := q.GetUserPromptByTaskID(ctx, rec2.ID)
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

func TestReaperFailsResumeWhenPinnedRunnerDisappears(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	q := data.New(tdb.DB, tdb.Dialect())
	rpc := NewRunnerRPCService(data.New(tdb.DB, tdb.Dialect()), tdb.DB, tdb.Dialect())

	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:      "create a PR",
		AgentImage:  "runner:latest",
		SessionMode: "resumable",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	claim, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
		RunnerId: "runner_gone", WaitSeconds: 0,
	}))
	if err != nil {
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
			ExecutionId:       claim.Msg.Task.ExecutionId,
			Status:            "done",
			EndedAt:           now.Format(time.RFC3339Nano),
			OpencodeSessionId: "oc_sid_gone",
			WorkspacePath:     "/var/lib/runner/" + rec.ID + "/workspace",
		}},
	})); err != nil {
		t.Fatalf("report terminal event: %v", err)
	}

	run, err := q.GetUserPromptByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get user prompt: %v", err)
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
	resumeRun, err := q.GetUserPromptByTaskID(ctx, resumeOut.Task.ID)
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
	q := data.New(tdb.DB, tdb.Dialect())
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

	q := data.New(tdb.DB, tdb.Dialect())
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

	q := data.New(tdb.DB, tdb.Dialect())
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

	row, err := data.New(tdb.DB, tdb.Dialect()).GetTaskByID(ctx, task.ID)
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

	q := svc.repo
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
	q := svc.repo
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
	q := data.New(tdb.DB, tdb.Dialect())
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
	q := data.New(db, store.ParseDialect(os.Getenv("CHETTER_TEST_DB_DIALECT")))
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

func ctxWithTeam(ctx context.Context, teamID string) context.Context {
	return auth.WithScope(ctx, auth.Scope{TeamID: teamID})
}

func ctxWithTeams(ctx context.Context, teamIDs ...string) context.Context {
	return auth.WithScope(ctx, auth.Scope{TeamIDs: teamIDs})
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

	q := data.New(tdb.DB, tdb.Dialect())
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

	q := data.New(tdb.DB, tdb.Dialect())
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

	q := data.New(tdb.DB, tdb.Dialect())

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

func TestMultiTeamTokenListsUnionAndRequiresSubmitOwner(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	teamA, _ := seedTeam(t, tdb.DB, "platform", "alice")
	teamB, _ := seedTeam(t, tdb.DB, "frontend", "bob")
	teamC, _ := seedTeam(t, tdb.DB, "security", "carol")
	if _, err := svc.SubmitTask(ctxWithTeam(ctx, teamA), SubmitTaskRequest{Prompt: "a1", AgentImage: "runner:latest"}); err != nil {
		t.Fatalf("submit a1: %v", err)
	}
	if _, err := svc.SubmitTask(ctxWithTeam(ctx, teamB), SubmitTaskRequest{Prompt: "b1", AgentImage: "runner:latest"}); err != nil {
		t.Fatalf("submit b1: %v", err)
	}
	if _, err := svc.SubmitTask(ctxWithTeam(ctx, teamC), SubmitTaskRequest{Prompt: "c1", AgentImage: "runner:latest"}); err != nil {
		t.Fatalf("submit c1: %v", err)
	}

	multiCtx := ctxWithTeams(ctx, teamA, teamB)
	if _, err := svc.SubmitTask(multiCtx, SubmitTaskRequest{Prompt: "missing owner", AgentImage: "runner:latest"}); err == nil {
		t.Fatal("expected multi-team submit without owner to fail")
	}
	rec, err := svc.SubmitTask(multiCtx, SubmitTaskRequest{TeamID: teamB, Prompt: "owned", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit with owner: %v", err)
	}
	if rec.TeamID != teamB {
		t.Fatalf("owned task team_id = %s, want %s", rec.TeamID, teamB)
	}
	if _, err := svc.SubmitTask(multiCtx, SubmitTaskRequest{TeamID: teamC, Prompt: "wrong owner", AgentImage: "runner:latest"}); err == nil {
		t.Fatal("expected submit for out-of-scope team to fail")
	}

	tasks, err := svc.ListTasks(multiCtx, "", 20, 0, "", nil, nil)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	seen := map[string]bool{}
	for _, task := range tasks {
		seen[task.Prompt] = true
		if task.TeamID == teamC {
			t.Fatalf("multi-team list leaked team C task: %#v", task)
		}
	}
	for _, prompt := range []string{"a1", "b1", "owned"} {
		if !seen[prompt] {
			t.Fatalf("multi-team list missing %q: %#v", prompt, tasks)
		}
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

	q := data.New(tdb.DB, tdb.Dialect())
	if _, err := tdb.DB.ExecContext(ctx, testQuery(tdb.Dialect(), "UPDATE chetter_tasks SET session_export = ? WHERE id = ?", "UPDATE chetter_tasks SET session_export = $1 WHERE id = $2"), "team A transcript", taskA.ID); err != nil {
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
		{"export", func() error {
			_, _, err := svc.taskExportTool(teamBCtx, nil, TaskExportInput{TaskID: taskA.ID})
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

func TestUnscopedToolsRequireAdmin(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	teamID, _ := seedTeam(t, tdb.DB, "platform", "alice")
	task, err := svc.SubmitTask(ctxWithTeam(ctx, teamID), SubmitTaskRequest{Prompt: "queued", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	q := data.New(tdb.DB, tdb.Dialect())
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

	q := data.New(tdb.DB, tdb.Dialect())
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

	q := data.New(tdb.DB, tdb.Dialect())

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

	q := data.New(tdb.DB, tdb.Dialect())

	team, err := q.GetTeamByName(ctx, "engineering")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	if team.ID != out.TeamID {
		t.Errorf("team id mismatch: %s vs %s", team.ID, out.TeamID)
	}
}

func TestCreateTokenWithMultipleTeams(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := ctxWithAdmin(context.Background())

	_, out, err := svc.createTokenTool(ctx, nil, CreateTokenInput{
		TeamNames: []string{"engineering", "platform", "engineering"},
		UserName:  "alice",
		TokenName: "alice-cli",
	})
	if err != nil {
		t.Fatalf("createTokenTool: %v", err)
	}
	if len(out.TeamNames) != 2 || out.TeamNames[0] != "engineering" || out.TeamNames[1] != "platform" {
		t.Fatalf("unexpected team names: %#v", out.TeamNames)
	}
	if len(out.TeamIDs) != 2 {
		t.Fatalf("expected two team ids, got %#v", out.TeamIDs)
	}

	q := data.New(tdb.DB, tdb.Dialect())
	tokens, err := q.ListTokens(ctx)
	if err != nil {
		t.Fatalf("list tokens: %v", err)
	}
	if len(tokens) != 1 || tokens[0].TeamNames != "engineering,platform" {
		t.Fatalf("unexpected token rows: %#v", tokens)
	}
	_, listed, err := svc.listTokensTool(ctx, nil, ListTokensInput{})
	if err != nil {
		t.Fatalf("listTokensTool: %v", err)
	}
	if len(listed.Tokens) != 1 || len(listed.Tokens[0].TeamNames) != 2 {
		t.Fatalf("unexpected token tool output: %#v", listed)
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

	q := data.New(tdb.DB, tdb.Dialect())
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

// --- Usage Summary Tests ---

func TestUsageSummaryGroupsByTeamTriggerRepo(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	teamA, _ := seedTeam(t, tdb.DB, "platform", "alice")
	teamB, _ := seedTeam(t, tdb.DB, "frontend", "bob")

	// Insert tasks with known token/cost values.
	now := time.Now().UTC()
	insertTask(t, tdb.DB, "task_a1", teamA, "trigger-x", "cron",
		"https://github.com/owner/repo-a.git", 100, 50, 0, 0, 0, 10, now)
	insertTask(t, tdb.DB, "task_a2", teamA, "trigger-x", "cron",
		"https://github.com/owner/repo-a.git", 200, 100, 0, 0, 0, 20, now)
	insertTask(t, tdb.DB, "task_a3", teamA, "trigger-y", "pr_review",
		"https://github.com/owner/repo-b.git", 300, 150, 50, 25, 0, 30, now)
	insertTask(t, tdb.DB, "task_b1", teamB, "trigger-z", "issue",
		"https://github.com/other/repo-c.git", 50, 25, 0, 0, 0, 5, now)

	// Admin sees all teams.
	out, err := svc.GetUsageSummary(ctxWithAdmin(ctx), UsageSummaryInput{})
	if err != nil {
		t.Fatalf("GetUsageSummary: %v", err)
	}
	if len(out.Summary) != 3 {
		t.Fatalf("expected 3 groups, got %d: %+v", len(out.Summary), out.Summary)
	}

	// Verify sums.
	var totalCost int64
	var totalTasks int64
	for _, row := range out.Summary {
		totalCost += row.CostCents
		totalTasks += row.TaskCount
	}
	if totalCost != 65 {
		t.Errorf("total cost: want 65, got %d", totalCost)
	}
	if totalTasks != 4 {
		t.Errorf("total tasks: want 4, got %d", totalTasks)
	}

	// Team-scoped: platform team only.
	outB, err := svc.GetUsageSummary(ctxWithTeam(ctx, teamA), UsageSummaryInput{})
	if err != nil {
		t.Fatalf("GetUsageSummary scoped: %v", err)
	}
	for _, row := range outB.Summary {
		if row.TeamID != teamA {
			t.Errorf("scoped result has wrong team: %s", row.TeamID)
		}
	}
	if len(outB.Summary) != 2 {
		t.Errorf("platform team: expected 2 groups, got %d", len(outB.Summary))
	}
}

func TestUsageSummaryDateFiltering(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	teamID, _ := seedTeam(t, tdb.DB, "infra", "carol")

	oldTime := time.Now().UTC().Add(-48 * time.Hour)
	recentTime := time.Now().UTC().Add(-1 * time.Hour)

	insertTask(t, tdb.DB, "old_1", teamID, "nightly", "cron",
		"https://github.com/owner/repo.git", 500, 250, 0, 0, 0, 50, oldTime)
	insertTask(t, tdb.DB, "recent_1", teamID, "nightly", "cron",
		"https://github.com/owner/repo.git", 100, 50, 0, 0, 0, 10, recentTime)

	// Last 24 hours — only recent task.
	out, err := svc.GetUsageSummary(ctxWithAdmin(ctx), UsageSummaryInput{SinceHours: 24})
	if err != nil {
		t.Fatalf("GetUsageSummary: %v", err)
	}
	if len(out.Summary) != 1 {
		t.Fatalf("expected 1 group with 24h filter, got %d", len(out.Summary))
	}
	row := out.Summary[0]
	if row.TaskCount != 1 {
		t.Errorf("expected 1 task in 24h window, got %d", row.TaskCount)
	}
	if row.CostCents != 10 {
		t.Errorf("expected 10 cost cents in 24h, got %d", row.CostCents)
	}

	// No filter (defaults to 30 days) — both tasks.
	out2, err := svc.GetUsageSummary(ctxWithAdmin(ctx), UsageSummaryInput{})
	if err != nil {
		t.Fatalf("GetUsageSummary default: %v", err)
	}
	row2 := out2.Summary[0]
	if row2.TaskCount != 2 {
		t.Errorf("expected 2 tasks with default window, got %d", row2.TaskCount)
	}
	if row2.CostCents != 60 {
		t.Errorf("expected 60 cost cents, got %d", row2.CostCents)
	}
}

func TestUsageSummaryTeamScopingDeniesOtherTeams(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	teamA, _ := seedTeam(t, tdb.DB, "backend", "dave")
	teamB, _ := seedTeam(t, tdb.DB, "mobile", "eve")

	now := time.Now().UTC()
	insertTask(t, tdb.DB, "ta1", teamA, "build", "cron",
		"https://github.com/a/repo.git", 100, 50, 0, 0, 0, 10, now)
	insertTask(t, tdb.DB, "tb1", teamB, "deploy", "cron",
		"https://github.com/b/repo.git", 200, 100, 0, 0, 0, 20, now)

	// Team A token should only see team A.
	out, err := svc.GetUsageSummary(ctxWithTeam(ctx, teamA), UsageSummaryInput{})
	if err != nil {
		t.Fatalf("GetUsageSummary: %v", err)
	}
	if len(out.Summary) != 1 {
		t.Fatalf("team A: expected 1 group, got %d", len(out.Summary))
	}
	if out.Summary[0].TeamID != teamA {
		t.Errorf("team A sees wrong team: %s", out.Summary[0].TeamID)
	}
	if out.Summary[0].CostCents != 10 {
		t.Errorf("expected 10 cents for team A, got %d", out.Summary[0].CostCents)
	}

	// Admin sees both.
	out2, err := svc.GetUsageSummary(ctxWithAdmin(ctx), UsageSummaryInput{})
	if err != nil {
		t.Fatalf("GetUsageSummary admin: %v", err)
	}
	if len(out2.Summary) != 2 {
		t.Fatalf("admin: expected 2 groups, got %d", len(out2.Summary))
	}
}

func TestUsageSummaryRepoFiltering(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	teamID, _ := seedTeam(t, tdb.DB, "ops", "frank")
	now := time.Now().UTC()

	insertTask(t, tdb.DB, "tr1", teamID, "lint", "cron",
		"https://github.com/flatout-works/chetter.git", 100, 50, 0, 0, 0, 10, now)
	insertTask(t, tdb.DB, "tr2", teamID, "test", "cron",
		"https://github.com/other-org/other-repo.git", 200, 100, 0, 0, 0, 20, now)

	out, err := svc.GetUsageSummary(ctxWithAdmin(ctx), UsageSummaryInput{Repo: "flatout-works/chetter"})
	if err != nil {
		t.Fatalf("GetUsageSummary with repo filter: %v", err)
	}
	if len(out.Summary) != 1 {
		t.Fatalf("repo filter: expected 1 group, got %d", len(out.Summary))
	}
	row := out.Summary[0]
	if row.Repo != "flatout-works/chetter" {
		t.Errorf("expected repo flatout-works/chetter, got %s", row.Repo)
	}
	if row.CostCents != 10 {
		t.Errorf("expected 10 cents, got %d", row.CostCents)
	}
}

func TestUsageSummaryTriggerFiltering(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	teamID, _ := seedTeam(t, tdb.DB, "qa", "grace")
	now := time.Now().UTC()

	insertTask(t, tdb.DB, "tq1", teamID, "nightly-build", "cron",
		"https://github.com/owner/repo.git", 100, 50, 0, 0, 0, 10, now)
	insertTask(t, tdb.DB, "tq2", teamID, "pr-review", "pr_review",
		"https://github.com/owner/repo.git", 200, 100, 0, 0, 0, 20, now)

	// Filter by trigger type.
	out, err := svc.GetUsageSummary(ctxWithAdmin(ctx), UsageSummaryInput{TriggerType: "cron"})
	if err != nil {
		t.Fatalf("GetUsageSummary by trigger type: %v", err)
	}
	if len(out.Summary) != 1 {
		t.Fatalf("trigger type cron: expected 1 group, got %d", len(out.Summary))
	}
	if out.Summary[0].TriggerType != "cron" {
		t.Errorf("expected cron, got %s", out.Summary[0].TriggerType)
	}

	// Filter by trigger name.
	out2, err := svc.GetUsageSummary(ctxWithAdmin(ctx), UsageSummaryInput{TriggerName: "pr-review"})
	if err != nil {
		t.Fatalf("GetUsageSummary by trigger name: %v", err)
	}
	if len(out2.Summary) != 1 {
		t.Fatalf("trigger name pr-review: expected 1 group, got %d", len(out2.Summary))
	}
	if out2.Summary[0].TriggerName != "pr-review" {
		t.Errorf("expected pr-review, got %s", out2.Summary[0].TriggerName)
	}
}

func TestUsageSummaryMCPToolReturnsValidOutput(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	teamID, _ := seedTeam(t, tdb.DB, "sec", "heidi")
	now := time.Now().UTC()
	insertTask(t, tdb.DB, "tm1", teamID, "audit", "cron",
		"https://github.com/owner/repo.git", 50, 25, 10, 5, 5, 8, now)

	_, out, err := svc.usageSummaryTool(ctxWithAdmin(ctx), nil, UsageSummaryInput{})
	if err != nil {
		t.Fatalf("usageSummaryTool: %v", err)
	}
	if len(out.Summary) != 1 {
		t.Fatalf("expected 1 row, got %d", len(out.Summary))
	}
	row := out.Summary[0]
	if row.TotalTokens != 95 { // 50+25+10+5+5
		t.Errorf("total tokens: want 95, got %d", row.TotalTokens)
	}
	validateGeneratedOutputSchema(t, out)
}

// insertTask creates a task row directly for usage summary testing.
func insertTask(t *testing.T, db *sql.DB, id, teamID, triggerName, triggerType, gitURL string,
	inputTokens, outputTokens, cacheRead, cacheWrite, reasoning int64,
	costCents int64, createdAt time.Time) {
	t.Helper()

	// Build a short random prompt for uniqueness.
	prompt := "test prompt " + id
	query := `
		INSERT INTO chetter_tasks (
			id, team_id, status, prompt, git_url, trigger_name, trigger_type,
			total_input_tokens, total_output_tokens, total_cache_read_tokens,
			total_cache_write_tokens, total_reasoning_tokens, cost_cents,
			skills, env, timeout_sec, created_at, updated_at
		) VALUES (?, ?, 'done', ?, ?, ?, ?,
			?, ?, ?,
			?, ?, ?,
			'[]', '{}', 600, ?, ?)`
	if store.ParseDialect(os.Getenv("CHETTER_TEST_DB_DIALECT")) == store.DialectPostgres {
		query = `
			INSERT INTO chetter_tasks (
				id, team_id, status, prompt, git_url, trigger_name, trigger_type,
				total_input_tokens, total_output_tokens, total_cache_read_tokens,
				total_cache_write_tokens, total_reasoning_tokens, cost_cents,
				skills, env, timeout_sec, created_at, updated_at
			) VALUES ($1, $2, 'done', $3, $4, $5, $6,
				$7, $8, $9,
				$10, $11, $12,
				'[]', '{}', 600, $13, $14)`
	}
	_, err := db.Exec(query,
		id, teamID, prompt, gitURL, triggerName, triggerType,
		inputTokens, outputTokens, cacheRead,
		cacheWrite, reasoning, costCents,
		createdAt, createdAt,
	)
	if err != nil {
		t.Fatalf("insertTask %s: %v", id, err)
	}
}

func testQuery(dialect store.Dialect, mysql, postgres string) string {
	if dialect == store.DialectPostgres {
		return postgres
	}
	return mysql
}
