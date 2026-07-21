// Package service contains chetter orchestration and MCP tool handlers.
package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/config"
	"github.com/flatout-works/chetter/internal/data"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/store"
	"github.com/flatout-works/chetter/internal/webhook"
	"github.com/flatout-works/chetter/pkg/definitions"
	"github.com/robfig/cron/v3"
)

// SubmitTaskRequest contains all fields needed to submit a runner task.
type SubmitTaskRequest struct {
	TeamID           string
	TeamName         string
	Prompt           string
	GitURL           string
	GitRef           string
	AgentImage       string
	Agent            string
	ProviderID       string
	ModelID          string
	VariantID        string
	Harness          string
	Skills           []string
	McpEndpoints     []string
	Env              map[string]string
	TimeoutSec       int
	TriggerName      string
	TriggerType      string
	SubmissionSource string
	SessionMode      string
	PauseReason      string
	TTLHours         int
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
	TokenID          string
	TokenName        string
}

type RecordArtifactParams struct {
	TaskID          string
	AgentSessionID  string
	SessionRunID    string
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
	triggerRunTimeout       = 30 * time.Second
	eventHandlerTimeout     = 10 * time.Second
	reaperInterval          = 30 * time.Second
	definitionsSyncInterval = 5 * time.Minute
	definitionsSyncTimeout  = 2 * time.Minute
	reaperGrace             = 120 * time.Second
	reaperHealthMaxEventSec = 120
	runnerPresenceMaxSec    = 60
)

var defaultCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

type Service struct {
	cfg            config.Config
	store          *store.Store
	repo           data.Repository
	rawDB          *sql.DB
	dialect        store.Dialect
	arcane         *ArcaneClient
	github         *webhook.Client
	runnerRPC      *RunnerRPCService
	cron           *cron.Cron
	cronMu         sync.Mutex
	cronEntries    map[string]cron.EntryID
	reaperStop     chan struct{}
	reaperSteps    []func()
	definitions    *definitions.Manager
	quotaExhausted atomic.Bool
	lastReapAt     atomic.Int64
}

func (s *Service) QuotaExhausted() bool {
	return s.quotaExhausted.Load()
}

func isQuotaExhaustedError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "quota being exhausted") ||
		strings.Contains(msg, "access has been restricted") ||
		strings.Contains(msg, "Error 1105")
}

func (s *Service) checkDBQuota() {
	ctx, cancel := context.WithTimeout(context.Background(), eventHandlerTimeout)
	defer cancel()
	err := s.rawDB.PingContext(ctx)
	if err != nil {
		if isQuotaExhaustedError(err) {
			if !s.quotaExhausted.Swap(true) {
				slog.Warn("database quota exhausted")
			}
		}
		return
	}
	if s.quotaExhausted.Swap(false) {
		slog.Info("database quota restored")
	}
}

func (s *Service) SetRunnerRPC(r *RunnerRPCService) {
	s.runnerRPC = r
}

func (s *Service) SetGitHubClient(c *webhook.Client) {
	s.github = c
}

func (s *Service) SetDefinitions(d *definitions.Manager) {
	s.definitions = d
}

func New(cfg config.Config, st *store.Store) *Service {
	svc := &Service{
		cfg:         cfg,
		store:       st,
		repo:        data.New(st.DB(), st.Dialect()),
		rawDB:       st.DB(),
		dialect:     st.Dialect(),
		cron:        cron.New(cron.WithParser(defaultCronParser), cron.WithLocation(time.UTC)),
		cronEntries: make(map[string]cron.EntryID),
		reaperStop:  make(chan struct{}),
	}
	if cfg.ArcaneServerURL != "" && cfg.ArcaneAPIKey != "" {
		svc.arcane = NewArcaneClient(cfg.ArcaneServerURL, cfg.ArcaneAPIKey)
	}
	svc.reaperSteps = []func(){
		svc.reapStaleTasks,
		svc.reapExpiredLeases,
		svc.reapStaleSessionRuns,
		svc.reapUnavailablePinnedResumeTasks,
		svc.reapExpiredSessions,
		svc.checkDBQuota,
		func() {
			if svc.runnerRPC != nil {
				svc.runnerRPC.cleanupHeartbeatSeen()
			}
		},
	}
	return svc
}

// Start loads triggers, starts cron, and starts the stale-task reaper.
func (s *Service) Start(ctx context.Context) error {
	s.cron.Start()
	if err := s.loadTriggers(ctx); err != nil {
		return err
	}
	go s.taskReaper()
	if s.definitions != nil {
		go s.definitionsSyncLoop()
	}
	return nil
}

// Stop stops the cron scheduler and the reaper.
func (s *Service) Stop() {
	close(s.reaperStop)
	ctx := s.cron.Stop()
	<-ctx.Done()
}

// taskReaper periodically scans for tasks that have been running without a
// heartbeat for longer than their configured timeout + grace period and marks
// them as error so they do not stay as zombie "running" rows forever.
func (s *Service) taskReaper() {
	s.runReaperCycle()
	ticker := time.NewTicker(reaperInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.runReaperCycle()
		case <-s.reaperStop:
			return
		}
	}
}

// runReaperCycle executes one pass of the reaper. A panic in any step is
// recovered and logged so a single bad cycle never kills the reaper goroutine;
// the loop continues on the next tick.
func (s *Service) runReaperCycle() {
	defer s.recoverReaperPanic()
	for _, step := range s.reaperSteps {
		step()
	}
	s.lastReapAt.Store(time.Now().UnixNano())
}

func (s *Service) recoverReaperPanic() {
	if r := recover(); r != nil {
		slog.Error("reaper panic recovered", "panic", r, "stack", string(debug.Stack()))
	}
}

func (s *Service) LastReapAt() time.Time {
	if v := s.lastReapAt.Load(); v != 0 {
		return time.Unix(0, v).UTC()
	}
	return time.Time{}
}

func (s *Service) definitionsSyncLoop() {
	ticker := time.NewTicker(definitionsSyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), definitionsSyncTimeout)
			if _, err := s.SyncDefinitions(ctx); err != nil {
				slog.Warn("periodic definitions sync failed", "err", err)
			}
			cancel()
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
		if isQuotaExhaustedError(err) {
			s.quotaExhausted.Store(true)
		}
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
		if isQuotaExhaustedError(err) {
			s.quotaExhausted.Store(true)
		}
		return
	}
	if reclaimed > 0 || failed > 0 {
		slog.Info("reaped expired task leases", "reclaimed", reclaimed, "failed", failed)
	}
}

func (s *Service) reapUnavailablePinnedResumeTasks() {
	ctx, cancel := context.WithTimeout(context.Background(), eventHandlerTimeout)
	defer cancel()
	now := time.Now().UTC()
	failedTasks, err := s.repo.FailPendingResumeTasksForMissingRunner(ctx, repository.FailPendingResumeTasksForMissingRunnerParams{
		EndedAt:      sql.NullTime{Time: now, Valid: true},
		UpdatedAt:    now,
		LastEventAt:  sql.NullTime{Time: now, Valid: true},
		StaleSeconds: runnerPresenceMaxSec,
	})
	if err != nil {
		slog.Error("pinned resume reaper task failure failed", "error", err)
		if isQuotaExhaustedError(err) {
			s.quotaExhausted.Store(true)
		}
		return
	}
	failedRuns, err := s.repo.FailPendingSessionRunsForUnavailableRunner(ctx, repository.FailPendingSessionRunsForUnavailableRunnerParams{
		EndedAt:   sql.NullTime{Time: now, Valid: true},
		UpdatedAt: now,
	})
	if err != nil {
		slog.Error("pinned resume reaper run failure failed", "error", err)
		if isQuotaExhaustedError(err) {
			s.quotaExhausted.Store(true)
		}
		return
	}
	failedSessions, err := s.repo.MarkResumingSessionsFailedForUnavailableRunner(ctx, now)
	if err != nil {
		slog.Error("pinned resume reaper session failure failed", "error", err)
		if isQuotaExhaustedError(err) {
			s.quotaExhausted.Store(true)
		}
		return
	}
	if failedTasks > 0 || failedRuns > 0 || failedSessions > 0 {
		slog.Info("failed pinned resume work for unavailable runners", "tasks", failedTasks, "runs", failedRuns, "sessions", failedSessions)
	}
}

func (s *Service) reapStaleTasks() {
	ctx, cancel := context.WithTimeout(context.Background(), eventHandlerTimeout)
	defer cancel()
	n, err := s.store.ReapStaleTasks(ctx, reaperGrace)
	if err != nil {
		slog.Error("task reaper failed", "error", err)
		if isQuotaExhaustedError(err) {
			s.quotaExhausted.Store(true)
		}
		return
	}
	if n > 0 {
		slog.Info("reaped stale tasks", "count", n)
	}
}

func (s *Service) reapStaleSessionRuns() {
	ctx, cancel := context.WithTimeout(context.Background(), eventHandlerTimeout)
	defer cancel()
	n, err := s.repo.ReapStaleSessionRuns(ctx)
	if err != nil {
		slog.Error("session run reaper failed", "error", err)
		if isQuotaExhaustedError(err) {
			s.quotaExhausted.Store(true)
		}
		return
	}
	if n > 0 {
		slog.Info("reaped stale session runs", "count", n)
	}
	o, err := s.repo.RevertOrphanedRunningSessionRuns(ctx)
	if err != nil {
		slog.Error("revert orphaned session runs failed", "error", err)
		if isQuotaExhaustedError(err) {
			s.quotaExhausted.Store(true)
		}
		return
	}
	if o > 0 {
		slog.Info("reverted orphaned session runs to pending", "count", o)
	}
	m, err := s.repo.ReapStaleSessionsForTerminalRuns(ctx)
	if err != nil {
		slog.Error("session reaper for terminal runs failed", "error", err)
		if isQuotaExhaustedError(err) {
			s.quotaExhausted.Store(true)
		}
		return
	}
	if m > 0 {
		slog.Info("reaped stale agent sessions for terminal runs", "count", m)
	}
}

func (s *Service) reapExpiredSessions() {
	ctx, cancel := context.WithTimeout(context.Background(), eventHandlerTimeout)
	defer cancel()
	now := time.Now().UTC()
	n, err := s.repo.ExpirePausedSessions(ctx, repository.ExpirePausedSessionsParams{
		UpdatedAt: now,
		ExpiresAt: sql.NullTime{Time: now, Valid: true},
	})
	if err != nil {
		slog.Error("session expiration failed", "error", err)
		if isQuotaExhaustedError(err) {
			s.quotaExhausted.Store(true)
		}
		return
	}
	if n > 0 {
		slog.Info("expired paused sessions", "count", n)
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
	in.AgentImage = s.resolveAgentImage(in.AgentImage)
	if in.TimeoutSec == 0 {
		in.TimeoutSec = s.cfg.DefaultTaskTimeoutSec
	}
	teamID, err := s.resolveOwnerTeamID(ctx, in.TeamID, in.TeamName)
	if err != nil {
		return store.TaskRecord{}, err
	}
	var gitIdentity GitIdentityRecord
	if strings.TrimSpace(in.Agent) != "" {
		gitIdentity, err = s.resolveTaskGitIdentity(ctx, in.Agent, teamID, in.GitURL)
		if err != nil {
			return store.TaskRecord{}, err
		}
	} else {
		gitIdentity, err = s.defaultGitIdentity(ctx, teamID)
		if err != nil {
			return store.TaskRecord{}, err
		}
	}
	taskID, err := randomID("task")
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("generate task id: %w", err)
	}
	in.Prompt = expandChetterPromptVars(in.Prompt, map[string]string{
		"CHETTER_AGENT_NAME":          in.Agent,
		"CHETTER_MODEL_ID":            in.ModelID,
		"CHETTER_TASK_ID":             taskID,
		"CHETTER_RUNNER_IMAGE":        in.AgentImage,
		"CHETTER_RUNNER_IMAGE_DIGEST": "unknown",
	})
	sessionID, err := randomID("sess")
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("generate session id: %w", err)
	}
	runID, err := randomID("run")
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("generate session run id: %w", err)
	}
	now := time.Now().UTC()
	submissionSource := in.SubmissionSource
	if submissionSource == "" {
		submissionSource = "manual"
	}
	skills, err := json.Marshal(nonEmptyStrings(in.Skills))
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("marshal skills: %w", err)
	}
	endpointNames := normalizeMcpEndpointNames(in.McpEndpoints)
	if len(endpointNames) > 0 {
		if in.SessionMode == "resumable" {
			return store.TaskRecord{}, fmt.Errorf("mcp_endpoints cannot be attached to resumable tasks")
		}
		if _, err := loadMcpEndpoints(ctx, s.rawDB, s.dialect, endpointNames, teamID); err != nil {
			return store.TaskRecord{}, err
		}
	}
	mcpEndpoints, err := json.Marshal(endpointNames)
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("marshal mcp_endpoints: %w", err)
	}
	taskEnv := sanitizeTaskEnv(in.Env)
	if in.Harness != "" {
		taskEnv["__chetter_harness"] = in.Harness
	}
	env, err := json.Marshal(taskEnv)
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("marshal env: %w", err)
	}
	resumeMode := "none"
	pauseReason := ""
	var expiresAt sql.NullTime
	checkpointAfterSuccess := false
	if in.SessionMode == "resumable" {
		resumeMode = "harness_session"
		checkpointAfterSuccess = true
		if in.PauseReason != "" {
			pauseReason = in.PauseReason
		}
		if in.TTLHours > 0 {
			expiresAt = sql.NullTime{Time: now.Add(time.Duration(in.TTLHours) * time.Hour), Valid: true}
		} else {
			expiresAt = sql.NullTime{Time: now.Add(72 * time.Hour), Valid: true}
		}
	}
	var task repository.ChetterTask
	err = withTxRetry(ctx, s.rawDB, s.dialect, func(q data.Repository) error {
		taskSearchText := strings.Join(strings.Fields(in.Prompt+" "+in.Agent+" "+in.ModelID+" "+in.TriggerName+" "+in.GitURL), " ")
		if err := q.InsertTask(ctx, repository.InsertTaskParams{
			ID:                     taskID,
			TeamID:                 nullString(teamID),
			Prompt:                 in.Prompt,
			GitUrl:                 nullString(in.GitURL),
			GitRef:                 nullString(in.GitRef),
			AgentImage:             nullString(in.AgentImage),
			Agent:                  nullString(in.Agent),
			ProviderID:             nullString(in.ProviderID),
			ModelID:                nullString(in.ModelID),
			VariantID:              nullString(in.VariantID),
			CommitAuthorName:       sql.NullString{String: gitIdentity.GitAuthorName, Valid: true},
			CommitAuthorEmail:      sql.NullString{String: gitIdentity.GitAuthorEmail, Valid: true},
			GitIdentityID:          nullString(gitIdentity.ID),
			TriggerName:            nullString(in.TriggerName),
			TriggerType:            nullString(in.TriggerType),
			SubmissionSource:       submissionSource,
			CheckpointAfterSuccess: checkpointAfterSuccess,
			Skills:                 skills,
			McpEndpoints:           nullableJSON(mcpEndpoints),
			Env:                    env,
			TimeoutSec:             int32(in.TimeoutSec),
			SearchText:             nullString(taskSearchText),
			CreatedAt:              now,
			UpdatedAt:              now,
		}); err != nil {
			return fmt.Errorf("insert task: %w", err)
		}
		sessionSearchText := strings.Join(strings.Fields(sessionID+" "+in.Agent+" "+in.ModelID+" "+in.GitURL), " ")
		if err := q.InsertAgentSession(ctx, repository.InsertAgentSessionParams{
			ID:          sessionID,
			TeamID:      nullString(teamID),
			Status:      "running",
			ResumeMode:  resumeMode,
			PauseReason: nullString(pauseReason),
			ExpiresAt:   expiresAt,
			GitUrl:      nullString(in.GitURL),
			GitRef:      nullString(in.GitRef),
			AgentImage:  nullString(in.AgentImage),
			Agent:       nullString(in.Agent),
			ProviderID:  nullString(in.ProviderID),
			ModelID:     nullString(in.ModelID),
			VariantID:   nullString(in.VariantID),
			SearchText:  nullString(sessionSearchText),
			CreatedAt:   now,
			UpdatedAt:   now,
		}); err != nil {
			return fmt.Errorf("insert agent session: %w", err)
		}
		if err := q.InsertSessionRun(ctx, repository.InsertSessionRunParams{
			ID:               runID,
			AgentSessionID:   sessionID,
			TaskID:           taskID,
			Status:           "pending",
			Prompt:           in.Prompt,
			RequiredRunnerID: sql.NullString{},
			CreatedAt:        now,
			UpdatedAt:        now,
		}); err != nil {
			return fmt.Errorf("insert session run: %w", err)
		}
		row, err := q.GetTaskByID(ctx, taskID)
		if err != nil {
			return fmt.Errorf("get task: %w", err)
		}
		task = row
		return nil
	})
	if err != nil {
		return store.TaskRecord{}, err
	}
	slog.Info("task queued", "task_id", taskID, "agent_session_id", sessionID, "session_run_id", runID)
	if in.TriggerName != "" {
		trigger, err := s.repo.GetTriggerByName(ctx, in.TriggerName)
		if err != nil {
			slog.Warn("failed to lookup trigger for run recording", "trigger", in.TriggerName, "task", taskID, "err", err)
		} else {
			runID, err := randomID("run")
			if err != nil {
				slog.Warn("failed to generate trigger run ID", "trigger", in.TriggerName, "task", taskID, "err", err)
			} else {
				if err := s.repo.InsertTriggerRun(ctx, repository.InsertTriggerRunParams{
					ID:          runID,
					TriggerID:   trigger.ID,
					TeamID:      nullString(teamID),
					TaskID:      taskID,
					Status:      "submitted",
					TriggeredAt: now,
					CreatedAt:   now,
				}); err != nil {
					slog.Warn("failed to insert trigger run", "trigger", in.TriggerName, "task", taskID, "err", err)
				}
				s.repo.SetTriggerLastRun(ctx, repository.SetTriggerLastRunParams{
					LastRunAt: sql.NullTime{Time: now, Valid: true},
					UpdatedAt: now,
					ID:        trigger.ID,
				})
			}
		}
		s.auditAsync(ctx, AuditEventParams{
			EventType:  "trigger_run",
			SourceType: in.TriggerType,
			SourceID:   in.TriggerName,
			TargetType: "task",
			TargetID:   taskID,
			Detail:     fmt.Sprintf("trigger %q ran, task %s created", in.TriggerName, taskID),
		})
	} else {
		s.auditAsync(ctx, AuditEventParams{
			EventType:  "task_submitted",
			SourceType: submissionSource,
			TargetType: "task",
			TargetID:   taskID,
			Detail:     fmt.Sprintf("task submitted: agent=%s model=%s prompt=%.100s", in.Agent, in.ModelID, in.Prompt),
		})
	}
	return repoTaskToStoreRecord(task), nil
}

// RecoverTask creates a new task from a failed task's configuration, including
// the previous session export as a recovery file so the agent can pick up where
// it left off.
func (s *Service) RecoverTask(ctx context.Context, taskID string) (TaskToolRecord, error) {
	orig, err := s.taskForToolAccess(ctx, taskID)
	if err != nil {
		return TaskToolRecord{}, fmt.Errorf("get original task: %w", err)
	}
	if orig.Status != "error" && orig.Status != "done" && orig.Status != "cancelled" {
		return TaskToolRecord{}, fmt.Errorf("task %s is %s, not a terminal state", taskID, orig.Status)
	}
	exportContent := ""
	if orig.SessionExport.Valid {
		exportContent = strings.ReplaceAll(orig.SessionExport.String, "\\n", "\n")
	}
	if exportContent == "" {
		return TaskToolRecord{}, fmt.Errorf("no session export available for task %s", taskID)
	}

	skills := parseJSON[[]string](orig.Skills, "task:"+taskID+" skills")
	_ = skills
	env := parseJSON[map[string]string](orig.Env, "task:"+taskID+" env")
	_ = env

	recoveryFileName := fmt.Sprintf("chetter_recovery_%s.md", taskID)
	recoveryPrompt := fmt.Sprintf(
		"The file %s in the workspace is the complete transcript of a previous session that attempted this work but did not succeed. "+
			"Please review the previous session thoroughly, understand what was accomplished and what went wrong, then finish the work. "+
			"Use the context from the previous session to continue efficiently — you don't need to redo work that was already completed successfully.\n\n"+
			"Original task:\n%s",
		recoveryFileName, orig.Prompt,
	)

	submitted, err := s.SubmitTask(ctx, recoveryTaskRequest(orig, taskID, recoveryPrompt))
	if err != nil {
		return TaskToolRecord{}, fmt.Errorf("submit recovery task: %w", err)
	}

	s.auditAsync(ctx, AuditEventParams{
		EventType:  "task_recover",
		SourceType: "task",
		SourceID:   taskID,
		TargetType: "task",
		TargetID:   submitted.ID,
		Detail:     fmt.Sprintf("recovery task created from %s", taskID),
	})

	return s.GetTask(ctx, submitted.ID)
}

// recoveryTaskRequest preserves execution dependencies while starting a new
// one-off task, so trigger and resumable-session lifecycle state is not copied.
func recoveryTaskRequest(orig repository.ChetterTask, taskID, prompt string) SubmitTaskRequest {
	env := parseJSON[map[string]string](orig.Env, "task:"+taskID+" env")
	if env == nil {
		env = map[string]string{}
	}
	harness := env["__chetter_harness"]
	delete(env, "__chetter_harness")
	env["__recover_from"] = taskID

	return SubmitTaskRequest{
		TeamID:           orig.TeamID.String,
		Prompt:           prompt,
		GitURL:           orig.GitUrl.String,
		GitRef:           orig.GitRef.String,
		AgentImage:       orig.AgentImage.String,
		Agent:            orig.Agent.String,
		ProviderID:       orig.ProviderID.String,
		ModelID:          orig.ModelID.String,
		VariantID:        orig.VariantID.String,
		Harness:          harness,
		Skills:           parseJSON[[]string](orig.Skills, "task:"+taskID+" skills"),
		McpEndpoints:     parseJSON[[]string](optionalJSON(orig.McpEndpoints), "task:"+taskID+" mcp_endpoints"),
		Env:              env,
		TimeoutSec:       int(orig.TimeoutSec),
		SubmissionSource: "recovery",
	}
}

// ResumeAgentSession creates a follow-up run for a paused or recoverable agent session.
func (s *Service) ResumeAgentSession(ctx context.Context, sessionID, prompt string, timeoutSec int) (ResumeAgentSessionOutput, error) {
	session, err := s.repo.GetAgentSessionByID(ctx, sessionID)
	if err != nil {
		return ResumeAgentSessionOutput{}, fmt.Errorf("get agent session: %w", err)
	}
	if err := authorizeAgentSessionAccess(ctx, session); err != nil {
		return ResumeAgentSessionOutput{}, err
	}
	if session.Status != "paused" && session.Status != "recoverable" && session.Status != "paused_waiting_review" {
		return ResumeAgentSessionOutput{}, fmt.Errorf("agent session is not resumable from status: %s", session.Status)
	}
	if session.ResumeMode != "gvisor_checkpoint" && session.ResumeMode != "harness_session" {
		return ResumeAgentSessionOutput{}, fmt.Errorf("agent session is not resumable (resume_mode: %s)", session.ResumeMode)
	}
	if !session.PinnedRunnerID.Valid || session.PinnedRunnerID.String == "" {
		return ResumeAgentSessionOutput{}, fmt.Errorf("agent session has no pinned runner")
	}
	if !session.WorkspacePath.Valid || session.WorkspacePath.String == "" {
		return ResumeAgentSessionOutput{}, fmt.Errorf("agent session has no workspace path")
	}
	if session.ResumeMode == "gvisor_checkpoint" {
		if !session.CheckpointID.Valid || session.CheckpointID.String == "" {
			return ResumeAgentSessionOutput{}, fmt.Errorf("agent session has no checkpoint")
		}
		chk, err := s.repo.GetLatestAgentSessionCheckpoint(ctx, sessionID)
		if err != nil {
			return ResumeAgentSessionOutput{}, fmt.Errorf("get checkpoint: %w", err)
		}
		if chk.Status != "ready" {
			return ResumeAgentSessionOutput{}, fmt.Errorf("checkpoint not ready (status: %s)", chk.Status)
		}
	} else {
		if !session.HarnessSessionID.Valid || session.HarnessSessionID.String == "" {
			return ResumeAgentSessionOutput{}, fmt.Errorf("agent session has no harness session ID")
		}
	}

	runnerAlive, err := s.repo.IsRunnerAlive(ctx, repository.IsRunnerAliveParams{
		RunnerID:     session.PinnedRunnerID.String,
		StaleSeconds: 120,
	})
	if err != nil {
		return ResumeAgentSessionOutput{}, fmt.Errorf("check runner: %w", err)
	}
	if !runnerAlive {
		return ResumeAgentSessionOutput{}, fmt.Errorf("pinned runner %s is not alive", session.PinnedRunnerID.String)
	}

	taskID, err := randomID("task")
	if err != nil {
		return ResumeAgentSessionOutput{}, fmt.Errorf("generate task id: %w", err)
	}
	runID, err := randomID("run")
	if err != nil {
		return ResumeAgentSessionOutput{}, fmt.Errorf("generate session run id: %w", err)
	}

	now := time.Now().UTC()
	teamID := session.TeamID.String
	gitIdentity, err := s.defaultGitIdentity(ctx, teamID)
	if err != nil {
		return ResumeAgentSessionOutput{}, err
	}
	if session.Agent.Valid && strings.TrimSpace(session.Agent.String) != "" {
		gitIdentity, err = s.resolveTaskGitIdentity(ctx, session.Agent.String, teamID, session.GitUrl.String)
		if err != nil {
			return ResumeAgentSessionOutput{}, err
		}
	}
	if timeoutSec == 0 {
		timeoutSec = s.cfg.DefaultTaskTimeoutSec
	}

	skills := mustMarshalJSON([]string{})
	mcpEndpoints := mustMarshalJSON([]string{})
	env := mustMarshalJSON(map[string]string{})

	var task repository.ChetterTask
	err = withTxRetry(ctx, s.rawDB, s.dialect, func(q data.Repository) error {
		if err := q.InsertTask(ctx, repository.InsertTaskParams{
			ID:                     taskID,
			TeamID:                 nullString(teamID),
			Prompt:                 prompt,
			GitUrl:                 session.GitUrl,
			GitRef:                 session.GitRef,
			AgentImage:             session.AgentImage,
			Agent:                  session.Agent,
			ProviderID:             session.ProviderID,
			ModelID:                session.ModelID,
			VariantID:              session.VariantID,
			CommitAuthorName:       sql.NullString{String: gitIdentity.GitAuthorName, Valid: true},
			CommitAuthorEmail:      sql.NullString{String: gitIdentity.GitAuthorEmail, Valid: true},
			GitIdentityID:          nullString(gitIdentity.ID),
			TriggerName:            sql.NullString{},
			TriggerType:            sql.NullString{},
			SubmissionSource:       "session_resume",
			CheckpointAfterSuccess: true,
			RequiredRunnerID:       sql.NullString{String: session.PinnedRunnerID.String, Valid: true},
			Skills:                 skills,
			McpEndpoints:           nullableJSON(mcpEndpoints),
			Env:                    env,
			TimeoutSec:             int32(timeoutSec),
			CreatedAt:              now,
			UpdatedAt:              now,
		}); err != nil {
			return fmt.Errorf("insert task: %w", err)
		}
		if err := q.InsertSessionRun(ctx, repository.InsertSessionRunParams{
			ID:               runID,
			AgentSessionID:   sessionID,
			TaskID:           taskID,
			Status:           "pending",
			Prompt:           prompt,
			RequiredRunnerID: sql.NullString{String: session.PinnedRunnerID.String, Valid: true},
			CreatedAt:        now,
			UpdatedAt:        now,
		}); err != nil {
			return fmt.Errorf("insert session run: %w", err)
		}
		if _, err := q.MarkAgentSessionResuming(ctx, repository.MarkAgentSessionResumingParams{
			ID:        sessionID,
			Status:    "resuming",
			UpdatedAt: now,
		}); err != nil {
			return fmt.Errorf("mark session resuming: %w", err)
		}
		row, err := q.GetTaskByID(ctx, taskID)
		if err != nil {
			return fmt.Errorf("get task: %w", err)
		}
		task = row
		return nil
	})
	if err != nil {
		return ResumeAgentSessionOutput{}, err
	}

	run, err := s.repo.GetSessionRunByTaskID(ctx, taskID)
	if err != nil {
		return ResumeAgentSessionOutput{}, fmt.Errorf("get session run: %w", err)
	}

	slog.Info("agent session resumed", "session_id", sessionID, "task_id", taskID, "session_run_id", runID)
	s.auditAsync(ctx, AuditEventParams{
		EventType:  "session_resumed",
		SourceType: "api",
		TargetType: "session",
		TargetID:   sessionID,
		SourceID:   taskID,
		Detail:     fmt.Sprintf("session resumed via API: prompt=%.100s", prompt),
	})
	return ResumeAgentSessionOutput{
		Task: taskToolRecord(repoTaskToStoreRecord(task)),
		Run:  sessionRunRecord(run),
	}, nil
}

// ResumeSessionForPR checks if a paused Chetter-authored session exists for a PR
// and enqueues a follow-up run with feedback response.
func (s *Service) ResumeSessionForPR(ctx context.Context, repo string, prNumber int) error {
	session, err := s.repo.GetPausedSessionByArtifact(ctx, repository.GetPausedSessionByArtifactParams{
		Repo:         repo,
		Number:       sql.NullInt32{Int32: int32(prNumber), Valid: true},
		ArtifactType: "pull_request",
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lookup paused session: %w", err)
	}

	runnerAlive, err := s.repo.IsRunnerAlive(ctx, repository.IsRunnerAliveParams{
		RunnerID:     session.PinnedRunnerID.String,
		StaleSeconds: 120,
	})
	if err != nil || !runnerAlive {
		return nil
	}

	prompt := fmt.Sprintf(
		"Your PR #%d in %s received review feedback.\n\n"+
			"Read the PR review comments and review threads using gh.\n"+
			"Address the feedback with the smallest correct changes.\n"+
			"Push updates to the existing branch.\n"+
			"Reply to resolved review comments where appropriate.\n"+
			"Do not open a new PR.",
		prNumber, repo,
	)

	_, err = s.ResumeAgentSession(ctx, session.ID, prompt, 0)
	if err != nil {
		return fmt.Errorf("resume session %s: %w", session.ID, err)
	}
	slog.Info("auto-resumed session for PR feedback", "session_id", session.ID, "repo", repo, "pr", prNumber)
	return nil
}

func repoTaskToStoreRecord(task repository.ChetterTask) store.TaskRecord {
	skills := parseJSON[[]string](task.Skills, "task:"+task.ID+" skills")
	mcpEndpoints := parseJSON[[]string](optionalJSON(task.McpEndpoints), "task:"+task.ID+" mcp_endpoints")
	env := parseJSON[map[string]string](task.Env, "task:"+task.ID+" env")
	var startedAt, endedAt *time.Time
	if task.StartedAt.Valid {
		startedAt = &task.StartedAt.Time
	}
	if task.EndedAt.Valid {
		endedAt = &task.EndedAt.Time
	}
	return store.TaskRecord{
		ID:                    task.ID,
		TeamID:                task.TeamID.String,
		Status:                task.Status,
		Prompt:                task.Prompt,
		GitURL:                task.GitUrl.String,
		GitRef:                task.GitRef.String,
		AgentImage:            task.AgentImage.String,
		Agent:                 task.Agent.String,
		ProviderID:            task.ProviderID.String,
		ModelID:               task.ModelID.String,
		VariantID:             task.VariantID.String,
		OpenCodeSessionID:     task.OpencodeSessionID.String,
		RunnerImageDigest:     task.RunnerImageDigest.String,
		CommitAuthorName:      task.CommitAuthorName.String,
		CommitAuthorEmail:     task.CommitAuthorEmail.String,
		GitIdentityID:         task.GitIdentityID.String,
		TriggerName:           task.TriggerName.String,
		TriggerType:           task.TriggerType.String,
		SubmissionSource:      task.SubmissionSource,
		Skills:                skills,
		McpEndpoints:          mcpEndpoints,
		Env:                   env,
		TimeoutSec:            int(task.TimeoutSec),
		Summary:               task.Summary.String,
		Error:                 task.Error.String,
		ErrorCategory:         task.ErrorCategory.String,
		CreatedAt:             task.CreatedAt,
		UpdatedAt:             task.UpdatedAt,
		StartedAt:             startedAt,
		EndedAt:               endedAt,
		TotalInputTokens:      task.TotalInputTokens,
		TotalOutputTokens:     task.TotalOutputTokens,
		TotalCacheReadTokens:  task.TotalCacheReadTokens,
		TotalCacheWriteTokens: task.TotalCacheWriteTokens,
		TotalReasoningTokens:  task.TotalReasoningTokens,
		CostCents:             task.CostCents,
	}
}

func expandChetterPromptVars(prompt string, values map[string]string) string {
	for _, key := range []string{
		"CHETTER_RUNNER_IMAGE_DIGEST",
		"CHETTER_RUNNER_IMAGE",
		"CHETTER_AGENT_NAME",
		"CHETTER_MODEL_ID",
		"CHETTER_TASK_ID",
	} {
		value := values[key]
		if value == "" {
			value = "unknown"
		}
		prompt = strings.ReplaceAll(prompt, "${"+key+"}", value)
		prompt = strings.ReplaceAll(prompt, "$"+key, value)
	}
	return prompt
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
func (s *Service) CreateTrigger(ctx context.Context, in store.TriggerInput) (store.TriggerRecord, error) {
	if in.Name == "" {
		return store.TriggerRecord{}, fmt.Errorf("name is required")
	}
	if in.TriggerType == "" {
		in.TriggerType = store.TriggerTypeCron
	}
	if in.Prompt == "" && in.TriggerType != store.TriggerTypePRReview && in.TriggerType != store.TriggerTypeIssue {
		return store.TriggerRecord{}, fmt.Errorf("prompt is required")
	}
	if in.ID == "" {
		id, err := randomID("trig")
		if err != nil {
			return store.TriggerRecord{}, fmt.Errorf("generate trigger id: %w", err)
		}
		in.ID = id
	}
	if in.AgentImage == "" {
		if s.cfg.DefaultAgentImage == "" {
			return store.TriggerRecord{}, fmt.Errorf("agent_image is required (no default configured)")
		}
		in.AgentImage = s.cfg.DefaultAgentImage
	}
	in.AgentImage = s.resolveAgentImage(in.AgentImage)
	if in.TimeoutSec == 0 {
		in.TimeoutSec = s.cfg.DefaultTaskTimeoutSec
	}
	switch in.TriggerType {
	case store.TriggerTypeCron:
		if in.CronExpr == "" {
			return store.TriggerRecord{}, fmt.Errorf("cron_expr is required for cron triggers")
		}
		if _, err := defaultCronParser.Parse(in.CronExpr); err != nil {
			return store.TriggerRecord{}, fmt.Errorf("parse cron: %w", err)
		}
	case store.TriggerTypePRReview:
		var cfg store.PRReviewTriggerConfig
		if err := json.Unmarshal([]byte(in.TriggerConfig), &cfg); err != nil || cfg.Repo == "" {
			return store.TriggerRecord{}, fmt.Errorf("repo is required in trigger_config for pr_review triggers")
		}
	case store.TriggerTypeIssue:
		var cfg struct{ Repo string }
		if err := json.Unmarshal([]byte(in.TriggerConfig), &cfg); err != nil || cfg.Repo == "" {
			return store.TriggerRecord{}, fmt.Errorf("repo is required in trigger_config for issue triggers")
		}
	default:
		return store.TriggerRecord{}, fmt.Errorf("unknown trigger_type %q", in.TriggerType)
	}
	now := time.Now().UTC()
	skills, err := json.Marshal(nonEmptyStrings(in.Skills))
	if err != nil {
		return store.TriggerRecord{}, fmt.Errorf("marshal skills: %w", err)
	}
	teamID, err := s.resolveOwnerTeamID(ctx, in.TeamID, in.TeamName)
	if err != nil {
		return store.TriggerRecord{}, err
	}
	triggerConfig := emptyTriggerConfig()
	if in.TriggerConfig != "" {
		triggerConfig = json.RawMessage(in.TriggerConfig)
	}
	if err := s.repo.CreateTrigger(ctx, repository.CreateTriggerParams{
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
		return store.TriggerRecord{}, fmt.Errorf("create trigger: %w", err)
	}
	record, err := s.repo.GetTriggerByID(ctx, in.ID)
	if err != nil {
		return store.TriggerRecord{}, fmt.Errorf("get trigger: %w", err)
	}
	sRecord := triggerToStoreRecord(record)
	s.auditAsync(ctx, AuditEventParams{
		EventType:  "trigger_created",
		SourceType: "api",
		TargetType: "trigger",
		TargetID:   in.Name,
		Detail:     fmt.Sprintf("trigger %q created (type=%s)", in.Name, in.TriggerType),
	})
	if in.TriggerType == store.TriggerTypeCron {
		if err := s.activateTrigger(ctx, sRecord); err != nil {
			return store.TriggerRecord{}, fmt.Errorf("activate trigger: %w", err)
		}
		updated, err := s.repo.GetTriggerByID(ctx, in.ID)
		if err != nil {
			return store.TriggerRecord{}, fmt.Errorf("get trigger after activation: %w", err)
		}
		return triggerToStoreRecord(updated), nil
	}
	return sRecord, nil
}

// UpdateTrigger updates all mutable fields on an existing trigger.
func (s *Service) UpdateTrigger(ctx context.Context, name string, in store.TriggerInput, enabled bool) (store.TriggerRecord, error) {
	if in.Name == "" {
		return store.TriggerRecord{}, fmt.Errorf("name is required")
	}
	if in.Prompt == "" && in.TriggerType != store.TriggerTypePRReview {
		return store.TriggerRecord{}, fmt.Errorf("prompt is required")
	}
	if in.TriggerType == "" {
		in.TriggerType = store.TriggerTypeCron
	}
	switch in.TriggerType {
	case store.TriggerTypeCron:
		if in.CronExpr == "" {
			return store.TriggerRecord{}, fmt.Errorf("cron_expr is required for cron triggers")
		}
		if _, err := defaultCronParser.Parse(in.CronExpr); err != nil {
			return store.TriggerRecord{}, fmt.Errorf("parse cron: %w", err)
		}
	case store.TriggerTypePRReview:
		var cfg store.PRReviewTriggerConfig
		if err := json.Unmarshal([]byte(in.TriggerConfig), &cfg); err != nil || cfg.Repo == "" {
			return store.TriggerRecord{}, fmt.Errorf("repo is required in trigger_config for pr_review triggers")
		}
	}
	if in.AgentImage == "" {
		return store.TriggerRecord{}, fmt.Errorf("agent_image is required")
	}
	in.AgentImage = s.resolveAgentImage(in.AgentImage)
	if in.TimeoutSec == 0 {
		in.TimeoutSec = s.cfg.DefaultTaskTimeoutSec
	}
	existing, err := s.triggerForToolAccess(ctx, name)
	if err != nil {
		return store.TriggerRecord{}, fmt.Errorf("get trigger: %w", err)
	}

	// Deactivate cron trigger if it's a cron type.
	s.cronMu.Lock()
	if entryID, ok := s.cronEntries[existing.ID]; ok {
		s.cron.Remove(entryID)
		delete(s.cronEntries, existing.ID)
	}
	s.cronMu.Unlock()
	now := time.Now().UTC()
	skills, err := json.Marshal(nonEmptyStrings(in.Skills))
	if err != nil {
		return store.TriggerRecord{}, fmt.Errorf("marshal skills: %w", err)
	}
	triggerConfig := emptyTriggerConfig()
	if in.TriggerConfig != "" {
		triggerConfig = json.RawMessage(in.TriggerConfig)
	}
	if err := s.repo.UpdateTrigger(ctx, repository.UpdateTriggerParams{
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
		return store.TriggerRecord{}, fmt.Errorf("update trigger: %w", err)
	}
	record, err := s.triggerForToolAccess(ctx, in.Name)
	if err != nil {
		return store.TriggerRecord{}, fmt.Errorf("get trigger: %w", err)
	}
	sRecord := triggerToStoreRecord(record)
	s.auditAsync(ctx, AuditEventParams{
		EventType:  "trigger_updated",
		SourceType: "api",
		SourceID:   name,
		TargetType: "trigger",
		TargetID:   in.Name,
		Detail:     fmt.Sprintf("trigger %q updated (was %q, type=%s)", in.Name, name, in.TriggerType),
	})
	if !enabled || in.TriggerType != store.TriggerTypeCron {
		return sRecord, nil
	}
	if err := s.activateTrigger(ctx, sRecord); err != nil {
		return store.TriggerRecord{}, fmt.Errorf("reactivate trigger: %w", err)
	}
	return sRecord, nil
}

// DeleteTrigger removes a trigger by name and stops its cron job if applicable.
func (s *Service) DeleteTrigger(ctx context.Context, name string) error {
	sch, err := s.triggerForToolAccess(ctx, name)
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
	if err := s.repo.DeleteTrigger(ctx, name); err != nil {
		return fmt.Errorf("delete trigger: %w", err)
	}
	s.auditAsync(ctx, AuditEventParams{
		EventType:  "trigger_deleted",
		SourceType: "api",
		TargetType: "trigger",
		TargetID:   name,
		Detail:     fmt.Sprintf("trigger %q deleted", name),
	})
	return nil
}

// RunTriggerNow submits a task from a named cron trigger immediately.
func (s *Service) RunTriggerNow(ctx context.Context, name string) (store.TaskRecord, error) {
	if name == "" {
		return store.TaskRecord{}, fmt.Errorf("name is required")
	}
	sch, err := s.triggerForToolAccess(ctx, name)
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("get trigger: %w", err)
	}
	runtime := triggerRuntimeConfigFromJSON(json.RawMessage(sch.TriggerConfig))
	targetSkills := parseJSON[[]string](sch.Skills, "trigger:"+sch.ID+" skills")
	task, err := s.submitTriggerTask(ctx,
		sch.ID,
		sch.Name,
		sch.TriggerType,
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
		runtime,
		time.Now().UTC(),
	)
	if err == nil {
		s.auditAsync(ctx, AuditEventParams{
			EventType:  "trigger_run",
			SourceType: "api",
			SourceID:   name,
			TargetType: "task",
			TargetID:   task.ID,
			Detail:     fmt.Sprintf("trigger %q run manually", name),
		})
	}
	return task, err
}

func (s *Service) loadTriggers(ctx context.Context) error {
	triggers, err := s.repo.ListEnabledTriggersByType(ctx, store.TriggerTypeCron)
	if err != nil {
		return fmt.Errorf("load cron triggers: %w", err)
	}
	for _, trigger := range triggers {
		if err := s.activateTrigger(ctx, triggerToStoreRecord(trigger)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) activateTrigger(ctx context.Context, trigger store.TriggerRecord) error {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()
	if existing, ok := s.cronEntries[trigger.ID]; ok {
		s.cron.Remove(existing)
	}
	entryID, err := s.cron.AddFunc(trigger.CronExpr, func() {
		runCtx, cancel := context.WithTimeout(context.Background(), triggerRunTimeout)
		defer cancel()
		if err := s.runTrigger(runCtx, trigger.ID, time.Now().UTC()); err != nil {
			slog.Error("trigger run failed", "triggerID", trigger.ID, "err", err)
		}
	})
	if err != nil {
		return fmt.Errorf("activate trigger %s: %w", trigger.ID, err)
	}
	s.cronEntries[trigger.ID] = entryID
	entry := s.cron.Entry(entryID)
	if !entry.Next.IsZero() {
		if err := s.repo.SetTriggerNextRun(ctx, repository.SetTriggerNextRunParams{
			NextRunAt: sql.NullTime{Time: entry.Next, Valid: true},
			UpdatedAt: time.Now().UTC(),
			ID:        trigger.ID,
		}); err != nil {
			return fmt.Errorf("set trigger next run: %w", err)
		}
	}
	return nil
}

func (s *Service) runTrigger(ctx context.Context, triggerID string, triggeredAt time.Time) error {
	trigger, err := s.repo.GetTriggerByID(ctx, triggerID)
	if err != nil {
		return fmt.Errorf("get trigger %s: %w", triggerID, err)
	}
	skills := parseJSON[[]string](trigger.Skills, "trigger:"+trigger.ID+" skills")
	runtime := triggerRuntimeConfigFromJSON(trigger.TriggerConfig)
	_, err = s.submitTriggerTask(ctx, trigger.ID, trigger.Name, trigger.TriggerType, trigger.TeamID.String, trigger.Prompt, trigger.GitUrl.String, trigger.GitRef.String,
		trigger.AgentImage.String, trigger.Agent.String, trigger.ProviderID.String, trigger.ModelID.String, trigger.VariantID.String,
		trigger.Harness.String, skills, int(trigger.TimeoutSec), runtime, triggeredAt)
	return err
}

func (s *Service) submitTriggerTask(ctx context.Context, triggerID, triggerName, triggerType, teamID, prompt, gitURL, gitRef, agentImage, agent, providerID, modelID, variantID, harness string, skills []string, timeoutSec int, runtime triggerRuntimeConfig, triggeredAt time.Time) (store.TaskRecord, error) {
	task, err := s.SubmitTask(ctx, SubmitTaskRequest{
		TeamID:           teamID,
		Prompt:           prompt,
		GitURL:           gitURL,
		GitRef:           gitRef,
		AgentImage:       agentImage,
		Agent:            agent,
		ProviderID:       providerID,
		ModelID:          modelID,
		VariantID:        variantID,
		Harness:          harness,
		Skills:           skills,
		TimeoutSec:       timeoutSec,
		TriggerName:      triggerName,
		TriggerType:      triggerType,
		SubmissionSource: "trigger",
		SessionMode:      runtime.SessionMode,
		PauseReason:      runtime.PauseReason,
		TTLHours:         runtime.TTLHours,
	})
	if err != nil {
		return store.TaskRecord{}, fmt.Errorf("submit triggered task: %w", err)
	}
	if entryID, ok := s.cronEntries[triggerID]; ok {
		entry := s.cron.Entry(entryID)
		if !entry.Next.IsZero() {
			if err := s.repo.SetTriggerNextRun(ctx, repository.SetTriggerNextRunParams{
				NextRunAt: sql.NullTime{Time: entry.Next, Valid: true},
				UpdatedAt: time.Now().UTC(),
				ID:        triggerID,
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
		skills := parseJSON[[]string](t.Skills, "trigger:"+t.ID+" skills")
		cfg := triggerRuntimeConfigFromJSON(t.TriggerConfig)
		out[i] = webhook.ReviewTrigger{
			TeamID:      t.TeamID.String,
			Name:        t.Name,
			TriggerType: t.TriggerType,
			Prompt:      t.Prompt,
			AgentImage:  t.AgentImage.String,
			Agent:       t.Agent.String,
			ProviderID:  t.ProviderID.String,
			ModelID:     t.ModelID.String,
			VariantID:   t.VariantID.String,
			TimeoutSec:  int(t.TimeoutSec),
			GitURL:      t.GitUrl.String,
			GitRef:      t.GitRef.String,
			Skills:      skills,
			Event:       cfg.Event,
			SessionMode: cfg.SessionMode,
			PauseReason: cfg.PauseReason,
			TTLHours:    cfg.TTLHours,
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
		skills := parseJSON[[]string](t.Skills, "trigger:"+t.ID+" skills")
		cfg := triggerRuntimeConfigFromJSON(t.TriggerConfig)
		out[i] = webhook.ReviewTrigger{
			TeamID:      t.TeamID.String,
			Name:        t.Name,
			TriggerType: t.TriggerType,
			Prompt:      t.Prompt,
			AgentImage:  t.AgentImage.String,
			Agent:       t.Agent.String,
			ProviderID:  t.ProviderID.String,
			ModelID:     t.ModelID.String,
			VariantID:   t.VariantID.String,
			TimeoutSec:  int(t.TimeoutSec),
			GitURL:      t.GitUrl.String,
			GitRef:      t.GitRef.String,
			Skills:      skills,
			Event:       cfg.Event,
			MatchLabels: cfg.MatchLabels,
			SessionMode: cfg.SessionMode,
			PauseReason: cfg.PauseReason,
			TTLHours:    cfg.TTLHours,
		}
	}
	return out, nil
}

type triggerRuntimeConfig struct {
	Event       string   `json:"event"`
	MatchLabels []string `json:"match_labels"`
	SessionMode string   `json:"session_mode"`
	PauseReason string   `json:"pause_reason"`
	TTLHours    int      `json:"ttl_hours"`
}

func triggerRuntimeConfigFromJSON(cfg json.RawMessage) triggerRuntimeConfig {
	return parseJSON[triggerRuntimeConfig](cfg, "trigger_runtime_config")
}

func randomID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(raw[:]), nil
}

func (s *Service) resolveOwnerTeamID(ctx context.Context, requestedID, requestedName string) (string, error) {
	scope, ok := auth.GetScope(ctx)
	if !ok || scope.Admin {
		if requestedID != "" {
			return requestedID, nil
		}
		if requestedName != "" {
			team, err := s.repo.GetTeamByName(ctx, requestedName)
			if err != nil {
				return "", fmt.Errorf("team %q not found", requestedName)
			}
			return team.ID, nil
		}
		return "", nil
	}
	teams := scope.Teams()
	if requestedName != "" {
		team, err := s.repo.GetTeamByName(ctx, requestedName)
		if err != nil {
			return "", fmt.Errorf("team %q not found", requestedName)
		}
		requestedID = team.ID
	}
	if requestedID != "" {
		if !scope.HasTeam(requestedID) {
			return "", fmt.Errorf("team %q is not in token scope", requestedID)
		}
		return requestedID, nil
	}
	if len(teams) == 1 {
		return teams[0], nil
	}
	if len(teams) > 1 {
		return "", fmt.Errorf("team_id or team_name is required for multi-team tokens")
	}
	return "", fmt.Errorf("token has no team scope")
}

func (s *Service) triggerForToolAccess(ctx context.Context, name string) (repository.ChetterTrigger, error) {
	trigger, err := s.repo.GetTriggerByName(ctx, name)
	if err != nil {
		return repository.ChetterTrigger{}, err
	}
	if err := authorizeTriggerAccess(ctx, trigger); err != nil {
		return repository.ChetterTrigger{}, err
	}
	return trigger, nil
}

func authorizeTriggerAccess(ctx context.Context, trigger repository.ChetterTrigger) error {
	scope, scoped := auth.GetScope(ctx)
	if !scoped || scope.Admin {
		return nil
	}
	if !trigger.TeamID.Valid || !scope.HasTeam(trigger.TeamID.String) {
		return fmt.Errorf("trigger not found")
	}
	return nil
}

func authorizeAgentSessionAccess(ctx context.Context, session repository.ChetterAgentSession) error {
	scope, scoped := auth.GetScope(ctx)
	if !scoped || scope.Admin {
		return nil
	}
	if !session.TeamID.Valid || !scope.HasTeam(session.TeamID.String) {
		return fmt.Errorf("agent session not found")
	}
	return nil
}

func (s *Service) LogAuditEvent(ctx context.Context, params AuditEventParams) error {
	id, err := randomID("aud")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	auditSearchText := strings.Join(strings.Fields(params.Detail+" "+params.SourceType+" "+params.SourceID+" "+params.TargetType+" "+params.TargetID+" "+params.Repo+" "+params.EventType+" "+params.TokenName+" "+params.TokenID), " ")
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
		SearchText:       nullString(auditSearchText),
		Payload:          (*json.RawMessage)(&params.Payload),
		TokenID:          nullString(params.TokenID),
		TokenName:        nullString(params.TokenName),
	})
}

func (s *Service) auditAsync(ctx context.Context, params AuditEventParams) {
	if scope, ok := auth.GetScope(ctx); ok {
		if params.TokenID == "" {
			params.TokenID = scope.TokenID
		}
		if params.TokenName == "" {
			params.TokenName = scope.TokenName
		}
	}
	auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.LogAuditEvent(auditCtx, params); err != nil {
		slog.Warn("audit event", "err", err, "event_type", params.EventType)
	}
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
	artifactSearchText := strings.Join(strings.Fields(params.TaskID+" "+params.Repo+" "+params.ArtifactType+" "+params.Ref), " ")
	return s.repo.InsertTaskArtifact(ctx, repository.InsertTaskArtifactParams{
		ID:              id,
		TaskID:          params.TaskID,
		AgentSessionID:  nullString(params.AgentSessionID),
		SessionRunID:    nullString(params.SessionRunID),
		ArtifactType:    params.ArtifactType,
		Repo:            params.Repo,
		Number:          number,
		Url:             nullString(params.URL),
		Ref:             nullString(params.Ref),
		Sha:             nullString(params.SHA),
		CreatedAt:       now,
		DiscoveredAt:    now,
		DiscoverySource: params.DiscoverySource,
		SearchText:      nullString(artifactSearchText),
	})
}
