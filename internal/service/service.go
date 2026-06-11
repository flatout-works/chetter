// Package service contains devfleet orchestration and MCP tool handlers.
package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/flatout-works/devfleet/internal/bus"
	"github.com/flatout-works/devfleet/internal/config"
	"github.com/flatout-works/devfleet/internal/store"
	"github.com/nats-io/nats.go"
	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

// TaskRequest is the JSON shape consumed by the existing runner.
type TaskRequest struct {
	TaskID      string            `json:"task_id"`
	AgentImage  string            `json:"agent_image"`
	Prompt      string            `json:"prompt,omitempty"`
	Command     []string          `json:"command,omitempty"`
	GitURL      string            `json:"git_url,omitempty"`
	GitRef      string            `json:"git_ref,omitempty"`
	Agent       string            `json:"agent,omitempty"`
	ProviderID  string            `json:"provider_id,omitempty"`
	ModelID     string            `json:"model_id,omitempty"`
	VariantID   string            `json:"variant_id,omitempty"`
	Skills      []string          `json:"skills,omitempty"`
	TimeoutSec  int               `json:"timeout_sec"`
	MaxMemoryMB int               `json:"max_memory_mb"`
	MaxCPU      int               `json:"max_cpu"`
	Env         map[string]string `json:"env,omitempty"`
}

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
	reaperInterval          = 5 * time.Minute
	reaperGrace             = 5 * time.Minute
	pendingReapGrace        = 30 * time.Minute
	reaperHealthMaxEventSec = 600
	runnerPresenceMaxSec    = 120
)

var defaultCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

type Service struct {
	cfg         config.Config
	store       *store.Store
	bus         *bus.Client
	arcane      *ArcaneClient
	cron        *cron.Cron
	cronMu      sync.Mutex
	cronEntries map[string]cron.EntryID
	reaperStop  chan struct{}
}

func New(cfg config.Config, st *store.Store, nc *bus.Client) *Service {
	svc := &Service{
		cfg:         cfg,
		store:       st,
		bus:         nc,
		cron:        cron.New(cron.WithParser(defaultCronParser), cron.WithLocation(time.UTC)),
		cronEntries: make(map[string]cron.EntryID),
		reaperStop:  make(chan struct{}),
	}
	if cfg.ArcaneServerURL != "" && cfg.ArcaneAPIKey != "" {
		svc.arcane = NewArcaneClient(cfg.ArcaneServerURL, cfg.ArcaneAPIKey)
	}
	return svc
}

// Start subscribes to task events, loads schedules, starts cron, and starts
// the stale-task reaper.
func (s *Service) Start(ctx context.Context) error {
	if _, err := s.bus.SubscribeEvents(s.handleEvent); err != nil {
		return fmt.Errorf("subscribe events: %w", err)
	}
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
// them as error so they do not stay as zombie "running" rows forever. It also
// cancels pending tasks that have not been picked up within pendingReapGrace.
func (s *Service) taskReaper() {
	s.reapStaleTasks()
	s.reapStalePending()
	ticker := time.NewTicker(reaperInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.reapStaleTasks()
			s.reapStalePending()
		case <-s.reaperStop:
			return
		}
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

func (s *Service) reapStalePending() {
	ctx, cancel := context.WithTimeout(context.Background(), eventHandlerTimeout)
	defer cancel()
	n, err := s.store.ReapStalePendingTasks(ctx, pendingReapGrace)
	if err != nil {
		slog.Error("pending task reaper failed", "error", err)
		return
	}
	if n > 0 {
		slog.Info("reaped stale pending tasks", "count", n)
	}
}

// SubmitTask stores and publishes a task.
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
	if err := s.store.InsertTask(ctx, store.TaskInput{
		ID:         taskID,
		Prompt:     in.Prompt,
		GitURL:     in.GitURL,
		GitRef:     in.GitRef,
		AgentImage: in.AgentImage,
		Agent:      in.Agent,
		ProviderID: in.ProviderID,
		ModelID:    in.ModelID,
		VariantID:  in.VariantID,
		Skills:     in.Skills,
		Env:        sanitizeTaskEnv(in.Env),
		TimeoutSec: in.TimeoutSec,
	}); err != nil {
		return store.TaskRecord{}, fmt.Errorf("insert task: %w", err)
	}
	payload, err := json.Marshal(TaskRequest{
		TaskID:      taskID,
		AgentImage:  in.AgentImage,
		Prompt:      in.Prompt,
		GitURL:      in.GitURL,
		GitRef:      in.GitRef,
		Agent:       in.Agent,
		ProviderID:  in.ProviderID,
		ModelID:     in.ModelID,
		VariantID:   in.VariantID,
		Skills:      in.Skills,
		TimeoutSec:  in.TimeoutSec,
		MaxMemoryMB: defaultMaxMemoryMB,
		MaxCPU:      defaultMaxCPU,
		Env:         in.Env,
	})
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("marshal task: %w", err)
	}
	if err := s.bus.PublishTask(payload); err != nil {
		return store.TaskRecord{}, fmt.Errorf("publish task: %w", err)
	}
	slog.Info("task published to NATS", "task_id", taskID, "subject", s.cfg.TaskSubject)
	return s.store.GetTask(ctx, taskID)
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
	record, err := s.store.CreateSchedule(ctx, in)
	if err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("create schedule: %w", err)
	}
	if err := s.activateSchedule(ctx, record); err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("activate schedule: %w", err)
	}
	return s.store.GetSchedule(ctx, record.ID)
}

// ScheduleFile is the YAML shape for declarative schedule definitions.
type ScheduleFile struct {
	Name       string   `yaml:"name"`
	Enabled    bool     `yaml:"enabled"`
	CronExpr   string   `yaml:"cron_expr"`
	Prompt     string   `yaml:"prompt"`
	GitURL     string   `yaml:"git_url,omitempty"`
	GitRef     string   `yaml:"git_ref,omitempty"`
	AgentImage string   `yaml:"agent_image,omitempty"`
	Agent      string   `yaml:"agent,omitempty"`
	ProviderID string   `yaml:"provider_id,omitempty"`
	ModelID    string   `yaml:"model_id,omitempty"`
	VariantID  string   `yaml:"variant_id,omitempty"`
	Skills     []string `yaml:"skills,omitempty"`
	TimeoutSec int      `yaml:"timeout_sec,omitempty"`
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
	// Deactivate old cron entry before updating DB.
	s.cronMu.Lock()
	existing, err := s.store.GetScheduleByName(ctx, name)
	if err == nil {
		if entryID, ok := s.cronEntries[existing.ID]; ok {
			s.cron.Remove(entryID)
			delete(s.cronEntries, existing.ID)
		}
	}
	s.cronMu.Unlock()
	record, err := s.store.UpdateSchedule(ctx, name, in, enabled)
	if err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("update schedule: %w", err)
	}
	if err := s.activateSchedule(ctx, record); err != nil {
		return store.ScheduleRecord{}, fmt.Errorf("reactivate schedule: %w", err)
	}
	return record, nil
}

// SyncSchedulesResult describes what changed during a schedule sync.
type SyncSchedulesResult struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
	Deleted int `json:"deleted,omitempty"`
}

// SyncSchedules reads schedule YAML files from a directory, upserts them,
// and optionally deletes DB schedules not present in the YAML set.
func (s *Service) SyncSchedules(ctx context.Context, dir string, prune bool) (SyncSchedulesResult, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return SyncSchedulesResult{}, fmt.Errorf("read schedule dir %q: %w", dir, err)
	}
	var files []ScheduleFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".yaml") && !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return SyncSchedulesResult{}, fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		var sf ScheduleFile
		if err := yaml.Unmarshal(data, &sf); err != nil {
			return SyncSchedulesResult{}, fmt.Errorf("parse %s: %w", entry.Name(), err)
		}
		if sf.Name == "" || sf.CronExpr == "" || sf.Prompt == "" {
			return SyncSchedulesResult{}, fmt.Errorf("%s: name, cron_expr, and prompt are required", entry.Name())
		}
		files = append(files, sf)
	}
	var result SyncSchedulesResult
	seen := make(map[string]bool, len(files))
	for _, sf := range files {
		seen[sf.Name] = true
		in := store.ScheduleInput{
			Name:       sf.Name,
			CronExpr:   sf.CronExpr,
			Prompt:     sf.Prompt,
			GitURL:     sf.GitURL,
			GitRef:     sf.GitRef,
			AgentImage: sf.AgentImage,
			Agent:      sf.Agent,
			ProviderID: sf.ProviderID,
			ModelID:    sf.ModelID,
			VariantID:  sf.VariantID,
			Skills:     sf.Skills,
			TimeoutSec: sf.TimeoutSec,
		}
		_, err := s.store.GetScheduleByName(ctx, sf.Name)
		if err == sql.ErrNoRows {
			if _, err := s.CreateSchedule(ctx, in); err != nil {
				return SyncSchedulesResult{}, fmt.Errorf("create %q: %w", sf.Name, err)
			}
			result.Created++
		} else if err != nil {
			return SyncSchedulesResult{}, fmt.Errorf("lookup %q: %w", sf.Name, err)
		} else {
			if _, err := s.UpdateSchedule(ctx, sf.Name, in, sf.Enabled); err != nil {
				return SyncSchedulesResult{}, fmt.Errorf("update %q: %w", sf.Name, err)
			}
			result.Updated++
		}
	}
	if prune {
		all, err := s.store.ListSchedules(ctx, false)
		if err != nil {
			return SyncSchedulesResult{}, fmt.Errorf("list schedules: %w", err)
		}
		for _, rec := range all {
			if !seen[rec.Name] {
				if err := s.DeleteSchedule(ctx, rec.Name); err != nil {
					return SyncSchedulesResult{}, fmt.Errorf("delete %q: %w", rec.Name, err)
				}
				result.Deleted++
			}
		}
	}
	return result, nil
}

// DeleteSchedule removes a schedule by name and stops its cron job.
func (s *Service) DeleteSchedule(ctx context.Context, name string) error {
	schedules, err := s.store.ListSchedules(ctx, false)
	if err != nil {
		return fmt.Errorf("list schedules: %w", err)
	}
	var target *store.ScheduleRecord
	for i := range schedules {
		if schedules[i].Name == name {
			target = &schedules[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("schedule %q not found", name)
	}
	s.cronMu.Lock()
	if entryID, ok := s.cronEntries[target.ID]; ok {
		s.cron.Remove(entryID)
		delete(s.cronEntries, target.ID)
	}
	s.cronMu.Unlock()
	if err := s.store.DeleteSchedule(ctx, name); err != nil {
		return fmt.Errorf("delete schedule: %w", err)
	}
	return nil
}

// RunScheduleNow submits a task from a named schedule immediately.
func (s *Service) RunScheduleNow(ctx context.Context, name string) (store.TaskRecord, error) {
	if name == "" {
		return store.TaskRecord{}, fmt.Errorf("name is required")
	}
	schedules, err := s.store.ListSchedules(ctx, false)
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("list schedules: %w", err)
	}
	var target *store.ScheduleRecord
	for i := range schedules {
		if schedules[i].Name == name {
			target = &schedules[i]
			break
		}
	}
	if target == nil {
		return store.TaskRecord{}, fmt.Errorf("schedule %q not found", name)
	}
	return s.submitScheduleTask(ctx, *target, time.Now().UTC())
}

func (s *Service) loadSchedules(ctx context.Context) error {
	schedules, err := s.store.ListSchedules(ctx, true)
	if err != nil {
		return fmt.Errorf("load schedules: %w", err)
	}
	for _, schedule := range schedules {
		if err := s.activateSchedule(ctx, schedule); err != nil {
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
		if err := s.store.SetScheduleNextRun(ctx, schedule.ID, entry.Next); err != nil {
			return fmt.Errorf("set schedule next run: %w", err)
		}
	}
	return nil
}

func (s *Service) runSchedule(ctx context.Context, scheduleID string, scheduledFor time.Time) error {
	schedule, err := s.store.GetSchedule(ctx, scheduleID)
	if err != nil {
		return fmt.Errorf("get schedule %s: %w", scheduleID, err)
	}
	_, err = s.submitScheduleTask(ctx, schedule, scheduledFor)
	return err
}

func (s *Service) submitScheduleTask(ctx context.Context, schedule store.ScheduleRecord, scheduledFor time.Time) (store.TaskRecord, error) {
	task, err := s.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:     schedule.Prompt,
		GitURL:     schedule.GitURL,
		GitRef:     schedule.GitRef,
		AgentImage: schedule.AgentImage,
		Agent:      schedule.Agent,
		ProviderID: schedule.ProviderID,
		ModelID:    schedule.ModelID,
		VariantID:  schedule.VariantID,
		Skills:     schedule.Skills,
		TimeoutSec: schedule.TimeoutSec,
	})
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("submit scheduled task: %w", err)
	}
	runID, err := randomID("run")
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("generate run id: %w", err)
	}
	if err := s.store.InsertScheduleRun(ctx, runID, schedule.ID, task.ID, "submitted", scheduledFor); err != nil {
		return store.TaskRecord{}, fmt.Errorf("insert schedule run: %w", err)
	}
	if entryID, ok := s.cronEntries[schedule.ID]; ok {
		entry := s.cron.Entry(entryID)
		if !entry.Next.IsZero() {
			if err := s.store.SetScheduleNextRun(ctx, schedule.ID, entry.Next); err != nil {
				return store.TaskRecord{}, err
			}
		}
	}
	return task, nil
}

func (s *Service) handleEvent(msg *nats.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), eventHandlerTimeout)
	defer cancel()

	if isRunnerHeartbeatSubject(msg.Subject) {
		var heartbeat store.RunnerHeartbeat
		if err := json.Unmarshal(msg.Data, &heartbeat); err != nil {
			slog.Warn("ignored malformed runner heartbeat", "subject", msg.Subject, "err", err)
			ackJetStreamMsg(msg)
			return
		}
		if heartbeat.RunnerID == "" {
			slog.Warn("ignored runner heartbeat without runner_id", "subject", msg.Subject)
			ackJetStreamMsg(msg)
			return
		}
		if err := s.store.UpsertRunnerHeartbeat(ctx, heartbeat); err != nil {
			slog.Error("persist runner heartbeat", "runnerID", heartbeat.RunnerID, "error", err)
			return
		}
		ackJetStreamMsg(msg)
		return
	}

	var resp store.TaskResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		slog.Warn("ignored malformed task event", "subject", msg.Subject, "err", err)
		ackJetStreamMsg(msg)
		return
	}
	eventID, err := randomID("evt")
	if err != nil {
		slog.Error("generate event id", "error", err)
		return
	}
	if err := s.store.InsertEvent(ctx, eventID, resp.TaskID, msg.Subject, resp.Status, msg.Data); err != nil {
		slog.Error("persist task event", "taskID", resp.TaskID, "error", err)
		return
	}
	if err := s.store.UpdateTaskFromResponse(ctx, resp); err != nil {
		slog.Error("update task from event", "taskID", resp.TaskID, "error", err)
		return
	}
	ackJetStreamMsg(msg)
}

func isRunnerHeartbeatSubject(subject string) bool {
	return strings.Contains(subject, ".runners.") && strings.HasSuffix(subject, ".heartbeat")
}

func ackJetStreamMsg(msg *nats.Msg) {
	if _, err := msg.Metadata(); err != nil {
		return
	}
	if err := msg.Ack(); err != nil {
		slog.Warn("ack event", "err", err)
	}
}

func randomID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(raw[:]), nil
}
