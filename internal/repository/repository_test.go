package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/testdb"
)

var repoTestDB *testdb.PackageDB

func TestMain(m *testing.M) {
	repoTestDB = testdb.StartPackageDB(m)
	code := m.Run()
	repoTestDB.Close()
	os.Exit(code)
}

func newRepo(t *testing.T) (*Queries, func()) {
	t.Helper()
	tdb, cleanup := repoTestDB.NewTestDB(t)
	q := New(tdb.DB)
	return q, func() { tdb.Truncate(t); cleanup() }
}

func TestTeamsCRUD(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	err := q.CreateTeam(ctx, CreateTeamParams{
		ID: "team-1", Name: "platform", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}

	got, err := q.GetTeamByID(ctx, "team-1")
	if err != nil {
		t.Fatalf("GetTeamByID: %v", err)
	}
	if got.ID != "team-1" || got.Name != "platform" {
		t.Errorf("GetTeamByID = %+v", got)
	}

	got, err = q.GetTeamByName(ctx, "platform")
	if err != nil {
		t.Fatalf("GetTeamByName: %v", err)
	}
	if got.ID != "team-1" {
		t.Errorf("GetTeamByName = %+v", got)
	}
}

func TestUsersCRUD(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	if err := q.CreateTeam(ctx, CreateTeamParams{ID: "t1", Name: "team1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if err := q.CreateUser(ctx, CreateUserParams{ID: "u1", Name: "alice", TeamID: "t1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	got, err := q.GetUserByID(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.Name != "alice" || got.TeamID != "t1" {
		t.Errorf("GetUserByID = %+v", got)
	}
}

func TestTokensCRUD(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	if err := q.CreateTeam(ctx, CreateTeamParams{ID: "t1", Name: "team1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if err := q.CreateUser(ctx, CreateUserParams{ID: "u1", Name: "alice", TeamID: "t1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := q.CreateToken(ctx, CreateTokenParams{
		ID: "tok1", Name: "alice-cli", TokenHash: "abc123", UserID: "u1",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	hashRow, err := q.GetTokenByHash(ctx, "abc123")
	if err != nil {
		t.Fatalf("GetTokenByHash: %v", err)
	}
	if hashRow.Name != "alice-cli" || hashRow.TeamName != "team1" {
		t.Errorf("GetTokenByHash = %+v", hashRow)
	}

	list, err := q.ListTokens(ctx)
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(list) != 1 || list[0].Name != "alice-cli" {
		t.Errorf("ListTokens = %+v", list)
	}

	if err := q.DeleteToken(ctx, "alice-cli"); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}
	list, _ = q.ListTokens(ctx)
	if len(list) != 0 {
		t.Errorf("expected 0 tokens after delete, got %d", len(list))
	}
}

func TestTaskLifecycle(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	start := now.Add(-time.Hour)
	if err := q.InsertTask(ctx, InsertTaskParams{
		ID: "task-1", Prompt: "hello", TeamID: sql.NullString{},
		Skills: json.RawMessage("null"), Env: json.RawMessage("null"),
		TimeoutSec: 600, CreatedAt: start, UpdatedAt: start,
	}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	got, err := q.GetTaskByID(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != "pending" || got.Prompt != "hello" {
		t.Errorf("GetTaskByID = %+v", got)
	}

	claimable, err := q.GetClaimableTaskForUpdate(ctx, sql.NullString{String: "runner-1", Valid: true})
	if err != nil {
		t.Fatalf("GetClaimableTaskForUpdate: %v", err)
	}
	if claimable.ID != "task-1" {
		t.Errorf("got claimable task %q, want task-1", claimable.ID)
	}

	n, err := q.MarkTaskClaimed(ctx, MarkTaskClaimedParams{
		ID: "task-1", RunnerID: sql.NullString{String: "runner-1", Valid: true},
		ClaimedAt:      sql.NullTime{Time: now, Valid: true},
		LeaseExpiresAt: sql.NullTime{Time: now.Add(30 * time.Second), Valid: true},
		StartedAt:      sql.NullTime{Time: now, Valid: true},
		UpdatedAt:      now, LastEventAt: sql.NullTime{Time: now, Valid: true},
	})
	if err != nil {
		t.Fatalf("MarkTaskClaimed: %v", err)
	}
	if n != 1 {
		t.Errorf("MarkTaskClaimed affected %d rows, want 1", n)
	}

	got, _ = q.GetTaskByID(ctx, "task-1")
	if got.Status != "running" || got.RunnerID.String != "runner-1" {
		t.Errorf("task should be running, got %+v", got)
	}

	n, err = q.CancelTask(ctx, CancelTaskParams{
		ID: "task-1", Error: sql.NullString{String: "cancelled", Valid: true},
		EndedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	if n != 1 {
		t.Errorf("CancelTask affected %d rows, want 1", n)
	}

	got, _ = q.GetTaskByID(ctx, "task-1")
	if got.Status != "cancelled" {
		t.Errorf("task status = %q, want cancelled", got.Status)
	}
}

func TestListTasksByStatus(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)

	null := sql.NullString{}
	nullJSON := json.RawMessage("null")
	for _, id := range []string{"t1", "t2", "t3"} {
		if err := q.InsertTask(ctx, InsertTaskParams{
			ID: id, Prompt: id, TeamID: null,
			Skills: nullJSON, Env: nullJSON, TimeoutSec: 300,
			CreatedAt: start, UpdatedAt: start,
		}); err != nil {
			t.Fatalf("InsertTask(%s): %v", id, err)
		}
	}

	all, err := q.ListTasksByStatus(ctx, ListTasksByStatusParams{StatusFilter: "", Limit: 100})
	if err != nil {
		t.Fatalf("ListTasksByStatus: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 tasks, got %d", len(all))
	}

	pending, err := q.ListTasksByStatus(ctx, ListTasksByStatusParams{StatusFilter: "pending", Limit: 100})
	if err != nil {
		t.Fatalf("ListTasksByStatus(pending): %v", err)
	}
	if len(pending) != 3 {
		t.Errorf("expected 3 pending tasks, got %d", len(pending))
	}
}

func TestRenewAndReclaimLeases(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	start := now.Add(-time.Hour)
	null := sql.NullString{}
	nullJSON := json.RawMessage("null")
	if err := q.InsertTask(ctx, InsertTaskParams{
		ID: "task-1", Prompt: "p1", TeamID: null,
		Skills: nullJSON, Env: nullJSON, TimeoutSec: 300,
		CreatedAt: start, UpdatedAt: start,
	}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	_, _ = q.MarkTaskClaimed(ctx, MarkTaskClaimedParams{
		ID: "task-1", RunnerID: sql.NullString{String: "r1", Valid: true},
		ClaimedAt:      sql.NullTime{Time: now, Valid: true},
		LeaseExpiresAt: sql.NullTime{Time: now.Add(30 * time.Second), Valid: true},
		StartedAt:      sql.NullTime{Time: now, Valid: true},
		UpdatedAt:      now, LastEventAt: sql.NullTime{Time: now, Valid: true},
	})

	n, err := q.RenewTaskLease(ctx, RenewTaskLeaseParams{
		ID: "task-1", RunnerID: sql.NullString{String: "r1", Valid: true},
		LeaseExpiresAt: sql.NullTime{Time: now.Add(60 * time.Second), Valid: true},
		UpdatedAt:      now, LastEventAt: sql.NullTime{Time: now, Valid: true},
	})
	if err != nil {
		t.Fatalf("RenewTaskLease: %v", err)
	}
	if n != 1 {
		t.Errorf("RenewTaskLease affected %d rows, want 1", n)
	}

	got, _ := q.GetTaskByID(ctx, "task-1")
	if got.LeaseExpiresAt.Time != now.Add(60*time.Second).Truncate(time.Second) {
		t.Errorf("lease not renewed, got %v", got.LeaseExpiresAt.Time)
	}

	n, err = q.ReclaimExpiredLeases(ctx, ReclaimExpiredLeasesParams{
		UpdatedAt: now, LeaseExpiresAt: sql.NullTime{Time: now.Add(30 * time.Second), Valid: true},
	})
	if err != nil {
		t.Fatalf("ReclaimExpiredLeases: %v", err)
	}
	if n != 0 {
		t.Errorf("ReclaimExpiredLeases affected %d rows (lease still valid), want 0", n)
	}

	n, err = q.ReclaimExpiredLeases(ctx, ReclaimExpiredLeasesParams{
		UpdatedAt: now, LeaseExpiresAt: sql.NullTime{Time: now.Add(90 * time.Second), Valid: true},
	})
	if err != nil {
		t.Fatalf("ReclaimExpiredLeases: %v", err)
	}
	if n != 1 {
		t.Errorf("ReclaimExpiredLeases affected %d rows (lease expired), want 1", n)
	}

	got, _ = q.GetTaskByID(ctx, "task-1")
	if got.Status != "pending" {
		t.Errorf("task status after reclaim = %q, want pending", got.Status)
	}
}

func TestClearPendingTasks(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	now := time.Now().UTC().Truncate(time.Second)

	null := sql.NullString{}
	nullJSON := json.RawMessage("null")
	if err := q.InsertTask(ctx, InsertTaskParams{
		ID: "task-1", Prompt: "p1", TeamID: null,
		Skills: nullJSON, Env: nullJSON, TimeoutSec: 300,
		CreatedAt: start, UpdatedAt: start,
	}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	n, err := q.ClearPendingTasks(ctx, ClearPendingTasksParams{
		Error:   sql.NullString{String: "cleared", Valid: true},
		EndedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("ClearPendingTasks: %v", err)
	}
	if n != 1 {
		t.Errorf("ClearPendingTasks affected %d rows, want 1", n)
	}

	got, _ := q.GetTaskByID(ctx, "task-1")
	if got.Status != "cancelled" {
		t.Errorf("task status = %q, want cancelled", got.Status)
	}
}

func TestTaskEvents(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	null := sql.NullString{}
	nullJSON := json.RawMessage("null")
	if err := q.InsertTask(ctx, InsertTaskParams{
		ID: "task-1", Prompt: "p1", TeamID: null,
		Skills: nullJSON, Env: nullJSON, TimeoutSec: 300,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	payload := json.RawMessage(`{"msg":"hello"}`)
	if err := q.InsertTaskEvent(ctx, InsertTaskEventParams{
		ID: "evt-1", TaskID: "task-1", Subject: "started", Status: "running",
		Payload: payload, CreatedAt: now,
	}); err != nil {
		t.Fatalf("InsertTaskEvent: %v", err)
	}

	list, err := q.ListTaskEvents(ctx, ListTaskEventsParams{TaskID: "task-1", Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListTaskEvents: %v", err)
	}
	if len(list) != 1 || list[0].Subject != "started" {
		t.Errorf("ListTaskEvents = %+v", list)
	}

	latest, err := q.GetLatestTaskEvent(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetLatestTaskEvent: %v", err)
	}
	if latest.ID != "evt-1" {
		t.Errorf("GetLatestTaskEvent = %+v", latest)
	}
}

func TestRunnerHeartbeat(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	meta := json.RawMessage(`{"os":"linux"}`)
	err := q.UpsertRunnerHeartbeat(ctx, UpsertRunnerHeartbeatParams{
		ID: "runner-1", Status: "active", MaxConcurrent: 5,
		RunningTasks: 2, AvailableSlots: 3,
		TotalStarted: 10, TotalCompleted: 7, TotalErrors: 1,
		FirstSeenAt: now, LastSeenAt: now, UpdatedAt: now,
		Metadata: meta,
	})
	if err != nil {
		t.Fatalf("UpsertRunnerHeartbeat: %v", err)
	}

	runners, err := q.ListLiveRunners(ctx, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("ListLiveRunners: %v", err)
	}
	if len(runners) != 1 || runners[0].ID != "runner-1" {
		t.Errorf("ListLiveRunners = %+v", runners)
	}

	err = q.UpsertRunnerHeartbeat(ctx, UpsertRunnerHeartbeatParams{
		ID: "runner-1", Status: "active", MaxConcurrent: 5,
		RunningTasks: 3, AvailableSlots: 2,
		TotalStarted: 11, TotalCompleted: 8, TotalErrors: 1,
		FirstSeenAt: now, LastSeenAt: now.Add(10 * time.Second), UpdatedAt: now.Add(10 * time.Second),
		Metadata: meta,
	})
	if err != nil {
		t.Fatalf("UpsertRunnerHeartbeat (update): %v", err)
	}

	runners, _ = q.ListLiveRunners(ctx, now.Add(-time.Minute))
	if len(runners) != 1 || runners[0].RunningTasks != 3 {
		t.Errorf("expected 1 runner with 3 running tasks, got %+v", runners)
	}
}

func TestSchedulesCRUD(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	null := sql.NullString{}
	nullJSON := json.RawMessage("null")
	err := q.CreateSchedule(ctx, CreateScheduleParams{
		ID: "sched-1", Name: "daily-report",
		TriggerType: "cron", TriggerConfig: json.RawMessage("{}"),
		CronExpr: "0 9 * * *",
		Prompt:   "generate report", TeamID: null,
		GitUrl: null, GitRef: null, AgentImage: null, Agent: null,
		ProviderID: null, ModelID: null, VariantID: null,
		Skills: nullJSON, TimeoutSec: 600,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	byName, err := q.GetScheduleByName(ctx, "daily-report")
	if err != nil {
		t.Fatalf("GetScheduleByName: %v", err)
	}
	if byName.CronExpr != "0 9 * * *" {
		t.Errorf("GetScheduleByName = %+v", byName)
	}

	byID, err := q.GetScheduleByID(ctx, "sched-1")
	if err != nil {
		t.Fatalf("GetScheduleByID: %v", err)
	}
	if byID.Name != "daily-report" {
		t.Errorf("GetScheduleByID = %+v", byID)
	}

	list, err := q.ListSchedules(ctx)
	if err != nil {
		t.Fatalf("ListSchedules: %v", err)
	}
	if len(list) != 1 || list[0].Name != "daily-report" {
		t.Errorf("ListSchedules = %+v", list)
	}

	enabled, err := q.ListEnabledSchedules(ctx)
	if err != nil {
		t.Fatalf("ListEnabledSchedules: %v", err)
	}
	if len(enabled) != 1 {
		t.Errorf("expected 1 enabled schedule, got %d", len(enabled))
	}

	if err := q.DeleteSchedule(ctx, "daily-report"); err != nil {
		t.Fatalf("DeleteSchedule: %v", err)
	}
	list, _ = q.ListSchedules(ctx)
	if len(list) != 0 {
		t.Errorf("expected 0 schedules after delete, got %d", len(list))
	}
}

func TestScheduleTeamScoping(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	nullJSON := json.RawMessage("null")
	if err := q.CreateSchedule(ctx, CreateScheduleParams{
		ID: "s1", Name: "team-a-sched",
		TriggerType: "cron", TriggerConfig: json.RawMessage("{}"),
		CronExpr: "0 * * * *",
		Prompt:   "a", TeamID: sql.NullString{String: "team-a", Valid: true},
		Skills: nullJSON, TimeoutSec: 300, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	if err := q.CreateSchedule(ctx, CreateScheduleParams{
		ID: "s2", Name: "global-sched",
		TriggerType: "cron", TriggerConfig: json.RawMessage("{}"),
		CronExpr: "0 * * * *",
		Prompt:   "global", TeamID: sql.NullString{},
		Skills: nullJSON, TimeoutSec: 300, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	teamScheds, err := q.ListSchedulesByTeam(ctx, sql.NullString{String: "team-a", Valid: true})
	if err != nil {
		t.Fatalf("ListSchedulesByTeam: %v", err)
	}
	if len(teamScheds) != 1 || teamScheds[0].Name != "team-a-sched" {
		t.Errorf("expected 1 team schedule, got %d: %+v", len(teamScheds), teamScheds)
	}

	teamEnabled, err := q.ListEnabledSchedulesByTeam(ctx, sql.NullString{String: "team-a", Valid: true})
	if err != nil {
		t.Fatalf("ListEnabledSchedulesByTeam: %v", err)
	}
	if len(teamEnabled) != 1 {
		t.Errorf("expected 1 enabled team schedule, got %d", len(teamEnabled))
	}
}

func TestScheduleLastAndNextRun(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	nullJSON := json.RawMessage("null")

	if err := q.CreateSchedule(ctx, CreateScheduleParams{
		ID: "s1", Name: "test-sched",
		TriggerType: "cron", TriggerConfig: json.RawMessage("{}"),
		CronExpr: "*/5 * * * *",
		Prompt:   "test", TeamID: sql.NullString{},
		Skills: nullJSON, TimeoutSec: 300, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	lastRun := now.Add(-time.Hour)
	if err := q.SetScheduleLastRun(ctx, SetScheduleLastRunParams{
		ID: "s1", LastRunAt: sql.NullTime{Time: lastRun, Valid: true}, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SetScheduleLastRun: %v", err)
	}

	nextRun := now.Add(time.Hour)
	if err := q.SetScheduleNextRun(ctx, SetScheduleNextRunParams{
		ID: "s1", NextRunAt: sql.NullTime{Time: nextRun, Valid: true}, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SetScheduleNextRun: %v", err)
	}

	got, _ := q.GetScheduleByName(ctx, "test-sched")
	if !got.LastRunAt.Time.Equal(lastRun) {
		t.Errorf("LastRunAt = %v, want %v", got.LastRunAt.Time, lastRun)
	}
	if !got.NextRunAt.Time.Equal(nextRun) {
		t.Errorf("NextRunAt = %v, want %v", got.NextRunAt.Time, nextRun)
	}
}

func TestUpdateSchedule(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	nullJSON := json.RawMessage("null")

	if err := q.CreateSchedule(ctx, CreateScheduleParams{
		ID: "s1", Name: "old-name",
		TriggerType: "cron", TriggerConfig: json.RawMessage("{}"),
		CronExpr: "0 * * * *",
		Prompt:   "old", TeamID: sql.NullString{},
		Skills: nullJSON, TimeoutSec: 300, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	if err := q.UpdateSchedule(ctx, UpdateScheduleParams{
		NewName: "new-name", TriggerType: "cron", TriggerConfig: json.RawMessage("{}"),
		CronExpr: "*/30 * * * *", Prompt: "new",
		Skills: nullJSON, TimeoutSec: 600, Enabled: true,
		UpdatedAt: now, OldName: "old-name",
	}); err != nil {
		t.Fatalf("UpdateSchedule: %v", err)
	}

	got, err := q.GetScheduleByID(ctx, "s1")
	if err != nil {
		t.Fatalf("GetScheduleByID: %v", err)
	}
	if got.Name != "new-name" || got.CronExpr != "*/30 * * * *" || got.Prompt != "new" {
		t.Errorf("updated schedule = %+v", got)
	}
}

func TestInsertScheduleRun(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	nullJSON := json.RawMessage("null")

	if err := q.CreateSchedule(ctx, CreateScheduleParams{
		ID: "s1", Name: "my-sched",
		TriggerType: "cron", TriggerConfig: json.RawMessage("{}"),
		CronExpr: "0 * * * *",
		Prompt:   "run", TeamID: sql.NullString{},
		Skills: nullJSON, TimeoutSec: 300, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	if err := q.InsertScheduleRun(ctx, InsertScheduleRunParams{
		ID: "run-1", ScheduleID: "s1", TaskID: "task-1",
		Status: "pending", ScheduledFor: now, CreatedAt: now,
	}); err != nil {
		t.Fatalf("InsertScheduleRun: %v", err)
	}
}

func TestListHeartbeatTasksEmpty(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()

	rows, err := q.ListHeartbeatTasks(ctx, ListHeartbeatTasksParams{
		Ids: []string{}, RunnerID: sql.NullString{},
	})
	if err != nil {
		t.Fatalf("ListHeartbeatTasks: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}

func TestFailExpiredLeases(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	start := now.Add(-time.Hour)
	null := sql.NullString{}
	nullJSON := json.RawMessage("null")
	if err := q.InsertTask(ctx, InsertTaskParams{
		ID: "task-expired", Prompt: "p1", TeamID: null,
		Skills: nullJSON, Env: nullJSON, TimeoutSec: 300,
		CreatedAt: start, UpdatedAt: start,
	}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	_, _ = q.MarkTaskClaimed(ctx, MarkTaskClaimedParams{
		ID: "task-expired", RunnerID: sql.NullString{String: "r1", Valid: true},
		ClaimedAt:      sql.NullTime{Time: now, Valid: true},
		LeaseExpiresAt: sql.NullTime{Time: now.Add(-time.Hour), Valid: true},
		StartedAt:      sql.NullTime{Time: now, Valid: true},
		UpdatedAt:      now, LastEventAt: sql.NullTime{Time: now, Valid: true},
	})

	got, _ := q.GetTaskByID(ctx, "task-expired")
	if got.Attempt >= got.MaxAttempts {
		t.Skip("task already at max_attempts, cannot test FailExpiredLeases")
	}

	n, err := q.FailExpiredLeases(ctx, FailExpiredLeasesParams{
		EndedAt:   sql.NullTime{Time: now, Valid: true},
		UpdatedAt: now, LastEventAt: sql.NullTime{Time: now, Valid: true},
		LeaseExpiresAt: sql.NullTime{Time: now, Valid: true},
	})
	if err != nil {
		t.Fatalf("FailExpiredLeases: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 failed (attempt < max_attempts), got %d", n)
	}
}

func TestUpdateTaskFromRunnerEvent(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	null := sql.NullString{}
	nullJSON := json.RawMessage("null")
	if err := q.InsertTask(ctx, InsertTaskParams{
		ID: "task-1", Prompt: "p1", TeamID: null,
		Skills: nullJSON, Env: nullJSON, TimeoutSec: 300,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	_, _ = q.MarkTaskClaimed(ctx, MarkTaskClaimedParams{
		ID: "task-1", RunnerID: sql.NullString{String: "r1", Valid: true},
		ClaimedAt:      sql.NullTime{Time: now, Valid: true},
		LeaseExpiresAt: sql.NullTime{Time: now.Add(30 * time.Second), Valid: true},
		StartedAt:      sql.NullTime{Time: now, Valid: true},
		UpdatedAt:      now, LastEventAt: sql.NullTime{Time: now, Valid: true},
	})

	n, err := q.UpdateTaskFromRunnerEvent(ctx, UpdateTaskFromRunnerEventParams{
		Status: "completed", Summary: sql.NullString{String: "done", Valid: true},
		Error: sql.NullString{}, ProviderID: nil, ModelID: nil, VariantID: nil,
		OpencodeSessionID: nil, RunnerImageDigest: nil,
		LeaseExpiresAt: sql.NullTime{},
		StartedAt:      sql.NullTime{}, EndedAt: sql.NullTime{Time: now, Valid: true},
		UpdatedAt: now, LastEventAt: sql.NullTime{Time: now, Valid: true},
		ID: "task-1", RunnerID: sql.NullString{String: "r1", Valid: true},
	})
	if err != nil {
		t.Fatalf("UpdateTaskFromRunnerEvent: %v", err)
	}
	if n != 1 {
		t.Errorf("UpdateTaskFromRunnerEvent affected %d rows, want 1", n)
	}

	got, _ := q.GetTaskByID(ctx, "task-1")
	if got.Status != "completed" || got.Summary.String != "done" {
		t.Errorf("task = %+v", got)
	}
}

func TestListTasksByStatusAndTeam(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)

	nullJSON := json.RawMessage("null")
	for _, tc := range []struct{ id, team string }{
		{"t1", "team-a"}, {"t2", "team-a"}, {"t3", "team-b"},
	} {
		teamID := sql.NullString{}
		if tc.team != "" {
			teamID = sql.NullString{String: tc.team, Valid: true}
		}
		if err := q.InsertTask(ctx, InsertTaskParams{
			ID: tc.id, Prompt: tc.id, TeamID: teamID,
			Skills: nullJSON, Env: nullJSON, TimeoutSec: 300,
			CreatedAt: start, UpdatedAt: start,
		}); err != nil {
			t.Fatalf("InsertTask(%s): %v", tc.id, err)
		}
	}

	teamATasks, err := q.ListTasksByStatusAndTeam(ctx, ListTasksByStatusAndTeamParams{
		TeamID:       sql.NullString{String: "team-a", Valid: true},
		StatusFilter: "", Limit: 100,
	})
	if err != nil {
		t.Fatalf("ListTasksByStatusAndTeam: %v", err)
	}
	if len(teamATasks) != 2 {
		t.Errorf("expected 2 tasks for team-a, got %d", len(teamATasks))
	}
}

func TestListTeamsAndDeleteTeam(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	if err := q.CreateTeam(ctx, CreateTeamParams{ID: "t1", Name: "alpha", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTeam(alpha): %v", err)
	}
	if err := q.CreateTeam(ctx, CreateTeamParams{ID: "t2", Name: "beta", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTeam(beta): %v", err)
	}

	teams, err := q.ListTeams(ctx)
	if err != nil {
		t.Fatalf("ListTeams: %v", err)
	}
	if len(teams) != 2 {
		t.Fatalf("expected 2 teams, got %d", len(teams))
	}
	if teams[0].Name != "alpha" || teams[1].Name != "beta" {
		t.Errorf("ListTeams order: %+v", teams)
	}

	if err := q.DeleteTeam(ctx, "alpha"); err != nil {
		t.Fatalf("DeleteTeam(alpha): %v", err)
	}
	teams, _ = q.ListTeams(ctx)
	if len(teams) != 1 || teams[0].Name != "beta" {
		t.Errorf("expected 1 team (beta) after delete, got %+v", teams)
	}
}

func TestListUsersAndCascadeDelete(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	if err := q.CreateTeam(ctx, CreateTeamParams{ID: "t1", Name: "eng", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if err := q.CreateTeam(ctx, CreateTeamParams{ID: "t2", Name: "ops", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if err := q.CreateUser(ctx, CreateUserParams{ID: "u1", Name: "alice", TeamID: "t1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateUser(alice): %v", err)
	}
	if err := q.CreateUser(ctx, CreateUserParams{ID: "u2", Name: "bob", TeamID: "t1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateUser(bob): %v", err)
	}
	if err := q.CreateUser(ctx, CreateUserParams{ID: "u3", Name: "carol", TeamID: "t2", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateUser(carol): %v", err)
	}
	if err := q.CreateToken(ctx, CreateTokenParams{ID: "tok1", Name: "alice-cli", TokenHash: "h1", UserID: "u1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	allUsers, err := q.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(allUsers) != 3 {
		t.Fatalf("expected 3 users, got %d", len(allUsers))
	}

	engUsers, err := q.ListUsersByTeam(ctx, "t1")
	if err != nil {
		t.Fatalf("ListUsersByTeam: %v", err)
	}
	if len(engUsers) != 2 {
		t.Fatalf("expected 2 eng users, got %d", len(engUsers))
	}
	if engUsers[0].Name != "alice" || engUsers[1].Name != "bob" {
		t.Errorf("ListUsersByTeam = %+v", engUsers)
	}

	if err := q.DeleteTokensByTeam(ctx, "t1"); err != nil {
		t.Fatalf("DeleteTokensByTeam: %v", err)
	}
	tokens, _ := q.ListTokens(ctx)
	if len(tokens) != 0 {
		t.Errorf("expected 0 tokens after DeleteTokensByTeam, got %d", len(tokens))
	}

	if err := q.DeleteUsersByTeam(ctx, "t1"); err != nil {
		t.Fatalf("DeleteUsersByTeam: %v", err)
	}
	allUsers, _ = q.ListUsers(ctx)
	if len(allUsers) != 1 || allUsers[0].Name != "carol" {
		t.Errorf("expected 1 user (carol) after DeleteUsersByTeam, got %+v", allUsers)
	}
}

func TestListScheduleRunsByTeamAndBySchedule(t *testing.T) {
	q, cleanup := newRepo(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	nullJSON := json.RawMessage("null")

	if err := q.CreateTeam(ctx, CreateTeamParams{ID: "t1", Name: "eng", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if err := q.CreateSchedule(ctx, CreateScheduleParams{
		ID: "s1", Name: "daily",
		TriggerType: "cron", TriggerConfig: json.RawMessage("{}"),
		CronExpr: "0 9 * * *",
		Prompt:   "daily report", TeamID: sql.NullString{String: "t1", Valid: true},
		Skills: nullJSON, TimeoutSec: 600, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	if err := q.CreateSchedule(ctx, CreateScheduleParams{
		ID: "s2", Name: "weekly",
		TriggerType: "cron", TriggerConfig: json.RawMessage("{}"),
		CronExpr: "0 9 * * 1",
		Prompt:   "weekly report", TeamID: sql.NullString{String: "t1", Valid: true},
		Skills: nullJSON, TimeoutSec: 600, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	for i, sid := range []string{"s1", "s1", "s2"} {
		rid := fmt.Sprintf("run-%d", i)
		if err := q.InsertScheduleRun(ctx, InsertScheduleRunParams{
			ID: rid, ScheduleID: sid, TeamID: sql.NullString{String: "t1", Valid: true},
			TaskID: "task-" + rid, Status: "submitted",
			ScheduledFor: now, CreatedAt: now,
		}); err != nil {
			t.Fatalf("InsertScheduleRun(%s): %v", rid, err)
		}
	}

	teamRuns, err := q.ListScheduleRunsByTeam(ctx, ListScheduleRunsByTeamParams{
		TeamID: sql.NullString{String: "t1", Valid: true}, Limit: 100,
	})
	if err != nil {
		t.Fatalf("ListScheduleRunsByTeam: %v", err)
	}
	if len(teamRuns) != 3 {
		t.Fatalf("expected 3 schedule runs for team t1, got %d", len(teamRuns))
	}

	schedRuns, err := q.ListScheduleRunsBySchedule(ctx, ListScheduleRunsByScheduleParams{
		ScheduleID: "s1", Limit: 100,
	})
	if err != nil {
		t.Fatalf("ListScheduleRunsBySchedule: %v", err)
	}
	if len(schedRuns) != 2 {
		t.Fatalf("expected 2 runs for schedule s1, got %d", len(schedRuns))
	}
	if schedRuns[0].ScheduleName != "daily" || schedRuns[1].ScheduleName != "daily" {
		t.Errorf("ListScheduleRunsBySchedule names: %+v", schedRuns)
	}
}
