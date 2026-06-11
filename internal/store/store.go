// Package store persists devfleet state in a TiDB/MySQL-compatible database.
package store

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
)

var tidbTLSMu sync.Mutex

const (
	maxOpenConns          = 10
	maxIdleConns          = 5
	connMaxLifetime       = 30 * time.Minute
	maxListTasksLimit     = 100
	defaultListTasksLimit = 20
)

var errTiDBRequiresTCPHost = fmt.Errorf("tls=tidb requires a tcp database host")

type Store struct {
	db *sql.DB
}

// TaskRecord is the persisted task state exposed by MCP tools.
type TaskRecord struct {
	ID                string            `json:"id"`
	Status            string            `json:"status"`
	Prompt            string            `json:"prompt"`
	GitURL            string            `json:"git_url,omitempty"`
	GitRef            string            `json:"git_ref,omitempty"`
	AgentImage        string            `json:"agent_image,omitempty"`
	Agent             string            `json:"agent,omitempty"`
	ProviderID        string            `json:"provider_id,omitempty"`
	ModelID           string            `json:"model_id,omitempty"`
	VariantID         string            `json:"variant_id,omitempty"`
	OpenCodeSessionID string            `json:"opencode_session_id,omitempty"`
	RunnerImageDigest string            `json:"runner_image_digest,omitempty"`
	CommitAuthorName  string            `json:"commit_author_name,omitempty"`
	CommitAuthorEmail string            `json:"commit_author_email,omitempty"`
	Skills            []string          `json:"skills"`
	Env               map[string]string `json:"env"`
	TimeoutSec        int               `json:"timeout_sec"`
	Summary           string            `json:"summary,omitempty"`
	Error             string            `json:"error,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
	StartedAt         *time.Time        `json:"started_at,omitempty"`
	EndedAt           *time.Time        `json:"ended_at,omitempty"`
}

// TaskInput contains fields needed to insert a new task.
type TaskInput struct {
	ID         string
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

// TaskResponse is the runner status event shape.
type TaskResponse struct {
	TaskID            string    `json:"task_id"`
	Status            string    `json:"status"`
	Summary           string    `json:"summary,omitempty"`
	Error             string    `json:"error,omitempty"`
	Artifacts         []string  `json:"artifacts,omitempty"`
	ProviderID        string    `json:"provider_id,omitempty"`
	ModelID           string    `json:"model_id,omitempty"`
	VariantID         string    `json:"variant_id,omitempty"`
	OpenCodeSessionID string    `json:"opencode_session_id,omitempty"`
	RunnerImageDigest string    `json:"runner_image_digest,omitempty"`
	StartedAt         time.Time `json:"started_at,omitempty"`
	EndedAt           time.Time `json:"ended_at,omitempty"`
}

// RunnerHeartbeat is the runner presence event persisted from NATS.
type RunnerHeartbeat struct {
	EventType      string    `json:"event_type,omitempty"`
	RunnerID       string    `json:"runner_id"`
	Status         string    `json:"status"`
	ImageRef       string    `json:"image_ref,omitempty"`
	ImageDigest    string    `json:"image_digest,omitempty"`
	Version        string    `json:"version,omitempty"`
	ListenSubject  string    `json:"listen_subject,omitempty"`
	ResultSubject  string    `json:"result_subject,omitempty"`
	MaxConcurrent  int       `json:"max_concurrent,omitempty"`
	RunningTasks   int       `json:"running_tasks"`
	AvailableSlots int       `json:"available_slots"`
	TotalStarted   int64     `json:"total_started"`
	TotalCompleted int64     `json:"total_completed"`
	TotalErrors    int64     `json:"total_errors"`
	CurrentTaskIDs []string  `json:"current_task_ids,omitempty"`
	ExecutionMode  string    `json:"execution_mode,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	SentAt         time.Time `json:"sent_at,omitempty"`
}

// EventRecord is a persisted runner message.
type EventRecord struct {
	ID        string
	TaskID    string
	Subject   string
	Status    string
	Payload   []byte
	CreatedAt time.Time
}

// ScheduleRecord is a cron-backed task template.
type ScheduleRecord struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CronExpr   string     `json:"cron_expr"`
	Prompt     string     `json:"prompt"`
	GitURL     string     `json:"git_url,omitempty"`
	GitRef     string     `json:"git_ref,omitempty"`
	AgentImage string     `json:"agent_image,omitempty"`
	Agent      string     `json:"agent,omitempty"`
	ProviderID string     `json:"provider_id,omitempty"`
	ModelID    string     `json:"model_id,omitempty"`
	VariantID  string     `json:"variant_id,omitempty"`
	Skills     []string   `json:"skills"`
	TimeoutSec int        `json:"timeout_sec"`
	Enabled    bool       `json:"enabled"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	LastRunAt  *time.Time `json:"last_run_at,omitempty"`
	NextRunAt  *time.Time `json:"next_run_at,omitempty"`
}

// ScheduleInput contains fields needed to create a schedule.
type ScheduleInput struct {
	ID         string
	Name       string
	CronExpr   string
	Prompt     string
	GitURL     string
	GitRef     string
	AgentImage string
	Agent      string
	ProviderID string
	ModelID    string
	VariantID  string
	Skills     []string
	TimeoutSec int
}

// Open creates a database pool and applies conservative connection limits.
func Open(dsn string) (*Store, error) {
	normalized := normalizeDSN(dsn)
	if err := registerTiDBTLS(normalized); err != nil {
		return nil, err
	}
	db, err := sql.Open("mysql", normalized)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxLifetime(connMaxLifetime)
	return &Store{db: db}, nil
}

// Close closes the database pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// Ping verifies database connectivity.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// ApplySchema creates the devfleet tables if they do not exist.
func (s *Store) ApplySchema(ctx context.Context) error {
	for _, stmt := range schemaStatements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
	}
	if err := s.ensureTaskMetadataColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureScheduleMetadataColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureRunnerMetadataColumns(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureTaskMetadataColumns(ctx context.Context) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"agent", "ALTER TABLE devfleet_tasks ADD COLUMN agent VARCHAR(128) NULL AFTER agent_image"},
		{"provider_id", "ALTER TABLE devfleet_tasks ADD COLUMN provider_id VARCHAR(128) NULL AFTER agent_image"},
		{"model_id", "ALTER TABLE devfleet_tasks ADD COLUMN model_id VARCHAR(255) NULL AFTER provider_id"},
		{"variant_id", "ALTER TABLE devfleet_tasks ADD COLUMN variant_id VARCHAR(128) NULL AFTER model_id"},
		{"opencode_session_id", "ALTER TABLE devfleet_tasks ADD COLUMN opencode_session_id VARCHAR(128) NULL AFTER variant_id"},
		{"runner_image_digest", "ALTER TABLE devfleet_tasks ADD COLUMN runner_image_digest VARCHAR(255) NULL AFTER opencode_session_id"},
		{"commit_author_name", "ALTER TABLE devfleet_tasks ADD COLUMN commit_author_name VARCHAR(128) NULL AFTER runner_image_digest"},
		{"commit_author_email", "ALTER TABLE devfleet_tasks ADD COLUMN commit_author_email VARCHAR(255) NULL AFTER commit_author_name"},
	}
	for _, column := range columns {
		exists, err := s.columnExists(ctx, "devfleet_tasks", column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := s.db.ExecContext(ctx, column.ddl); err != nil {
			return fmt.Errorf("add devfleet_tasks.%s: %w", column.name, err)
		}
	}
	return nil
}

func (s *Store) ensureScheduleMetadataColumns(ctx context.Context) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"agent", "ALTER TABLE devfleet_schedules ADD COLUMN agent VARCHAR(128) NULL AFTER agent_image"},
		{"provider_id", "ALTER TABLE devfleet_schedules ADD COLUMN provider_id VARCHAR(128) NULL AFTER agent"},
		{"model_id", "ALTER TABLE devfleet_schedules ADD COLUMN model_id VARCHAR(255) NULL AFTER provider_id"},
		{"variant_id", "ALTER TABLE devfleet_schedules ADD COLUMN variant_id VARCHAR(128) NULL AFTER model_id"},
	}
	for _, column := range columns {
		exists, err := s.columnExists(ctx, "devfleet_schedules", column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := s.db.ExecContext(ctx, column.ddl); err != nil {
			return fmt.Errorf("add devfleet_schedules.%s: %w", column.name, err)
		}
	}
	return nil
}

func (s *Store) ensureRunnerMetadataColumns(ctx context.Context) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"image_ref", "ALTER TABLE devfleet_runners ADD COLUMN image_ref VARCHAR(512) NULL AFTER status"},
		{"image_digest", "ALTER TABLE devfleet_runners ADD COLUMN image_digest VARCHAR(255) NULL AFTER image_ref"},
		{"version", "ALTER TABLE devfleet_runners ADD COLUMN version VARCHAR(128) NULL AFTER image_digest"},
		{"listen_subject", "ALTER TABLE devfleet_runners ADD COLUMN listen_subject VARCHAR(255) NULL AFTER version"},
		{"result_subject", "ALTER TABLE devfleet_runners ADD COLUMN result_subject VARCHAR(255) NULL AFTER listen_subject"},
		{"max_concurrent", "ALTER TABLE devfleet_runners ADD COLUMN max_concurrent INT NOT NULL DEFAULT 0 AFTER result_subject"},
		{"running_tasks", "ALTER TABLE devfleet_runners ADD COLUMN running_tasks INT NOT NULL DEFAULT 0 AFTER max_concurrent"},
		{"available_slots", "ALTER TABLE devfleet_runners ADD COLUMN available_slots INT NOT NULL DEFAULT 0 AFTER running_tasks"},
		{"total_started", "ALTER TABLE devfleet_runners ADD COLUMN total_started BIGINT NOT NULL DEFAULT 0 AFTER available_slots"},
		{"total_completed", "ALTER TABLE devfleet_runners ADD COLUMN total_completed BIGINT NOT NULL DEFAULT 0 AFTER total_started"},
		{"total_errors", "ALTER TABLE devfleet_runners ADD COLUMN total_errors BIGINT NOT NULL DEFAULT 0 AFTER total_completed"},
		{"started_at", "ALTER TABLE devfleet_runners ADD COLUMN started_at DATETIME(6) NULL AFTER total_errors"},
		{"first_seen_at", "ALTER TABLE devfleet_runners ADD COLUMN first_seen_at DATETIME(6) NULL AFTER started_at"},
		{"updated_at", "ALTER TABLE devfleet_runners ADD COLUMN updated_at DATETIME(6) NULL AFTER last_seen_at"},
	}
	for _, column := range columns {
		exists, err := s.columnExists(ctx, "devfleet_runners", column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := s.db.ExecContext(ctx, column.ddl); err != nil {
			return fmt.Errorf("add devfleet_runners.%s: %w", column.name, err)
		}
	}
	return nil
}

func (s *Store) columnExists(ctx context.Context, table, column string) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?
	`, table, column).Scan(&count); err != nil {
		return false, fmt.Errorf("check column %s.%s: %w", table, column, err)
	}
	return count > 0, nil
}

// InsertTask stores a pending task before it is published to NATS.
func (s *Store) InsertTask(ctx context.Context, in TaskInput) error {
	skills, err := json.Marshal(nonNilStrings(in.Skills))
	if err != nil {
		return fmt.Errorf("marshal skills: %w", err)
	}
	env, err := json.Marshal(nonNilMap(in.Env))
	if err != nil {
		return fmt.Errorf("marshal env: %w", err)
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO devfleet_tasks
			(id, status, prompt, git_url, git_ref, agent_image, agent, provider_id, model_id, variant_id, commit_author_name, commit_author_email, skills, env, timeout_sec, created_at, updated_at)
		VALUES (?, 'pending', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, in.ID, in.Prompt, in.GitURL, in.GitRef, in.AgentImage, in.Agent, in.ProviderID, in.ModelID, in.VariantID, "Devfleet", "devfleet@devfleet.works", string(skills), string(env), in.TimeoutSec, now, now)
	if err != nil {
		return fmt.Errorf("insert task: %w", err)
	}
	return nil
}

// GetTask returns one task by ID.
func (s *Store) GetTask(ctx context.Context, id string) (TaskRecord, error) {
	rows, err := s.queryTasks(ctx, `WHERE id = ?`, id)
	if err != nil {
		return TaskRecord{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return TaskRecord{}, sql.ErrNoRows
	}
	return scanTask(rows)
}

// ListTasks returns recent tasks, optionally filtered by status.
func (s *Store) ListTasks(ctx context.Context, status string, limit int) ([]TaskRecord, error) {
	if limit <= 0 || limit > maxListTasksLimit {
		limit = defaultListTasksLimit
	}
	var rows *sql.Rows
	var err error
	if status == "" {
		rows, err = s.queryTasks(ctx, `ORDER BY created_at DESC LIMIT ?`, limit)
	} else {
		rows, err = s.queryTasks(ctx, `WHERE status = ? ORDER BY created_at DESC LIMIT ?`, status, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskRecord
	for rows.Next() {
		record, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// CancelTask marks a single task as cancelled by ID. It only allows
// cancelling tasks that are pending or running (not already terminal).
func (s *Store) CancelTask(ctx context.Context, taskID, reason string) (TaskRecord, error) {
	if taskID == "" {
		return TaskRecord{}, fmt.Errorf("task_id is required")
	}
	if reason == "" {
		reason = "cancelled by operator"
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		UPDATE devfleet_tasks
		SET status = 'cancelled', error = ?, ended_at = COALESCE(ended_at, ?), updated_at = ?
		WHERE id = ? AND status IN ('pending', 'running')
	`, reason, now, now, taskID)
	if err != nil {
		return TaskRecord{}, fmt.Errorf("cancel task: %w", err)
	}
	return s.GetTask(ctx, taskID)
}

// ClearPendingTasks moves queued DB tasks out of the pending queue.
func (s *Store) ClearPendingTasks(ctx context.Context, reason string) (int, error) {
	if reason == "" {
		reason = "cancelled by devfleet queue clear"
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE devfleet_tasks
		SET status = 'cancelled', error = ?, ended_at = COALESCE(ended_at, ?), updated_at = ?
		WHERE status = 'pending'
	`, reason, now, now)
	if err != nil {
		return 0, fmt.Errorf("clear pending tasks: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return int(affected), nil
}

// ReapStaleTasks finds running tasks that have not received a heartbeat
// within their timeout + grace period and marks them as error.
func (s *Store) ReapStaleTasks(ctx context.Context, grace time.Duration) (int, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE devfleet_tasks
		SET status = 'error',
		    error = CONCAT('runner timeout: no heartbeat for ', TIMESTAMPDIFF(SECOND, updated_at, NOW()), ' seconds (timeout was ', timeout_sec, 's)'),
		    ended_at = ?,
		    updated_at = ?
		WHERE status = 'running'
		  AND TIMESTAMPDIFF(SECOND, updated_at, NOW()) > timeout_sec + ?
	`, time.Now().UTC(), time.Now().UTC(), int(grace.Seconds()))
	if err != nil {
		return 0, fmt.Errorf("reap stale tasks: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return int(affected), nil
}

// ReapStalePendingTasks finds pending tasks that have not been picked up by a
// runner within the grace period and cancels them. This handles the rare case
// where a runner acked the NATS message but crashed before publishing a
// "running" status event, leaving the DB row stuck in pending.
func (s *Store) ReapStalePendingTasks(ctx context.Context, grace time.Duration) (int, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE devfleet_tasks
		SET status = 'cancelled',
		    error = CONCAT('pending timeout: no runner picked up task within ', TIMESTAMPDIFF(SECOND, created_at, NOW()), ' seconds'),
		    ended_at = ?,
		    updated_at = ?
		WHERE status = 'pending'
		  AND TIMESTAMPDIFF(SECOND, created_at, NOW()) > ?
	`, now, now, int(grace.Seconds()))
	if err != nil {
		return 0, fmt.Errorf("reap stale pending tasks: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return int(affected), nil
}

// RunnerFleetHealth holds aggregate health metrics derived from task activity.
type RunnerFleetHealth struct {
	TotalTasks       int               `json:"total_tasks"`
	PendingTasks     int               `json:"pending_tasks"`
	RunningTasks     int               `json:"running_tasks"`
	StaleTasks       int               `json:"stale_tasks"`
	DoneTasks        int               `json:"done_tasks"`
	ErrorTasks       int               `json:"error_tasks"`
	RunnerImages     []RunnerImageInfo `json:"runner_images"`
	Runners          []RunnerInfo      `json:"runners"`
	RunningTaskInfos []RunningTaskInfo `json:"running_task_infos,omitempty"`
	FleetActive      bool              `json:"fleet_active"`
	GeneratedAt      time.Time         `json:"generated_at"`
}

// RunnerImageInfo counts active runners and running tasks grouped by image.
type RunnerImageInfo struct {
	ImageDigest string `json:"image_digest"`
	ImageRef    string `json:"image_ref,omitempty"`
	RunnerCount int    `json:"runner_count"`
	TaskCount   int    `json:"task_count"`
}

// RunnerInfo is one runner's latest heartbeat and lightweight counters.
type RunnerInfo struct {
	ID             string     `json:"id"`
	Status         string     `json:"status"`
	ImageRef       string     `json:"image_ref,omitempty"`
	ImageDigest    string     `json:"image_digest,omitempty"`
	Version        string     `json:"version,omitempty"`
	ListenSubject  string     `json:"listen_subject,omitempty"`
	ResultSubject  string     `json:"result_subject,omitempty"`
	MaxConcurrent  int        `json:"max_concurrent"`
	RunningTasks   int        `json:"running_tasks"`
	AvailableSlots int        `json:"available_slots"`
	TotalStarted   int64      `json:"total_started"`
	TotalCompleted int64      `json:"total_completed"`
	TotalErrors    int64      `json:"total_errors"`
	CurrentTaskIDs []string   `json:"current_task_ids"`
	FirstSeenAt    *time.Time `json:"first_seen_at,omitempty"`
	LastSeenAt     time.Time  `json:"last_seen_at"`
	LastSeenSec    int        `json:"last_seen_sec"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	IsStale        bool       `json:"is_stale"`
}

// RunningTaskInfo shows per-task details for currently running tasks.
type RunningTaskInfo struct {
	TaskID       string     `json:"task_id"`
	PromptHdr    string     `json:"prompt_hdr"`
	Summary      string     `json:"summary,omitempty"`
	ModelID      string     `json:"model_id,omitempty"`
	ImageDigest  string     `json:"image_digest,omitempty"`
	LastEventSec int        `json:"last_event_sec"`
	IsStale      bool       `json:"is_stale"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
}

// GetRunnerFleetHealth computes fleet health from task state and runner presence.
func (s *Store) GetRunnerFleetHealth(ctx context.Context, maxEventSecForActive, maxRunnerPresenceSec int) (RunnerFleetHealth, error) {
	health := RunnerFleetHealth{GeneratedAt: time.Now().UTC()}

	rows, err := s.db.QueryContext(ctx, `
		SELECT status, COUNT(*) FROM devfleet_tasks GROUP BY status
	`)
	if err != nil {
		return health, fmt.Errorf("count by status: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return health, fmt.Errorf("scan status count: %w", err)
		}
		health.TotalTasks += count
		switch status {
		case "pending":
			health.PendingTasks = count
		case "running":
			health.RunningTasks = count
		case "done":
			health.DoneTasks = count
		case "error":
			health.ErrorTasks = count
		}
	}
	if err := rows.Err(); err != nil {
		return health, fmt.Errorf("rows err after status count: %w", err)
	}

	runningRows, err := s.db.QueryContext(ctx, `
		SELECT id, prompt, summary, model_id, runner_image_digest, started_at,
		       TIMESTAMPDIFF(SECOND, updated_at, NOW()) AS last_event_sec
		FROM devfleet_tasks
		WHERE status = 'running'
		ORDER BY started_at ASC
	`)
	if err != nil {
		return health, fmt.Errorf("query running tasks: %w", err)
	}
	defer runningRows.Close()
	imageCounts := map[string]int{}
	for runningRows.Next() {
		var taskID, prompt, summary, modelID, imageDigest sql.NullString
		var startedAt sql.NullTime
		var lastEventSec int
		if err := runningRows.Scan(&taskID, &prompt, &summary, &modelID, &imageDigest, &startedAt, &lastEventSec); err != nil {
			return health, fmt.Errorf("scan running task: %w", err)
		}
		promptHdr := firstLineOrNA(prompt.String)
		info := RunningTaskInfo{
			TaskID:       taskID.String,
			PromptHdr:    promptHdr,
			Summary:      summary.String,
			ModelID:      modelID.String,
			ImageDigest:  imageDigest.String,
			LastEventSec: lastEventSec,
			IsStale:      lastEventSec > maxEventSecForActive,
		}
		if startedAt.Valid {
			info.StartedAt = &startedAt.Time
		}
		if info.IsStale {
			health.StaleTasks++
		}
		health.RunningTaskInfos = append(health.RunningTaskInfos, info)

		imgKey := imageDigest.String
		if imgKey == "" {
			imgKey = "unknown"
		}
		imageCounts[imgKey]++
	}
	if err := runningRows.Err(); err != nil {
		return health, fmt.Errorf("rows err after running tasks: %w", err)
	}

	runnerImageCounts := map[string]RunnerImageInfo{}
	runnerRows, err := s.db.QueryContext(ctx, `
		SELECT id, status, image_ref, image_digest, version, listen_subject, result_subject,
		       max_concurrent, running_tasks, available_slots, total_started, total_completed, total_errors,
		       first_seen_at, last_seen_at, started_at, metadata,
		       TIMESTAMPDIFF(SECOND, last_seen_at, NOW()) AS last_seen_sec
		FROM devfleet_runners
		ORDER BY last_seen_at DESC
	`)
	if err != nil {
		return health, fmt.Errorf("query runners: %w", err)
	}
	defer runnerRows.Close()
	for runnerRows.Next() {
		var info RunnerInfo
		var imageRef, imageDigest, version, listenSubject, resultSubject sql.NullString
		var firstSeen, startedAt sql.NullTime
		var metadata []byte
		if err := runnerRows.Scan(
			&info.ID, &info.Status, &imageRef, &imageDigest, &version, &listenSubject, &resultSubject,
			&info.MaxConcurrent, &info.RunningTasks, &info.AvailableSlots, &info.TotalStarted, &info.TotalCompleted, &info.TotalErrors,
			&firstSeen, &info.LastSeenAt, &startedAt, &metadata, &info.LastSeenSec,
		); err != nil {
			return health, fmt.Errorf("scan runner: %w", err)
		}
		info.ImageRef = imageRef.String
		info.ImageDigest = imageDigest.String
		info.Version = version.String
		info.ListenSubject = listenSubject.String
		info.ResultSubject = resultSubject.String
		info.FirstSeenAt = nullTimePtr(firstSeen)
		info.StartedAt = nullTimePtr(startedAt)
		info.IsStale = info.LastSeenSec > maxRunnerPresenceSec
		info.CurrentTaskIDs = currentTaskIDsFromMetadata(metadata)
		health.Runners = append(health.Runners, info)
		if !info.IsStale {
			health.FleetActive = true
		}

		imgKey := info.ImageDigest
		if imgKey == "" {
			imgKey = "unknown"
		}
		imageInfo := runnerImageCounts[imgKey]
		imageInfo.ImageDigest = imgKey
		if imageInfo.ImageRef == "" {
			imageInfo.ImageRef = info.ImageRef
		}
		imageInfo.RunnerCount++
		runnerImageCounts[imgKey] = imageInfo
	}
	if err := runnerRows.Err(); err != nil {
		return health, fmt.Errorf("rows err after runners: %w", err)
	}

	for img, cnt := range imageCounts {
		imageInfo := runnerImageCounts[img]
		imageInfo.ImageDigest = img
		imageInfo.TaskCount = cnt
		runnerImageCounts[img] = imageInfo
	}
	for _, imageInfo := range runnerImageCounts {
		health.RunnerImages = append(health.RunnerImages, imageInfo)
	}

	return health, nil
}

// UpsertRunnerHeartbeat stores the latest presence record for a runner.
func (s *Store) UpsertRunnerHeartbeat(ctx context.Context, hb RunnerHeartbeat) error {
	if hb.RunnerID == "" {
		return fmt.Errorf("runner_id is required")
	}
	if hb.Status == "" {
		hb.Status = "active"
	}
	seenAt := hb.SentAt
	if seenAt.IsZero() {
		seenAt = time.Now().UTC()
	} else {
		seenAt = seenAt.UTC()
	}
	metadata, err := json.Marshal(hb)
	if err != nil {
		return fmt.Errorf("marshal runner metadata: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO devfleet_runners
			(id, status, image_ref, image_digest, version, listen_subject, result_subject,
			 max_concurrent, running_tasks, available_slots, total_started, total_completed, total_errors,
			 started_at, first_seen_at, last_seen_at, updated_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			status = VALUES(status),
			image_ref = VALUES(image_ref),
			image_digest = VALUES(image_digest),
			version = VALUES(version),
			listen_subject = VALUES(listen_subject),
			result_subject = VALUES(result_subject),
			max_concurrent = VALUES(max_concurrent),
			running_tasks = VALUES(running_tasks),
			available_slots = VALUES(available_slots),
			total_started = VALUES(total_started),
			total_completed = VALUES(total_completed),
			total_errors = VALUES(total_errors),
			started_at = COALESCE(VALUES(started_at), started_at),
			last_seen_at = VALUES(last_seen_at),
			updated_at = VALUES(updated_at),
			metadata = VALUES(metadata)
	`, hb.RunnerID, hb.Status, hb.ImageRef, hb.ImageDigest, hb.Version, hb.ListenSubject, hb.ResultSubject,
		hb.MaxConcurrent, hb.RunningTasks, hb.AvailableSlots, hb.TotalStarted, hb.TotalCompleted, hb.TotalErrors,
		nullableTime(hb.StartedAt), seenAt, seenAt, time.Now().UTC(), string(metadata))
	if err != nil {
		return fmt.Errorf("upsert runner heartbeat: %w", err)
	}
	return nil
}

func currentTaskIDsFromMetadata(data []byte) []string {
	var meta struct {
		CurrentTaskIDs []string `json:"current_task_ids"`
	}
	if len(data) == 0 || json.Unmarshal(data, &meta) != nil {
		return []string{}
	}
	return nonNilStrings(meta.CurrentTaskIDs)
}

func firstLineOrNA(s string) string {
	if s == "" {
		return "N/A"
	}
	idx := strings.IndexByte(s, '\n')
	if idx < 0 {
		idx = len(s)
	}
	if idx > 200 {
		idx = 200
	}
	return s[:idx]
}

// UpdateTaskFromResponse persists a runner status event.
func (s *Store) UpdateTaskFromResponse(ctx context.Context, resp TaskResponse) error {
	if resp.TaskID == "" {
		return fmt.Errorf("task_id is required")
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE devfleet_tasks
		SET status = ?, summary = ?, error = ?,
			provider_id = COALESCE(NULLIF(?, ''), provider_id),
			model_id = COALESCE(NULLIF(?, ''), model_id),
			variant_id = COALESCE(NULLIF(?, ''), variant_id),
			opencode_session_id = COALESCE(NULLIF(?, ''), opencode_session_id),
			runner_image_digest = COALESCE(NULLIF(?, ''), runner_image_digest),
			started_at = COALESCE(?, started_at), ended_at = COALESCE(?, ended_at), updated_at = ?
		WHERE id = ? AND (status NOT IN ('done', 'error', 'cancelled') OR status = ?)
	`, resp.Status, resp.Summary, resp.Error, resp.ProviderID, resp.ModelID, resp.VariantID, resp.OpenCodeSessionID, resp.RunnerImageDigest, nullableTime(resp.StartedAt), nullableTime(resp.EndedAt), time.Now().UTC(), resp.TaskID, resp.Status)
	if err != nil {
		return fmt.Errorf("update task status: %w", err)
	}
	return nil
}

// InsertEvent persists a raw task event payload.
func (s *Store) InsertEvent(ctx context.Context, id, taskID, subject, status string, payload []byte) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO devfleet_task_events (id, task_id, subject, status, payload, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, taskID, subject, status, string(payload), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("insert task event: %w", err)
	}
	return nil
}

// CreateSchedule stores a new enabled schedule.
func (s *Store) CreateSchedule(ctx context.Context, in ScheduleInput) (ScheduleRecord, error) {
	skills, err := json.Marshal(nonNilStrings(in.Skills))
	if err != nil {
		return ScheduleRecord{}, fmt.Errorf("marshal skills: %w", err)
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO devfleet_schedules
			(id, name, cron_expr, prompt, git_url, git_ref, agent_image, agent, provider_id, model_id, variant_id, skills, timeout_sec, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, TRUE, ?, ?)
	`, in.ID, in.Name, in.CronExpr, in.Prompt, in.GitURL, in.GitRef, in.AgentImage, in.Agent, in.ProviderID, in.ModelID, in.VariantID, string(skills), in.TimeoutSec, now, now)
	if err != nil {
		return ScheduleRecord{}, fmt.Errorf("insert schedule: %w", err)
	}
	return s.GetSchedule(ctx, in.ID)
}

// GetScheduleByName returns one schedule by name.
func (s *Store) GetScheduleByName(ctx context.Context, name string) (ScheduleRecord, error) {
	rows, err := s.querySchedules(ctx, `WHERE name = ?`, name)
	if err != nil {
		return ScheduleRecord{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return ScheduleRecord{}, sql.ErrNoRows
	}
	return scanSchedule(rows)
}

// UpdateSchedule updates all mutable fields on an existing schedule by name.
func (s *Store) UpdateSchedule(ctx context.Context, name string, in ScheduleInput, enabled bool) (ScheduleRecord, error) {
	skills, err := json.Marshal(nonNilStrings(in.Skills))
	if err != nil {
		return ScheduleRecord{}, fmt.Errorf("marshal skills: %w", err)
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
		UPDATE devfleet_schedules
		SET name = ?, cron_expr = ?, prompt = ?,
		    git_url = ?, git_ref = ?, agent_image = ?,
		    agent = ?, provider_id = ?, model_id = ?, variant_id = ?,
		    skills = ?, timeout_sec = ?, enabled = ?,
		    updated_at = ?
		WHERE name = ?
	`, in.Name, in.CronExpr, in.Prompt, in.GitURL, in.GitRef, in.AgentImage,
		in.Agent, in.ProviderID, in.ModelID, in.VariantID,
		string(skills), in.TimeoutSec, enabled, now, name)
	if err != nil {
		return ScheduleRecord{}, fmt.Errorf("update schedule: %w", err)
	}
	return s.GetScheduleByName(ctx, in.Name)
}

// GetSchedule returns one schedule by ID.
func (s *Store) GetSchedule(ctx context.Context, id string) (ScheduleRecord, error) {
	rows, err := s.querySchedules(ctx, `WHERE id = ?`, id)
	if err != nil {
		return ScheduleRecord{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return ScheduleRecord{}, sql.ErrNoRows
	}
	return scanSchedule(rows)
}

// ListSchedules returns schedules, optionally only enabled ones.
func (s *Store) ListSchedules(ctx context.Context, enabledOnly bool) ([]ScheduleRecord, error) {
	filter := `ORDER BY created_at DESC`
	if enabledOnly {
		filter = `WHERE enabled = TRUE ORDER BY created_at DESC`
	}
	rows, err := s.querySchedules(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScheduleRecord
	for rows.Next() {
		record, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// SetScheduleNextRun records the next cron fire time for display.
func (s *Store) SetScheduleNextRun(ctx context.Context, id string, next time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE devfleet_schedules SET next_run_at = ?, updated_at = ? WHERE id = ?`, next.UTC(), time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("update next run: %w", err)
	}
	return nil
}

// DeleteSchedule removes a schedule by name.
func (s *Store) DeleteSchedule(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM devfleet_schedules WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("delete schedule: %w", err)
	}
	return nil
}

// InsertScheduleRun records that a schedule submitted a task.
func (s *Store) InsertScheduleRun(ctx context.Context, id, scheduleID, taskID, status string, scheduledFor time.Time) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO devfleet_schedule_runs (id, schedule_id, task_id, status, scheduled_for, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, scheduleID, taskID, status, scheduledFor.UTC(), now)
	if err != nil {
		return fmt.Errorf("insert schedule run: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `UPDATE devfleet_schedules SET last_run_at = ?, updated_at = ? WHERE id = ?`, now, now, scheduleID)
	if err != nil {
		return fmt.Errorf("update schedule last run: %w", err)
	}
	return nil
}

func (s *Store) queryTasks(ctx context.Context, suffix string, args ...any) (*sql.Rows, error) {
	query := `
		SELECT id, status, prompt, git_url, git_ref, agent_image,
			agent, provider_id, model_id, variant_id, opencode_session_id, runner_image_digest,
			commit_author_name, commit_author_email,
			skills, env, timeout_sec,
			summary, error, created_at, updated_at, started_at, ended_at
		FROM devfleet_tasks ` + suffix
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}
	return rows, nil
}

func scanTask(rows *sql.Rows) (TaskRecord, error) {
	var record TaskRecord
	var gitURL, gitRef, agentImage, agent, providerID, modelID, variantID, opencodeSessionID, runnerImageDigest sql.NullString
	var commitAuthorName, commitAuthorEmail, summary, errMsg sql.NullString
	var skillsJSON, envJSON []byte
	var startedAt, endedAt sql.NullTime
	err := rows.Scan(
		&record.ID, &record.Status, &record.Prompt, &gitURL, &gitRef, &agentImage,
		&agent, &providerID, &modelID, &variantID, &opencodeSessionID, &runnerImageDigest,
		&commitAuthorName, &commitAuthorEmail,
		&skillsJSON, &envJSON,
		&record.TimeoutSec, &summary, &errMsg, &record.CreatedAt, &record.UpdatedAt, &startedAt, &endedAt,
	)
	if err != nil {
		return TaskRecord{}, fmt.Errorf("scan task: %w", err)
	}
	record.GitURL = gitURL.String
	record.GitRef = gitRef.String
	record.AgentImage = agentImage.String
	record.Agent = agent.String
	record.ProviderID = providerID.String
	record.ModelID = modelID.String
	record.VariantID = variantID.String
	record.OpenCodeSessionID = opencodeSessionID.String
	record.RunnerImageDigest = runnerImageDigest.String
	record.CommitAuthorName = commitAuthorName.String
	record.CommitAuthorEmail = commitAuthorEmail.String
	record.Summary = summary.String
	record.Error = errMsg.String
	record.StartedAt = nullTimePtr(startedAt)
	record.EndedAt = nullTimePtr(endedAt)
	if err := json.Unmarshal(skillsJSON, &record.Skills); err != nil {
		return TaskRecord{}, fmt.Errorf("unmarshal skills: %w", err)
	}
	if err := json.Unmarshal(envJSON, &record.Env); err != nil {
		return TaskRecord{}, fmt.Errorf("unmarshal env: %w", err)
	}
	return record, nil
}

func (s *Store) querySchedules(ctx context.Context, suffix string, args ...any) (*sql.Rows, error) {
	query := `
		SELECT id, name, cron_expr, prompt, git_url, git_ref, agent_image,
			agent, provider_id, model_id, variant_id, skills, timeout_sec,
			enabled, created_at, updated_at, last_run_at, next_run_at
		FROM devfleet_schedules ` + suffix
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query schedules: %w", err)
	}
	return rows, nil
}

func scanSchedule(rows *sql.Rows) (ScheduleRecord, error) {
	var record ScheduleRecord
	var gitURL, gitRef, agentImage, agent, providerID, modelID, variantID sql.NullString
	var skillsJSON []byte
	var lastRunAt, nextRunAt sql.NullTime
	err := rows.Scan(
		&record.ID, &record.Name, &record.CronExpr, &record.Prompt, &gitURL, &gitRef, &agentImage,
		&agent, &providerID, &modelID, &variantID, &skillsJSON,
		&record.TimeoutSec, &record.Enabled, &record.CreatedAt, &record.UpdatedAt, &lastRunAt, &nextRunAt,
	)
	if err != nil {
		return ScheduleRecord{}, fmt.Errorf("scan schedule: %w", err)
	}
	record.GitURL = gitURL.String
	record.GitRef = gitRef.String
	record.AgentImage = agentImage.String
	record.Agent = agent.String
	record.ProviderID = providerID.String
	record.ModelID = modelID.String
	record.VariantID = variantID.String
	record.LastRunAt = nullTimePtr(lastRunAt)
	record.NextRunAt = nullTimePtr(nextRunAt)
	if err := json.Unmarshal(skillsJSON, &record.Skills); err != nil {
		return ScheduleRecord{}, fmt.Errorf("unmarshal skills: %w", err)
	}
	return record, nil
}

// GetTaskEvents returns events for a task, newest first.
func (s *Store) GetTaskEvents(ctx context.Context, taskID string, limit int) ([]EventRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, subject, status, payload, created_at
		FROM devfleet_task_events
		WHERE task_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var out []EventRecord
	for rows.Next() {
		var ev EventRecord
		if err := rows.Scan(&ev.ID, &ev.TaskID, &ev.Subject, &ev.Status, &ev.Payload, &ev.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// GetLatestTaskEvent returns the most recent event for a task.
func (s *Store) GetLatestTaskEvent(ctx context.Context, taskID string) (EventRecord, error) {
	var ev EventRecord
	err := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, subject, status, payload, created_at
		FROM devfleet_task_events
		WHERE task_id = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, taskID).Scan(&ev.ID, &ev.TaskID, &ev.Subject, &ev.Status, &ev.Payload, &ev.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return EventRecord{}, sql.ErrNoRows
		}
		return EventRecord{}, fmt.Errorf("query latest event: %w", err)
	}
	return ev, nil
}

func normalizeDSN(dsn string) string {
	if parsed, err := url.Parse(dsn); err == nil && parsed.Scheme == "mysql" && parsed.Host != "" {
		user := parsed.User.Username()
		password, hasPassword := parsed.User.Password()
		credentials := user
		if hasPassword {
			credentials += ":" + password
		}
		database := strings.TrimPrefix(parsed.Path, "/")
		params := parsed.Query()
		if params.Get("parseTime") == "" {
			params.Set("parseTime", "true")
		}
		if params.Get("tls") == "" && strings.HasSuffix(parsed.Hostname(), ".tidbcloud.com") {
			params.Set("tls", "tidb")
		}
		query := params.Encode()
		if query != "" {
			query = "?" + query
		}
		return fmt.Sprintf("%s@tcp(%s)/%s%s", credentials, parsed.Host, database, query)
	}
	if strings.Contains(dsn, "parseTime=") {
		return dsn
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + "parseTime=true"
}

func registerTiDBTLS(dsn string) error {
	if !strings.Contains(dsn, "tls=tidb") {
		return nil
	}
	host, err := hostFromDriverDSN(dsn)
	if err != nil {
		return err
	}
	tidbTLSMu.Lock()
	defer tidbTLSMu.Unlock()
	if err := mysql.RegisterTLSConfig("tidb", &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: host,
	}); err != nil && !strings.Contains(err.Error(), "already registered") {
		return fmt.Errorf("register tidb tls config: %w", err)
	}
	return nil
}

func hostFromDriverDSN(dsn string) (string, error) {
	start := strings.Index(dsn, "@tcp(")
	if start == -1 {
		return "", errTiDBRequiresTCPHost
	}
	start += len("@tcp(")
	end := strings.Index(dsn[start:], ")")
	if end == -1 {
		return "", errTiDBRequiresTCPHost
	}
	hostPort := dsn[start : start+end]
	host := hostPort
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}
	if host == "" {
		return "", errTiDBRequiresTCPHost
	}
	return host, nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func nullTimePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	out := t.Time.UTC()
	return &out
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nonNilMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}

// NonZero returns a if non-empty, otherwise b.
func NonZero(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// NonZeroInt returns a if non-zero, otherwise b.
func NonZeroInt(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}

// NonNilSlice returns a if non-nil, otherwise b.
func NonNilSlice(a, b []string) []string {
	if a != nil {
		return a
	}
	return b
}
