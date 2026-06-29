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
	"github.com/flatout-works/chetter/pkg/definitions"
	"github.com/flatout-works/chetter/pkg/modelcatalog"
)

const (
	defaultClaimWaitSec       = 30
	defaultTaskLeaseSec       = 60
	claimPollInterval         = time.Second
	runnerEventSubject        = "connect.runner"
	heartbeatEventMinInterval = 60 * time.Second
	injectedGitHubTokenEnv    = "__chetter_github_token"
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
			mcpProfileNames := parseJSON[[]string](task.McpProfiles, "task:"+task.ID+" mcp_profiles")
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
			s.resolveTaskDefinitions(ctx, protoTask, task, mcpProfileNames)
			s.injectGitHubToken(ctx, protoTask)
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

func (s *RunnerRPCService) injectGitHubToken(ctx context.Context, task *runnerv1.Task) {
	if task == nil || task.Env == nil {
		return
	}
	writeAllowed := task.Env[gitHubTokenAllowedEnv] == "true"
	readAllowed := task.Env[gitHubReadTokenAllowedEnv] == "true"
	delete(task.Env, gitHubTokenAllowedEnv)
	delete(task.Env, gitHubReadTokenAllowedEnv)
	if (!writeAllowed && !readAllowed) || !githubTokenContextComplete(task.Env) {
		return
	}
	if s.ghActions == nil {
		slog.Warn("task requested GitHub token but GitHub App is not configured", "taskID", task.TaskId)
		return
	}
	repoName, ok := canonicalRepoName(task.Env["GITHUB_REPO"])
	if !ok {
		slog.Warn("task requested GitHub token with invalid repo", "taskID", task.TaskId)
		return
	}
	task.Env["GITHUB_REPO"] = repoName
	var token string
	var err error
	if writeAllowed {
		token, err = s.ghActions.GitHubInstallationTokenForRepository(repoName)
	} else {
		token, err = s.ghActions.GitHubReadInstallationTokenForRepository(repoName)
	}
	if err != nil {
		slog.Warn("mint GitHub installation token for task", "taskID", task.TaskId, "err", err)
		return
	}
	task.Env[injectedGitHubTokenEnv] = token
}

type resolvedModelConfig struct {
	ProviderID        string
	ModelID           string
	ProviderName      string
	ProviderBaseURL   string
	ProviderAPIKeyEnv string
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
}

func (s *RunnerRPCService) resolveTaskDefinitions(ctx context.Context, task *runnerv1.Task, dbTask repository.ChetterTask, mcpProfileNames []string) {
	if task == nil {
		return
	}
	teamID := dbTask.TeamID.String
	gitURL := definitionLookupRef(dbTask.GitUrl.String, task.Env)
	allowPrivilegedMCPProfiles := task.Env[mcpProfilePrivilegedEnv] == "true"
	delete(task.Env, definitionRepoEnv)
	delete(task.Env, mcpProfilePrivilegedEnv)
	if task.Agent != "" {
		groups, err := selectScopedDefinitionGroups(ctx, s.rawDB, definitions.DefinitionTypeAgent, []string{task.Agent}, teamID, gitURL)
		if err == nil {
			if rows := groups[task.Agent]; len(rows) > 0 {
				task.AgentDefinition = rows[0].Content
			}
		} else {
			slog.Warn("resolve agent definition query", "err", err)
		}
	}
	if len(task.Skills) > 0 {
		skillDefs := s.resolveSkillDefinitions(ctx, task.Skills, teamID, gitURL)
		if len(skillDefs) > 0 {
			task.SkillDefinitions = skillDefs
		}
	}
	if len(mcpProfileNames) > 0 {
		task.McpProfiles = s.resolveMCPProfiles(ctx, mcpProfileNames, teamID, gitURL, allowPrivilegedMCPProfiles)
	}
}

func (s *RunnerRPCService) resolveMCPProfiles(ctx context.Context, profileNames []string, teamID, gitURL string, allowPrivileged bool) []*runnerv1.MCPProfile {
	names := uniqueNonEmptyStrings(profileNames)
	if len(names) == 0 {
		return nil
	}
	groups, err := selectScopedDefinitionGroups(ctx, s.rawDB, definitions.DefinitionTypeMCPProfile, names, teamID, gitURL)
	if err != nil {
		slog.Warn("resolve mcp profile definitions query", "err", err)
		return invalidMCPProfiles(names)
	}

	resolved := make(map[string]*runnerv1.MCPProfile, len(names))
	for name, rows := range groups {
		if len(rows) == 0 {
			continue
		}
		profile, err := definitions.ParseMCPProfileYAML(rows[0].Content)
		if err != nil {
			slog.Warn("parse mcp profile definition", "name", name, "err", err)
			continue
		}
		if profile.Name != name {
			slog.Warn("mcp profile definition name mismatch", "selected", name, "content_name", profile.Name)
			continue
		}
		if !allowPrivileged && mcpProfileRequiresPrivilegedAccess(profile) {
			slog.Warn("selected mcp profile requires admin access", "name", name)
			continue
		}
		resolved[name] = &runnerv1.MCPProfile{
			Name:          profile.Name,
			Type:          profile.Type,
			Transport:     profile.Transport,
			Url:           profile.URL,
			Headers:       profile.Headers,
			ToolAllowlist: profile.ToolAllowlist,
		}
	}
	out := make([]*runnerv1.MCPProfile, 0, len(names))
	for _, name := range names {
		if profile, ok := resolved[name]; ok {
			out = append(out, profile)
			continue
		}
		slog.Warn("selected mcp profile is not active or valid", "name", name)
		out = append(out, &runnerv1.MCPProfile{Name: name})
	}
	return out
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func invalidMCPProfiles(names []string) []*runnerv1.MCPProfile {
	out := make([]*runnerv1.MCPProfile, 0, len(names))
	for _, name := range names {
		out = append(out, &runnerv1.MCPProfile{Name: name})
	}
	return out
}

func (s *RunnerRPCService) resolveSkillDefinitions(ctx context.Context, skillNames []string, teamID, gitURL string) map[string][]byte {
	groups, err := selectScopedDefinitionGroups(ctx, s.rawDB, definitions.DefinitionTypeSkill, skillNames, teamID, gitURL)
	if err != nil {
		slog.Warn("resolve skill definitions query", "err", err)
		return nil
	}
	if len(groups) == 0 {
		return nil
	}

	out := make(map[string][]byte, len(groups))
	for name, rows := range groups {
		files := make([]skillFileEntry, 0, len(rows))
		for _, row := range rows {
			files = append(files, skillFileEntry{path: row.Path, content: row.Content})
		}
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
	provider, ok := catalog.Providers[providerID]
	if !ok {
		return resolvedModelConfig{ProviderID: providerID, ModelID: modelID}
	}
	if hp, ok := provider.Harnesses[harness]; ok && hp.Disabled {
		defaultProvider, defaultModel := catalogDefaultForHarness(catalog, harness)
		return catalogProviderModelConfig(catalog, harness, defaultProvider, defaultModel)
	}
	resolved := resolvedModelConfig{ProviderID: providerID, ModelID: modelID}
	if hp, ok := provider.Harnesses[harness]; ok && !hp.Disabled {
		if hp.ID != "" {
			resolved.ProviderID = hp.ID
		}
		resolved.ProviderName = firstNonEmpty(hp.Name, provider.Name, resolved.ProviderID)
		resolved.ProviderBaseURL = firstNonEmpty(hp.BaseURL, provider.BaseURL)
		resolved.ProviderAPIKeyEnv = firstNonEmpty(hp.APIKeyEnv, provider.APIKeyEnv)
	} else {
		resolved.ProviderName = firstNonEmpty(provider.Name, resolved.ProviderID)
		resolved.ProviderBaseURL = provider.BaseURL
		resolved.ProviderAPIKeyEnv = provider.APIKeyEnv
	}
	for _, model := range provider.Models {
		if model.ID != modelID {
			continue
		}
		if hm, ok := model.Harnesses[harness]; ok {
			if hm.Disabled {
				defaultProvider, defaultModel := catalogDefaultForHarness(catalog, harness)
				return catalogProviderModelConfig(catalog, harness, defaultProvider, defaultModel)
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
	case "", "codex":
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
					case terminalSessionStatus == "error" && errorCategory == "timeout" && session.ResumeMode == "harness_session" && event.OpencodeSessionId != "":
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
						slog.Info("agent session marked recoverable after timeout", "session_id", session.ID, "workspace_path", event.WorkspacePath, "runner_id", runnerID)
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
	case "budget_exceeded", "model_error", "runtime_error", "timeout", "stuck", "cancelled", "unknown":
		return category
	default:
		return ""
	}
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
	}
}

func nullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
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
