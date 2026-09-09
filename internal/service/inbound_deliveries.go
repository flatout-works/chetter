package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"text/template"
	"time"

	"github.com/flatout-works/chetter/internal/data"
	"github.com/flatout-works/chetter/internal/store"
)

// inboundEndpointRow is the full materialized endpoint row used by the
// delivery worker to render and submit the action.
type inboundEndpointRow struct {
	ID               string
	Name             string
	TeamID           string
	Enabled          bool
	AuthType         string
	SecretEnv        string
	ActionType       string
	ActionPrompt     string
	ActionAgent      string
	ActionTimeoutSec int
	AcceptedEvents   []string
}

// inboundDeliveryRow is one claimed inbound delivery row.
type inboundDeliveryRow struct {
	ID          string
	EndpointID  string
	TeamID      string
	DeliveryID  string
	EventType   string
	SourceIP    string
	Payload     string
	Attempts    int
	MaxAttempts int
	TaskID      string
}

// inboundDeliveryLoop is the background worker that drains the inbound
// deliveries inbox (issue #120). It runs on every server replica; claims are
// lease-fenced with FOR UPDATE SKIP LOCKED so a delivery is processed once.
func (s *Service) inboundDeliveryLoop() {
	ticker := time.NewTicker(inboundDeliveryWorkerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			parent := context.Background()
			if s.shutdownCtx != nil {
				parent = s.shutdownCtx
			}
			ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
			func() {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("inbound delivery worker panic recovered", "panic", r)
					}
				}()
				if err := s.processDueInboundDeliveries(ctx); err != nil {
					slog.Warn("inbound delivery worker cycle failed", "err", err)
				}
			}()
			cancel()
		case <-s.reaperStop:
			return
		}
	}
}

// processDueInboundDeliveries claims and processes one batch of due inbound
// deliveries. Exposed on Service so tests can drive worker cycles
// deterministically without starting the ticker loop.
func (s *Service) processDueInboundDeliveries(ctx context.Context) error {
	if s.rawDB == nil {
		return nil
	}
	now := time.Now().UTC()
	deliveries, err := s.claimDueInboundDeliveries(ctx, now)
	if err != nil {
		return fmt.Errorf("claim due inbound deliveries: %w", err)
	}
	for _, delivery := range deliveries {
		processCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		processErr := s.processInboundDelivery(processCtx, delivery)
		cancel()
		if processErr != nil {
			slog.Warn("inbound delivery processing failed", "delivery_id", delivery.ID, "endpoint_id", delivery.EndpointID, "err", processErr)
		}
	}
	return nil
}

// claimDueInboundDeliveries atomically claims due deliveries under a lease
// inside one transaction: pending/retry_wait rows whose next_attempt_at has
// arrived plus processing rows whose lease expired (crashed worker) are
// selected with FOR UPDATE SKIP LOCKED and marked processing with a fresh
// lease and an incremented attempt count before the transaction commits.
func (s *Service) claimDueInboundDeliveries(ctx context.Context, now time.Time) ([]inboundDeliveryRow, error) {
	var claimed []inboundDeliveryRow
	err := withTxRetryOptions(ctx, s.rawDB, s.dialect, nil, func(_ data.Repository, tx *sql.Tx) error {
		limitClause := fmt.Sprintf(" ORDER BY next_attempt_at ASC LIMIT %d FOR UPDATE SKIP LOCKED", inboundDeliveryClaimLimit)
		ph := s.inboundPh
		query := `SELECT id, endpoint_id, COALESCE(team_id, ''), COALESCE(delivery_id, ''),
		                COALESCE(event_type, ''), COALESCE(source_ip, ''), payload,
		                attempts, max_attempts, COALESCE(task_id, '')
		         FROM inbound_deliveries
		         WHERE (status = 'pending' AND next_attempt_at <= ` + ph(1) + `)
		            OR (status = 'retry_wait' AND next_attempt_at <= ` + ph(2) + `)
		            OR (status = 'processing' AND lease_expires_at < ` + ph(3) + `)` + limitClause
		rows, err := tx.QueryContext(ctx, query, now, now, now)
		if err != nil {
			return fmt.Errorf("select due inbound deliveries: %w", err)
		}
		items := make([]inboundDeliveryRow, 0, 8)
		for rows.Next() {
			var item inboundDeliveryRow
			if err := rows.Scan(&item.ID, &item.EndpointID, &item.TeamID, &item.DeliveryID, &item.EventType,
				&item.SourceIP, &item.Payload, &item.Attempts, &item.MaxAttempts, &item.TaskID); err != nil {
				rows.Close()
				return fmt.Errorf("scan due inbound delivery: %w", err)
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate due inbound deliveries: %w", err)
		}
		rows.Close()
		for _, item := range items {
			attempt := item.Attempts + 1
			markQuery := fmt.Sprintf(
				`UPDATE inbound_deliveries
				 SET status = 'processing', attempts = %d, lease_expires_at = %s, updated_at = %s
				 WHERE id = %s`,
				attempt, s.inboundPh(1), s.inboundPh(2), s.inboundPh(3),
			)
			lease := sql.NullTime{Time: now.Add(inboundDeliveryLease), Valid: true}
			if _, err := tx.ExecContext(ctx, markQuery, lease, now, item.ID); err != nil {
				return fmt.Errorf("mark inbound delivery processing: %w", err)
			}
		}
		claimed = items
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Carry the incremented attempt count into processing.
	for i := range claimed {
		claimed[i].Attempts++
	}
	return claimed, nil
}

// inboundPh returns the dialect placeholder sequence used by the worker's raw
// SQL updates.
func (s *Service) inboundPh(n int) string {
	if s.dialect == store.DialectPostgres {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

// processInboundDelivery performs the endpoint action for one claimed
// delivery and records the terminal or retry transition. The delivery/task
// correlation is persisted on the row (task_id) and task creation uses a
// deterministic id derived from the delivery row, so retries after a crash
// cannot create duplicate tasks.
func (s *Service) processInboundDelivery(ctx context.Context, delivery inboundDeliveryRow) error {
	endpoint, err := s.loadInboundEndpoint(ctx, delivery.EndpointID)
	if err != nil {
		return err
	}
	if endpoint == nil {
		// The endpoint definition was removed while the delivery was queued.
		// The request was durably accepted; without a target action it can
		// never complete, so it is terminal with a visible reason.
		return s.finishInboundDelivery(ctx, delivery, inboundDeliveryStatusFailedPermanent,
			"endpoint no longer exists", "", nil)
	}
	if !endpoint.Enabled {
		return s.finishInboundDelivery(ctx, delivery, inboundDeliveryStatusFailedPermanent,
			"endpoint disabled", "", nil)
	}
	if len(endpoint.AcceptedEvents) > 0 && !stringInSlice(delivery.EventType, endpoint.AcceptedEvents) {
		// Valid, authenticated request for an event this endpoint does not
		// act on: nothing to do, record as succeeded without creating a task.
		return s.markInboundDeliverySucceeded(ctx, delivery, "")
	}
	if endpoint.ActionType != "create_task" {
		return s.finishInboundDelivery(ctx, delivery, inboundDeliveryStatusFailedPermanent,
			fmt.Sprintf("unsupported action type %q", endpoint.ActionType), "", nil)
	}
	prompt, err := renderInboundPrompt(endpoint, delivery)
	if err != nil {
		// A static template that cannot render never will; terminal.
		return s.finishInboundDelivery(ctx, delivery, inboundDeliveryStatusFailedPermanent,
			fmt.Sprintf("render action prompt: %v", err), "", nil)
	}

	taskID := inboundDeliveryTaskID(delivery.ID)
	_, submitErr := s.SubmitTask(ctx, SubmitTaskRequest{
		TeamID:           endpoint.TeamID,
		Prompt:           prompt,
		Agent:            endpoint.ActionAgent,
		TimeoutSec:       endpoint.ActionTimeoutSec,
		TriggerType:      "inbound_webhook",
		SubmissionSource: "inbound_webhook",
		ExplicitTaskID:   taskID,
	})
	if submitErr != nil {
		if isDuplicateKeyError(submitErr) {
			// A previous attempt created the task and crashed before marking
			// this delivery succeeded. Resolve the existing task instead of
			// creating a duplicate.
			if _, getErr := s.repo.GetTaskByID(ctx, taskID); getErr == nil {
				slog.Info("inbound delivery: task already created by previous attempt", "delivery_id", delivery.ID, "task_id", taskID)
				return s.markInboundDeliverySucceeded(ctx, delivery, taskID)
			}
		}
		status := inboundDeliveryStatusRetryWait
		if isInboundPermanentTaskError(submitErr) {
			status = inboundDeliveryStatusFailedPermanent
		}
		return s.finishInboundDelivery(ctx, delivery, status, fmt.Sprintf("create task: %v", submitErr), taskID, nil)
	}
	return s.markInboundDeliverySucceeded(ctx, delivery, taskID)
}

// isInboundPermanentTaskError distinguishes deterministic task-submission
// failures (validation, missing configuration, unknown agent/team) from
// transient database/network outages. Deterministic failures dead-end as
// failed_permanent; transient ones retry with backoff.
func isInboundPermanentTaskError(err error) bool {
	if err == nil {
		return false
	}
	if isRetryableTxError(err) {
		return false
	}
	if store.IsTransientError(err) {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"database not available", "connection refused", "connection reset",
		"dial tcp", "context deadline exceeded", "i/o timeout", "write conflict",
		"lock wait timeout", "deadlock", "too many connections",
	} {
		if strings.Contains(msg, marker) {
			return false
		}
	}
	return true
}

// finishInboundDelivery records a failed or retry-wait transition and audits
// it. Transient failures advance the attempt count (already incremented at
// claim time); when attempts reach max_attempts the delivery dead-letters.
// taskID is best-effort correlation info for the audit detail.
func (s *Service) finishInboundDelivery(ctx context.Context, delivery inboundDeliveryRow, status, errMsg, taskID string, nextAttemptAt *time.Time) error {
	if status == inboundDeliveryStatusFailedPermanent {
		s.auditInboundDelivery(ctx, delivery, inboundDeliveryStatusFailedPermanent, errMsg)
		return s.markInboundDeliveryFailed(ctx, delivery, status, errMsg)
	}
	// retry_wait vs dead_letter decision based on the attempt count.
	if delivery.Attempts >= delivery.MaxAttempts {
		s.auditInboundDelivery(ctx, delivery, inboundDeliveryStatusDeadLetter,
			fmt.Sprintf("delivery failed after %d/%d attempts: %s", delivery.Attempts, delivery.MaxAttempts, errMsg))
		return s.markInboundDeliveryFailed(ctx, delivery, inboundDeliveryStatusDeadLetter, errMsg)
	}
	nextAt := time.Now().UTC().Add(inboundDeliveryBackoff(int32(delivery.Attempts)))
	s.auditInboundDelivery(ctx, delivery, inboundDeliveryStatusRetryWait, errMsg)
	return s.markInboundDeliveryFailed(ctx, delivery, inboundDeliveryStatusRetryWait, errMsg, &nextAt)
}

// markInboundDeliverySucceeded records a successful delivery and persists the
// delivery/task correlation. taskID may be empty when no task was created
// (e.g. event filtered by accepted_events).
func (s *Service) markInboundDeliverySucceeded(ctx context.Context, delivery inboundDeliveryRow, taskID string) error {
	now := time.Now().UTC()
	var query string
	if s.dialect == store.DialectPostgres {
		query = `UPDATE inbound_deliveries
		         SET status = 'succeeded', error = NULL, task_id = $1, lease_expires_at = NULL,
		             next_attempt_at = NULL, processed_at = $2, updated_at = $3
		         WHERE id = $4`
	} else {
		query = `UPDATE inbound_deliveries
		         SET status = 'succeeded', error = NULL, task_id = ?, lease_expires_at = NULL,
		             next_attempt_at = NULL, processed_at = ?, updated_at = ?
		         WHERE id = ?`
	}
	var taskIDParam any
	if taskID != "" {
		taskIDParam = taskID
	}
	if _, err := s.rawDB.ExecContext(ctx, query, taskIDParam, now, now, delivery.ID); err != nil {
		return fmt.Errorf("mark inbound delivery succeeded: %w", err)
	}
	s.auditInboundDelivery(ctx, delivery, inboundDeliveryStatusSucceeded,
		fmt.Sprintf("delivery completed task_id=%s", taskID))
	return nil
}

// markInboundDeliveryFailed records a failed/retry_wait/dead_letter state.
func (s *Service) markInboundDeliveryFailed(ctx context.Context, delivery inboundDeliveryRow, status, errMsg string, nextAttemptAt ...*time.Time) error {
	now := time.Now().UTC()
	var next any = sql.NullTime{}
	if len(nextAttemptAt) > 0 && nextAttemptAt[0] != nil {
		next = sql.NullTime{Time: *nextAttemptAt[0], Valid: true}
	}
	var query string
	if s.dialect == store.DialectPostgres {
		query = `UPDATE inbound_deliveries
		         SET status = $1, error = $2, lease_expires_at = NULL, next_attempt_at = $3, updated_at = $4
		         WHERE id = $5`
	} else {
		query = `UPDATE inbound_deliveries
		         SET status = ?, error = ?, lease_expires_at = NULL, next_attempt_at = ?, updated_at = ?
		         WHERE id = ?`
	}
	if _, err := s.rawDB.ExecContext(ctx, query, status, truncateTo500(errMsg), next, now, delivery.ID); err != nil {
		return fmt.Errorf("mark inbound delivery %s: %w", status, err)
	}
	return nil
}

// loadInboundEndpoint loads the endpoint row for processing.
func (s *Service) loadInboundEndpoint(ctx context.Context, endpointID string) (*inboundEndpointRow, error) {
	if s.rawDB == nil {
		return nil, fmt.Errorf("database not available")
	}
	var query string
	if s.dialect == store.DialectPostgres {
		query = `SELECT id, name, COALESCE(team_id, ''), enabled, action_type, action_prompt,
		                COALESCE(action_agent, ''), COALESCE(action_timeout_sec, 0),
		                COALESCE(accepted_events, 'null')
		         FROM webhook_endpoints WHERE id = $1`
	} else {
		query = `SELECT id, name, COALESCE(team_id, ''), enabled, action_type, action_prompt,
		                COALESCE(action_agent, ''), COALESCE(action_timeout_sec, 0),
		                COALESCE(accepted_events, 'null')
		         FROM webhook_endpoints WHERE id = ?`
	}
	var row inboundEndpointRow
	var acceptedEvents []byte
	err := s.rawDB.QueryRowContext(ctx, query, endpointID).Scan(&row.ID, &row.Name, &row.TeamID, &row.Enabled,
		&row.ActionType, &row.ActionPrompt, &row.ActionAgent, &row.ActionTimeoutSec, &acceptedEvents)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load inbound endpoint: %w", err)
	}
	if len(acceptedEvents) > 0 && string(acceptedEvents) != "null" {
		_ = json.Unmarshal(acceptedEvents, &row.AcceptedEvents)
	}
	return &row, nil
}

// inboundTemplateData is the normalized metadata context available to action
// prompt templates. Only the decoded JSON payload and safe metadata fields
// are exposed — never raw request headers or secret values.
type inboundTemplateData struct {
	EndpointName string
	EventType    string
	DeliveryID   string
	SourceIP     string
	Payload      any
	PayloadRaw   string
}

func renderInboundPrompt(endpoint *inboundEndpointRow, delivery inboundDeliveryRow) (string, error) {
	tmpl, err := template.New("inbound_action").Parse(endpoint.ActionPrompt)
	if err != nil {
		return "", err
	}
	var payload any
	var payloadObject map[string]any
	if err := json.Unmarshal([]byte(delivery.Payload), &payloadObject); err == nil {
		payload = payloadObject
	} else {
		_ = json.Unmarshal([]byte(delivery.Payload), &payload)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, inboundTemplateData{
		EndpointName: endpoint.Name,
		EventType:    delivery.EventType,
		DeliveryID:   delivery.DeliveryID,
		SourceIP:     delivery.SourceIP,
		Payload:      payload,
		PayloadRaw:   delivery.Payload,
	}); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

// auditInboundDelivery records a delivery state transition. Detail carries
// only ids, statuses, and error text — never the payload or secret material.
func (s *Service) auditInboundDelivery(ctx context.Context, delivery inboundDeliveryRow, eventType, detail string) {
	s.auditAsync(ctx, AuditEventParams{
		EventType:  "inbound_delivery_" + eventType,
		SourceType: "inbound_delivery",
		SourceID:   delivery.ID,
		TargetType: "task",
		TargetID:   delivery.TaskID,
		Detail:     fmt.Sprintf("endpoint %s %s", delivery.EndpointID, detail),
	})
}

func stringInSlice(needle string, haystack []string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}
