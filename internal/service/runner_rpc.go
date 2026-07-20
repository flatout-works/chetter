package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/pkg/modelcatalog"
)

const (
	defaultClaimWaitSec       = 30
	defaultTaskLeaseSec       = 60
	claimPollInterval         = time.Second
	runnerEventSubject        = "connect.runner"
	heartbeatEventMinInterval = 60 * time.Second
)

var errNoClaimableTask = errors.New("no claimable task")
var errTaskNotClaimed = errors.New("task is not claimed by runner")

type RunnerRPCService struct {
	db            *repository.Queries
	rawDB         *sql.DB
	heartbeatSeen sync.Map
	drainRequests sync.Map // map[string]bool — runner ID → drain requested
	eventBus      TaskEventPublisher
	callbacks     TaskEventCallbackDispatcher
	ghActions     GitHubActionService
}

// TaskEventPublisher fans out task events to streaming subscribers.
// Implemented by webapi.EventBus.
type TaskEventPublisher interface {
	PublishTaskEvent(taskID, eventID, status, eventType, summary, payload, createdAt string)
}

type TaskEventCallbackDispatcher interface {
	DispatchTaskEventCallbacks(ctx context.Context, event TaskEventCallbackContext)
}

type TaskEventCallbackContext struct {
	ID            string
	TaskID        string
	TeamID        string
	Subject       string
	Status        string
	EventType     string
	Summary       string
	Error         string
	ErrorCategory string
	Payload       json.RawMessage
	CreatedAt     time.Time
}

func NewRunnerRPCService(db *repository.Queries, rawDB *sql.DB) *RunnerRPCService {
	return &RunnerRPCService{db: db, rawDB: rawDB}
}

func (s *RunnerRPCService) WithEventBus(bus TaskEventPublisher) *RunnerRPCService {
	s.eventBus = bus
	return s
}

func (s *RunnerRPCService) WithEventCallbacks(callbacks TaskEventCallbackDispatcher) *RunnerRPCService {
	s.callbacks = callbacks
	return s
}

func (s *RunnerRPCService) RegisterRunner(ctx context.Context, req *connect.Request[runnerv1.RegisterRunnerRequest]) (*connect.Response[runnerv1.RegisterRunnerResponse], error) {
	if err := s.upsertRunner(ctx, req.Msg.Runner); err != nil {
		return nil, err
	}
	return connect.NewResponse(&runnerv1.RegisterRunnerResponse{}), nil
}

func (s *RunnerRPCService) Heartbeat(ctx context.Context, req *connect.Request[runnerv1.HeartbeatRequest]) (*connect.Response[runnerv1.HeartbeatResponse], error) {
	if err := s.upsertRunner(ctx, req.Msg.Runner); err != nil {
		return nil, err
	}
	commands, err := s.runnerCommands(ctx, req.Msg.Runner)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&runnerv1.HeartbeatResponse{Commands: commands}), nil
}

func (s *RunnerRPCService) ClaimTask(ctx context.Context, req *connect.Request[runnerv1.ClaimTaskRequest]) (*connect.Response[runnerv1.ClaimTaskResponse], error) {
	waitSec := req.Msg.WaitSeconds
	if waitSec == 0 {
		waitSec = defaultClaimWaitSec
	}
	if waitSec < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("wait_seconds must be non-negative"))
	}
	if waitSec > defaultClaimWaitSec {
		waitSec = defaultClaimWaitSec
	}
	leaseSec := req.Msg.LeaseSeconds
	if leaseSec == 0 {
		leaseSec = defaultTaskLeaseSec
	}
	if leaseSec < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("lease_seconds must be non-negative"))
	}
	if leaseSec > 3600 {
		leaseSec = 3600
	}
	deadline := time.Now().Add(time.Duration(waitSec) * time.Second)

	for {
		task, err := s.claimOnce(ctx, req.Msg.RunnerId, time.Duration(leaseSec)*time.Second)
		if err == nil {
			resumeCheckpointPath := ""
			resumeWorkspacePath := ""
			resumeHarnessSessionID := ""
			if task.RequiredRunnerID.Valid {
				sess, sessErr := s.db.GetAgentSessionByTaskID(ctx, task.ID)
				if sessErr == nil {
					if sess.WorkspacePath.Valid {
						resumeWorkspacePath = sess.WorkspacePath.String
					}
					if sess.HarnessSessionID.Valid {
						resumeHarnessSessionID = sess.HarnessSessionID.String
					}
					if sess.ResumeMode == "gvisor_checkpoint" && sess.CheckpointID.Valid {
						chk, chkErr := s.db.GetLatestAgentSessionCheckpoint(ctx, sess.ID)
						if chkErr == nil && chk.Status == "ready" {
							resumeCheckpointPath = chk.CheckpointPath
							if resumeWorkspacePath == "" {
								resumeWorkspacePath = chk.WorkspacePath
							}
						}
					}
				}
			}
			protoTask := taskToProto(task, resumeCheckpointPath, resumeWorkspacePath)
			protoTask.ResumeHarnessSessionId = resumeHarnessSessionID
			if recoverFrom, ok := protoTask.Env["__recover_from"]; ok && recoverFrom != "" {
				delete(protoTask.Env, "__recover_from")
				origTask, origErr := s.db.GetTaskByID(ctx, recoverFrom)
				if origErr == nil && origTask.SessionExport.Valid {
					exportContent := strings.ReplaceAll(origTask.SessionExport.String, "\\n", "\n")
					if exportContent != "" {
						protoTask.ExtraFiles = map[string][]byte{
							fmt.Sprintf("chetter_recovery_%s.md", recoverFrom): []byte(exportContent),
						}
					}
				} else if origErr != nil {
					slog.Warn("recover task: lookup original session export", "taskID", task.ID, "recoverFrom", recoverFrom, "err", origErr)
				}
			}
			s.resolveTaskModel(ctx, protoTask)
			s.resolveTaskDefinitions(ctx, protoTask)
			return connect.NewResponse(&runnerv1.ClaimTaskResponse{Task: protoTask}), nil
		}
		if !errors.Is(err, errNoClaimableTask) {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if waitSec == 0 || time.Now().After(deadline) {
			return connect.NewResponse(&runnerv1.ClaimTaskResponse{}), nil
		}
		select {
		case <-ctx.Done():
			return nil, connect.NewError(connect.CodeCanceled, ctx.Err())
		case <-time.After(claimPollInterval):
		}
	}
}

type resolvedModelConfig struct {
	ProviderID         string
	ModelID            string
	ProviderName       string
	ProviderBaseURL    string
	ProviderAPIKeyEnv  string
	ProviderKind       string
	AwsProfile         string
	AwsRegion          string
	ProviderAPI        string
	ProviderAuthHeader bool
}

func (s *RunnerRPCService) resolveTaskModel(ctx context.Context, task *runnerv1.Task) {
	if task == nil {
		return
	}
	if task.Harness == "" {
		task.Harness = "opencode"
	}
	catalog := modelcatalog.Default()
	row, err := s.db.GetActiveModelCatalog(ctx)
	if err == nil {
		if parsed, parseErr := modelcatalog.ParseYAML([]byte(row.Yaml)); parseErr == nil {
			catalog = parsed
		} else {
			slog.Warn("invalid active model catalog; using default", "err", parseErr)
		}
	} else if err != sql.ErrNoRows {
		slog.Warn("load active model catalog; using default", "err", err)
	}
	resolved := resolveModelForTask(catalog, task)
	task.ProviderId = resolved.ProviderID
	task.ModelId = resolved.ModelID
	task.ProviderName = resolved.ProviderName
	task.ProviderBaseUrl = resolved.ProviderBaseURL
	task.ProviderApiKeyEnv = resolved.ProviderAPIKeyEnv
	if task.Env == nil {
		task.Env = make(map[string]string)
	}
	if resolved.ProviderKind != "" {
		task.Env["__chetter_provider_kind"] = resolved.ProviderKind
	}
	if resolved.AwsProfile != "" {
		task.Env["__chetter_aws_profile"] = resolved.AwsProfile
	}
	if resolved.AwsRegion != "" {
		task.Env["__chetter_aws_region"] = resolved.AwsRegion
	}
	task.ProviderApi = resolved.ProviderAPI
	task.ProviderAuthHeader = resolved.ProviderAuthHeader
}

func (s *RunnerRPCService) resolveTaskDefinitions(ctx context.Context, task *runnerv1.Task) {
	if task == nil {
		return
	}
	if task.Agent != "" {
		var content string
		err := s.rawDB.QueryRowContext(ctx,
			`SELECT content FROM definitions WHERE definition_type='agent' AND name=? AND active=true ORDER BY updated_at DESC LIMIT 1`,
			task.Agent,
		).Scan(&content)
		if err == nil {
			task.AgentDefinition = content
		}
	}
	if len(task.Skills) > 0 {
		skillDefs := s.resolveSkillDefinitions(ctx, task.Skills)
		if len(skillDefs) > 0 {
			task.SkillDefinitions = skillDefs
		}
	}
}

func (s *RunnerRPCService) resolveSkillDefinitions(ctx context.Context, skillNames []string) map[string][]byte {
	placeholders := strings.Repeat(",?", len(skillNames))[1:]
	query := `SELECT name, path, content FROM definitions WHERE definition_type='skill' AND name IN (` + placeholders + `) AND active=true`
	args := make([]any, len(skillNames))
	for i, n := range skillNames {
		args[i] = n
	}
	rows, err := s.rawDB.QueryContext(ctx, query, args...)
	if err != nil {
		slog.Warn("resolve skill definitions query", "err", err)
		return nil
	}
	defer rows.Close()

	grouped := make(map[string][]skillFileEntry, len(skillNames))
	for rows.Next() {
		var name, path, content string
		if err := rows.Scan(&name, &path, &content); err != nil {
			slog.Warn("scan skill definition row", "err", err)
			continue
		}
		grouped[name] = append(grouped[name], skillFileEntry{path: path, content: content})
	}
	if len(grouped) == 0 {
		return nil
	}

	out := make(map[string][]byte, len(grouped))
	for name, files := range grouped {
		tarBytes, err := tarSkill(name, files)
		if err != nil {
			slog.Warn("tar skill", "skill", name, "err", err)
			continue
		}
		out[name] = tarBytes
	}
	return out
}

type skillFileEntry struct {
	path    string
	content string
}

func tarSkill(name string, files []skillFileEntry) ([]byte, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	prefix := "skills/" + name + "/"
	for _, f := range files {
		entryName := strings.TrimPrefix(f.path, prefix)
		if entryName == f.path {
			entryName = f.path
		}
		hdr := &tar.Header{
			Name:   entryName,
			Size:   int64(len(f.content)),
			Mode:   0644,
			Format: tar.FormatUSTAR,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("write tar header for %s: %w", entryName, err)
		}
		if _, err := tw.Write([]byte(f.content)); err != nil {
			return nil, fmt.Errorf("write tar content for %s: %w", entryName, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar writer: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("close gzip writer: %w", err)
	}
	return buf.Bytes(), nil
}

func resolveModelForTask(catalog *modelcatalog.Catalog, task *runnerv1.Task) resolvedModelConfig {
	if catalog == nil {
		catalog = modelcatalog.Default()
	}
	harness := catalogHarnessName(task.Harness)
	providerID := strings.TrimSpace(task.ProviderId)
	modelID := strings.TrimSpace(task.ModelId)
	if providerID == "" && strings.Contains(modelID, "/") {
		parts := strings.SplitN(modelID, "/", 2)
		providerID = parts[0]
		modelID = parts[1]
	}
	if task.Env != nil {
		if providerID == "" {
			providerID = strings.TrimSpace(firstNonEmpty(task.Env["LLM_PROVIDER"], task.Env["PI_PROVIDER"]))
		}
		if modelID == "" {
			modelID = strings.TrimSpace(firstNonEmpty(task.Env["LLM_MODEL_CODER"], task.Env["PI_MODEL"], task.Env["ANTHROPIC_MODEL"]))
		}
	}
	defaultProvider, defaultModel := catalogDefaultForHarness(catalog, harness)
	if providerID == "" {
		providerID = defaultProvider
	}
	if modelID == "" {
		modelID = defaultModel
	}
	return catalogProviderModelConfig(catalog, harness, providerID, modelID)
}

func catalogDefaultForHarness(catalog *modelcatalog.Catalog, harness string) (providerID, modelID string) {
	providerID = catalog.DefaultProvider
	modelID = catalog.DefaultModel
	if def, ok := catalog.Defaults[harness]; ok {
		if def.Provider != "" {
			providerID = def.Provider
		}
		if def.Model != "" {
			modelID = def.Model
		}
	}
	if providerID == "" {
		providerID = "synthetic"
	}
	if modelID == "" {
		modelID = "hf:zai-org/GLM-5.2"
	}
	return providerID, modelID
}

func catalogProviderModelConfig(catalog *modelcatalog.Catalog, harness, providerID, modelID string) resolvedModelConfig {
	return catalogProviderModelConfigVisited(catalog, harness, providerID, modelID, nil)
}

func catalogProviderModelConfigVisited(catalog *modelcatalog.Catalog, harness, providerID, modelID string, visited map[string]bool) resolvedModelConfig {
	provider, ok := catalog.Providers[providerID]
	if !ok {
		return resolvedModelConfig{ProviderID: providerID, ModelID: modelID}
	}
	if hp, ok := provider.Harnesses[harness]; ok && hp.Disabled {
		if visited == nil {
			visited = map[string]bool{}
		}
		visited[providerID+"/"+modelID] = true
		defaultProvider, defaultModel := catalogDefaultForHarness(catalog, harness)
		if visited[defaultProvider+"/"+defaultModel] {
			return resolvedModelConfig{ProviderID: defaultProvider, ModelID: defaultModel}
		}
		return catalogProviderModelConfigVisited(catalog, harness, defaultProvider, defaultModel, visited)
	}
	resolved := resolvedModelConfig{ProviderID: providerID, ModelID: modelID}
	if hp, ok := provider.Harnesses[harness]; ok && !hp.Disabled {
		if hp.ID != "" {
			resolved.ProviderID = hp.ID
		}
		resolved.ProviderName = firstNonEmpty(hp.Name, provider.Name, resolved.ProviderID)
		resolved.ProviderBaseURL = firstNonEmpty(hp.BaseURL, provider.BaseURL)
		resolved.ProviderAPIKeyEnv = firstNonEmpty(hp.APIKeyEnv, provider.APIKeyEnv)
		resolved.AwsProfile = firstNonEmpty(hp.AwsProfile, provider.AwsProfile)
		resolved.AwsRegion = firstNonEmpty(hp.AwsRegion, provider.AwsRegion)
		resolved.ProviderAPI = hp.API
		resolved.ProviderAuthHeader = hp.AuthHeader
	} else {
		resolved.ProviderName = firstNonEmpty(provider.Name, resolved.ProviderID)
		resolved.ProviderBaseURL = provider.BaseURL
		resolved.ProviderAPIKeyEnv = provider.APIKeyEnv
		resolved.AwsProfile = provider.AwsProfile
		resolved.AwsRegion = provider.AwsRegion
	}
	resolved.ProviderKind = provider.Kind
	for _, model := range provider.Models {
		if model.ID != modelID {
			continue
		}
		if hm, ok := model.Harnesses[harness]; ok {
			if hm.Disabled {
				if visited == nil {
					visited = map[string]bool{}
				}
				visited[providerID+"/"+modelID] = true
				defaultProvider, defaultModel := catalogDefaultForHarness(catalog, harness)
				if visited[defaultProvider+"/"+defaultModel] {
					return resolvedModelConfig{ProviderID: defaultProvider, ModelID: defaultModel}
				}
				return catalogProviderModelConfigVisited(catalog, harness, defaultProvider, defaultModel, visited)
			}
			if hm.ID != "" {
				resolved.ModelID = hm.ID
			}
		}
		break
	}
	return resolved
}

func catalogHarnessName(harness string) string {
	switch strings.TrimSpace(harness) {
	case "":
		return "opencode"
	default:
		return strings.TrimSpace(harness)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (s *RunnerRPCService) ReportTaskEvents(ctx context.Context, req *connect.Request[runnerv1.ReportTaskEventsRequest]) (*connect.Response[runnerv1.ReportTaskEventsResponse], error) {
	if req.Msg.RunnerId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("runner_id is required"))
	}
	if len(req.Msg.Events) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("events are required"))
	}
	for _, event := range req.Msg.Events {
		if err := s.recordTaskEvent(ctx, req.Msg.RunnerId, event); err != nil {
			return nil, err
		}
	}
	return connect.NewResponse(&runnerv1.ReportTaskEventsResponse{}), nil
}

func (s *RunnerRPCService) PruneWorkspaces(ctx context.Context, req *connect.Request[runnerv1.PruneWorkspacesRequest]) (*connect.Response[runnerv1.PruneWorkspacesResponse], error) {
	if req.Msg.RunnerId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("runner_id is required"))
	}
	taskIDs := req.Msg.TaskIds
	if len(taskIDs) == 0 {
		return connect.NewResponse(&runnerv1.PruneWorkspacesResponse{}), nil
	}

	args := make([]any, 0, len(taskIDs))
	placeholders := make([]string, 0, len(taskIDs))
	for _, id := range taskIDs {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}

	query := `SELECT DISTINCT t.id
		FROM chetter_tasks t
		LEFT JOIN chetter_session_runs sr ON sr.task_id = t.id
		LEFT JOIN chetter_agent_sessions s ON s.id = sr.agent_session_id AND s.status IN ('paused', 'recoverable', 'paused_waiting_review')
		WHERE t.id IN (` + strings.Join(placeholders, ",") + `)
		  AND (t.status IN ('running', 'pending') OR s.id IS NOT NULL)`

	rows, err := s.rawDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer rows.Close()

	protected := make(map[string]bool, len(taskIDs))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		protected[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	safe := make([]string, 0, len(taskIDs))
	for _, id := range taskIDs {
		if !protected[id] {
			safe = append(safe, id)
		}
	}
	return connect.NewResponse(&runnerv1.PruneWorkspacesResponse{SafeToDelete: safe}), nil
}

func (s *RunnerRPCService) upsertRunner(ctx context.Context, info *runnerv1.RunnerInfo) error {
	if info == nil || info.RunnerId == "" {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("runner_id is required"))
	}
	now := time.Now().UTC()
	status := info.Status
	if status == "" {
		status = "active"
	}
	metadata, err := json.Marshal(info)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	startedAt := parseOptionalTime(info.StartedAt)
	if err := s.db.UpsertRunnerHeartbeat(ctx, repository.UpsertRunnerHeartbeatParams{
		ID:             info.RunnerId,
		Status:         status,
		ImageRef:       nullString(info.ImageRef),
		ImageDigest:    nullString(info.ImageDigest),
		Version:        nullString(info.Version),
		MaxConcurrent:  info.MaxConcurrent,
		RunningTasks:   info.RunningTasks,
		AvailableSlots: info.AvailableSlots,
		TotalStarted:   info.TotalStarted,
		TotalCompleted: info.TotalCompleted,
		TotalErrors:    info.TotalErrors,
		StartedAt:      startedAt,
		FirstSeenAt:    now,
		LastSeenAt:     now,
		UpdatedAt:      now,
		Metadata:       metadata,
	}); err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	return nil
}

func (s *RunnerRPCService) RequestDrain(runnerID string) {
	s.drainRequests.Store(runnerID, true)
}

func (s *RunnerRPCService) runnerCommands(ctx context.Context, info *runnerv1.RunnerInfo) ([]*runnerv1.RunnerCommand, error) {
	commands := make([]*runnerv1.RunnerCommand, 0)
	if info != nil {
		if _, draining := s.drainRequests.LoadAndDelete(info.RunnerId); draining {
			commands = append(commands, &runnerv1.RunnerCommand{Type: "drain"})
		}
	}
	if info == nil || len(info.CurrentTaskIds) == 0 {
		return commands, nil
	}
	now := time.Now().UTC()
	lease := sql.NullTime{Time: now.Add(defaultTaskLeaseSec * time.Second), Valid: true}
	rows, err := s.db.ListHeartbeatTasks(ctx, repository.ListHeartbeatTasksParams{
		RunnerID: sql.NullString{String: info.RunnerId, Valid: true},
		Ids:      info.CurrentTaskIds,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	statusByID := make(map[string]repository.ListHeartbeatTasksRow, len(rows))
	for _, row := range rows {
		statusByID[row.ID] = row
	}
	runningIDs := make([]string, 0, len(statusByID))
	for _, taskID := range info.CurrentTaskIds {
		row, ok := statusByID[taskID]
		if !ok {
			continue
		}
		switch row.Status {
		case "cancelled":
			reason := row.Error.String
			if reason == "" {
				reason = "cancelled by operator"
			}
			commands = append(commands, &runnerv1.RunnerCommand{Type: "cancel", TaskId: row.ID, Reason: reason})
		case "pending":
			commands = append(commands, &runnerv1.RunnerCommand{Type: "cancel", TaskId: row.ID, Reason: "lease reclaimed; please stop"})
		case "running":
			runningIDs = append(runningIDs, row.ID)
		}
	}
	if len(runningIDs) > 0 {
		if _, err := s.db.RenewRunningTaskLeases(ctx, repository.RenewRunningTaskLeasesParams{
			LeaseExpiresAt: lease,
			UpdatedAt:      now,
			LastEventAt:    sql.NullTime{Time: now, Valid: true},
			RunnerID:       sql.NullString{String: info.RunnerId, Valid: true},
			Ids:            runningIDs,
		}); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	return commands, nil
}

func (s *RunnerRPCService) claimOnce(ctx context.Context, runnerID string, lease time.Duration) (repository.ChetterTask, error) {
	if runnerID == "" {
		return repository.ChetterTask{}, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("runner_id is required"))
	}
	var claimed repository.ChetterTask
	var eventID string
	var eventPayload json.RawMessage
	var eventCreatedAt time.Time
	eventID, err := randomID("evt")
	if err != nil {
		return repository.ChetterTask{}, connect.NewError(connect.CodeInternal, err)
	}
	err = withTxRetry(ctx, s.rawDB, func(q *repository.Queries) error {
		task, err := q.GetClaimableTaskForUpdate(ctx, sql.NullString{String: runnerID, Valid: true})
		if errors.Is(err, sql.ErrNoRows) {
			return errNoClaimableTask
		}
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		eventCreatedAt = now
		rows, err := q.MarkTaskClaimed(ctx, repository.MarkTaskClaimedParams{
			RunnerID:       nullString(runnerID),
			ClaimedAt:      sql.NullTime{Time: now, Valid: true},
			LeaseExpiresAt: sql.NullTime{Time: now.Add(lease), Valid: true},
			StartedAt:      sql.NullTime{Time: now, Valid: true},
			UpdatedAt:      now,
			LastEventAt:    sql.NullTime{Time: now, Valid: true},
			ID:             task.ID,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return errNoClaimableTask
		}
		if _, err := q.MarkSessionRunRunningByTask(ctx, repository.MarkSessionRunRunningByTaskParams{
			StartedAt: sql.NullTime{Time: now, Valid: true},
			UpdatedAt: now,
			TaskID:    task.ID,
		}); err != nil {
			return err
		}
		task.Status = "running"
		task.RunnerID = nullString(runnerID)
		task.ClaimedAt = sql.NullTime{Time: now, Valid: true}
		task.LeaseExpiresAt = sql.NullTime{Time: now.Add(lease), Valid: true}
		task.StartedAt = sql.NullTime{Time: now, Valid: true}
		task.UpdatedAt = now
		task.LastEventAt = sql.NullTime{Time: now, Valid: true}
		task.Attempt++
		eventPayload = mustMarshalJSON(map[string]any{
			"task_id":   task.ID,
			"runner_id": runnerID,
			"status":    "running",
			"summary":   "Task claimed by runner",
		})
		if err := q.InsertTaskEvent(ctx, repository.InsertTaskEventParams{
			ID:        eventID,
			TaskID:    task.ID,
			Subject:   fmt.Sprintf("%s.%s.%s", runnerEventSubject, runnerID, task.ID),
			Status:    "running",
			EventType: "task.claimed",
			Payload:   eventPayload,
			CreatedAt: now,
		}); err != nil {
			return err
		}
		claimed = task
		return nil
	})
	if err == nil {
		if s.eventBus != nil {
			s.eventBus.PublishTaskEvent(claimed.ID, eventID, "running", "task.claimed", "Task claimed by runner", string(eventPayload), eventCreatedAt.Format(time.RFC3339))
		}
		if s.callbacks != nil {
			dispatch := TaskEventCallbackContext{
				ID:        eventID,
				TaskID:    claimed.ID,
				TeamID:    claimed.TeamID.String,
				Subject:   fmt.Sprintf("%s.%s.%s", runnerEventSubject, runnerID, claimed.ID),
				Status:    "running",
				EventType: "task.claimed",
				Summary:   "Task claimed by runner",
				Payload:   eventPayload,
				CreatedAt: eventCreatedAt,
			}
			go func() {
				callbackCtx, cancel := context.WithTimeout(context.Background(), eventHandlerTimeout)
				defer cancel()
				s.callbacks.DispatchTaskEventCallbacks(callbackCtx, dispatch)
			}()
		}
	}
	return claimed, err
}

func (s *RunnerRPCService) recordTaskEvent(ctx context.Context, runnerID string, event *runnerv1.TaskEvent) error {
	if runnerID == "" {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("runner_id is required"))
	}
	if event == nil || event.TaskId == "" {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("task_id is required"))
	}
	now := time.Now().UTC()
	payload := json.RawMessage(event.PayloadJson)
	if len(payload) == 0 || !json.Valid(payload) {
		data, err := json.Marshal(event)
		if err != nil {
			return connect.NewError(connect.CodeInternal, err)
		}
		payload = data
	}
	eventID, err := randomID("evt")
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	status := event.Status
	if status == "" {
		status = "running"
	}
	errorCategory := normalizeErrorCategory(event.ErrorCategory)
	if errorCategory == "" && statusIsErrorCategoryCandidate(status) {
		errorCategory = classifyTaskErrorCategory(status, event.Error)
	}
	isHeartbeat := status == "running" && isHeartbeatSummary(event.Summary)
	eventType := taskEventType(status, errorCategory, isHeartbeat)
	lease := sql.NullTime{Time: now.Add(defaultTaskLeaseSec * time.Second), Valid: status == "running"}
	eventInsert := repository.InsertTaskEventParams{
		ID:        eventID,
		TaskID:    event.TaskId,
		Subject:   fmt.Sprintf("%s.%s.%s", runnerEventSubject, runnerID, event.TaskId),
		Status:    status,
		EventType: eventType,
		Payload:   payload,
		CreatedAt: now,
	}
	updateParams := repository.UpdateTaskFromRunnerEventParams{
		Status:                status,
		Summary:               nullString(event.Summary),
		Error:                 nullString(event.Error),
		ErrorCategory:         errorCategory,
		SessionExport:         nullString(event.SessionExport),
		ProviderID:            event.ProviderId,
		ModelID:               event.ModelId,
		VariantID:             event.VariantId,
		OpencodeSessionID:     event.OpencodeSessionId,
		RunnerImageDigest:     event.RunnerImageDigest,
		LeaseExpiresAt:        lease,
		StartedAt:             parseOptionalTime(event.StartedAt),
		EndedAt:               parseOptionalTime(event.EndedAt),
		UpdatedAt:             now,
		LastEventAt:           sql.NullTime{Time: now, Valid: true},
		TotalInputTokens:      tokenUsageInputTokens(event.TokenUsage),
		TotalOutputTokens:     tokenUsageOutputTokens(event.TokenUsage),
		TotalCacheReadTokens:  tokenUsageCacheReadTokens(event.TokenUsage),
		TotalCacheWriteTokens: tokenUsageCacheWriteTokens(event.TokenUsage),
		TotalReasoningTokens:  tokenUsageReasoningTokens(event.TokenUsage),
		CostCents:             tokenUsageCostCents(event.TokenUsage),
		ID:                    event.TaskId,
		RunnerID:              sql.NullString{String: runnerID, Valid: true},
	}
	skipEventRow := isHeartbeat && !s.shouldStoreHeartbeat(event.TaskId)
	err = withTxRetry(ctx, s.rawDB, func(q *repository.Queries) error {
		rows, err := q.UpdateTaskFromRunnerEvent(ctx, updateParams)
		if err != nil {
			return err
		}
		if rows == 0 {
			return errTaskNotClaimed
		}
		if err := q.UpdateTaskSearchText(ctx, event.TaskId); err != nil {
			slog.DebugContext(ctx, "update task search_text", "task_id", event.TaskId, "err", err)
		}
		if terminalRunStatus, terminalSessionStatus, ok := sessionTerminalStatuses(status); ok {
			startedAt := parseOptionalTime(event.StartedAt)
			endedAt := parseOptionalTime(event.EndedAt)
			if !endedAt.Valid {
				endedAt = sql.NullTime{Time: now, Valid: true}
			}
			if _, err := q.MarkSessionRunTerminalByTask(ctx, repository.MarkSessionRunTerminalByTaskParams{
				Status:        terminalRunStatus,
				Summary:       nullString(event.Summary),
				Error:         nullString(event.Error),
				SessionExport: nullString(event.SessionExport),
				StartedAt:     startedAt,
				EndedAt:       endedAt,
				UpdatedAt:     now,
				TaskID:        event.TaskId,
			}); err != nil {
				return err
			}

			sessionStatus := terminalSessionStatus
			if event.WorkspacePath != "" {
				var session repository.ChetterAgentSession
				if session, err = q.GetAgentSessionByTaskID(ctx, event.TaskId); err == nil {
					switch {
					case terminalSessionStatus == "error" && isRecoverablePromptError(errorCategory) && session.ResumeMode == "harness_session" && event.OpencodeSessionId != "":
						if _, err := q.PauseAgentSessionByTaskID(ctx, repository.PauseAgentSessionByTaskIDParams{
							Status:           "recoverable",
							PinnedRunnerID:   nullString(runnerID),
							CheckpointID:     sql.NullString{},
							WorkspacePath:    nullString(event.WorkspacePath),
							ContainerName:    sql.NullString{},
							HarnessSessionID: nullString(event.OpencodeSessionId),
							PausedAt:         sql.NullTime{Time: now, Valid: true},
							UpdatedAt:        now,
							TaskID:           event.TaskId,
						}); err != nil {
							return err
						}
						slog.Info("agent session marked recoverable after prompt failure", "session_id", session.ID, "workspace_path", event.WorkspacePath, "runner_id", runnerID, "error_category", errorCategory)
						sessionStatus = ""
					case terminalSessionStatus == "completed" && session.ResumeMode == "gvisor_checkpoint" && event.CheckpointPath != "":
						chkID, _ := randomID("chk")
						containerName := "chetter-task-" + event.TaskId
						if err := q.InsertAgentSessionCheckpoint(ctx, repository.InsertAgentSessionCheckpointParams{
							ID:             chkID,
							AgentSessionID: session.ID,
							RunnerID:       runnerID,
							CheckpointPath: event.CheckpointPath,
							WorkspacePath:  event.WorkspacePath,
							ContainerName:  nullString(containerName),
							Status:         "ready",
							CreatedAt:      now,
							UpdatedAt:      now,
						}); err != nil {
							return err
						}
						if _, err := q.PauseAgentSessionByTaskID(ctx, repository.PauseAgentSessionByTaskIDParams{
							Status:           "paused",
							PinnedRunnerID:   nullString(runnerID),
							CheckpointID:     nullString(chkID),
							WorkspacePath:    nullString(event.WorkspacePath),
							ContainerName:    nullString(containerName),
							HarnessSessionID: nullString(event.OpencodeSessionId),
							PausedAt:         sql.NullTime{Time: now, Valid: true},
							UpdatedAt:        now,
							TaskID:           event.TaskId,
						}); err != nil {
							return err
						}
						slog.Info("agent session paused with checkpoint", "session_id", session.ID, "checkpoint_id", chkID, "runner_id", runnerID)
						sessionStatus = ""
					case terminalSessionStatus == "completed" && (session.ResumeMode == "harness_session" || session.ResumeMode == "gvisor_checkpoint"):
						if _, err := q.PauseAgentSessionByTaskID(ctx, repository.PauseAgentSessionByTaskIDParams{
							Status:           "paused",
							PinnedRunnerID:   nullString(runnerID),
							CheckpointID:     sql.NullString{},
							WorkspacePath:    nullString(event.WorkspacePath),
							ContainerName:    sql.NullString{},
							HarnessSessionID: nullString(event.OpencodeSessionId),
							PausedAt:         sql.NullTime{Time: now, Valid: true},
							UpdatedAt:        now,
							TaskID:           event.TaskId,
						}); err != nil {
							return err
						}
						slog.Info("agent session paused for resume", "session_id", session.ID, "workspace_path", event.WorkspacePath, "runner_id", runnerID)
						sessionStatus = ""
					}
				}
			}

			if sessionStatus != "" {
				if _, err := q.MarkAgentSessionTerminalByTask(ctx, repository.MarkAgentSessionTerminalByTaskParams{
					Status:           sessionStatus,
					HarnessSessionID: event.OpencodeSessionId,
					Error:            nullString(event.Error),
					UpdatedAt:        now,
					TaskID:           event.TaskId,
				}); err != nil {
					return err
				}
			}
		}
		if !skipEventRow {
			return q.InsertTaskEvent(ctx, eventInsert)
		}
		return nil
	})
	if errors.Is(err, errTaskNotClaimed) {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task is not running for runner %s", runnerID))
	}
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	if s.eventBus != nil && !skipEventRow {
		s.eventBus.PublishTaskEvent(event.TaskId, eventID, status, eventType, event.Summary, string(payload), now.Format(time.RFC3339))
	}
	if s.callbacks != nil && !skipEventRow {
		if task, err := s.db.GetTaskByID(ctx, event.TaskId); err == nil {
			dispatch := TaskEventCallbackContext{
				ID:            eventID,
				TaskID:        event.TaskId,
				TeamID:        task.TeamID.String,
				Subject:       eventInsert.Subject,
				Status:        status,
				EventType:     eventType,
				Summary:       event.Summary,
				Error:         event.Error,
				ErrorCategory: errorCategory,
				Payload:       payload,
				CreatedAt:     now,
			}
			go func() {
				callbackCtx, cancel := context.WithTimeout(context.Background(), eventHandlerTimeout)
				defer cancel()
				s.callbacks.DispatchTaskEventCallbacks(callbackCtx, dispatch)
			}()
		} else {
			slog.Warn("could not load task for event callbacks", "task_id", event.TaskId, "error", err)
		}
	}
	return nil
}

func sessionTerminalStatuses(taskStatus string) (runStatus, sessionStatus string, ok bool) {
	switch taskStatus {
	case "done", "completed":
		return "completed", "completed", true
	case "error":
		return "failed", "error", true
	case "cancelled":
		return "cancelled", "error", true
	default:
		return "", "", false
	}
}

func statusIsErrorCategoryCandidate(status string) bool {
	return status == "error" || status == "cancelled"
}

func normalizeErrorCategory(category string) string {
	switch category {
	case "budget_exceeded", "model_error", "runtime_error", "timeout", "transport_error", "stuck", "cancelled", "unknown":
		return category
	default:
		return ""
	}
}

func isRecoverablePromptError(errorCategory string) bool {
	return errorCategory == "timeout" || errorCategory == "transport_error"
}

func classifyTaskErrorCategory(status, message string) string {
	if status == "cancelled" {
		return "cancelled"
	}
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "budget"), strings.Contains(lower, "cost limit"), strings.Contains(lower, "max budget"):
		return "budget_exceeded"
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "deadline exceeded"), strings.Contains(lower, "context deadline"), strings.Contains(lower, "lease expired"):
		return "timeout"
	case isPromptTransportFailureMessage(lower):
		return "transport_error"
	case strings.Contains(lower, "stuck"), strings.Contains(lower, "loop"):
		return "stuck"
	case strings.Contains(lower, "model"), strings.Contains(lower, "llm"), strings.Contains(lower, "rate limit"), strings.Contains(lower, "provider"), strings.Contains(lower, "api error"):
		return "model_error"
	case message == "":
		return "unknown"
	default:
		return "runtime_error"
	}
}

func isPromptTransportFailureMessage(lower string) bool {
	if !strings.Contains(lower, "post /message") {
		return false
	}
	return strings.Contains(lower, "eof") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "broken pipe") ||
		strings.Contains(lower, "server closed") ||
		strings.Contains(lower, "connection refused")
}

func taskEventType(status, errorCategory string, heartbeat bool) string {
	if heartbeat {
		return "task.heartbeat"
	}
	switch status {
	case "done", "completed":
		return "task.completed"
	case "error":
		if errorCategory == "" {
			errorCategory = "unknown"
		}
		return "task.failed." + errorCategory
	case "cancelled":
		return "task.cancelled"
	case "running":
		return "task.progress"
	default:
		return "task." + sanitizeEventTypePart(status)
	}
}

func sanitizeEventTypePart(part string) string {
	if part == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(part) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

func taskToProto(task repository.ChetterTask, resumeCheckpointPath, resumeWorkspacePath string) *runnerv1.Task {
	skills := parseJSON[[]string](task.Skills, "task:"+task.ID+" skills")
	env := parseJSON[map[string]string](task.Env, "task:"+task.ID+" env")
	harness := env["__chetter_harness"]
	delete(env, "__chetter_harness")
	return &runnerv1.Task{
		TaskId:                 task.ID,
		AgentImage:             task.AgentImage.String,
		Prompt:                 task.Prompt,
		GitUrl:                 task.GitUrl.String,
		GitRef:                 task.GitRef.String,
		Agent:                  task.Agent.String,
		ProviderId:             task.ProviderID.String,
		ModelId:                task.ModelID.String,
		VariantId:              task.VariantID.String,
		Skills:                 skills,
		TimeoutSeconds:         task.TimeoutSec,
		MaxMemoryMb:            defaultMaxMemoryMB,
		MaxCpu:                 defaultMaxCPU,
		Env:                    env,
		Attempt:                task.Attempt,
		CheckpointAfterSuccess: task.CheckpointAfterSuccess,
		ResumeCheckpointPath:   resumeCheckpointPath,
		ResumeWorkspacePath:    resumeWorkspacePath,
		Harness:                harness,
		GitIdentityId:          task.GitIdentityID.String,
		GitAuthorName:          task.CommitAuthorName.String,
		GitAuthorEmail:         task.CommitAuthorEmail.String,
	}
}

func nullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func nullStringSlice(values []string) []sql.NullString {
	out := make([]sql.NullString, 0, len(values))
	for _, value := range values {
		if value != "" {
			out = append(out, nullString(value))
		}
	}
	return out
}

func normalizeTeamNames(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return normalizeTeamNames(strings.Split(value, ","))
}

func parseOptionalTime(value string) sql.NullTime {
	if value == "" {
		return sql.NullTime{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: parsed.UTC(), Valid: true}
}

func isHeartbeatSummary(summary string) bool {
	return strings.Contains(summary, "server.heartbeat")
}

func (s *RunnerRPCService) shouldStoreHeartbeat(taskID string) bool {
	now := time.Now()
	if v, ok := s.heartbeatSeen.Load(taskID); ok {
		if last, ok := v.(time.Time); ok && now.Sub(last) < heartbeatEventMinInterval {
			return false
		}
	}
	s.heartbeatSeen.Store(taskID, now)
	return true
}

// cleanupHeartbeatSeen removes entries older than 2x the throttle interval
// to prevent unbounded memory growth in the heartbeatSeen sync.Map.
func (s *RunnerRPCService) cleanupHeartbeatSeen() {
	deadline := time.Now().Add(-heartbeatEventMinInterval * 2)
	s.heartbeatSeen.Range(func(key, value any) bool {
		if last, ok := value.(time.Time); ok && last.Before(deadline) {
			s.heartbeatSeen.Delete(key)
		}
		return true
	})
}

func tokenUsageInputTokens(tu *runnerv1.TokenUsage) int64 {
	if tu == nil {
		return 0
	}
	return tu.InputTokens
}

func tokenUsageOutputTokens(tu *runnerv1.TokenUsage) int64 {
	if tu == nil {
		return 0
	}
	return tu.OutputTokens
}

func tokenUsageCacheReadTokens(tu *runnerv1.TokenUsage) int64 {
	if tu == nil {
		return 0
	}
	return tu.CacheReadTokens
}

func tokenUsageCacheWriteTokens(tu *runnerv1.TokenUsage) int64 {
	if tu == nil {
		return 0
	}
	return tu.CacheWriteTokens
}

func tokenUsageReasoningTokens(tu *runnerv1.TokenUsage) int64 {
	if tu == nil {
		return 0
	}
	return tu.ReasoningTokens
}

func tokenUsageCostCents(tu *runnerv1.TokenUsage) int64 {
	if tu == nil {
		return 0
	}
	return tu.CostCents
}
