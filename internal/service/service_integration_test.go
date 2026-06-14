package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/config"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/store"
	"github.com/flatout-works/chetter/internal/testdb"
)

func newServiceForTest(t *testing.T) (*Service, *testdb.TestDB, func()) {
	t.Helper()
	tdb, cleanup := testdb.NewForTesting(t)
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

func TestServiceCreateSchedulePersistsAndActivates(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()

	rec, err := svc.CreateSchedule(ctx, store.ScheduleInput{
		Name:       "hourly-check",
		CronExpr:   "@hourly",
		Prompt:     "check the logs",
		AgentImage: "runner:latest",
		TimeoutSec: 300,
	})
	if err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	if rec.Name != "hourly-check" {
		t.Errorf("name: %s", rec.Name)
	}
	if !rec.Enabled {
		t.Error("new schedule should be enabled")
	}
	if rec.NextRunAt == nil {
		t.Error("next_run_at should be set after activation")
	}

	q := repository.New(tdb.DB)
	row, err := q.GetScheduleByName(ctx, "hourly-check")
	if err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	if row.Prompt != "check the logs" {
		t.Errorf("prompt: %s", row.Prompt)
	}
}

func TestServiceCreateScheduleRejectsInvalidCron(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	_, err := svc.CreateSchedule(context.Background(), store.ScheduleInput{
		Name:       "bad",
		CronExpr:   "not a cron",
		Prompt:     "x",
		AgentImage: "runner:latest",
		TimeoutSec: 60,
	})
	if err == nil {
		t.Fatal("expected error for invalid cron")
	}
}

func TestServiceCreateScheduleRequiresPrompt(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	_, err := svc.CreateSchedule(context.Background(), store.ScheduleInput{
		Name:       "no-prompt",
		CronExpr:   "@hourly",
		AgentImage: "runner:latest",
		TimeoutSec: 60,
	})
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}
}

func TestServiceListSchedulesReturnsEnabled(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := svc.CreateSchedule(ctx, store.ScheduleInput{
		Name: "enabled", CronExpr: "@hourly", Prompt: "x",
		AgentImage: "runner:latest", TimeoutSec: 60,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.CreateSchedule(ctx, store.ScheduleInput{
		Name: "disabled", CronExpr: "@daily", Prompt: "y",
		AgentImage: "runner:latest", TimeoutSec: 60,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.UpdateSchedule(ctx, "disabled", store.ScheduleInput{
		Name: "disabled", CronExpr: "@daily", Prompt: "y",
		AgentImage: "runner:latest", TimeoutSec: 60,
	}, false); err != nil {
		t.Fatalf("update: %v", err)
	}

	q := repository.New(svc.repo.DB())
	enabled, err := q.ListEnabledSchedules(ctx)
	if err != nil {
		t.Fatalf("list enabled: %v", err)
	}
	if len(enabled) != 1 || enabled[0].Name != "enabled" {
		t.Errorf("expected only 'enabled' in list, got %+v", enabled)
	}
}

func TestServiceDeleteScheduleRemovesRow(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := svc.CreateSchedule(ctx, store.ScheduleInput{
		Name: "doomed", CronExpr: "@hourly", Prompt: "x",
		AgentImage: "runner:latest", TimeoutSec: 60,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.DeleteSchedule(ctx, "doomed"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	q := repository.New(svc.repo.DB())
	if _, err := q.GetScheduleByName(ctx, "doomed"); err == nil {
		t.Error("expected schedule to be gone")
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

// --- Team-scoped schedule tests ---

func TestCreateScheduleWithTeamContextStampsTeamID(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	teamID, _ := seedTeam(t, tdb.DB, "engineering", "alice")

	ctx = ctxWithTeam(ctx, teamID)
	rec, err := svc.CreateSchedule(ctx, store.ScheduleInput{
		Name: "hourly-check", CronExpr: "@hourly", Prompt: "check the logs",
		AgentImage: "runner:latest", TimeoutSec: 300,
	})
	if err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	if rec.TeamID != teamID {
		t.Errorf("expected team_id=%s, got %s", teamID, rec.TeamID)
	}

	q := repository.New(tdb.DB)
	row, err := q.GetScheduleByName(ctx, "hourly-check")
	if err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	if row.TeamID.String != teamID {
		t.Errorf("db team_id=%s, want %s", row.TeamID.String, teamID)
	}
}

func TestListSchedulesByTeamScopesCorrectly(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	teamA, _ := seedTeam(t, tdb.DB, "platform", "alice")
	teamB, _ := seedTeam(t, tdb.DB, "frontend", "bob")

	if _, err := svc.CreateSchedule(ctxWithTeam(ctx, teamA), store.ScheduleInput{
		Name: "a-check", CronExpr: "@hourly", Prompt: "a",
		AgentImage: "runner:latest", TimeoutSec: 60,
	}); err != nil {
		t.Fatalf("create schedule a: %v", err)
	}
	if _, err := svc.CreateSchedule(ctxWithTeam(ctx, teamB), store.ScheduleInput{
		Name: "b-check", CronExpr: "@daily", Prompt: "b",
		AgentImage: "runner:latest", TimeoutSec: 60,
	}); err != nil {
		t.Fatalf("create schedule b: %v", err)
	}

	q := repository.New(tdb.DB)

	aSchedules, err := q.ListSchedulesByTeam(ctx, sql.NullString{String: teamA, Valid: true})
	if err != nil {
		t.Fatalf("list team a: %v", err)
	}
	if len(aSchedules) != 1 || aSchedules[0].Name != "a-check" {
		t.Errorf("team A: got %d schedules, expected 1 (a-check)", len(aSchedules))
	}

	bSchedules, err := q.ListSchedulesByTeam(ctx, sql.NullString{String: teamB, Valid: true})
	if err != nil {
		t.Fatalf("list team b: %v", err)
	}
	if len(bSchedules) != 1 || bSchedules[0].Name != "b-check" {
		t.Errorf("team B: got %d schedules, expected 1 (b-check)", len(bSchedules))
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
