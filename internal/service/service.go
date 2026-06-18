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
	"github.com/flatout-works/chetter/internal/webhook"
	"github.com/robfig/cron/v3"
)

// SubmitTaskRequest contains all fields needed to submit a runner task.
type SubmitTaskRequest struct {
	Prompt      string
	GitURL      string
	GitRef      string
	AgentImage  string
	Agent       string
	ProviderID  string
	ModelID     string
	VariantID   string
	Harness     string
	Skills      []string
	Env         map[string]string
	TimeoutSec  int
	TriggerName string
	TriggerType string
}

type AuditEventParams struct {
	EventType        string
	SourceType       string
	SourceID         string
	TargetType       string
	TargetID         string
	Repo             string
	GitHubEvent      string
	GitHubAction     string
	GitHubDeliveryID string
	ParentEventID    string
	Detail           string
	Payload          json.RawMessage
}

type RecordArtifactParams struct {
	TaskID          string
	ArtifactType    string
	Repo            string
	Number          int
	URL             string
	Ref             string
	SHA             string
	DiscoverySource string
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
	taskEnv := sanitizeTaskEnv(in.Env)
	if in.Harness != "" {
		taskEnv["__chetter_harness"] = in.Harness
	}
	env, err := json.Marshal(taskEnv)
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
		TriggerName:       nullString(in.TriggerName),
		TriggerType:       nullString(in.TriggerType),
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

// emptyTriggerConfig returns an empty JSON object as the trigger_config value
// for triggers that have no type-specific data.
func emptyTriggerConfig() json.RawMessage {
	return json.RawMessage("{}")
}

// CreateTrigger persists and activates a trigger (cron, pr_review, issue).
func (s *Service) CreateTrigger(ctx context.Context, in store.ScheduleInput) (store.ScheduleRecord, error) {
	if in.Name == "" {
		return store.ScheduleRecord{}, fmt.Errorf("name is required")
	}
	if in.TriggerType == "" {
		in.TriggerType = store.TriggerTypeCron
	}
	if in.Prompt == "" && in.TriggerType != store.TriggerTypePRReview && in.TriggerType != store.TriggerTypeIssue {
		return store.ScheduleRecord{}, fmt.Errorf("prompt is required")
	}
	if in.ID == "" {
		id, err := randomID("trig")
		if err != nil {
			return store.ScheduleRecord{}, fmt.Errorf("generate trigger id: %w", err)
		}
		in.ID = id
	}
	if in.AgentImage == "" {
		return store.ScheduleRecord{}, fmt.Errorf("agent_image is required")
	}
	if in.TimeoutSec == 0 {
		in.TimeoutSec = s.cfg.DefaultTaskTimeoutSec
	}
	switch in.TriggerType {
	case store.TriggerTypeCron:
		if in.CronExpr == "" {
			return store.ScheduleRecord{}, fmt.Errorf("cron_expr is required for cron triggers")
		}
		if _, err := defaultCronParser.Parse(in.CronExpr); err != nil {
			return store.ScheduleRecord{}, fmt.Errorf("parse cron: %w", err)
		}
	case store.TriggerTypePRReview:
		var cfg store.PRReviewTriggerConfig
		if err := json.Unmarshal([]byte(in.TriggerConfig), &cfg); err != nil || cfg.Repo == "" {
			return store.ScheduleRecord{}, fmt.Errorf("repo is required in trigger_config for pr_review triggers")
		}
	case store.TriggerTypeIssue:
		var cfg struct{ Repo string }
		if err := json.Unmarshal([]byte(in.TriggerConfig), &cfg); err != nil || cfg.Repo == "" {
			return store.ScheduleRecord{}, fmt.Errorf("repo is required in trigger_config for issue triggers")
		}
	default:
		return store.ScheduleRecord{}, fmt.Errorf("unknown trigger_type %q", in.TriggerType)
	}
	now := time.Now().UTC()
	skills, err := json.Marshal(nonEmptyStrings(in.Skills))
	if err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("marshal skills: %w", err)
	}
	teamID := teamIDFromContext(ctx)
	triggerConfig := emptyTriggerConfig()
	if in.TriggerConfig != "" {
		triggerConfig = json.RawMessage(in.TriggerConfig)
	}
	if err := s.repo.CreateSchedule(ctx, repository.CreateScheduleParams{
		ID:            in.ID,
		TeamID:        nullString(teamID),
		Name:          in.Name,
		TriggerType:   in.TriggerType,
		TriggerConfig: triggerConfig,
		CronExpr:      in.CronExpr,
		Prompt:        in.Prompt,
		GitUrl:        nullString(in.GitURL),
		GitRef:        nullString(in.GitRef),
		AgentImage:    nullString(in.AgentImage),
		Agent:         nullString(in.Agent),
		ProviderID:    nullString(in.ProviderID),
		ModelID:       nullString(in.ModelID),
		VariantID:     nullString(in.VariantID),
		Harness:       nullString(in.Harness),
		Skills:        skills,
		TimeoutSec:    int32(in.TimeoutSec),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("create trigger: %w", err)
	}
	record, err := s.repo.GetScheduleByID(ctx, in.ID)
	if err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("get trigger: %w", err)
	}
	sRecord := scheduleToStoreRecord(record)
	if in.TriggerType == store.TriggerTypeCron {
		if err := s.activateSchedule(ctx, sRecord); err != nil {
			return store.ScheduleRecord{}, fmt.Errorf("activate schedule: %w", err)
		}
		updated, err := s.repo.GetScheduleByID(ctx, in.ID)
		if err != nil {
			return store.ScheduleRecord{}, fmt.Errorf("get trigger after activation: %w", err)
		}
		return scheduleToStoreRecord(updated), nil
	}
	return sRecord, nil
}

// UpdateTrigger updates all mutable fields on an existing trigger.
func (s *Service) UpdateTrigger(ctx context.Context, name string, in store.ScheduleInput, enabled bool) (store.ScheduleRecord, error) {
	if in.Name == "" {
		return store.ScheduleRecord{}, fmt.Errorf("name is required")
	}
	if in.Prompt == "" && in.TriggerType != store.TriggerTypePRReview {
		return store.ScheduleRecord{}, fmt.Errorf("prompt is required")
	}
	if in.TriggerType == "" {
		in.TriggerType = store.TriggerTypeCron
	}
	switch in.TriggerType {
	case store.TriggerTypeCron:
		if in.CronExpr == "" {
			return store.ScheduleRecord{}, fmt.Errorf("cron_expr is required for cron triggers")
		}
		if _, err := defaultCronParser.Parse(in.CronExpr); err != nil {
			return store.ScheduleRecord{}, fmt.Errorf("parse cron: %w", err)
		}
	case store.TriggerTypePRReview:
		var cfg store.PRReviewTriggerConfig
		if err := json.Unmarshal([]byte(in.TriggerConfig), &cfg); err != nil || cfg.Repo == "" {
			return store.ScheduleRecord{}, fmt.Errorf("repo is required in trigger_config for pr_review triggers")
		}
	}
	if in.AgentImage == "" {
		return store.ScheduleRecord{}, fmt.Errorf("agent_image is required")
	}
	if in.TimeoutSec == 0 {
		in.TimeoutSec = s.cfg.DefaultTaskTimeoutSec
	}
	// Deactivate cron trigger if it's a cron type.
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
	triggerConfig := emptyTriggerConfig()
	if in.TriggerConfig != "" {
		triggerConfig = json.RawMessage(in.TriggerConfig)
	}
	if err := s.repo.UpdateSchedule(ctx, repository.UpdateScheduleParams{
		NewName:       in.Name,
		TriggerType:   in.TriggerType,
		TriggerConfig: triggerConfig,
		CronExpr:      in.CronExpr,
		Prompt:        in.Prompt,
		GitUrl:        nullString(in.GitURL),
		GitRef:        nullString(in.GitRef),
		AgentImage:    nullString(in.AgentImage),
		Agent:         nullString(in.Agent),
		ProviderID:    nullString(in.ProviderID),
		ModelID:       nullString(in.ModelID),
		VariantID:     nullString(in.VariantID),
		Harness:       nullString(in.Harness),
		Skills:        skills,
		TimeoutSec:    int32(in.TimeoutSec),
		Enabled:       enabled,
		UpdatedAt:     now,
		OldName:       name,
	}); err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("update trigger: %w", err)
	}
	record, err := s.repo.GetScheduleByName(ctx, in.Name)
	if err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("get trigger: %w", err)
	}
	sRecord := scheduleToStoreRecord(record)
	if !enabled || in.TriggerType != store.TriggerTypeCron {
		return sRecord, nil
	}
	if err := s.activateSchedule(ctx, sRecord); err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("reactivate schedule: %w", err)
	}
	return sRecord, nil
}

// DeleteTrigger removes a trigger by name and stops its cron job if applicable.
func (s *Service) DeleteTrigger(ctx context.Context, name string) error {
	sch, err := s.repo.GetScheduleByName(ctx, name)
	if err != nil {
		return fmt.Errorf("get trigger: %w", err)
	}
	targetID := sch.ID
	s.cronMu.Lock()
	if entryID, ok := s.cronEntries[targetID]; ok {
		s.cron.Remove(entryID)
		delete(s.cronEntries, targetID)
	}
	s.cronMu.Unlock()
	if err := s.repo.DeleteSchedule(ctx, name); err != nil {
		return fmt.Errorf("delete trigger: %w", err)
	}
	return nil
}

// RunTriggerNow submits a task from a named cron trigger immediately.
func (s *Service) RunTriggerNow(ctx context.Context, name string) (store.TaskRecord, error) {
	if name == "" {
		return store.TaskRecord{}, fmt.Errorf("name is required")
	}
	sch, err := s.repo.GetScheduleByName(ctx, name)
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("get trigger: %w", err)
	}
	targetSkills := []string(nil)
	_ = json.Unmarshal(sch.Skills, &targetSkills)
	return s.submitScheduleTask(ctx,
		sch.ID,
		sch.TeamID.String,
		sch.Prompt,
		sch.GitUrl.String,
		sch.GitRef.String,
		sch.AgentImage.String,
		sch.Agent.String,
		sch.ProviderID.String,
		sch.ModelID.String,
		sch.VariantID.String,
		sch.Harness.String,
		targetSkills,
		int(sch.TimeoutSec),
		time.Now().UTC(),
	)
}

func (s *Service) loadSchedules(ctx context.Context) error {
	triggers, err := s.repo.ListEnabledTriggersByType(ctx, store.TriggerTypeCron)
	if err != nil {
		return fmt.Errorf("load cron triggers: %w", err)
	}
	for _, trigger := range triggers {
		if err := s.activateSchedule(ctx, scheduleToStoreRecord(trigger)); err != nil {
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
	_, err = s.submitScheduleTask(ctx, schedule.ID, schedule.TeamID.String, schedule.Prompt, schedule.GitUrl.String, schedule.GitRef.String,
		schedule.AgentImage.String, schedule.Agent.String, schedule.ProviderID.String, schedule.ModelID.String, schedule.VariantID.String,
		schedule.Harness.String, skills, int(schedule.TimeoutSec), scheduledFor)
	return err
}

func (s *Service) submitScheduleTask(ctx context.Context, scheduleID, teamID, prompt, gitURL, gitRef, agentImage, agent, providerID, modelID, variantID, harness string, skills []string, timeoutSec int, scheduledFor time.Time) (store.TaskRecord, error) {
	task, err := s.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:     prompt,
		GitURL:     gitURL,
		GitRef:     gitRef,
		AgentImage: agentImage,
		Agent:      agent,
		ProviderID: providerID,
		ModelID:    modelID,
		VariantID:  variantID,
		Harness:    harness,
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
		TeamID:       nullString(teamID),
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

// ListEnabledPRReviewTriggersByRepo finds all enabled PR review triggers
// matching a given repo. Used by the webhook handler to dispatch reviews.
func (s *Service) ListEnabledPRReviewTriggersByRepo(ctx context.Context, repo string) ([]webhook.ReviewTrigger, error) {
	// Pass the raw repo string. The query uses ->> (JSON_UNQUOTE + JSON_EXTRACT)
	// on the LHS, so the comparison value is the unquoted string.
	triggers, err := s.repo.ListEnabledPRReviewTriggersByRepo(ctx, json.RawMessage(repo))
	if err != nil {
		return nil, fmt.Errorf("list pr review triggers: %w", err)
	}
	out := make([]webhook.ReviewTrigger, len(triggers))
	for i, t := range triggers {
		var skills []string
		_ = json.Unmarshal(t.Skills, &skills)
		ev := triggerEventFromConfig(t.TriggerConfig)
		out[i] = webhook.ReviewTrigger{
			Name:       t.Name,
			Prompt:     t.Prompt,
			AgentImage: t.AgentImage.String,
			Agent:      t.Agent.String,
			ProviderID: t.ProviderID.String,
			ModelID:    t.ModelID.String,
			VariantID:  t.VariantID.String,
			TimeoutSec: int(t.TimeoutSec),
			GitURL:     t.GitUrl.String,
			GitRef:     t.GitRef.String,
			Skills:     skills,
			Event:      ev,
		}
	}
	return out, nil
}

// ListEnabledIssueTriggersByRepo finds all enabled issue triggers for a repo.
func (s *Service) ListEnabledIssueTriggersByRepo(ctx context.Context, repo string) ([]webhook.ReviewTrigger, error) {
	triggers, err := s.repo.ListEnabledIssueTriggersByRepo(ctx, json.RawMessage(repo))
	if err != nil {
		return nil, fmt.Errorf("list issue triggers: %w", err)
	}
	out := make([]webhook.ReviewTrigger, len(triggers))
	for i, t := range triggers {
		var skills []string
		_ = json.Unmarshal(t.Skills, &skills)
		ev := triggerEventFromConfig(t.TriggerConfig)
		out[i] = webhook.ReviewTrigger{
			Name:       t.Name,
			Prompt:     t.Prompt,
			AgentImage: t.AgentImage.String,
			Agent:      t.Agent.String,
			ProviderID: t.ProviderID.String,
			ModelID:    t.ModelID.String,
			VariantID:  t.VariantID.String,
			TimeoutSec: int(t.TimeoutSec),
			GitURL:     t.GitUrl.String,
			GitRef:     t.GitRef.String,
			Skills:     skills,
			Event:      ev,
		}
	}
	return out, nil
}

// triggerEventFromConfig extracts the "event" field from a trigger_config JSON.
func triggerEventFromConfig(cfg json.RawMessage) string {
	var parsed struct {
		Event string `json:"event"`
	}
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		return ""
	}
	return parsed.Event
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

func (s *Service) LogAuditEvent(ctx context.Context, params AuditEventParams) error {
	id, err := randomID("aud")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.repo.InsertAuditLog(ctx, repository.InsertAuditLogParams{
		ID:               id,
		EventType:        params.EventType,
		CreatedAt:        now,
		SourceType:       nullString(params.SourceType),
		SourceID:         nullString(params.SourceID),
		TargetType:       nullString(params.TargetType),
		TargetID:         nullString(params.TargetID),
		Repo:             nullString(params.Repo),
		GithubEvent:      nullString(params.GitHubEvent),
		GithubAction:     nullString(params.GitHubAction),
		GithubDeliveryID: nullString(params.GitHubDeliveryID),
		ParentEventID:    nullString(params.ParentEventID),
		Detail:           nullString(params.Detail),
		Payload:          (*json.RawMessage)(&params.Payload),
	})
}

func (s *Service) RecordArtifact(ctx context.Context, params RecordArtifactParams) error {
	id, err := randomID("art")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	number := sql.NullInt32{}
	if params.Number > 0 {
		number = sql.NullInt32{Int32: int32(params.Number), Valid: true}
	}
	return s.repo.InsertTaskArtifact(ctx, repository.InsertTaskArtifactParams{
		ID:              id,
		TaskID:          params.TaskID,
		ArtifactType:    params.ArtifactType,
		Repo:            params.Repo,
		Number:          number,
		Url:             nullString(params.URL),
		Ref:             nullString(params.Ref),
		Sha:             nullString(params.SHA),
		CreatedAt:       now,
		DiscoveredAt:    now,
		DiscoverySource: params.DiscoverySource,
	})
}
