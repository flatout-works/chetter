package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/flatout-works/chetter/internal/store"
	"github.com/flatout-works/chetter/internal/webhook"
)

// webhookDeliveryStore implements webhook.DeliveryStore using raw SQL against
// the chetter_webhook_deliveries table. It avoids the sqlc generation cycle by
// using direct database/sql queries, similar to store.ReapStaleTasks. See
// issue #102.
type webhookDeliveryStore struct {
	db          *sql.DB
	dialect     store.Dialect
	auditLogger func(context.Context, AuditEventParams) error
}

func newWebhookDeliveryStore(db *sql.DB, dialect store.Dialect, auditLogger func(context.Context, AuditEventParams) error) *webhookDeliveryStore {
	return &webhookDeliveryStore{db: db, dialect: dialect, auditLogger: auditLogger}
}

func (d *webhookDeliveryStore) placeholder(n int) string {
	if d.dialect == store.DialectPostgres {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

func (d *webhookDeliveryStore) RecordDelivery(ctx context.Context, params webhook.RecordDeliveryParams) (bool, error) {
	id, err := randomID("dvr")
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	query := fmt.Sprintf(
		`INSERT INTO chetter_webhook_deliveries (id, delivery_id, event_type, event_action, payload, status, attempts, max_attempts, created_at, updated_at)
		 VALUES (%s, %s, %s, %s, %s, 'received', 0, 3, %s, %s)`,
		d.placeholder(1), d.placeholder(2), d.placeholder(3), d.placeholder(4), d.placeholder(5), d.placeholder(6), d.placeholder(7),
	)
	_, err = d.db.ExecContext(ctx, query, id, params.DeliveryID, params.EventType, params.Action, string(params.Payload), now, now)
	if err != nil {
		if isDuplicateKeyError(err) {
			return false, nil
		}
		return false, fmt.Errorf("insert webhook delivery: %w", err)
	}
	d.audit(ctx, params.DeliveryID, "webhook_delivery_received", "delivery received")
	return true, nil
}

func (d *webhookDeliveryStore) MarkDeliveryCompleted(ctx context.Context, deliveryID string) error {
	now := time.Now().UTC()
	query := fmt.Sprintf(
		`UPDATE chetter_webhook_deliveries SET status = 'completed', processed_at = %s, updated_at = %s WHERE delivery_id = %s`,
		d.placeholder(1), d.placeholder(2), d.placeholder(3),
	)
	_, err := d.db.ExecContext(ctx, query, now, now, deliveryID)
	if err == nil {
		d.audit(ctx, deliveryID, "webhook_delivery_completed", "delivery completed")
	}
	return err
}

func (d *webhookDeliveryStore) MarkDeliveryProcessing(ctx context.Context, deliveryID string) error {
	now := time.Now().UTC()
	query := fmt.Sprintf(
		`UPDATE chetter_webhook_deliveries SET status = 'processing', updated_at = %s WHERE delivery_id = %s`,
		d.placeholder(1), d.placeholder(2),
	)
	_, err := d.db.ExecContext(ctx, query, now, deliveryID)
	return err
}

func (d *webhookDeliveryStore) MarkDeliveryFailed(ctx context.Context, deliveryID string, errMsg string) error {
	now := time.Now().UTC()
	// Exponential backoff: 1s, 5s, 15s for attempts 1, 2, 3. After 3 attempts,
	// mark as dead_letter. See issue #102 criterion 1 and 4.
	var attempts, maxAttempts int
	selectQuery := fmt.Sprintf(`SELECT attempts, max_attempts FROM chetter_webhook_deliveries WHERE delivery_id = %s`, d.placeholder(1))
	if err := d.db.QueryRowContext(ctx, selectQuery, deliveryID).Scan(&attempts, &maxAttempts); err != nil {
		return err
	}
	backoff := 15 * time.Second
	if attempts == 0 {
		backoff = 1 * time.Second
	} else if attempts == 1 {
		backoff = 5 * time.Second
	}
	newAttempts := attempts + 1
	status := "failed"
	var nextAttempt any = sql.NullTime{Time: now.Add(backoff), Valid: true}
	if newAttempts >= maxAttempts {
		status = "dead_letter"
		nextAttempt = nil
	}
	query := fmt.Sprintf(
		`UPDATE chetter_webhook_deliveries
		 SET status = %s,
		     attempts = %s,
		     error = %s,
		     next_attempt_at = %s,
		     updated_at = %s
		 WHERE delivery_id = %s`,
		d.placeholder(1), d.placeholder(2), d.placeholder(3), d.placeholder(4), d.placeholder(5), d.placeholder(6),
	)
	_, err := d.db.ExecContext(ctx, query, status, newAttempts, errMsg, nextAttempt, now, deliveryID)
	if err == nil {
		d.audit(ctx, deliveryID, "webhook_delivery_"+status, errMsg)
	}
	return err
}

func (d *webhookDeliveryStore) audit(ctx context.Context, deliveryID, eventType, detail string) {
	if d.auditLogger == nil {
		return
	}
	if err := d.auditLogger(ctx, AuditEventParams{
		EventType:  eventType,
		SourceType: "webhook",
		SourceID:   deliveryID,
		TargetType: "webhook_delivery",
		TargetID:   deliveryID,
		Detail:     detail,
	}); err != nil {
		slog.Warn("webhook delivery: audit log failed", "err", err, "delivery_id", deliveryID, "event_type", eventType)
	}
}

// WebhookDeliveryRecord represents a webhook delivery for the MCP tool.
type WebhookDeliveryRecord struct {
	ID          string
	DeliveryID  string
	EventType   string
	EventAction string
	Status      string
	Attempts    int
	MaxAttempts int
	Error       string
	CreatedAt   time.Time
	ProcessedAt *time.Time
}

// ListWebhookDeliveries returns recent webhook deliveries for the MCP tool.
// See issue #102 criterion 3.
func (s *Service) ListWebhookDeliveries(ctx context.Context, limit, offset int) ([]WebhookDeliveryRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	if s.rawDB == nil {
		return nil, fmt.Errorf("database not available")
	}
	var query string
	if s.dialect == store.DialectPostgres {
		query = `SELECT id, delivery_id, event_type, event_action, status, attempts, max_attempts,
		                COALESCE(error, ''), created_at, processed_at
		         FROM chetter_webhook_deliveries
		         ORDER BY created_at DESC
		         LIMIT $1 OFFSET $2`
	} else {
		query = `SELECT id, delivery_id, event_type, event_action, status, attempts, max_attempts,
		                COALESCE(error, ''), created_at, processed_at
		         FROM chetter_webhook_deliveries
		         ORDER BY created_at DESC
		         LIMIT ? OFFSET ?`
	}
	rows, err := s.rawDB.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list webhook deliveries: %w", err)
	}
	defer rows.Close()
	var records []WebhookDeliveryRecord
	for rows.Next() {
		var r WebhookDeliveryRecord
		var processedAt sql.NullTime
		if err := rows.Scan(&r.ID, &r.DeliveryID, &r.EventType, &r.EventAction, &r.Status, &r.Attempts, &r.MaxAttempts, &r.Error, &r.CreatedAt, &processedAt); err != nil {
			return nil, fmt.Errorf("scan webhook delivery: %w", err)
		}
		if processedAt.Valid {
			r.ProcessedAt = &processedAt.Time
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// webhookDeliveryWorker is a background goroutine that retries failed webhook
// deliveries with exponential backoff. See issue #102 criterion 1 and 5.
type WebhookDeliveryWorker struct {
	db      *sql.DB
	dialect store.Dialect
	handler *webhook.Handler
	stopCh  <-chan struct{}
	done    chan struct{}
}

// NewWebhookDeliveryWorker creates a background worker that retries failed
// webhook deliveries. The worker runs until stopCh is closed. See issue #102.
func NewWebhookDeliveryWorker(db *sql.DB, dialect store.Dialect, handler *webhook.Handler, stopCh <-chan struct{}) *WebhookDeliveryWorker {
	return &WebhookDeliveryWorker{db: db, dialect: dialect, handler: handler, stopCh: stopCh, done: make(chan struct{})}
}

// Start begins the retry loop in a background goroutine. See issue #102.
func (w *WebhookDeliveryWorker) Start() {
	go w.run()
}

// Shutdown waits for the retry worker to observe the service stop signal.
// This keeps delivery processing from using the database after it closes.
func (w *WebhookDeliveryWorker) Shutdown(ctx context.Context) error {
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *WebhookDeliveryWorker) run() {
	defer close(w.done)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.retryFailedDeliveries()
		case <-w.stopCh:
			return
		}
	}
}

type pendingDelivery struct {
	id         string
	deliveryID string
	eventType  string
	payload    string
}

func (w *WebhookDeliveryWorker) retryFailedDeliveries() {
	if w.db == nil || w.handler == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	now := time.Now().UTC()
	var query string
	if w.dialect == store.DialectPostgres {
		query = `SELECT id, delivery_id, event_type, payload FROM chetter_webhook_deliveries
		         WHERE status = 'failed' AND next_attempt_at <= $1
		         ORDER BY next_attempt_at ASC LIMIT 10`
	} else {
		query = `SELECT id, delivery_id, event_type, payload FROM chetter_webhook_deliveries
		         WHERE status = 'failed' AND next_attempt_at <= ?
		         ORDER BY next_attempt_at ASC LIMIT 10`
	}
	rows, err := w.db.QueryContext(ctx, query, now)
	if err != nil {
		slog.Warn("webhook delivery worker: query failed deliveries", "err", err)
		return
	}
	var pending []pendingDelivery
	for rows.Next() {
		var d pendingDelivery
		if err := rows.Scan(&d.id, &d.deliveryID, &d.eventType, &d.payload); err != nil {
			slog.Warn("webhook delivery worker: scan delivery", "err", err)
			rows.Close()
			return
		}
		pending = append(pending, d)
	}
	rows.Close()

	for _, d := range pending {
		slog.Info("webhook delivery worker: retrying delivery", "delivery_id", d.deliveryID, "event_type", d.eventType)
		if err := w.handler.ProcessDelivery(d.eventType, []byte(d.payload), d.deliveryID); err != nil {
			slog.Warn("webhook delivery worker: retry failed", "delivery_id", d.deliveryID, "err", err)
		}
	}
}

func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return contains(msg, "Duplicate entry") || contains(msg, "duplicate key") || contains(msg, "unique constraint")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

var _ json.RawMessage

// NewWebhookDeliveryStoreAdapter returns a webhook.DeliveryStore backed by the
// chetter_webhook_deliveries table. Returns nil if db is nil (delivery tracking
// is disabled). See issue #102.
func NewWebhookDeliveryStoreAdapter(db *sql.DB, dialect store.Dialect, auditLogger func(context.Context, AuditEventParams) error) webhook.DeliveryStore {
	if db == nil {
		return nil
	}
	return newWebhookDeliveryStore(db, dialect, auditLogger)
}
