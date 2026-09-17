package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/flatout-works/chetter/internal/data"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/ssrf"
	"github.com/flatout-works/chetter/internal/store"
)

// Callback delivery state machine (issue #357). Rows are written in the same
// transaction as their task_events row (durable outbox), then a leased
// multi-replica worker claims due rows and delivers them with exponential
// backoff until success or max_attempts (dead_letter):
//
//	pending -> in_flight -> completed
//	                     -> failed (retry_wait) -> in_flight -> ... -> dead_letter
//
// A crashed worker's in_flight rows are reclaimed once lease_expires_at
// passes, so no delivery is lost to a process crash.
const (
	callbackDeliveryStatusPending    = "pending"
	callbackDeliveryStatusInFlight   = "in_flight"
	callbackDeliveryStatusCompleted  = "completed"
	callbackDeliveryStatusFailed     = "failed"
	callbackDeliveryStatusDeadLetter = "dead_letter"

	// defaultCallbackDeliveryMaxAttempts bounds outbound webhook/slack retries
	// before a delivery dead-letters. Mirror of the inbound webhook_deliveries
	// worker (issue #102).
	defaultCallbackDeliveryMaxAttempts = 3
	// callbackDeliveryLease is how long a claimed delivery stays reserved for
	// the claiming replica. It comfortably exceeds the SSRF-safe client's
	// total request timeout, so a delivery cannot be reclaimed while the
	// replica that claimed it is still awaiting the HTTP response.
	callbackDeliveryLease = 60 * time.Second
	// callbackDeliveryClaimLimit is the maximum number of due deliveries one
	// worker cycle claims under FOR UPDATE SKIP LOCKED.
	callbackDeliveryClaimLimit = 50
	// callbackDeliveryWorkerInterval is the delivery worker poll interval.
	callbackDeliveryWorkerInterval = 5 * time.Second
	// callbackDeliveryRequestTimeout bounds a single outbound delivery request.
	callbackDeliveryRequestTimeout = 15 * time.Second
)

// callbackDeliveryBackoff returns the delay before the next attempt after n
// failed attempts (1-based). Exponential: 1s, 5s, 15s, then capped at 30s.
func callbackDeliveryBackoff(attempts int32) time.Duration {
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

// nextCallbackDeliveryAttempt is the pure state transition after a failed
// delivery attempt. attemptsAfter is the 1-based count of failures including
// this one. When attemptsAfter reaches maxAttempts the delivery dead-letters
// (no further retry); otherwise it returns to the failed/retry_wait state with
// nextAttemptAt advanced by the backoff for the upcoming attempt.
func nextCallbackDeliveryAttempt(now time.Time, attemptsAfter, maxAttempts int32, backoff func(int32) time.Duration) (status string, nextAttemptAt time.Time, retry bool) {
	if attemptsAfter >= maxAttempts {
		return callbackDeliveryStatusDeadLetter, time.Time{}, false
	}
	return callbackDeliveryStatusFailed, now.Add(backoff(attemptsAfter)), true
}

// CallbackDeliveryRecord is the MCP-tool view of one outbound callback
// delivery. Payloads and stored headers are deliberately not exposed; the
// record carries endpoint, status, retry, and provenance fields.
type CallbackDeliveryRecord struct {
	ID            string     `json:"id"`
	CallbackID    string     `json:"callback_id"`
	EventID       string     `json:"event_id"`
	TaskID        string     `json:"task_id,omitempty"`
	TeamID        string     `json:"team_id,omitempty"`
	EventType     string     `json:"event_type"`
	ActionType    string     `json:"action_type"`
	EndpointURL   string     `json:"endpoint_url"`
	Method        string     `json:"method"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	MaxAttempts   int        `json:"max_attempts"`
	Error         string     `json:"error,omitempty"`
	ChildTaskID   string     `json:"child_task_id,omitempty"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	ProcessedAt   *time.Time `json:"processed_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// ListCallbackDeliveries returns recent outbound callback deliveries for the
// chetter_list_callback_deliveries MCP tool (issue #357 AC4). It uses raw SQL
// like ListWebhookDeliveries; the enqueue/claim/mark hot paths use the sqlc
// queries.
func (s *Service) ListCallbackDeliveries(ctx context.Context, statusFilter string, limit, offset int) ([]CallbackDeliveryRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	if s.rawDB == nil {
		return nil, fmt.Errorf("database not available")
	}
	var query string
	if s.dialect == store.DialectPostgres {
		query = `SELECT id, callback_id, event_id, COALESCE(task_id, ''), COALESCE(team_id, ''),
		                event_type, action_type, endpoint_url, method, status, attempts, max_attempts,
		                COALESCE(error, ''), COALESCE(child_task_id, ''),
		                next_attempt_at, processed_at, created_at, updated_at
		         FROM callback_deliveries
		         WHERE ($1 = '' OR status = $1)
		         ORDER BY created_at DESC
		         LIMIT $2 OFFSET $3`
	} else {
		query = `SELECT id, callback_id, event_id, COALESCE(task_id, ''), COALESCE(team_id, ''),
		                event_type, action_type, endpoint_url, method, status, attempts, max_attempts,
		                COALESCE(error, ''), COALESCE(child_task_id, ''),
		                next_attempt_at, processed_at, created_at, updated_at
		         FROM callback_deliveries
		         WHERE (? = '' OR status = ?)
		         ORDER BY created_at DESC
		         LIMIT ? OFFSET ?`
	}
	rows, err := s.rawDB.QueryContext(ctx, query, statusFilter, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list callback deliveries: %w", err)
	}
	defer rows.Close()
	var records []CallbackDeliveryRecord
	for rows.Next() {
		var r CallbackDeliveryRecord
		var nextAttemptAt, processedAt sql.NullTime
		if err := rows.Scan(&r.ID, &r.CallbackID, &r.EventID, &r.TaskID, &r.TeamID, &r.EventType,
			&r.ActionType, &r.EndpointURL, &r.Method, &r.Status, &r.Attempts, &r.MaxAttempts, &r.Error,
			&r.ChildTaskID, &nextAttemptAt, &processedAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan callback delivery: %w", err)
		}
		if nextAttemptAt.Valid {
			r.NextAttemptAt = &nextAttemptAt.Time
		}
		if processedAt.Valid {
			r.ProcessedAt = &processedAt.Time
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// EnqueueTaskEventCallbackDeliveries persists one outbound delivery row per
// enabled callback matching the event. It must run inside the same
// transaction that inserts the task_events row (issue #357 AC1, extended by
// issue #405) so a persisted event can never lack its delivery record; the
// unique (callback_id, event_id) key makes replays idempotent. All three
// action types are durable: webhook/slack rows hold the rendered HTTP request
// and create_task rows hold a JSON snapshot of the callback config and event,
// executed later by the leased delivery worker.
func (s *Service) EnqueueTaskEventCallbackDeliveries(ctx context.Context, q data.Repository, event TaskEventCallbackContext) error {
	callbacks, err := q.ListEnabledEventCallbacksForEvent(ctx, repository.ListEnabledEventCallbacksForEventParams{
		TeamID:    nullString(event.TeamID),
		EventType: event.EventType,
	})
	if err != nil {
		return fmt.Errorf("list event callbacks for delivery enqueue: %w", err)
	}
	now := time.Now().UTC()
	for _, callback := range callbacks {
		switch callback.ActionType {
		case EventCallbackActionWebhook, EventCallbackActionSlack:
			if err := s.enqueueCallbackDelivery(ctx, q, event, callback, now); err != nil {
				return err
			}
		case EventCallbackActionCreateTask:
			if err := s.enqueueCreateTaskCallbackDelivery(ctx, q, event, callback, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// callbackCreateTaskDelivery is the durable snapshot stored in a create_task
// delivery row's payload column (issue #405). It carries everything needed to
// spawn the child at execution time without depending on the live callback or
// the task_events row still existing: the callback name (for env/provenance),
// the action config (prompt template and task parameters), and the event that
// triggered it (template data).
type callbackCreateTaskDelivery struct {
	Name   string                   `json:"name"`
	Config callbackCreateTaskConfig `json:"config"`
	Event  TaskEventCallbackContext `json:"event"`
}

// enqueueCreateTaskCallbackDelivery writes a durable create_task delivery row
// in the caller's transaction. A config that can never spawn a task is
// recorded directly as dead_letter (mirroring webhook/slack) rather than
// being logged and dropped; valid configs are stored as pending rows and
// executed by the leased worker.
func (s *Service) enqueueCreateTaskCallbackDelivery(ctx context.Context, q data.Repository, event TaskEventCallbackContext, callback repository.EventCallback, now time.Time) error {
	var cfg callbackCreateTaskConfig
	if err := json.Unmarshal(callback.ActionConfig, &cfg); err != nil {
		return s.enqueueBrokenCallbackDelivery(ctx, q, event, callback, now, EventCallbackActionCreateTask, fmt.Sprintf("parse action_config: %v", err))
	}
	if cfg.Prompt == "" {
		return s.enqueueBrokenCallbackDelivery(ctx, q, event, callback, now, EventCallbackActionCreateTask, "create_task action_config.prompt is required")
	}
	snapshot, err := json.Marshal(callbackCreateTaskDelivery{Name: callback.Name, Config: cfg, Event: event})
	if err != nil {
		return s.enqueueBrokenCallbackDelivery(ctx, q, event, callback, now, EventCallbackActionCreateTask, fmt.Sprintf("encode delivery snapshot: %v", err))
	}
	id, err := randomID("cbd")
	if err != nil {
		return fmt.Errorf("generate callback delivery id: %w", err)
	}
	return q.InsertCallbackDelivery(ctx, repository.InsertCallbackDeliveryParams{
		ID:             id,
		CallbackID:     callback.ID,
		EventID:        event.ID,
		TaskID:         nullString(event.TaskID),
		TeamID:         nullString(event.TeamID),
		EventType:      event.EventType,
		ActionType:     EventCallbackActionCreateTask,
		EndpointUrl:    "",
		Method:         "",
		Headers:        sql.NullString{},
		Payload:        string(snapshot),
		Status:         callbackDeliveryStatusPending,
		Attempts:       0,
		MaxAttempts:    defaultCallbackDeliveryMaxAttempts,
		Error:          sql.NullString{},
		ChildTaskID:    sql.NullString{},
		LeaseExpiresAt: sql.NullTime{},
		NextAttemptAt:  sql.NullTime{Time: now, Valid: true},
		ProcessedAt:    sql.NullTime{},
		CreatedAt:      now,
		UpdatedAt:      now,
	})
}

// enqueueCallbackDelivery renders the delivery snapshot for one callback and
// inserts it. Only unexpected database failures abort the caller's
// transaction: a callback whose config cannot produce a deliverable payload
// (bad action_config, empty URL, or unrenderable template) is enqueued
// directly as a dead_letter row so it is visible and never silently dropped,
// and a destination rejected by the SSRF policy (issue #337) is audited and
// skipped exactly as the previous inline delivery path behaved.
func (s *Service) enqueueCallbackDelivery(ctx context.Context, q data.Repository, event TaskEventCallbackContext, callback repository.EventCallback, now time.Time) error {
	var cfg callbackWebhookConfig
	if err := json.Unmarshal(callback.ActionConfig, &cfg); err != nil {
		return s.enqueueBrokenCallbackDelivery(ctx, q, event, callback, now, callback.ActionType, fmt.Sprintf("parse action_config: %v", err))
	}
	if strings.TrimSpace(cfg.URL) == "" {
		return s.enqueueBrokenCallbackDelivery(ctx, q, event, callback, now, callback.ActionType, "webhook action_config.url is required")
	}
	// Defense in depth (issue #337): never enqueue a delivery for a
	// destination the SSRF-safe policy currently rejects. The client also
	// re-enforces the policy on every dial, so a policy change after enqueue
	// still cannot reach a blocked destination.
	if err := validateWebhookDestination(cfg.URL, s.cfg.WebhookDestinationPolicy()); err != nil {
		s.auditWebhookDestinationRejection(ctx, callback.Name, callback.ActionType, err)
		slog.Warn("event callback delivery not enqueued: destination rejected by SSRF-safe policy", "callback", callback.Name, "event_id", event.ID, "error", err)
		return nil
	}
	body, err := renderWebhookBody(callback.ActionType, cfg, event)
	if err != nil {
		return s.enqueueBrokenCallbackDelivery(ctx, q, event, callback, now, callback.ActionType, fmt.Sprintf("render payload: %v", err))
	}
	method := cfg.Method
	if method == "" {
		method = http.MethodPost
	}
	var headersJSON sql.NullString
	if len(cfg.Headers) > 0 {
		raw, err := json.Marshal(cfg.Headers)
		if err != nil {
			return s.enqueueBrokenCallbackDelivery(ctx, q, event, callback, now, callback.ActionType, fmt.Sprintf("encode headers: %v", err))
		}
		headersJSON = sql.NullString{String: string(raw), Valid: true}
	}
	id, err := randomID("cbd")
	if err != nil {
		return fmt.Errorf("generate callback delivery id: %w", err)
	}
	return q.InsertCallbackDelivery(ctx, repository.InsertCallbackDeliveryParams{
		ID:             id,
		CallbackID:     callback.ID,
		EventID:        event.ID,
		TaskID:         nullString(event.TaskID),
		TeamID:         nullString(event.TeamID),
		EventType:      event.EventType,
		ActionType:     callback.ActionType,
		EndpointUrl:    cfg.URL,
		Method:         method,
		Headers:        headersJSON,
		Payload:        string(body),
		Status:         callbackDeliveryStatusPending,
		Attempts:       0,
		MaxAttempts:    defaultCallbackDeliveryMaxAttempts,
		Error:          sql.NullString{},
		ChildTaskID:    sql.NullString{},
		LeaseExpiresAt: sql.NullTime{},
		NextAttemptAt:  sql.NullTime{Time: now, Valid: true},
		ProcessedAt:    sql.NullTime{},
		CreatedAt:      now,
		UpdatedAt:      now,
	})
}

// enqueueBrokenCallbackDelivery records a delivery that can never succeed
// (config/template error at enqueue time) directly as dead_letter so the
// misconfiguration is surfaced by chetter_list_callback_deliveries instead of
// being logged and dropped. The caller's task-event transaction is not
// aborted: a broken callback must not prevent the event itself from being
// persisted.
func (s *Service) enqueueBrokenCallbackDelivery(ctx context.Context, q data.Repository, event TaskEventCallbackContext, callback repository.EventCallback, now time.Time, actionType, errMsg string) error {
	id, err := randomID("cbd")
	if err != nil {
		return fmt.Errorf("generate callback delivery id: %w", err)
	}
	if actionType == "" {
		actionType = callback.ActionType
	}
	slog.Warn("event callback delivery enqueued as dead_letter (config error)", "callback", callback.Name, "action_type", actionType, "event_id", event.ID, "error", errMsg)
	return q.InsertCallbackDelivery(ctx, repository.InsertCallbackDeliveryParams{
		ID:             id,
		CallbackID:     callback.ID,
		EventID:        event.ID,
		TaskID:         nullString(event.TaskID),
		TeamID:         nullString(event.TeamID),
		EventType:      event.EventType,
		ActionType:     actionType,
		EndpointUrl:    "",
		Method:         http.MethodPost,
		Headers:        sql.NullString{},
		Payload:        "",
		Status:         callbackDeliveryStatusDeadLetter,
		Attempts:       1,
		MaxAttempts:    defaultCallbackDeliveryMaxAttempts,
		Error:          sql.NullString{String: errMsg, Valid: true},
		ChildTaskID:    sql.NullString{},
		LeaseExpiresAt: sql.NullTime{},
		NextAttemptAt:  sql.NullTime{},
		ProcessedAt:    sql.NullTime{},
		CreatedAt:      now,
		UpdatedAt:      now,
	})
}

// callbackDeliveryLoop is the background delivery worker (issue #357 AC2). It
// runs until the service stops and is wired into Service.Start so every
// server replica drains the shared queue; claims are lease-fenced so replicas
// never process the same delivery concurrently.
func (s *Service) callbackDeliveryLoop() {
	ticker := time.NewTicker(callbackDeliveryWorkerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			parent := context.Background()
			if s.shutdownCtx != nil {
				parent = s.shutdownCtx
			}
			// A full cycle can deliver up to the claim limit of requests, so it
			// gets a generous budget independent of the short reaper timeout.
			ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
			func() {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("callback delivery worker panic recovered", "panic", r)
					}
				}()
				if err := s.processDueCallbackDeliveries(ctx); err != nil {
					slog.Warn("callback delivery worker cycle failed", "err", err)
				}
			}()
			cancel()
		case <-s.reaperStop:
			return
		}
	}
}

// processDueCallbackDeliveries claims and delivers one batch of due callback
// deliveries. Exposed on Service so tests can drive worker cycles
// deterministically without starting the ticker loop.
func (s *Service) processDueCallbackDeliveries(ctx context.Context) error {
	if s.rawDB == nil || s.repo == nil {
		return nil
	}
	now := time.Now().UTC()
	deliveries, err := s.claimDueCallbackDeliveries(ctx, now)
	if err != nil {
		return fmt.Errorf("claim due callback deliveries: %w", err)
	}
	for _, delivery := range deliveries {
		deliverCtx, cancel := context.WithTimeout(ctx, callbackDeliveryRequestTimeout)
		deliveryErr := s.deliverCallbackDelivery(deliverCtx, delivery)
		cancel()
		if deliveryErr != nil {
			// Recorded (retry/dead-letter + audit) inside deliverCallbackDelivery;
			// only a storage failure here is worth surfacing to the cycle.
			slog.Warn("callback delivery attempt failed", "delivery_id", delivery.ID, "callback_id", delivery.CallbackID, "err", deliveryErr)
		}
	}
	return nil
}

// claimDueCallbackDeliveries atomically claims due deliveries under a lease
// inside one transaction (issue #357 AC2): due pending/failed rows plus
// in_flight rows whose lease expired (crashed worker) are selected with
// FOR UPDATE SKIP LOCKED and marked in_flight with a fresh lease before the
// transaction commits. The raw-SQL select mirrors the portable row-lock
// pattern used by task claiming.
func (s *Service) claimDueCallbackDeliveries(ctx context.Context, now time.Time) ([]repository.CallbackDelivery, error) {
	var claimed []repository.CallbackDelivery
	err := withTxRetryOptions(ctx, s.rawDB, s.dialect, nil, func(q data.Repository, tx *sql.Tx) error {
		limitClause := fmt.Sprintf(" ORDER BY next_attempt_at ASC LIMIT %d FOR UPDATE SKIP LOCKED", callbackDeliveryClaimLimit)
		var query string
		if s.dialect == store.DialectPostgres {
			query = `SELECT id, callback_id, event_id, task_id, team_id, event_type, endpoint_url,
			                method, headers, payload, status, attempts, max_attempts, error,
			                lease_expires_at, next_attempt_at, processed_at, created_at, updated_at,
			                action_type, child_task_id
			         FROM callback_deliveries
			         WHERE (status = 'pending' AND next_attempt_at <= $1)
			            OR (status = 'failed' AND next_attempt_at <= $1)
			            OR (status = 'in_flight' AND lease_expires_at < $1)` + limitClause
		} else {
			query = `SELECT id, callback_id, event_id, task_id, team_id, event_type, endpoint_url,
			                method, headers, payload, status, attempts, max_attempts, error,
			                lease_expires_at, next_attempt_at, processed_at, created_at, updated_at,
			                action_type, child_task_id
			         FROM callback_deliveries
			         WHERE (status = 'pending' AND next_attempt_at <= ?)
			            OR (status = 'failed' AND next_attempt_at <= ?)
			            OR (status = 'in_flight' AND lease_expires_at < ?)` + limitClause
		}
		rows, err := tx.QueryContext(ctx, query, now, now, now)
		if err != nil {
			return fmt.Errorf("select due callback deliveries: %w", err)
		}
		items := make([]repository.CallbackDelivery, 0, 8)
		for rows.Next() {
			var item repository.CallbackDelivery
			if err := rows.Scan(&item.ID, &item.CallbackID, &item.EventID, &item.TaskID, &item.TeamID,
				&item.EventType, &item.EndpointUrl, &item.Method, &item.Headers, &item.Payload,
				&item.Status, &item.Attempts, &item.MaxAttempts, &item.Error,
				&item.LeaseExpiresAt, &item.NextAttemptAt, &item.ProcessedAt,
				&item.CreatedAt, &item.UpdatedAt, &item.ActionType, &item.ChildTaskID); err != nil {
				rows.Close()
				return fmt.Errorf("scan due callback delivery: %w", err)
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate due callback deliveries: %w", err)
		}
		rows.Close()
		for _, item := range items {
			if _, err := q.MarkCallbackDeliveryInFlight(ctx, repository.MarkCallbackDeliveryInFlightParams{
				LeaseExpiresAt: sql.NullTime{Time: now.Add(callbackDeliveryLease), Valid: true},
				UpdatedAt:      now,
				ID:             item.ID,
				Now:            sql.NullTime{Time: now, Valid: true},
			}); err != nil {
				return fmt.Errorf("mark callback delivery in flight: %w", err)
			}
		}
		claimed = items
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

// deliverCallbackDelivery dispatches one claimed delivery to its action-type
// executor and persists the outcome: completed on success, failed with
// exponential backoff on transient errors, and dead_letter once max_attempts
// is exhausted or the failure is terminal. Every transition is audited
// (issue #357 AC4 for webhook/slack, issue #405 for create_task).
func (s *Service) deliverCallbackDelivery(ctx context.Context, delivery repository.CallbackDelivery) error {
	now := time.Now().UTC()
	if delivery.ActionType == EventCallbackActionCreateTask {
		return s.deliverCreateTaskCallback(ctx, delivery, now)
	}
	err := s.sendCallbackDelivery(ctx, delivery)
	if err == nil {
		return s.completeCallbackDelivery(ctx, delivery, "", now)
	}
	var polErr *ssrf.Error
	if errors.As(err, &polErr) {
		s.auditCallbackDelivery(ctx, delivery, "event_callback_destination_rejected", fmt.Sprintf("destination rejected by SSRF-safe policy: %v", polErr))
		slog.Warn("callback delivery destination rejected by SSRF-safe policy; dead-lettering", "delivery_id", delivery.ID, "callback_id", delivery.CallbackID, "error", polErr)
		return s.failCallbackDelivery(ctx, delivery, now, true, err)
	}
	return s.failCallbackDelivery(ctx, delivery, now, false, err)
}

// completeCallbackDelivery marks a delivery completed and records the spawned
// child task (create_task) for replay-safe recovery. childTaskID is empty for
// webhook/slack deliveries.
func (s *Service) completeCallbackDelivery(ctx context.Context, delivery repository.CallbackDelivery, childTaskID string, now time.Time) error {
	if _, err := s.repo.MarkCallbackDeliverySucceeded(ctx, repository.MarkCallbackDeliverySucceededParams{
		ChildTaskID: nullString(childTaskID),
		ProcessedAt: sql.NullTime{Time: now, Valid: true},
		UpdatedAt:   now,
		ID:          delivery.ID,
	}); err != nil {
		return fmt.Errorf("mark callback delivery completed: %w", err)
	}
	detail := "delivery completed"
	if childTaskID != "" {
		detail = fmt.Sprintf("delivery completed child_task_id=%s", childTaskID)
	}
	s.auditCallbackDelivery(ctx, delivery, "callback_delivery_completed", detail)
	return nil
}

// failCallbackDelivery records a failed attempt: terminal failures (and the
// max_attempts exhaustion) dead-letter immediately, transients are retried
// after the exponential backoff for the upcoming attempt. It returns the
// original cause so the worker cycle can log it.
func (s *Service) failCallbackDelivery(ctx context.Context, delivery repository.CallbackDelivery, now time.Time, terminal bool, cause error) error {
	status := callbackDeliveryStatusDeadLetter
	var nextAttemptAt sql.NullTime
	if !terminal {
		nextStatus, nextAt, retry := nextCallbackDeliveryAttempt(now, delivery.Attempts+1, delivery.MaxAttempts, s.callbackDeliveryBackoff())
		status = nextStatus
		if retry {
			nextAttemptAt = sql.NullTime{Time: nextAt, Valid: true}
		}
	}
	if _, markErr := s.repo.FailCallbackDelivery(ctx, repository.FailCallbackDeliveryParams{
		Status:        status,
		Error:         sql.NullString{String: truncateTo500(cause.Error()), Valid: true},
		NextAttemptAt: nextAttemptAt,
		UpdatedAt:     now,
		ID:            delivery.ID,
	}); markErr != nil {
		return fmt.Errorf("mark callback delivery %s: %w", status, markErr)
	}
	auditEvent := "callback_delivery_failed"
	if status == callbackDeliveryStatusDeadLetter {
		auditEvent = "callback_delivery_dead_letter"
	}
	s.auditCallbackDelivery(ctx, delivery, auditEvent, fmt.Sprintf("delivery attempt failed after %d/%d attempts: %v", delivery.Attempts+1, delivery.MaxAttempts, cause))
	return cause
}

// callbackDeliveryTerminalError marks a create_task delivery failure that can
// never succeed on retry (bad snapshot/config/template). The worker
// dead-letters these immediately instead of exhausting max_attempts
// (issue #405 AC5/AC6).
type callbackDeliveryTerminalError struct{ err error }

func (e *callbackDeliveryTerminalError) Error() string { return e.err.Error() }
func (e *callbackDeliveryTerminalError) Unwrap() error { return e.err }

func terminalCallbackDeliveryError(format string, args ...any) error {
	return &callbackDeliveryTerminalError{err: fmt.Errorf(format, args...)}
}

// deliverCreateTaskCallback executes a durable create_task delivery: it
// re-checks the recursion-depth guard (issue #312) at execution time, spawns
// the child task with a deterministic id derived from the delivery row (so a
// crash between spawn and completion cannot duplicate it), and persists the
// outcome. Dead letters are visible via chetter_list_callback_deliveries and
// audited.
func (s *Service) deliverCreateTaskCallback(ctx context.Context, delivery repository.CallbackDelivery, now time.Time) error {
	var snapshot callbackCreateTaskDelivery
	if err := json.Unmarshal([]byte(delivery.Payload), &snapshot); err != nil {
		return s.failCallbackDelivery(ctx, delivery, now, true, terminalCallbackDeliveryError("parse create_task delivery snapshot: %v", err))
	}
	childTaskID := callbackDeliveryTaskID(delivery.ID)
	if err := s.spawnCreateTaskFromEvent(ctx, snapshot.Event, snapshot.Name, snapshot.Config, childTaskID); err != nil {
		if isDuplicateKeyError(err) {
			// A previous attempt spawned the child and crashed before marking
			// this delivery completed. The deterministic id makes the replay
			// idempotent: adopt the existing task instead of duplicating it.
			if _, getErr := s.repo.GetTaskByID(ctx, childTaskID); getErr == nil {
				slog.Info("create_task callback delivery: task already created by previous attempt", "delivery_id", delivery.ID, "child_task_id", childTaskID)
				return s.completeCallbackDelivery(ctx, delivery, childTaskID, now)
			}
		}
		terminal := errors.Is(err, errEventCallbackRecursionLimit) || isCallbackDeliveryPermanentError(err)
		return s.failCallbackDelivery(ctx, delivery, now, terminal, err)
	}
	return s.completeCallbackDelivery(ctx, delivery, childTaskID, now)
}

// callbackDeliveryTaskID derives the deterministic child task id for one
// create_task delivery row. A retry that re-runs the spawn after a crash
// reuses the same id, so the tasks primary key makes it idempotent (issue
// #405 AC3), mirroring the inbound webhook worker.
func callbackDeliveryTaskID(deliveryRowID string) string {
	sum := sha256.Sum256([]byte("callback_delivery:" + deliveryRowID))
	return "task_" + hex.EncodeToString(sum[:])[:32]
}

// isCallbackDeliveryPermanentError classifies a create_task spawn failure as
// terminal (config/template/validation) versus transient (database/network).
func isCallbackDeliveryPermanentError(err error) bool {
	return isInboundPermanentTaskError(err)
}

// sendCallbackDelivery performs the SSRF-safe HTTP request for one claimed
// delivery row. The client (issue #337) enforces the destination policy on
// every address actually dialed and never follows redirects or honors ambient
// proxy configuration.
func (s *Service) sendCallbackDelivery(ctx context.Context, delivery repository.CallbackDelivery) error {
	method := delivery.Method
	if method == "" {
		method = http.MethodPost
	}
	headers := map[string]string{}
	if delivery.Headers.Valid && strings.TrimSpace(delivery.Headers.String) != "" && delivery.Headers.String != "null" {
		if err := json.Unmarshal([]byte(delivery.Headers.String), &headers); err != nil {
			return fmt.Errorf("parse stored delivery headers: %w", err)
		}
	}
	req, err := buildCallbackHTTPRequest(ctx, method, delivery.EndpointUrl, headers, []byte(delivery.Payload))
	if err != nil {
		return err
	}
	resp, err := s.webhookHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Drain a bounded prefix so keep-alive connections stay reusable; the
	// SSRF-safe client already bounds the total request time.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// buildCallbackHTTPRequest constructs the outbound callback request with the
// default JSON content type unless the callback supplies one. Shared by the
// queued delivery worker and the inline webhook/slack sender so their header
// and method semantics cannot drift apart.
func buildCallbackHTTPRequest(ctx context.Context, method, rawURL string, headers map[string]string, body []byte) (*http.Request, error) {
	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

// auditCallbackDelivery records a delivery state transition in the audit log.
// Detail carries only status/attempt/error-classification text — never the
// payload or stored headers.
func (s *Service) auditCallbackDelivery(ctx context.Context, delivery repository.CallbackDelivery, eventType, detail string) {
	s.auditAsync(ctx, AuditEventParams{
		EventType:  eventType,
		SourceType: "callback_delivery",
		SourceID:   delivery.ID,
		TargetType: "task",
		TargetID:   delivery.TaskID.String,
		Detail:     fmt.Sprintf("callback %s event %s %s", delivery.CallbackID, delivery.EventID, detail),
	})
}

// callbackDeliveryBackoff returns the active backoff function; tests inject a
// fast schedule by setting svc.deliveryBackoff.
func (s *Service) callbackDeliveryBackoff() func(int32) time.Duration {
	if s.deliveryBackoff != nil {
		return s.deliveryBackoff
	}
	return callbackDeliveryBackoff
}
