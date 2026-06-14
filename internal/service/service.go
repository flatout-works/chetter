// Package service contains chetter orchestration and MCP tool handlers.
package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/config"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/store"
	"github.com/robfig/cron/v3"
)

// SubmitTaskRequest contains all fields needed to submit a runner task.
type SubmitTaskRequest struct {
	Prompt     string
	GitURL     string
	GitRef     string
	AgentImage string
	Agent      string
	ProviderID string
	ModelID    string
	VariantID  string
	Skills     []string
	Env        map[string]string
	TimeoutSec int
}

const (
	defaultMaxMemoryMB      = 4096
	defaultMaxCPU           = 2
	scheduleRunTimeout      = 30 * time.Second
	eventHandlerTimeout     = 10 * time.Second
	reaperInterval          = 30 * time.Second
	reaperGrace             = 120 * time.Second
	reaperHealthMaxEventSec = 120
	runnerPresenceMaxSec    = 60
)

var defaultCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

type Service struct {
	cfg         config.Config
	store       *store.Store
	repo        *repository.Queries
	arcane      *ArcaneClient
	cron        *cron.Cron
	cronMu      sync.Mutex
	cronEntries map[string]cron.EntryID
	reaperStop  chan struct{}
}

func New(cfg config.Config, st *store.Store) *Service {
	svc := &Service{
		cfg:         cfg,
		store:       st,
		repo:        repository.New(st.DB()),
		cron:        cron.New(cron.WithParser(defaultCronParser), cron.WithLocation(time.UTC)),
		cronEntries: make(map[string]cron.EntryID),
		reaperStop:  make(chan struct{}),
	}
	if cfg.ArcaneServerURL != "" && cfg.ArcaneAPIKey != "" {
		svc.arcane = NewArcaneClient(cfg.ArcaneServerURL, cfg.ArcaneAPIKey)
	}
	return svc
}

// Start loads schedules, starts cron, and starts the stale-task reaper.
func (s *Service) Start(ctx context.Context) error {
	s.cron.Start()
	if err := s.loadSchedules(ctx); err != nil {
		return err
	}
	go s.taskReaper()
	return nil
}

// Stop stops the scheduler and the reaper.
func (s *Service) Stop() {
	close(s.reaperStop)
	ctx := s.cron.Stop()
	<-ctx.Done()
}

// taskReaper periodically scans for tasks that have been running without a
// heartbeat for longer than their configured timeout + grace period and marks
// them as error so they do not stay as zombie "running" rows forever.
func (s *Service) taskReaper() {
	s.reapStaleTasks()
	s.reapExpiredLeases()
	ticker := time.NewTicker(reaperInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.reapStaleTasks()
			s.reapExpiredLeases()
		case <-s.reaperStop:
			return
		}
	}
}

func (s *Service) reapExpiredLeases() {
	ctx, cancel := context.WithTimeout(context.Background(), eventHandlerTimeout)
	defer cancel()
	now := time.Now().UTC()
	expiredBefore := sql.NullTime{Time: now, Valid: true}
	reclaimed, err := s.repo.ReclaimExpiredLeases(ctx, repository.ReclaimExpiredLeasesParams{
		UpdatedAt:      now,
		LeaseExpiresAt: expiredBefore,
	})
	if err != nil {
		slog.Error("lease reaper reclaim failed", "error", err)
		return
	}
	failed, err := s.repo.FailExpiredLeases(ctx, repository.FailExpiredLeasesParams{
		EndedAt:        sql.NullTime{Time: now, Valid: true},
		UpdatedAt:      now,
		LastEventAt:    sql.NullTime{Time: now, Valid: true},
		LeaseExpiresAt: expiredBefore,
	})
	if err != nil {
		slog.Error("lease reaper fail failed", "error", err)
		return
	}
	if reclaimed > 0 || failed > 0 {
		slog.Info("reaped expired task leases", "reclaimed", reclaimed, "failed", failed)
	}
}

func (s *Service) reapStaleTasks() {
	ctx, cancel := context.WithTimeout(context.Background(), eventHandlerTimeout)
	defer cancel()
	n, err := s.store.ReapStaleTasks(ctx, reaperGrace)
	if err != nil {
		slog.Error("task reaper failed", "error", err)
		return
	}
	if n > 0 {
		slog.Info("reaped stale tasks", "count", n)
	}
}

// SubmitTask stores a pending task for runners to claim through ConnectRPC.
func (s *Service) SubmitTask(ctx context.Context, in SubmitTaskRequest) (store.TaskRecord, error) {
	if in.Prompt == "" {
		return store.TaskRecord{}, fmt.Errorf("prompt is required")
	}
	if in.AgentImage == "" {
		if s.cfg.DefaultAgentImage == "" {
			return store.TaskRecord{}, fmt.Errorf("agent_image is required (no default configured)")
		}
		in.AgentImage = s.cfg.DefaultAgentImage
	}
	if in.TimeoutSec == 0 {
		in.TimeoutSec = s.cfg.DefaultTaskTimeoutSec
	}
	taskID, err := randomID("task")
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("generate task id: %w", err)
	}
	now := time.Now().UTC()
	skills, err := json.Marshal(nonEmptyStrings(in.Skills))
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("marshal skills: %w", err)
	}
	env, err := json.Marshal(sanitizeTaskEnv(in.Env))
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("marshal env: %w", err)
	}
	teamID := teamIDFromContext(ctx)
	if err := s.repo.InsertTask(ctx, repository.InsertTaskParams{
		ID:                taskID,
		TeamID:            nullString(teamID),
		Prompt:            in.Prompt,
		GitUrl:            nullString(in.GitURL),
		GitRef:            nullString(in.GitRef),
		AgentImage:        nullString(in.AgentImage),
		Agent:             nullString(in.Agent),
		ProviderID:        nullString(in.ProviderID),
		ModelID:           nullString(in.ModelID),
		VariantID:         nullString(in.VariantID),
		CommitAuthorName:  sql.NullString{String: "Chetter", Valid: true},
		CommitAuthorEmail: sql.NullString{String: "chetter@chetter.flatout.works", Valid: true},
		Skills:            skills,
		Env:               env,
		TimeoutSec:        int32(in.TimeoutSec),
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		return store.TaskRecord{}, fmt.Errorf("insert task: %w", err)
	}
	slog.Info("task queued", "task_id", taskID)
	task, err := s.repo.GetTaskByID(ctx, taskID)
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("get task: %w", err)
	}
	return repoTaskToStoreRecord(task), nil
}

func repoTaskToStoreRecord(task repository.ChetterTask) store.TaskRecord {
	var skills []string
	_ = json.Unmarshal(task.Skills, &skills)
	env := map[string]string{}
	_ = json.Unmarshal(task.Env, &env)
	var startedAt, endedAt *time.Time
	if task.StartedAt.Valid {
		startedAt = &task.StartedAt.Time
	}
	if task.EndedAt.Valid {
		endedAt = &task.EndedAt.Time
	}
	return store.TaskRecord{
		ID:                task.ID,
		TeamID:            task.TeamID.String,
		Status:            task.Status,
		Prompt:            task.Prompt,
		GitURL:            task.GitUrl.String,
		GitRef:            task.GitRef.String,
		AgentImage:        task.AgentImage.String,
		Agent:             task.Agent.String,
		ProviderID:        task.ProviderID.String,
		ModelID:           task.ModelID.String,
		VariantID:         task.VariantID.String,
		OpenCodeSessionID: task.OpencodeSessionID.String,
		RunnerImageDigest: task.RunnerImageDigest.String,
		CommitAuthorName:  task.CommitAuthorName.String,
		CommitAuthorEmail: task.CommitAuthorEmail.String,
		Skills:            skills,
		Env:               env,
		TimeoutSec:        int(task.TimeoutSec),
		Summary:           task.Summary.String,
		Error:             task.Error.String,
		CreatedAt:         task.CreatedAt,
		UpdatedAt:         task.UpdatedAt,
		StartedAt:         startedAt,
		EndedAt:           endedAt,
	}
}

func nonEmptyStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func sanitizeTaskEnv(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for key, value := range env {
		upper := strings.ToUpper(key)
		if strings.Contains(upper, "SECRET") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "KEY") || strings.Contains(upper, "PASSWORD") {
			out[key] = "[redacted]"
			continue
		}
		out[key] = value
	}
	return out
}

// CreateSchedule persists and activates a cron schedule.
func (s *Service) CreateSchedule(ctx context.Context, in store.ScheduleInput) (store.ScheduleRecord, error) {
	if in.Name == "" {
		return store.ScheduleRecord{}, fmt.Errorf("name is required")
	}
	if in.CronExpr == "" {
		return store.ScheduleRecord{}, fmt.Errorf("cron_expr is required")
	}
	if in.Prompt == "" {
		return store.ScheduleRecord{}, fmt.Errorf("prompt is required")
	}
	if _, err := defaultCronParser.Parse(in.CronExpr); err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("parse cron: %w", err)
	}
	if in.ID == "" {
		id, err := randomID("sched")
		if err != nil {
			return store.ScheduleRecord{}, fmt.Errorf("generate schedule id: %w", err)
		}
		in.ID = id
	}
	if in.AgentImage == "" {
		return store.ScheduleRecord{}, fmt.Errorf("agent_image is required")
	}
	if in.TimeoutSec == 0 {
		in.TimeoutSec = s.cfg.DefaultTaskTimeoutSec
	}
	now := time.Now().UTC()
	skills, err := json.Marshal(nonEmptyStrings(in.Skills))
	if err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("marshal skills: %w", err)
	}
	teamID := teamIDFromContext(ctx)
	if err := s.repo.CreateSchedule(ctx, repository.CreateScheduleParams{
		ID:         in.ID,
		TeamID:     nullString(teamID),
		Name:       in.Name,
		CronExpr:   in.CronExpr,
		Prompt:     in.Prompt,
		GitUrl:     nullString(in.GitURL),
		GitRef:     nullString(in.GitRef),
		AgentImage: nullString(in.AgentImage),
		Agent:      nullString(in.Agent),
		ProviderID: nullString(in.ProviderID),
		ModelID:    nullString(in.ModelID),
		VariantID:  nullString(in.VariantID),
		Skills:     skills,
		TimeoutSec: int32(in.TimeoutSec),
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("create schedule: %w", err)
	}
	record, err := s.repo.GetScheduleByID(ctx, in.ID)
	if err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("get schedule: %w", err)
	}
	sRecord := scheduleToStoreRecord(record)
	if err := s.activateSchedule(ctx, sRecord); err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("activate schedule: %w", err)
	}
	// Re-fetch so the returned record reflects next_run_at set by activation.
	updated, err := s.repo.GetScheduleByID(ctx, in.ID)
	if err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("get schedule after activation: %w", err)
	}
	return scheduleToStoreRecord(updated), nil
}

// UpdateSchedule updates all mutable fields on an existing schedule.
func (s *Service) UpdateSchedule(ctx context.Context, name string, in store.ScheduleInput, enabled bool) (store.ScheduleRecord, error) {
	if in.Name == "" {
		return store.ScheduleRecord{}, fmt.Errorf("name is required")
	}
	if in.CronExpr == "" {
		return store.ScheduleRecord{}, fmt.Errorf("cron_expr is required")
	}
	if in.Prompt == "" {
		return store.ScheduleRecord{}, fmt.Errorf("prompt is required")
	}
	if _, err := defaultCronParser.Parse(in.CronExpr); err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("parse cron: %w", err)
	}
	if in.AgentImage == "" {
		return store.ScheduleRecord{}, fmt.Errorf("agent_image is required")
	}
	if in.TimeoutSec == 0 {
		in.TimeoutSec = s.cfg.DefaultTaskTimeoutSec
	}
	s.cronMu.Lock()
	existing, err := s.repo.GetScheduleByName(ctx, name)
	if err == nil {
		if entryID, ok := s.cronEntries[existing.ID]; ok {
			s.cron.Remove(entryID)
			delete(s.cronEntries, existing.ID)
		}
	}
	s.cronMu.Unlock()
	now := time.Now().UTC()
	skills, err := json.Marshal(nonEmptyStrings(in.Skills))
	if err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("marshal skills: %w", err)
	}
	if err := s.repo.UpdateSchedule(ctx, repository.UpdateScheduleParams{
		NewName:    in.Name,
		CronExpr:   in.CronExpr,
		Prompt:     in.Prompt,
		GitUrl:     nullString(in.GitURL),
		GitRef:     nullString(in.GitRef),
		AgentImage: nullString(in.AgentImage),
		Agent:      nullString(in.Agent),
		ProviderID: nullString(in.ProviderID),
		ModelID:    nullString(in.ModelID),
		VariantID:  nullString(in.VariantID),
		Skills:     skills,
		TimeoutSec: int32(in.TimeoutSec),
		Enabled:    enabled,
		UpdatedAt:  now,
		OldName:    name,
	}); err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("update schedule: %w", err)
	}
	record, err := s.repo.GetScheduleByName(ctx, in.Name)
	if err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("get schedule: %w", err)
	}
	sRecord := scheduleToStoreRecord(record)
	if !enabled {
		return sRecord, nil
	}
	if err := s.activateSchedule(ctx, sRecord); err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("reactivate schedule: %w", err)
	}
	return sRecord, nil
}

// DeleteSchedule removes a schedule by name and stops its cron job.
func (s *Service) DeleteSchedule(ctx context.Context, name string) error {
	schedules, err := s.repo.ListSchedules(ctx)
	if err != nil {
		return fmt.Errorf("list schedules: %w", err)
	}
	var targetID string
	for i := range schedules {
		if schedules[i].Name == name {
			targetID = schedules[i].ID
			break
		}
	}
	if targetID == "" {
		return fmt.Errorf("schedule %q not found", name)
	}
	s.cronMu.Lock()
	if entryID, ok := s.cronEntries[targetID]; ok {
		s.cron.Remove(entryID)
		delete(s.cronEntries, targetID)
	}
	s.cronMu.Unlock()
	if err := s.repo.DeleteSchedule(ctx, name); err != nil {
		return fmt.Errorf("delete schedule: %w", err)
	}
	return nil
}

// RunScheduleNow submits a task from a named schedule immediately.
func (s *Service) RunScheduleNow(ctx context.Context, name string) (store.TaskRecord, error) {
	if name == "" {
		return store.TaskRecord{}, fmt.Errorf("name is required")
	}
	schedules, err := s.repo.ListSchedules(ctx)
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("list schedules: %w", err)
	}
	var targetID, targetPrompt, targetGitURL, targetGitRef, targetAgentImage, targetAgent, targetProviderID, targetModelID, targetVariantID string
	targetSkills := []string(nil)
	var targetTimeoutSec int
	found := false
	for _, sch := range schedules {
		if sch.Name == name {
			targetID = sch.ID
			targetPrompt = sch.Prompt
			targetGitURL = sch.GitUrl.String
			targetGitRef = sch.GitRef.String
			targetAgentImage = sch.AgentImage.String
			targetAgent = sch.Agent.String
			targetProviderID = sch.ProviderID.String
			targetModelID = sch.ModelID.String
			targetVariantID = sch.VariantID.String
			_ = json.Unmarshal(sch.Skills, &targetSkills)
			targetTimeoutSec = int(sch.TimeoutSec)
			found = true
			break
		}
	}
	if !found {
		return store.TaskRecord{}, fmt.Errorf("schedule %q not found", name)
	}
	return s.submitScheduleTask(ctx, targetID, targetPrompt, targetGitURL, targetGitRef, targetAgentImage, targetAgent, targetProviderID, targetModelID, targetVariantID, targetSkills, targetTimeoutSec, time.Now().UTC())
}

func (s *Service) loadSchedules(ctx context.Context) error {
	schedules, err := s.repo.ListEnabledSchedules(ctx)
	if err != nil {
		return fmt.Errorf("load schedules: %w", err)
	}
	for _, schedule := range schedules {
		if err := s.activateSchedule(ctx, scheduleToStoreRecord(schedule)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) activateSchedule(ctx context.Context, schedule store.ScheduleRecord) error {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()
	if existing, ok := s.cronEntries[schedule.ID]; ok {
		s.cron.Remove(existing)
	}
	entryID, err := s.cron.AddFunc(schedule.CronExpr, func() {
		runCtx, cancel := context.WithTimeout(context.Background(), scheduleRunTimeout)
		defer cancel()
		if err := s.runSchedule(runCtx, schedule.ID, time.Now().UTC()); err != nil {
			slog.Error("schedule run failed", "scheduleID", schedule.ID, "err", err)
		}
	})
	if err != nil {
		return fmt.Errorf("activate schedule %s: %w", schedule.ID, err)
	}
	s.cronEntries[schedule.ID] = entryID
	entry := s.cron.Entry(entryID)
	if !entry.Next.IsZero() {
		if err := s.repo.SetScheduleNextRun(ctx, repository.SetScheduleNextRunParams{
			NextRunAt: sql.NullTime{Time: entry.Next, Valid: true},
			UpdatedAt: time.Now().UTC(),
			ID:        schedule.ID,
		}); err != nil {
			return fmt.Errorf("set schedule next run: %w", err)
		}
	}
	return nil
}

func (s *Service) runSchedule(ctx context.Context, scheduleID string, scheduledFor time.Time) error {
	schedule, err := s.repo.GetScheduleByID(ctx, scheduleID)
	if err != nil {
		return fmt.Errorf("get schedule %s: %w", scheduleID, err)
	}
	var skills []string
	_ = json.Unmarshal(schedule.Skills, &skills)
	_, err = s.submitScheduleTask(ctx, schedule.ID, schedule.Prompt, schedule.GitUrl.String, schedule.GitRef.String,
		schedule.AgentImage.String, schedule.Agent.String, schedule.ProviderID.String, schedule.ModelID.String, schedule.VariantID.String,
		skills, int(schedule.TimeoutSec), scheduledFor)
	return err
}

func (s *Service) submitScheduleTask(ctx context.Context, scheduleID, prompt, gitURL, gitRef, agentImage, agent, providerID, modelID, variantID string, skills []string, timeoutSec int, scheduledFor time.Time) (store.TaskRecord, error) {
	task, err := s.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:     prompt,
		GitURL:     gitURL,
		GitRef:     gitRef,
		AgentImage: agentImage,
		Agent:      agent,
		ProviderID: providerID,
		ModelID:    modelID,
		VariantID:  variantID,
		Skills:     skills,
		TimeoutSec: timeoutSec,
	})
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("submit scheduled task: %w", err)
	}
	runID, err := randomID("run")
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("generate run id: %w", err)
	}
	if err := s.repo.InsertScheduleRun(ctx, repository.InsertScheduleRunParams{
		ID:           runID,
		ScheduleID:   scheduleID,
		TaskID:       task.ID,
		Status:       "submitted",
		ScheduledFor: scheduledFor.UTC(),
		CreatedAt:    time.Now().UTC(),
	}); err != nil {
		return store.TaskRecord{}, fmt.Errorf("insert schedule run: %w", err)
	}
	if err := s.repo.SetScheduleLastRun(ctx, repository.SetScheduleLastRunParams{
		LastRunAt: sql.NullTime{Time: scheduledFor.UTC(), Valid: true},
		UpdatedAt: time.Now().UTC(),
		ID:        scheduleID,
	}); err != nil {
		return store.TaskRecord{}, fmt.Errorf("set schedule last run: %w", err)
	}
	if entryID, ok := s.cronEntries[scheduleID]; ok {
		entry := s.cron.Entry(entryID)
		if !entry.Next.IsZero() {
			if err := s.repo.SetScheduleNextRun(ctx, repository.SetScheduleNextRunParams{
				NextRunAt: sql.NullTime{Time: entry.Next, Valid: true},
				UpdatedAt: time.Now().UTC(),
				ID:        scheduleID,
			}); err != nil {
				return store.TaskRecord{}, err
			}
		}
	}
	return task, nil
}

func randomID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(raw[:]), nil
}

func teamIDFromContext(ctx context.Context) string {
	scope, ok := auth.GetScope(ctx)
	if !ok || scope.Admin {
		return ""
	}
	return scope.TeamID
}
