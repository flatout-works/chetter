// Package store persists chetter state in a TiDB or MySQL-compatible database.
package store

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
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

// Dialect identifies the database backend.
type Dialect int

const (
	// DialectUnknown triggers auto-detection on Open.
	DialectUnknown Dialect = iota
	// DialectTiDB is TiDB (including TiDB Cloud).
	DialectTiDB
	// DialectMySQL is MySQL or a wire-compatible engine such as AWS Aurora MySQL.
	DialectMySQL
)

func (d Dialect) String() string {
	switch d {
	case DialectTiDB:
		return "tidb"
	case DialectMySQL:
		return "mysql"
	default:
		return "unknown"
	}
}

// ParseDialect converts a config string ("tidb", "mysql", or "") into a Dialect.
func ParseDialect(s string) Dialect {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "tidb":
		return DialectTiDB
	case "mysql":
		return DialectMySQL
	default:
		return DialectUnknown
	}
}

type Store struct {
	db      *sql.DB
	dialect Dialect
}

// TaskRecord is the persisted task state exposed by MCP tools.
type TaskRecord struct {
	ID                    string            `json:"id"`
	TeamID                string            `json:"team_id,omitempty"`
	Status                string            `json:"status"`
	Prompt                string            `json:"prompt"`
	GitURL                string            `json:"git_url,omitempty"`
	GitRef                string            `json:"git_ref,omitempty"`
	AgentImage            string            `json:"agent_image,omitempty"`
	Agent                 string            `json:"agent,omitempty"`
	ProviderID            string            `json:"provider_id,omitempty"`
	ModelID               string            `json:"model_id,omitempty"`
	VariantID             string            `json:"variant_id,omitempty"`
	OpenCodeSessionID     string            `json:"opencode_session_id,omitempty"`
	RunnerImageDigest     string            `json:"runner_image_digest,omitempty"`
	CommitAuthorName      string            `json:"commit_author_name,omitempty"`
	CommitAuthorEmail     string            `json:"commit_author_email,omitempty"`
	GitIdentityID         string            `json:"git_identity_id,omitempty"`
	TriggerName           string            `json:"trigger_name,omitempty"`
	TriggerType           string            `json:"trigger_type,omitempty"`
	SubmissionSource      string            `json:"submission_source,omitempty"`
	Skills                []string          `json:"skills"`
	Env                   map[string]string `json:"env"`
	TimeoutSec            int               `json:"timeout_sec"`
	Summary               string            `json:"summary,omitempty"`
	Error                 string            `json:"error,omitempty"`
	ErrorCategory         string            `json:"error_category,omitempty"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
	StartedAt             *time.Time        `json:"started_at,omitempty"`
	EndedAt               *time.Time        `json:"ended_at,omitempty"`
	TotalInputTokens      int64             `json:"total_input_tokens"`
	TotalOutputTokens     int64             `json:"total_output_tokens"`
	TotalCacheReadTokens  int64             `json:"total_cache_read_tokens"`
	TotalCacheWriteTokens int64             `json:"total_cache_write_tokens"`
	TotalReasoningTokens  int64             `json:"total_reasoning_tokens"`
	CostCents             int64             `json:"cost_cents"`
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
	ErrorCategory     string    `json:"error_category,omitempty"`
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

// TriggerRecord is a persisted task trigger (cron, pr_review, etc.).
type TriggerRecord struct {
	ID            string   `json:"id"`
	TeamID        string   `json:"team_id,omitempty"`
	Name          string   `json:"name"`
	TriggerType   string   `json:"trigger_type"`
	TriggerConfig string   `json:"trigger_config"`
	CronExpr      string   `json:"cron_expr"`
	Prompt        string   `json:"prompt"`
	GitURL        string   `json:"git_url,omitempty"`
	GitRef        string   `json:"git_ref,omitempty"`
	AgentImage    string   `json:"agent_image,omitempty"`
	Agent         string   `json:"agent,omitempty"`
	ProviderID    string   `json:"provider_id,omitempty"`
	ModelID       string   `json:"model_id,omitempty"`
	VariantID     string   `json:"variant_id,omitempty"`
	Harness       string   `json:"harness,omitempty"`
	Skills        []string `json:"skills"`
	TimeoutSec    int      `json:"timeout_sec"`
	Enabled       bool     `json:"enabled"`
	SourceID      string   `json:"source_id,omitempty"`
	// SourceRepoURL, SourceBranch, and SourcePath are transient fields
	// populated by the service layer from the definition_sources and
	// definitions tables. They are not stored in chetter_triggers.
	SourceRepoURL string     `json:"source_repo_url,omitempty"`
	SourceBranch  string     `json:"source_branch,omitempty"`
	SourcePath    string     `json:"source_path,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	LastRunAt     *time.Time `json:"last_run_at,omitempty"`
	NextRunAt     *time.Time `json:"next_run_at,omitempty"`
}

// TriggerInput contains fields needed to create a trigger.
type TriggerInput struct {
	TeamID        string
	TeamName      string
	ID            string
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
	Harness       string
	Skills        []string
	TimeoutSec    int
	SourceID      string
}

// Open creates a database pool and applies conservative connection limits.
// If dialect is DialectUnknown, the backend is auto-detected via SELECT VERSION().
func Open(dsn string, dialect Dialect) (*Store, error) {
	normalized := normalizeDSN(dsn)
	if err := registerTiDBTLS(normalized); err != nil {
		return nil, err
	}
	ensureDatabaseExists(normalized)
	db, err := sql.Open("mysql", normalized)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxLifetime(connMaxLifetime)
	db.SetConnMaxIdleTime(connMaxIdleTime)
	st := &Store{db: db, dialect: dialect}
	if st.dialect == DialectUnknown {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		st.detectDialect(ctx)
	}
	return st, nil
}

// ensureDatabaseExists best-effort creates the database named in the DSN if it
// does not already exist. The server applies its schema (tables) on boot via
// ApplySchema but does not own the database itself, so a fresh TiDB — whether a
// local container or a new TiDB Cloud Serverless cluster — otherwise crash-loops
// on "Unknown database". This is best-effort on purpose: a failure here is not
// fatal, because the operator may have pre-created the database, or the account
// may lack CREATE privileges, in which case the main connection below surfaces a
// clear error of its own.
func ensureDatabaseExists(normalizedDSN string) {
	cfg, err := mysql.ParseDSN(normalizedDSN)
	if err != nil || cfg.DBName == "" || !validDatabaseName(cfg.DBName) {
		return
	}
	dbName := cfg.DBName
	cfg.DBName = "" // connect without selecting a schema so we can create it
	adminDB, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return
	}
	defer adminDB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// dbName is validated above and quoted with backticks; it cannot be a bind
	// parameter because it is a schema identifier.
	if _, err := adminDB.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS `"+dbName+"`"); err != nil {
		slog.Warn("could not ensure database exists; assuming it is pre-created",
			"database", dbName, "error", err)
	}
}

// validDatabaseName reports whether name is a safe schema identifier to splice
// into a CREATE DATABASE statement (letters, digits, underscore, dash).
func validDatabaseName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// Close closes the database pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB exposes the underlying database pool for generated sqlc repositories.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Dialect returns the detected or configured database dialect.
func (s *Store) Dialect() Dialect { return s.dialect }

// IsTiDB reports whether the backend is TiDB.
func (s *Store) IsTiDB() bool { return s.dialect == DialectTiDB }

// IsMySQL reports whether the backend is MySQL or a MySQL-compatible engine.
func (s *Store) IsMySQL() bool { return s.dialect == DialectMySQL }

// detectDialect probes the database version string to determine the backend.
// Defaults to DialectTiDB if the probe fails (preserving existing behaviour).
func (s *Store) detectDialect(ctx context.Context) {
	var version string
	if err := s.db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		slog.Warn("could not detect database dialect; defaulting to TiDB", "err", err)
		s.dialect = DialectTiDB
		return
	}
	if strings.Contains(strings.ToUpper(version), "TIDB") {
		s.dialect = DialectTiDB
	} else {
		s.dialect = DialectMySQL
	}
	slog.Info("database dialect", "dialect", s.dialect, "version", version)
}

// fulltextParserClause returns the FULLTEXT index parser clause for the
// current dialect: " WITH PARSER MULTILINGUAL" for TiDB, " WITH PARSER ngram"
// for MySQL, or "" (default parser) for unknown dialects.
func (s *Store) fulltextParserClause() string {
	switch s.dialect {
	case DialectMySQL:
		return " WITH PARSER ngram"
	case DialectTiDB:
		return " WITH PARSER MULTILINGUAL"
	default:
		return ""
	}
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
	if err := s.ensureTriggerMetadataColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureRunnerMetadataColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureArtifactDedupIndex(ctx); err != nil {
		return err
	}
	if err := s.ensureTriggerRunDedupIndex(ctx); err != nil {
		return err
	}
	if err := s.ensureSearchTextColumns(ctx); err != nil {
		return err
	}
	if err := s.backfillSearchText(ctx); err != nil {
		return err
	}
	if err := s.ensureAuditFulltextIndex(ctx); err != nil {
		return err
	}
	if err := s.ensureAuditTokenIdentityColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureTaskFulltextIndex(ctx); err != nil {
		return err
	}
	if err := s.ensureSessionFulltextIndex(ctx); err != nil {
		return err
	}
	if err := s.ensureArtifactFulltextIndex(ctx); err != nil {
		return err
	}
	if err := s.ensureTriggerColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureTriggerRunTeamIDColumn(ctx); err != nil {
		return err
	}
	if err := s.ensureSessionExportColumn(ctx); err != nil {
		return err
	}
	if err := s.ensureTaskArtifactSessionColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureTaskEventTypeColumn(ctx); err != nil {
		return err
	}
	if err := s.ensureTokenColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureTeamAuthSchema(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureTeamAuthSchema(ctx context.Context) error {
	columns := []struct {
		table string
		name  string
		ddl   string
	}{
		{"teams", "okta_group_id", "ALTER TABLE teams ADD COLUMN okta_group_id VARCHAR(255) NULL AFTER name"},
		{"teams", "okta_group_name", "ALTER TABLE teams ADD COLUMN okta_group_name VARCHAR(255) NULL AFTER okta_group_id"},
	}
	for _, column := range columns {
		exists, err := s.columnExists(ctx, column.table, column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := s.db.ExecContext(ctx, column.ddl); err != nil {
			return fmt.Errorf("add %s.%s: %w", column.table, column.name, err)
		}
	}
	indexExists, err := s.indexExists(ctx, "teams", "uq_teams_okta_group_id")
	if err != nil {
		return err
	}
	if !indexExists {
		if _, err := s.db.ExecContext(ctx, "CREATE UNIQUE INDEX uq_teams_okta_group_id ON teams (okta_group_id)"); err != nil {
			return fmt.Errorf("create teams.okta_group_id index: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS user_team_memberships (
			user_id VARCHAR(64) NOT NULL,
			team_id VARCHAR(64) NOT NULL,
			source VARCHAR(32) NOT NULL DEFAULT 'manual',
			created_at DATETIME(6) NOT NULL,
			updated_at DATETIME(6) NOT NULL,
			PRIMARY KEY (user_id, team_id),
			KEY idx_user_team_memberships_team (team_id)
		)`); err != nil {
		return fmt.Errorf("create user_team_memberships: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS api_token_teams (
			token_id VARCHAR(64) NOT NULL,
			team_id VARCHAR(64) NOT NULL,
			created_at DATETIME(6) NOT NULL,
			PRIMARY KEY (token_id, team_id),
			KEY idx_api_token_teams_team (team_id)
		)`); err != nil {
		return fmt.Errorf("create api_token_teams: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT IGNORE INTO user_team_memberships (user_id, team_id, source, created_at, updated_at)
		SELECT id, team_id, 'manual', created_at, updated_at
		FROM users
		WHERE team_id IS NOT NULL AND team_id <> ''
	`); err != nil {
		return fmt.Errorf("backfill user team memberships: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT IGNORE INTO api_token_teams (token_id, team_id, created_at)
		SELECT t.id, u.team_id, t.created_at
		FROM api_tokens t
		JOIN users u ON u.id = t.user_id
		WHERE u.team_id IS NOT NULL AND u.team_id <> ''
	`); err != nil {
		return fmt.Errorf("backfill api token teams: %w", err)
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
		{"required_runner_id", "ALTER TABLE chetter_tasks ADD COLUMN required_runner_id VARCHAR(64) NULL AFTER runner_id"},
		{"checkpoint_after_success", "ALTER TABLE chetter_tasks ADD COLUMN checkpoint_after_success BOOL NOT NULL DEFAULT false AFTER required_runner_id"},
		{"claimed_at", "ALTER TABLE chetter_tasks ADD COLUMN claimed_at DATETIME(6) NULL AFTER runner_id"},
		{"lease_expires_at", "ALTER TABLE chetter_tasks ADD COLUMN lease_expires_at DATETIME(6) NULL AFTER claimed_at"},
		{"attempt", "ALTER TABLE chetter_tasks ADD COLUMN attempt INT NOT NULL DEFAULT 0 AFTER lease_expires_at"},
		{"max_attempts", "ALTER TABLE chetter_tasks ADD COLUMN max_attempts INT NOT NULL DEFAULT 3 AFTER attempt"},
		{"last_event_at", "ALTER TABLE chetter_tasks ADD COLUMN last_event_at DATETIME(6) NULL AFTER updated_at"},
		{"team_id", "ALTER TABLE chetter_tasks ADD COLUMN team_id VARCHAR(64) NULL AFTER id"},
		{"trigger_name", "ALTER TABLE chetter_tasks ADD COLUMN trigger_name VARCHAR(128) NULL AFTER runner_id"},
		{"trigger_type", "ALTER TABLE chetter_tasks ADD COLUMN trigger_type VARCHAR(32) NULL AFTER trigger_name"},
		{"submission_source", "ALTER TABLE chetter_tasks ADD COLUMN submission_source VARCHAR(32) NOT NULL DEFAULT 'manual' AFTER trigger_type"},
		{"error_category", "ALTER TABLE chetter_tasks ADD COLUMN error_category VARCHAR(32) NULL AFTER error"},
		{"total_input_tokens", "ALTER TABLE chetter_tasks ADD COLUMN total_input_tokens BIGINT NOT NULL DEFAULT 0 AFTER cost_cents"},
		{"total_output_tokens", "ALTER TABLE chetter_tasks ADD COLUMN total_output_tokens BIGINT NOT NULL DEFAULT 0 AFTER total_input_tokens"},
		{"total_cache_read_tokens", "ALTER TABLE chetter_tasks ADD COLUMN total_cache_read_tokens BIGINT NOT NULL DEFAULT 0 AFTER total_output_tokens"},
		{"total_cache_write_tokens", "ALTER TABLE chetter_tasks ADD COLUMN total_cache_write_tokens BIGINT NOT NULL DEFAULT 0 AFTER total_cache_read_tokens"},
		{"total_reasoning_tokens", "ALTER TABLE chetter_tasks ADD COLUMN total_reasoning_tokens BIGINT NOT NULL DEFAULT 0 AFTER total_cache_write_tokens"},
		{"cost_cents", "ALTER TABLE chetter_tasks ADD COLUMN cost_cents BIGINT NOT NULL DEFAULT 0 AFTER total_reasoning_tokens"},
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

func (s *Store) ensureTriggerMetadataColumns(ctx context.Context) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"agent", "ALTER TABLE chetter_triggers ADD COLUMN agent VARCHAR(128) NULL AFTER agent_image"},
		{"provider_id", "ALTER TABLE chetter_triggers ADD COLUMN provider_id VARCHAR(128) NULL AFTER agent"},
		{"model_id", "ALTER TABLE chetter_triggers ADD COLUMN model_id VARCHAR(255) NULL AFTER provider_id"},
		{"variant_id", "ALTER TABLE chetter_triggers ADD COLUMN variant_id VARCHAR(128) NULL AFTER model_id"},
		{"harness", "ALTER TABLE chetter_triggers ADD COLUMN harness VARCHAR(64) NULL AFTER variant_id"},
		{"team_id", "ALTER TABLE chetter_triggers ADD COLUMN team_id VARCHAR(64) NULL AFTER id"},
		{"source_id", "ALTER TABLE chetter_triggers ADD COLUMN source_id VARCHAR(64) NULL AFTER enabled"},
	}
	for _, column := range columns {
		exists, err := s.columnExists(ctx, "chetter_triggers", column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := s.db.ExecContext(ctx, column.ddl); err != nil {
			return fmt.Errorf("add chetter_triggers.%s: %w", column.name, err)
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

func (s *Store) ensureArtifactDedupIndex(ctx context.Context) error {
	exists, err := s.indexExists(ctx, "chetter_task_artifacts", "idx_task_artifacts_dedup")
	if err != nil {
		return err
	}
	if !exists {
		// Clean up existing duplicates before creating the unique index.
		// Keep the first row for each (task_id, artifact_type, repo, number) tuple.
		if _, err := s.db.ExecContext(ctx, `
			DELETE t1 FROM chetter_task_artifacts t1
			INNER JOIN chetter_task_artifacts t2
			WHERE t1.id > t2.id
			  AND t1.task_id = t2.task_id
			  AND t1.artifact_type = t2.artifact_type
			  AND t1.repo = t2.repo
			  AND t1.number = t2.number
		`); err != nil {
			return fmt.Errorf("dedup artifacts: %w", err)
		}
		if _, err := s.db.ExecContext(ctx, "CREATE UNIQUE INDEX idx_task_artifacts_dedup ON chetter_task_artifacts (task_id, artifact_type, repo, number)"); err != nil {
			return fmt.Errorf("add artifact dedup index: %w", err)
		}
	}
	return nil
}

func (s *Store) ensureTriggerRunDedupIndex(ctx context.Context) error {
	exists, err := s.indexExists(ctx, "chetter_trigger_runs", "idx_trigger_runs_dedup")
	if err != nil {
		return err
	}
	if !exists {
		if _, err := s.db.ExecContext(ctx, `
			DELETE t1 FROM chetter_trigger_runs t1
			INNER JOIN chetter_trigger_runs t2
			WHERE t1.id > t2.id
			  AND t1.trigger_id = t2.trigger_id
			  AND t1.task_id = t2.task_id
		`); err != nil {
			return fmt.Errorf("dedup trigger runs: %w", err)
		}
		if _, err := s.db.ExecContext(ctx, "CREATE UNIQUE INDEX idx_trigger_runs_dedup ON chetter_trigger_runs (trigger_id, task_id)"); err != nil {
			return fmt.Errorf("add trigger run dedup index: %w", err)
		}
	}
	return nil
}

func (s *Store) ensureAuditFulltextIndex(ctx context.Context) error {
	exists, err := s.indexExists(ctx, "chetter_audit_log", "idx_audit_search")
	if err != nil {
		return err
	}
	if exists {
		col, err := s.indexColumnName(ctx, "chetter_audit_log", "idx_audit_search")
		if err != nil {
			return err
		}
		if col == "search_text" {
			return nil
		}
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE chetter_audit_log DROP INDEX idx_audit_search"); err != nil {
			return fmt.Errorf("drop old audit fulltext index: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, "ALTER TABLE chetter_audit_log ADD FULLTEXT INDEX idx_audit_search (search_text)"+s.fulltextParserClause()); err != nil {
		slog.Warn("failed to add audit fulltext index", "err", err, "dialect", s.dialect)
		return nil
	}
	return nil
}

func (s *Store) ensureAuditTokenIdentityColumns(ctx context.Context) error {
	cols := []struct {
		name string
		ddl  string
	}{
		{"token_id", "ALTER TABLE chetter_audit_log ADD COLUMN token_id VARCHAR(64) NULL AFTER payload"},
		{"token_name", "ALTER TABLE chetter_audit_log ADD COLUMN token_name VARCHAR(128) NULL AFTER token_id"},
	}
	for _, c := range cols {
		exists, err := s.columnExists(ctx, "chetter_audit_log", c.name)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := s.db.ExecContext(ctx, c.ddl); err != nil {
				return fmt.Errorf("add %s: %w", c.name, err)
			}
		}
	}
	indexExists, err := s.indexExists(ctx, "chetter_audit_log", "idx_audit_token")
	if err != nil {
		return err
	}
	if !indexExists {
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE chetter_audit_log ADD KEY idx_audit_token (token_id)"); err != nil {
			slog.Warn("failed to add audit token index", "err", err)
		}
	}
	return nil
}

func (s *Store) ensureTaskFulltextIndex(ctx context.Context) error {
	exists, err := s.indexExists(ctx, "chetter_tasks", "idx_tasks_search")
	if err != nil {
		return err
	}
	if exists {
		col, err := s.indexColumnName(ctx, "chetter_tasks", "idx_tasks_search")
		if err != nil {
			return err
		}
		if col == "search_text" {
			return nil
		}
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE chetter_tasks DROP INDEX idx_tasks_search"); err != nil {
			return fmt.Errorf("drop old tasks fulltext index: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, "ALTER TABLE chetter_tasks ADD FULLTEXT INDEX idx_tasks_search (search_text)"+s.fulltextParserClause()); err != nil {
		slog.Warn("failed to add tasks fulltext index", "err", err, "dialect", s.dialect)
		return nil
	}
	return nil
}

func (s *Store) ensureSessionFulltextIndex(ctx context.Context) error {
	exists, err := s.indexExists(ctx, "chetter_agent_sessions", "idx_sessions_search")
	if err != nil {
		return err
	}
	if exists {
		col, err := s.indexColumnName(ctx, "chetter_agent_sessions", "idx_sessions_search")
		if err != nil {
			return err
		}
		if col == "search_text" {
			return nil
		}
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE chetter_agent_sessions DROP INDEX idx_sessions_search"); err != nil {
			return fmt.Errorf("drop old sessions fulltext index: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, "ALTER TABLE chetter_agent_sessions ADD FULLTEXT INDEX idx_sessions_search (search_text)"+s.fulltextParserClause()); err != nil {
		slog.Warn("failed to add sessions fulltext index", "err", err, "dialect", s.dialect)
		return nil
	}
	return nil
}

func (s *Store) ensureArtifactFulltextIndex(ctx context.Context) error {
	exists, err := s.indexExists(ctx, "chetter_task_artifacts", "idx_artifacts_search")
	if err != nil {
		return err
	}
	if exists {
		col, err := s.indexColumnName(ctx, "chetter_task_artifacts", "idx_artifacts_search")
		if err != nil {
			return err
		}
		if col == "search_text" {
			return nil
		}
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE chetter_task_artifacts DROP INDEX idx_artifacts_search"); err != nil {
			return fmt.Errorf("drop old artifacts fulltext index: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, "ALTER TABLE chetter_task_artifacts ADD FULLTEXT INDEX idx_artifacts_search (search_text)"+s.fulltextParserClause()); err != nil {
		slog.Warn("failed to add artifacts fulltext index", "err", err, "dialect", s.dialect)
		return nil
	}
	return nil
}

func (s *Store) ensureTriggerColumns(ctx context.Context) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"trigger_type", "ALTER TABLE chetter_triggers ADD COLUMN trigger_type VARCHAR(32) NOT NULL DEFAULT 'cron' AFTER name"},
		{"trigger_config", "ALTER TABLE chetter_triggers ADD COLUMN trigger_config JSON NULL AFTER trigger_type"},
	}
	for _, column := range columns {
		exists, err := s.columnExists(ctx, "chetter_triggers", column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := s.db.ExecContext(ctx, column.ddl); err != nil {
			return fmt.Errorf("add chetter_triggers.%s: %w", column.name, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE chetter_triggers SET trigger_config = '{}' WHERE trigger_config IS NULL"); err != nil {
		return fmt.Errorf("backfill trigger_config: %w", err)
	}
	return nil
}

func (s *Store) ensureTriggerRunTeamIDColumn(ctx context.Context) error {
	exists, err := s.columnExists(ctx, "chetter_trigger_runs", "team_id")
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = s.db.ExecContext(ctx, "ALTER TABLE chetter_trigger_runs ADD COLUMN team_id VARCHAR(64) NULL AFTER trigger_id")
	if err != nil {
		return fmt.Errorf("add chetter_trigger_runs.team_id: %w", err)
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

func (s *Store) ensureTaskArtifactSessionColumns(ctx context.Context) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"agent_session_id", "ALTER TABLE chetter_task_artifacts ADD COLUMN agent_session_id VARCHAR(64) NULL AFTER task_id"},
		{"session_run_id", "ALTER TABLE chetter_task_artifacts ADD COLUMN session_run_id VARCHAR(64) NULL AFTER agent_session_id"},
	}
	for _, column := range columns {
		exists, err := s.columnExists(ctx, "chetter_task_artifacts", column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := s.db.ExecContext(ctx, column.ddl); err != nil {
			return fmt.Errorf("add chetter_task_artifacts.%s: %w", column.name, err)
		}
	}
	return nil
}

func (s *Store) ensureTaskEventTypeColumn(ctx context.Context) error {
	exists, err := s.columnExists(ctx, "chetter_task_events", "event_type")
	if err != nil {
		return err
	}
	if !exists {
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE chetter_task_events ADD COLUMN event_type VARCHAR(64) NOT NULL DEFAULT 'task.progress' AFTER status"); err != nil {
			return fmt.Errorf("add chetter_task_events.event_type: %w", err)
		}
	}
	indexExists, err := s.indexExists(ctx, "chetter_task_events", "idx_chetter_task_events_type_created")
	if err != nil {
		return err
	}
	if !indexExists {
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE chetter_task_events ADD KEY idx_chetter_task_events_type_created (event_type, created_at)"); err != nil {
			return fmt.Errorf("add chetter_task_events event_type index: %w", err)
		}
	}
	return nil
}

func (s *Store) ensureTokenColumns(ctx context.Context) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"total_input_tokens", "ALTER TABLE chetter_tasks ADD COLUMN total_input_tokens BIGINT NOT NULL DEFAULT 0 AFTER session_export"},
		{"total_output_tokens", "ALTER TABLE chetter_tasks ADD COLUMN total_output_tokens BIGINT NOT NULL DEFAULT 0 AFTER total_input_tokens"},
		{"total_cache_read_tokens", "ALTER TABLE chetter_tasks ADD COLUMN total_cache_read_tokens BIGINT NOT NULL DEFAULT 0 AFTER total_output_tokens"},
		{"total_cache_write_tokens", "ALTER TABLE chetter_tasks ADD COLUMN total_cache_write_tokens BIGINT NOT NULL DEFAULT 0 AFTER total_cache_read_tokens"},
		{"total_reasoning_tokens", "ALTER TABLE chetter_tasks ADD COLUMN total_reasoning_tokens BIGINT NOT NULL DEFAULT 0 AFTER total_cache_write_tokens"},
		{"cost_cents", "ALTER TABLE chetter_tasks ADD COLUMN cost_cents BIGINT NOT NULL DEFAULT 0 AFTER total_reasoning_tokens"},
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

func (s *Store) indexExists(ctx context.Context, table, index string) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM information_schema.statistics
		WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?
	`, table, index).Scan(&count); err != nil {
		return false, fmt.Errorf("check index %s.%s: %w", table, index, err)
	}
	return count > 0, nil
}

func (s *Store) indexColumnName(ctx context.Context, table, index string) (string, error) {
	var col string
	err := s.db.QueryRowContext(ctx, `
		SELECT column_name
		FROM information_schema.statistics
		WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?
		ORDER BY seq_in_index
		LIMIT 1
	`, table, index).Scan(&col)
	if err != nil {
		return "", fmt.Errorf("get index column %s.%s: %w", table, index, err)
	}
	return col, nil
}

func (s *Store) ensureSearchTextColumns(ctx context.Context) error {
	columns := []struct {
		table string
		ddl   string
	}{
		{"chetter_tasks", "ALTER TABLE chetter_tasks ADD COLUMN search_text TEXT NULL AFTER session_export"},
		{"chetter_agent_sessions", "ALTER TABLE chetter_agent_sessions ADD COLUMN search_text TEXT NULL AFTER error"},
		{"chetter_audit_log", "ALTER TABLE chetter_audit_log ADD COLUMN search_text TEXT NULL AFTER detail"},
		{"chetter_task_artifacts", "ALTER TABLE chetter_task_artifacts ADD COLUMN search_text TEXT NULL AFTER discovery_source"},
	}
	for _, c := range columns {
		exists, err := s.columnExists(ctx, c.table, "search_text")
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := s.db.ExecContext(ctx, c.ddl); err != nil {
			return fmt.Errorf("add %s.search_text: %w", c.table, err)
		}
	}
	return nil
}

func (s *Store) backfillSearchText(ctx context.Context) error {
	backfills := []string{
		`UPDATE chetter_tasks SET search_text = CONCAT_WS(' ',
			COALESCE(prompt,''), COALESCE(summary,''), COALESCE(error,''),
			COALESCE(agent,''), COALESCE(model_id,''), COALESCE(trigger_name,''),
			COALESCE(git_url,'')
		) WHERE search_text IS NULL`,
		`UPDATE chetter_agent_sessions SET search_text = CONCAT_WS(' ',
			COALESCE(id,''), COALESCE(agent,''), COALESCE(model_id,''),
			COALESCE(git_url,''), COALESCE(error,'')
		) WHERE search_text IS NULL`,
		`UPDATE chetter_audit_log SET search_text = CONCAT_WS(' ',
			COALESCE(detail,''), COALESCE(source_type,''), COALESCE(source_id,''),
			COALESCE(target_type,''), COALESCE(target_id,''), COALESCE(repo,''),
			COALESCE(event_type,'')
		) WHERE search_text IS NULL`,
		`UPDATE chetter_task_artifacts SET search_text = CONCAT_WS(' ',
			COALESCE(task_id,''), COALESCE(repo,''), COALESCE(artifact_type,''),
			COALESCE(ref,'')
		) WHERE search_text IS NULL`,
	}
	for _, q := range backfills {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			slog.Warn("backfill search_text failed", "err", err)
		}
	}
	return nil
}

// ReapStaleTasks finds running tasks that have exceeded their timeout + grace
// period and marks them as error. Uses started_at (not updated_at) because
// updated_at is refreshed on every heartbeat, which would prevent the reaper
// from ever firing on tasks that keep heartbeating past their timeout.
func (s *Store) ReapStaleTasks(ctx context.Context, grace time.Duration) (int, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE chetter_tasks
		SET status = 'error',
		    error = CONCAT('runner timeout: task ran for ', TIMESTAMPDIFF(SECOND, started_at, NOW()), ' seconds (timeout was ', timeout_sec, 's)'),
		    error_category = 'timeout',
		    ended_at = ?,
		    updated_at = ?
		WHERE status = 'running'
		  AND TIMESTAMPDIFF(SECOND, started_at, NOW()) > timeout_sec + ?
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
