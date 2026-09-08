package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/inbound"
	"github.com/flatout-works/chetter/internal/store"
)

// Inbound webhook delivery state machine (issue #120, epic #253 Phase 2).
// Requests are durably inserted as pending rows by the receiver before it
// answers 202; a leased multi-replica worker claims due rows and performs the
// endpoint action (create_task) exactly once:
//
//	pending -> processing -> succeeded
//	                     -> retry_wait (transient failure) -> processing -> ... -> dead_letter
//	                     -> failed_permanent (non-retryable)
//
// A crashed worker's processing rows are reclaimed once lease_expires_at
// passes. Each delivery row persists the task it created (task_id) and task
// creation uses a deterministic task id derived from the delivery row id, so
// a retry after a crash can never create a duplicate task.
const (
	inboundDeliveryStatusPending         = "pending"
	inboundDeliveryStatusProcessing      = "processing"
	inboundDeliveryStatusSucceeded       = "succeeded"
	inboundDeliveryStatusRetryWait       = "retry_wait"
	inboundDeliveryStatusFailedPermanent = "failed_permanent"
	inboundDeliveryStatusDeadLetter      = "dead_letter"

	// defaultInboundDeliveryMaxAttempts bounds retries of transient failures
	// before a delivery dead-letters.
	defaultInboundDeliveryMaxAttempts = 3
	// inboundDeliveryLease is how long a claimed delivery stays reserved for
	// the claiming replica. It comfortably exceeds one task-submission cycle.
	inboundDeliveryLease = 60 * time.Second
	// inboundDeliveryClaimLimit is the maximum number of due deliveries one
	// worker cycle claims under FOR UPDATE SKIP LOCKED.
	inboundDeliveryClaimLimit = 50
	// inboundDeliveryWorkerInterval is the delivery worker poll interval.
	inboundDeliveryWorkerInterval = 5 * time.Second
)

// inboundDeliveryBackoff returns the delay before the next attempt after n
// failed attempts (1-based): 1s, 5s, 15s, then capped at 30s.
func inboundDeliveryBackoff(attempts int32) time.Duration {
	switch {
	case attempts >= 4:
		return 30 * time.Second
	case attempts == 3:
		return 15 * time.Second
	case attempts == 2:
		return 5 * time.Second
	default:
		return time.Second
	}
}

// inboundEndpointRecord is the MCP-tool and Web API view of one materialized
// inbound endpoint. Secret values are never exposed: only the secret_env
// reference and a derived "secret configured" boolean are returned.
type inboundEndpointRecord struct {
	ID               string    `json:"id"`
	PublicID         string    `json:"public_id"`
	Name             string    `json:"name"`
	Scope            string    `json:"scope"`
	TeamID           string    `json:"team_id,omitempty"`
	SourcePath       string    `json:"source_path"`
	Enabled          bool      `json:"enabled"`
	AuthType         string    `json:"auth_type"`
	SecretEnv        string    `json:"secret_env"`
	SignatureHeader  string    `json:"signature_header,omitempty"`
	SignaturePrefix  string    `json:"signature_prefix,omitempty"`
	DeliveryIDHdr    string    `json:"delivery_id_header,omitempty"`
	EventTypeHdr     string    `json:"event_type_header,omitempty"`
	AcceptedEvents   []string  `json:"accepted_events,omitempty"`
	ActionType       string    `json:"action_type"`
	ActionAgent      string    `json:"action_agent,omitempty"`
	ActionTimeout    int       `json:"action_timeout_sec,omitempty"`
	PublicURL        string    `json:"public_url"`
	SecretConfigured bool      `json:"secret_configured"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// inboundDeliveryRecord is the MCP-tool view of one inbound delivery. Payloads
// are deliberately not exposed; the record carries status, retry, task, and
// provenance fields.
type inboundDeliveryRecord struct {
	ID            string     `json:"id"`
	EndpointID    string     `json:"endpoint_id"`
	EndpointName  string     `json:"endpoint_name,omitempty"`
	TeamID        string     `json:"team_id,omitempty"`
	DeliveryID    string     `json:"delivery_id,omitempty"`
	EventType     string     `json:"event_type,omitempty"`
	SourceIP      string     `json:"source_ip,omitempty"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	MaxAttempts   int        `json:"max_attempts"`
	Error         string     `json:"error,omitempty"`
	TaskID        string     `json:"task_id,omitempty"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	ProcessedAt   *time.Time `json:"processed_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// inboundStore implements inbound.Store against the raw database so the
// receiver can resolve endpoints and durably insert deliveries. See issue
// #102 for the same raw-SQL pattern.
type inboundStore struct {
	db      *sql.DB
	dialect store.Dialect
}

func newInboundStore(db *sql.DB, dialect store.Dialect) *inboundStore {
	return &inboundStore{db: db, dialect: dialect}
}

func (d *inboundStore) ph(n int) string {
	if d.dialect == store.DialectPostgres {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

func (d *inboundStore) GetEndpointByPublicID(ctx context.Context, publicID string) (inbound.Endpoint, bool, error) {
	if d.db == nil {
		return inbound.Endpoint{}, false, fmt.Errorf("database not available")
	}
	query := fmt.Sprintf(
		`SELECT id, public_id, name, scope, COALESCE(team_id, ''), enabled, auth_type, secret_env,
		        COALESCE(signature_header, ''), COALESCE(signature_prefix, ''),
		        COALESCE(delivery_id_header, ''), COALESCE(event_type_header, '')
		 FROM webhook_endpoints
		 WHERE public_id = %s AND enabled = 1 LIMIT 1`,
		d.ph(1),
	)
	if d.dialect == store.DialectPostgres {
		query = `SELECT id, public_id, name, scope, COALESCE(team_id, ''), enabled, auth_type, secret_env,
		                COALESCE(signature_header, ''), COALESCE(signature_prefix, ''),
		                COALESCE(delivery_id_header, ''), COALESCE(event_type_header, '')
		         FROM webhook_endpoints
		         WHERE public_id = $1 AND enabled = TRUE LIMIT 1`
	}
	row := d.db.QueryRowContext(ctx, query, publicID)
	var endpoint inbound.Endpoint
	if err := row.Scan(&endpoint.ID, &endpoint.PublicID, &endpoint.Name, &endpoint.Scope, &endpoint.TeamID,
		&endpoint.Enabled, &endpoint.AuthType, &endpoint.SecretEnv,
		&endpoint.SignatureHeader, &endpoint.SignaturePrefix,
		&endpoint.DeliveryIDHeader, &endpoint.EventTypeHeader); err != nil {
		if err == sql.ErrNoRows {
			return inbound.Endpoint{}, false, nil
		}
		return inbound.Endpoint{}, false, fmt.Errorf("get endpoint by public id: %w", err)
	}
	return endpoint, true, nil
}

func (d *inboundStore) InsertDelivery(ctx context.Context, insert inbound.DeliveryInsert) (bool, error) {
	if d.db == nil {
		return false, fmt.Errorf("database not available")
	}
	id, err := randomID("ibd")
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	query := fmt.Sprintf(
		`INSERT INTO inbound_deliveries
		   (id, endpoint_id, team_id, delivery_id, event_type, source_ip, payload,
		    status, attempts, max_attempts, created_at, updated_at, next_attempt_at)
		 VALUES (%s, %s, %s, %s, %s, %s, %s, 'pending', 0, %d, %s, %s, %s)`,
		d.ph(1), d.ph(2), d.ph(3), d.ph(4), d.ph(5), d.ph(6), d.ph(7), defaultInboundDeliveryMaxAttempts, d.ph(8), d.ph(9), d.ph(10),
	)
	var deliveryID any
	if insert.DeliveryID != "" {
		deliveryID = insert.DeliveryID
	}
	var eventType any
	if insert.EventType != "" {
		eventType = insert.EventType
	}
	var teamID any
	if insert.TeamID != "" {
		teamID = insert.TeamID
	}
	_, err = d.db.ExecContext(ctx, query, id, insert.EndpointID, teamID, deliveryID, eventType, insert.SourceIP,
		insert.Payload, now, now, now)
	if err != nil {
		if isDuplicateKeyError(err) {
			return false, nil
		}
		return false, fmt.Errorf("insert inbound delivery: %w", err)
	}
	return true, nil
}

func (d *inboundStore) PendingDeliveryCount(ctx context.Context, endpointID string) (int, error) {
	if d.db == nil {
		return 0, fmt.Errorf("database not available")
	}
	query := fmt.Sprintf(
		`SELECT COUNT(*) FROM inbound_deliveries
		  WHERE endpoint_id = %s AND status IN ('pending', 'processing', 'retry_wait')`,
		d.ph(1),
	)
	var count int
	if err := d.db.QueryRowContext(ctx, query, endpointID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count pending inbound deliveries: %w", err)
	}
	return count, nil
}

// inboundVisibleToScope reports whether a team-scoped row (endpoint or
// delivery) may be seen by the caller. Admins and unscoped callers see
// everything; token-scoped callers see only their own teams.
func inboundVisibleToScope(ctx context.Context, teamID string) bool {
	scope, ok := auth.GetScope(ctx)
	if !ok || scope.Admin {
		return true
	}
	if teamID == "" {
		return false
	}
	return scope.HasTeam(teamID)
}

// ListInboundEndpoints returns materialized inbound endpoints the caller may
// see. Secret values are never returned; SecretConfigured reports whether the
// referenced environment variable currently holds a value.
func (s *Service) ListInboundEndpoints(ctx context.Context, limit, offset int) ([]inboundEndpointRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	if s.rawDB == nil {
		return nil, fmt.Errorf("database not available")
	}
	var query string
	if s.dialect == store.DialectPostgres {
		query = `SELECT id, public_id, name, scope, COALESCE(team_id, ''), source_path, enabled, auth_type,
		                secret_env, COALESCE(signature_header, ''), COALESCE(signature_prefix, ''),
		                COALESCE(delivery_id_header, ''), COALESCE(event_type_header, ''),
		                COALESCE(accepted_events, 'null'), action_type, action_prompt, COALESCE(action_agent, ''),
		                COALESCE(action_timeout_sec, 0), created_at, updated_at
		         FROM webhook_endpoints
		         ORDER BY created_at DESC
		         LIMIT $1 OFFSET $2`
	} else {
		query = `SELECT id, public_id, name, scope, COALESCE(team_id, ''), source_path, enabled, auth_type,
		                secret_env, COALESCE(signature_header, ''), COALESCE(signature_prefix, ''),
		                COALESCE(delivery_id_header, ''), COALESCE(event_type_header, ''),
		                COALESCE(accepted_events, 'null'), action_type, action_prompt, COALESCE(action_agent, ''),
		                COALESCE(action_timeout_sec, 0), created_at, updated_at
		         FROM webhook_endpoints
		         ORDER BY created_at DESC
		         LIMIT ? OFFSET ?`
	}
	rows, err := s.rawDB.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list inbound endpoints: %w", err)
	}
	defer rows.Close()
	var records []inboundEndpointRecord
	for rows.Next() {
		record, teamID, err := scanInboundEndpoint(rows)
		if err != nil {
			return nil, err
		}
		if !inboundVisibleToScope(ctx, teamID) {
			continue
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanInboundEndpoint(row rowScanner) (inboundEndpointRecord, string, error) {
	var record inboundEndpointRecord
	var acceptedEvents []byte
	var actionPrompt string
	var actionTimeout sql.NullInt64
	if err := row.Scan(&record.ID, &record.PublicID, &record.Name, &record.Scope, &record.TeamID,
		&record.SourcePath, &record.Enabled, &record.AuthType, &record.SecretEnv,
		&record.SignatureHeader, &record.SignaturePrefix, &record.DeliveryIDHdr, &record.EventTypeHdr,
		&acceptedEvents, &record.ActionType, &actionPrompt, &record.ActionAgent,
		&actionTimeout, &record.CreatedAt, &record.UpdatedAt); err != nil {
		return record, "", fmt.Errorf("scan inbound endpoint: %w", err)
	}
	if len(acceptedEvents) > 0 && string(acceptedEvents) != "null" {
		_ = json.Unmarshal(acceptedEvents, &record.AcceptedEvents)
	}
	if actionTimeout.Valid {
		record.ActionTimeout = int(actionTimeout.Int64)
	}
	record.PublicURL = "/hooks/inbound/" + record.PublicID
	if record.Enabled {
		_, configured := os.LookupEnv(record.SecretEnv)
		record.SecretConfigured = configured
	}
	return record, record.TeamID, nil
}

// ListInboundDeliveries returns inbound delivery rows the caller may see,
// newest first. Payloads are never returned. When endpointID is non-empty only
// deliveries of that endpoint are listed.
func (s *Service) ListInboundDeliveries(ctx context.Context, endpointID, status string, limit, offset int) ([]inboundDeliveryRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	if s.rawDB == nil {
		return nil, fmt.Errorf("database not available")
	}
	where := "WHERE (? = '' OR endpoint_id = ?) AND (? = '' OR status = ?)"
	if s.dialect == store.DialectPostgres {
		where = "WHERE ($1 = '' OR endpoint_id = $1) AND ($2 = '' OR status = $2)"
	}
	orderLimit := " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	if s.dialect == store.DialectPostgres {
		orderLimit = " ORDER BY created_at DESC LIMIT $3 OFFSET $4"
	}
	query := `SELECT id, endpoint_id, COALESCE(team_id, ''), COALESCE(delivery_id, ''), COALESCE(event_type, ''),
	                 COALESCE(source_ip, ''), status, attempts, max_attempts, COALESCE(error, ''),
	                 COALESCE(task_id, ''), next_attempt_at, processed_at, created_at, updated_at
	          FROM inbound_deliveries ` + where + orderLimit
	rows, err := s.rawDB.QueryContext(ctx, query, endpointID, status, limit, offset)
	if s.dialect != store.DialectPostgres {
		// MySQL repeats each filter placeholder for the AND clauses; the
		// pagination limit/offset placeholders follow.
		rows, err = s.rawDB.QueryContext(ctx, query, endpointID, endpointID, status, status, limit, offset)
	}
	if err != nil {
		return nil, fmt.Errorf("list inbound deliveries: %w", err)
	}
	defer rows.Close()
	var records []inboundDeliveryRecord
	for rows.Next() {
		var record inboundDeliveryRecord
		var nextAttemptAt, processedAt sql.NullTime
		if err := rows.Scan(&record.ID, &record.EndpointID, &record.TeamID, &record.DeliveryID, &record.EventType,
			&record.SourceIP, &record.Status, &record.Attempts, &record.MaxAttempts, &record.Error,
			&record.TaskID, &nextAttemptAt, &processedAt, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan inbound delivery: %w", err)
		}
		if nextAttemptAt.Valid {
			record.NextAttemptAt = &nextAttemptAt.Time
		}
		if processedAt.Valid {
			record.ProcessedAt = &processedAt.Time
		}
		if !inboundVisibleToScope(ctx, record.TeamID) {
			continue
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// inboundDeliveryTaskID derives the deterministic task id for one delivery
// row. A retry that re-runs task creation after a crash reuses the same id,
// so the tasks primary key makes retries idempotent.
func inboundDeliveryTaskID(deliveryRowID string) string {
	sum := sha256.Sum256([]byte("inbound_delivery:" + deliveryRowID))
	return "task_" + hex.EncodeToString(sum[:])[:32]
}
