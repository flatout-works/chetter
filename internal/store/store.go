// Package store persists chetter state in a TiDB/MySQL-compatible database.
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
	connMaxIdleTime       = 5 * time.Minute
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
	TeamID            string            `json:"team_id,omitempty"`
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

const (
	TriggerTypeCron     = "cron"
	TriggerTypePRReview = "pr_review"
	TriggerTypeIssue    = "issue"
)

// PRReviewTriggerConfig is stored in the trigger_config JSON column for
// PR review triggers. It carries the repository to watch.
type PRReviewTriggerConfig struct {
	Repo string `json:"repo"`
}

// ScheduleRecord is a persisted task trigger (cron, pr_review, etc.).
type ScheduleRecord struct {
	ID            string     `json:"id"`
	TeamID        string     `json:"team_id,omitempty"`
	Name          string     `json:"name"`
	TriggerType   string     `json:"trigger_type"`
	TriggerConfig string     `json:"trigger_config"`
	CronExpr      string     `json:"cron_expr"`
	Prompt        string     `json:"prompt"`
	GitURL        string     `json:"git_url,omitempty"`
	GitRef        string     `json:"git_ref,omitempty"`
	AgentImage    string     `json:"agent_image,omitempty"`
	Agent         string     `json:"agent,omitempty"`
	ProviderID    string     `json:"provider_id,omitempty"`
	ModelID       string     `json:"model_id,omitempty"`
	VariantID     string     `json:"variant_id,omitempty"`
	Skills        []string   `json:"skills"`
	TimeoutSec    int        `json:"timeout_sec"`
	Enabled       bool       `json:"enabled"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	LastRunAt     *time.Time `json:"last_run_at,omitempty"`
	NextRunAt     *time.Time `json:"next_run_at,omitempty"`
}

// ScheduleInput contains fields needed to create a trigger.
type ScheduleInput struct {
	ID            string
	TeamID        string
	Name          string
	TriggerType   string
	TriggerConfig string
	CronExpr      string
	Prompt        string
	GitURL        string
	GitRef        string
	AgentImage    string
	Agent         string
	ProviderID    string
	ModelID       string
	VariantID     string
	Skills        []string
	TimeoutSec    int
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
	db.SetConnMaxIdleTime(connMaxIdleTime)
	return &Store{db: db}, nil
}

// Close closes the database pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB exposes the underlying database pool for generated sqlc repositories.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Ping verifies database connectivity.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// ApplySchema creates the chetter tables if they do not exist.
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
	if err := s.ensureTriggerColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureScheduleRunTeamIDColumn(ctx); err != nil {
		return err
	}
	if err := s.ensureSessionExportColumn(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureTaskMetadataColumns(ctx context.Context) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"agent", "ALTER TABLE chetter_tasks ADD COLUMN agent VARCHAR(128) NULL AFTER agent_image"},
		{"provider_id", "ALTER TABLE chetter_tasks ADD COLUMN provider_id VARCHAR(128) NULL AFTER agent_image"},
		{"model_id", "ALTER TABLE chetter_tasks ADD COLUMN model_id VARCHAR(255) NULL AFTER provider_id"},
		{"variant_id", "ALTER TABLE chetter_tasks ADD COLUMN variant_id VARCHAR(128) NULL AFTER model_id"},
		{"opencode_session_id", "ALTER TABLE chetter_tasks ADD COLUMN opencode_session_id VARCHAR(128) NULL AFTER variant_id"},
		{"runner_image_digest", "ALTER TABLE chetter_tasks ADD COLUMN runner_image_digest VARCHAR(255) NULL AFTER opencode_session_id"},
		{"commit_author_name", "ALTER TABLE chetter_tasks ADD COLUMN commit_author_name VARCHAR(128) NULL AFTER runner_image_digest"},
		{"commit_author_email", "ALTER TABLE chetter_tasks ADD COLUMN commit_author_email VARCHAR(255) NULL AFTER commit_author_name"},
		{"runner_id", "ALTER TABLE chetter_tasks ADD COLUMN runner_id VARCHAR(64) NULL AFTER commit_author_email"},
		{"claimed_at", "ALTER TABLE chetter_tasks ADD COLUMN claimed_at DATETIME(6) NULL AFTER runner_id"},
		{"lease_expires_at", "ALTER TABLE chetter_tasks ADD COLUMN lease_expires_at DATETIME(6) NULL AFTER claimed_at"},
		{"attempt", "ALTER TABLE chetter_tasks ADD COLUMN attempt INT NOT NULL DEFAULT 0 AFTER lease_expires_at"},
		{"max_attempts", "ALTER TABLE chetter_tasks ADD COLUMN max_attempts INT NOT NULL DEFAULT 3 AFTER attempt"},
		{"last_event_at", "ALTER TABLE chetter_tasks ADD COLUMN last_event_at DATETIME(6) NULL AFTER updated_at"},
		{"team_id", "ALTER TABLE chetter_tasks ADD COLUMN team_id VARCHAR(64) NULL AFTER id"},
		{"trigger_name", "ALTER TABLE chetter_tasks ADD COLUMN trigger_name VARCHAR(128) NULL AFTER runner_id"},
		{"trigger_type", "ALTER TABLE chetter_tasks ADD COLUMN trigger_type VARCHAR(32) NULL AFTER trigger_name"},
	}
	for _, column := range columns {
		exists, err := s.columnExists(ctx, "chetter_tasks", column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := s.db.ExecContext(ctx, column.ddl); err != nil {
			return fmt.Errorf("add chetter_tasks.%s: %w", column.name, err)
		}
	}
	return nil
}

func (s *Store) ensureScheduleMetadataColumns(ctx context.Context) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"agent", "ALTER TABLE chetter_schedules ADD COLUMN agent VARCHAR(128) NULL AFTER agent_image"},
		{"provider_id", "ALTER TABLE chetter_schedules ADD COLUMN provider_id VARCHAR(128) NULL AFTER agent"},
		{"model_id", "ALTER TABLE chetter_schedules ADD COLUMN model_id VARCHAR(255) NULL AFTER provider_id"},
		{"variant_id", "ALTER TABLE chetter_schedules ADD COLUMN variant_id VARCHAR(128) NULL AFTER model_id"},
		{"team_id", "ALTER TABLE chetter_schedules ADD COLUMN team_id VARCHAR(64) NULL AFTER id"},
	}
	for _, column := range columns {
		exists, err := s.columnExists(ctx, "chetter_schedules", column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := s.db.ExecContext(ctx, column.ddl); err != nil {
			return fmt.Errorf("add chetter_schedules.%s: %w", column.name, err)
		}
	}
	return nil
}

func (s *Store) ensureRunnerMetadataColumns(ctx context.Context) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"image_ref", "ALTER TABLE chetter_runners ADD COLUMN image_ref VARCHAR(512) NULL AFTER status"},
		{"image_digest", "ALTER TABLE chetter_runners ADD COLUMN image_digest VARCHAR(255) NULL AFTER image_ref"},
		{"version", "ALTER TABLE chetter_runners ADD COLUMN version VARCHAR(128) NULL AFTER image_digest"},
		{"max_concurrent", "ALTER TABLE chetter_runners ADD COLUMN max_concurrent INT NOT NULL DEFAULT 0 AFTER version"},
		{"running_tasks", "ALTER TABLE chetter_runners ADD COLUMN running_tasks INT NOT NULL DEFAULT 0 AFTER max_concurrent"},
		{"available_slots", "ALTER TABLE chetter_runners ADD COLUMN available_slots INT NOT NULL DEFAULT 0 AFTER running_tasks"},
		{"total_started", "ALTER TABLE chetter_runners ADD COLUMN total_started BIGINT NOT NULL DEFAULT 0 AFTER available_slots"},
		{"total_completed", "ALTER TABLE chetter_runners ADD COLUMN total_completed BIGINT NOT NULL DEFAULT 0 AFTER total_started"},
		{"total_errors", "ALTER TABLE chetter_runners ADD COLUMN total_errors BIGINT NOT NULL DEFAULT 0 AFTER total_completed"},
		{"started_at", "ALTER TABLE chetter_runners ADD COLUMN started_at DATETIME(6) NULL AFTER total_errors"},
		{"first_seen_at", "ALTER TABLE chetter_runners ADD COLUMN first_seen_at DATETIME(6) NULL AFTER started_at"},
		{"updated_at", "ALTER TABLE chetter_runners ADD COLUMN updated_at DATETIME(6) NULL AFTER last_seen_at"},
	}
	for _, column := range columns {
		exists, err := s.columnExists(ctx, "chetter_runners", column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := s.db.ExecContext(ctx, column.ddl); err != nil {
			return fmt.Errorf("add chetter_runners.%s: %w", column.name, err)
		}
	}
	return nil
}

func (s *Store) ensureTriggerColumns(ctx context.Context) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"trigger_type", "ALTER TABLE chetter_schedules ADD COLUMN trigger_type VARCHAR(32) NOT NULL DEFAULT 'cron' AFTER name"},
		{"trigger_config", "ALTER TABLE chetter_schedules ADD COLUMN trigger_config JSON NULL AFTER trigger_type"},
	}
	for _, column := range columns {
		exists, err := s.columnExists(ctx, "chetter_schedules", column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := s.db.ExecContext(ctx, column.ddl); err != nil {
			return fmt.Errorf("add chetter_schedules.%s: %w", column.name, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE chetter_schedules SET trigger_config = '{}' WHERE trigger_config IS NULL"); err != nil {
		return fmt.Errorf("backfill trigger_config: %w", err)
	}
	return nil
}

func (s *Store) ensureScheduleRunTeamIDColumn(ctx context.Context) error {
	exists, err := s.columnExists(ctx, "chetter_schedule_runs", "team_id")
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = s.db.ExecContext(ctx, "ALTER TABLE chetter_schedule_runs ADD COLUMN team_id VARCHAR(64) NULL AFTER schedule_id")
	if err != nil {
		return fmt.Errorf("add chetter_schedule_runs.team_id: %w", err)
	}
	return nil
}

func (s *Store) ensureSessionExportColumn(ctx context.Context) error {
	exists, err := s.columnExists(ctx, "chetter_tasks", "session_export")
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = s.db.ExecContext(ctx, "ALTER TABLE chetter_tasks ADD COLUMN session_export MEDIUMTEXT NULL AFTER error")
	if err != nil {
		return fmt.Errorf("add chetter_tasks.session_export: %w", err)
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

// ReapStaleTasks finds running tasks that have not received a heartbeat
// within their timeout + grace period and marks them as error.
func (s *Store) ReapStaleTasks(ctx context.Context, grace time.Duration) (int, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE chetter_tasks
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
		SELECT status, COUNT(*) FROM chetter_tasks GROUP BY status
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
		FROM chetter_tasks
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
		SELECT id, status, image_ref, image_digest, version,
		       max_concurrent, running_tasks, available_slots, total_started, total_completed, total_errors,
		       first_seen_at, last_seen_at, started_at, metadata,
		       TIMESTAMPDIFF(SECOND, last_seen_at, NOW()) AS last_seen_sec
		FROM chetter_runners
		ORDER BY last_seen_at DESC
	`)
	if err != nil {
		return health, fmt.Errorf("query runners: %w", err)
	}
	defer runnerRows.Close()
	for runnerRows.Next() {
		var info RunnerInfo
		var imageRef, imageDigest, version sql.NullString
		var firstSeen, startedAt sql.NullTime
		var metadata []byte
		if err := runnerRows.Scan(
			&info.ID, &info.Status, &imageRef, &imageDigest, &version,
			&info.MaxConcurrent, &info.RunningTasks, &info.AvailableSlots, &info.TotalStarted, &info.TotalCompleted, &info.TotalErrors,
			&firstSeen, &info.LastSeenAt, &startedAt, &metadata, &info.LastSeenSec,
		); err != nil {
			return health, fmt.Errorf("scan runner: %w", err)
		}
		info.ImageRef = imageRef.String
		info.ImageDigest = imageDigest.String
		info.Version = version.String
		info.FirstSeenAt = NullTimePtr(firstSeen)
		info.StartedAt = NullTimePtr(startedAt)
		info.IsStale = info.LastSeenSec > maxRunnerPresenceSec
		info.CurrentTaskIDs = currentTaskIDsFromMetadata(metadata)
		if info.IsStale {
			continue
		}
		health.Runners = append(health.Runners, info)
		health.FleetActive = true

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

func NullTimePtr(t sql.NullTime) *time.Time {
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
